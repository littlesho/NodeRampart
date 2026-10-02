// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"errors"
	"time"

	"github.com/littlesho/NodeRampart/internal/api"
)

const (
	MaxDeliveryAttempts = 10
	OutboxTTL           = 7 * 24 * time.Hour
)

// ReserveNotificationAttempt persists a paced receiver's consumed interval
// before any external request. Failed requests and process interruption still
// spend that interval. A reservation changes neither attempts nor the saved
// body, and is never permission to redirect an old claim to another target.
func (s *Store) ReserveNotificationAttempt(ctx context.Context, id, destination string, now time.Time, minimumInterval time.Duration) (bool, error) {
	if !api.ValidID(id) || destination == "" || len(destination) > 128 || now.IsZero() || minimumInterval < time.Second || minimumInterval > 30*time.Second {
		return false, errors.New("invalid notification attempt reservation")
	}
	release, err := s.beginWrite(ctx, writeCritical)
	if err != nil {
		return false, err
	}
	defer release()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var eligible bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM notification_outbox o WHERE id=? AND destination=?
 AND sent_at IS NULL AND quarantined_at IS NULL AND isolated_at IS NULL AND suppressed_at IS NULL
 AND lease_until>? AND expires_at>?
 AND (channel='' OR EXISTS(SELECT 1 FROM notification_targets t WHERE t.channel=o.channel AND t.destination=o.destination AND t.enabled=1))
 AND NOT EXISTS(SELECT 1 FROM notification_cooldowns c WHERE c.destination=o.destination AND c.until_at>?))`, id, destination, now.UnixMilli(), now.UnixMilli(), now.UnixMilli()).Scan(&eligible); err != nil {
		return false, err
	}
	if !eligible {
		return false, nil
	}
	until := now.Add(minimumInterval)
	if !until.Equal(until.Truncate(time.Millisecond)) {
		until = until.Truncate(time.Millisecond).Add(time.Millisecond)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO notification_cooldowns(destination,until_at) VALUES (?,?)
 ON CONFLICT(destination) DO UPDATE SET until_at=MAX(until_at,excluded.until_at)`, destination, until.UnixMilli()); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// MarkRateLimited durably pauses every message sharing a destination, including
// messages enqueued after this transaction and messages loaded after a restart.
func (s *Store) MarkRateLimited(ctx context.Context, id, destination string, until time.Time, reason string) error {
	release, admitErr := s.beginWrite(ctx, writeCritical)
	if admitErr != nil {
		return admitErr
	}
	defer release()

	if destination == "" || len(destination) > 128 || until.IsZero() {
		return errors.New("invalid destination cooldown")
	}
	if !until.Equal(until.Truncate(time.Millisecond)) {
		until = until.Truncate(time.Millisecond).Add(time.Millisecond)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO notification_cooldowns(destination, until_at) VALUES (?,?)
 ON CONFLICT(destination) DO UPDATE SET until_at=MAX(until_at, excluded.until_at)`, destination, until.UnixMilli()); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE notification_outbox SET lease_until=NULL, attempts=attempts+1, next_attempt=MAX(next_attempt,?), last_error=?,
 quarantined_at=CASE WHEN attempts+1>=? THEN ? ELSE quarantined_at END WHERE id=? AND destination=? AND sent_at IS NULL`, until.UnixMilli(), limit(reason, 512), MaxDeliveryAttempts, time.Now().UTC().UnixMilli(), id, destination); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) MarkQuarantined(ctx context.Context, id string, now time.Time, reason string) error {
	return s.quarantine(ctx, id, now, reason, true)
}
func (s *Store) QuarantineNotification(ctx context.Context, id string, now time.Time) error {
	return s.quarantine(ctx, id, now, "manually quarantined", false)
}
func (s *Store) quarantine(ctx context.Context, id string, now time.Time, reason string, attempted bool) error {
	release, admitErr := s.beginWrite(ctx, writeCritical)
	if admitErr != nil {
		return admitErr
	}
	defer release()

	result, err := s.db.ExecContext(ctx, `UPDATE notification_outbox SET lease_until=CASE WHEN ? THEN NULL ELSE lease_until END, attempts=attempts+?, quarantined_at=?, last_error=? WHERE id=? AND sent_at IS NULL AND expires_at>?`, boolInt(attempted), boolInt(attempted), now.UnixMilli(), limit(reason, 512), id, now.UnixMilli())
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return errors.New("notification is absent, sent or expired")
	}
	return nil
}

// RetryNotification retries one retained message without bypassing a destination
// cooldown. Expired notifications cannot be revived with stale contents.
func (s *Store) RetryNotification(ctx context.Context, id string, now time.Time) error {
	release, admitErr := s.beginWrite(ctx, writeNormal)
	if admitErr != nil {
		return admitErr
	}
	defer release()

	result, err := s.db.ExecContext(ctx, `UPDATE notification_outbox SET attempts=0, quarantined_at=NULL, lease_until=NULL, merge_until=0, next_attempt=?, last_error=''
 WHERE id=? AND sent_at IS NULL AND isolated_at IS NULL AND suppressed_at IS NULL AND (lease_until IS NULL OR lease_until<=?) AND expires_at>? AND NOT EXISTS(SELECT 1 FROM notification_dispatch d WHERE d.notification_id=id AND d.state NOT IN ('prepared','retry_ready'))`, now.UnixMilli(), id, now.UnixMilli(), now.UnixMilli())
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return errors.New("notification is absent, sent or expired")
	}
	return nil
}

func (s *Store) ExpireNotifications(ctx context.Context, now time.Time) error {
	release, admitErr := s.beginWrite(ctx, writeCritical)
	if admitErr != nil {
		return admitErr
	}
	defer release()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := recoverOfficialIntents(ctx, tx, now); err != nil {
		return err
	}
	if err := expireOfficialPolls(ctx, tx, now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE notification_counters SET expired=MIN(expired,9223372036854775807-(SELECT COUNT(*) FROM notification_outbox WHERE sent_at IS NULL AND expires_at<=?))+(SELECT COUNT(*) FROM notification_outbox WHERE sent_at IS NULL AND expires_at<=?) WHERE id=1`, now.UnixMilli(), now.UnixMilli()); err != nil {
		return err
	}
	if _, err := pruneRows(ctx, tx, "notification_outbox", "time_expiry", `SELECT rowid FROM notification_outbox WHERE sent_at IS NULL AND expires_at<=?`, []any{now.UnixMilli()}, now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM notification_cooldowns WHERE until_at<=?`, now.UnixMilli()); err != nil {
		return err
	}
	return tx.Commit()
}

type QueueStatus struct {
	Isolated      int64                 `json:"isolated"`
	Suppressed    int64                 `json:"suppressed"`
	Pending       int64                 `json:"pending"`
	PendingBytes  int64                 `json:"pending_bytes"`
	Quarantined   int64                 `json:"quarantined"`
	OldestPending time.Time             `json:"oldest_pending_utc,omitzero"`
	LastSent      time.Time             `json:"last_sent_utc,omitzero"`
	Rejected      uint64                `json:"rejected"`
	Expired       uint64                `json:"expired"`
	MaxMessages   int                   `json:"max_messages"`
	MaxBytes      int                   `json:"max_bytes"`
	Channels      []ChannelQueueStatus  `json:"channels"`
	Cooldowns     []DestinationCooldown `json:"cooldowns"`
}

// Channel shares include isolated and quarantined bodies across all previous
// identities. Existing larger queues are retained, with new admission paused
// until usage falls below the share; migration never discards their bodies.
type ChannelQueueStatus struct {
	Channel      string `json:"channel"`
	Pending      int64  `json:"pending"`
	PendingBytes int64  `json:"pending_bytes"`
	Isolated     int64  `json:"isolated"`
	Quarantined  int64  `json:"quarantined"`
	MaxMessages  int    `json:"max_messages"`
	MaxBytes     int    `json:"max_bytes"`
}

type DestinationCooldown struct {
	Destination string    `json:"destination"`
	Until       time.Time `json:"until_utc"`
}

func (s *Store) QueueStatus(ctx context.Context, now time.Time) (QueueStatus, error) {
	status := QueueStatus{MaxMessages: maxPendingOutboxMessages, MaxBytes: maxPendingOutboxBytes}
	var oldest, sent int64
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(LENGTH(CAST(o.body AS BLOB))+COALESCE(LENGTH(CAST(d.frozen_payload AS BLOB)),0)),0), COALESCE(SUM(quarantined_at IS NOT NULL),0), COALESCE(MIN(created_at),0) FROM notification_outbox o LEFT JOIN notification_dispatch d ON d.notification_id=o.id WHERE sent_at IS NULL AND suppressed_at IS NULL AND expires_at>?`, now.UnixMilli()).Scan(&status.Pending, &status.PendingBytes, &status.Quarantined, &oldest)
	if err != nil {
		return status, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM notification_outbox WHERE sent_at IS NULL AND suppressed_at IS NOT NULL`).Scan(&status.Suppressed); err != nil {
		return status, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM notification_outbox WHERE sent_at IS NULL AND suppressed_at IS NULL AND isolated_at IS NOT NULL AND expires_at>?`, now.UnixMilli()).Scan(&status.Isolated); err != nil {
		return status, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT rejected, expired, last_sent_at FROM notification_counters WHERE id=1`).Scan(&status.Rejected, &status.Expired, &sent); err != nil {
		return status, err
	}
	if oldest != 0 {
		status.OldestPending = time.UnixMilli(oldest).UTC()
	}
	if sent != 0 {
		status.LastSent = time.UnixMilli(sent).UTC()
	}
	// One bounded aggregate avoids scanning retained bodies once per channel.
	channelRows, err := s.db.QueryContext(ctx, `SELECT o.channel,COUNT(*),COALESCE(SUM(LENGTH(CAST(o.body AS BLOB))+COALESCE(LENGTH(CAST(d.frozen_payload AS BLOB)),0)),0),
 COALESCE(SUM(isolated_at IS NOT NULL),0),COALESCE(SUM(quarantined_at IS NOT NULL),0)
 FROM notification_outbox o LEFT JOIN notification_dispatch d ON d.notification_id=o.id WHERE o.channel IN (`+notificationChannelsSQL+`) AND sent_at IS NULL AND suppressed_at IS NULL AND expires_at>?
 GROUP BY o.channel LIMIT ?`, now.UnixMilli(), MaxNotificationChannels)
	if err != nil {
		return status, err
	}
	channels := make(map[string]ChannelQueueStatus, MaxNotificationChannels)
	for channelRows.Next() {
		var entry ChannelQueueStatus
		if err := channelRows.Scan(&entry.Channel, &entry.Pending, &entry.PendingBytes, &entry.Isolated, &entry.Quarantined); err != nil {
			channelRows.Close()
			return status, err
		}
		channels[entry.Channel] = entry
	}
	if err := channelRows.Err(); err != nil {
		channelRows.Close()
		return status, err
	}
	if err := channelRows.Close(); err != nil {
		return status, err
	}
	// Return the same fixed, credential-free list for disabled/unused channels.
	status.Channels = make([]ChannelQueueStatus, 0, MaxNotificationChannels)
	for _, channel := range notificationChannels {
		entry := channels[channel]
		entry.Channel = channel
		entry.MaxMessages, entry.MaxBytes = channelOutboxLimits(channel)
		status.Channels = append(status.Channels, entry)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT destination, until_at FROM notification_cooldowns WHERE until_at>? ORDER BY destination LIMIT 100`, now.UnixMilli())
	if err != nil {
		return status, err
	}
	defer rows.Close()
	for rows.Next() {
		var c DestinationCooldown
		var until int64
		if err := rows.Scan(&c.Destination, &until); err != nil {
			return status, err
		}
		c.Until = time.UnixMilli(until).UTC()
		status.Cooldowns = append(status.Cooldowns, c)
	}
	return status, rows.Err()
}

// ResumeDestination is an explicit operator action; individual message retries
// never clear a server-wide hold.
func (s *Store) ResumeDestination(ctx context.Context, destination string) error {
	release, admitErr := s.beginWrite(ctx, writeNormal)
	if admitErr != nil {
		return admitErr
	}
	defer release()

	if destination == "" || len(destination) > 128 {
		return errors.New("invalid destination")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM notification_cooldowns WHERE destination=?`, destination); err != nil {
		return err
	}
	now := time.Now().UTC().UnixMilli()
	if _, err := tx.ExecContext(ctx, `UPDATE notification_outbox SET next_attempt=MAX(?,merge_until) WHERE destination=? AND sent_at IS NULL AND quarantined_at IS NULL AND suppressed_at IS NULL AND expires_at>?`, now, destination, now); err != nil {
		return err
	}
	return tx.Commit()
}

type NotificationInfo struct {
	DispatchState    string    `json:"dispatch_state,omitempty"`
	FirstAttemptAt   time.Time `json:"first_attempt_at_utc,omitzero"`
	HTTPStatus       int       `json:"http_status"`
	APIErrorCode     int       `json:"api_error_code"`
	ID               string    `json:"id"`
	Channel          string    `json:"channel,omitempty"`
	Destination      string    `json:"destination"`
	State            string    `json:"state"`
	Attempts         int       `json:"attempts"`
	Bytes            int       `json:"bytes"`
	CreatedAt        time.Time `json:"created_at_utc"`
	NextAttempt      time.Time `json:"next_attempt_utc"`
	ExpiresAt        time.Time `json:"expires_at_utc"`
	LastError        string    `json:"last_error,omitempty"`
	ProviderState    string    `json:"provider_delivery_status,omitempty"`
	PlatformSegments *int      `json:"platform_segments"`
	PlatformPrice    *string   `json:"platform_price"`
	PriceUnit        string    `json:"price_unit,omitempty"`
}

// Notifications excludes message bodies and destination credentials. Pagination
// uses the opaque ID; all retained states remain visible for diagnosis.
func (s *Store) Notifications(ctx context.Context, beforeID string, count int) ([]NotificationInfo, error) {
	if count < 1 || count > 100 || len(beforeID) > 128 {
		return nil, errors.New("invalid notification query")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,channel,destination,CASE WHEN sent_at IS NOT NULL THEN CASE WHEN channel IN ('teams','qqbot','line','twilio_sms','whatsapp_cloud') THEN 'accepted' ELSE 'sent' END WHEN suppressed_at IS NOT NULL THEN CASE WHEN isolated_at IS NOT NULL THEN 'discarded' ELSE 'silenced' END WHEN expires_at<=? THEN 'expired' WHEN isolated_at IS NOT NULL THEN 'isolated' WHEN EXISTS(SELECT 1 FROM notification_dispatch d WHERE d.notification_id=o.id AND d.state='delivery_unknown') THEN 'delivery_unknown' WHEN EXISTS(SELECT 1 FROM notification_dispatch d WHERE d.notification_id=o.id AND d.state='blocked') THEN 'blocked' WHEN quarantined_at IS NOT NULL THEN 'quarantined' WHEN EXISTS(SELECT 1 FROM notification_targets t WHERE t.channel=o.channel AND t.destination=o.destination AND t.enabled=0) THEN 'paused' WHEN EXISTS(SELECT 1 FROM notification_dispatch d WHERE d.notification_id=o.id AND d.state='in_flight') THEN 'in_flight' ELSE 'pending' END,
 attempts,LENGTH(CAST(body AS BLOB))+COALESCE((SELECT LENGTH(CAST(frozen_payload AS BLOB)) FROM notification_dispatch d WHERE d.notification_id=o.id),0),created_at,next_attempt,expires_at,last_error,COALESCE((SELECT provider_state FROM notification_dispatch d WHERE d.notification_id=o.id),''),(SELECT platform_segments FROM notification_dispatch d WHERE d.notification_id=o.id),(SELECT platform_price FROM notification_dispatch d WHERE d.notification_id=o.id),COALESCE((SELECT price_unit FROM notification_dispatch d WHERE d.notification_id=o.id),''),COALESCE((SELECT http_status FROM notification_dispatch d WHERE d.notification_id=o.id),0),COALESCE((SELECT api_error_code FROM notification_dispatch d WHERE d.notification_id=o.id),0),COALESCE((SELECT state FROM notification_dispatch d WHERE d.notification_id=o.id),''),COALESCE((SELECT first_attempt_at FROM notification_dispatch d WHERE d.notification_id=o.id),0) FROM notification_outbox o WHERE (?='' OR id<?) ORDER BY id DESC LIMIT ?`, time.Now().UTC().UnixMilli(), beforeID, beforeID, count)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []NotificationInfo{}
	for rows.Next() {
		var n NotificationInfo
		var created, next, expires, first int64
		if err := rows.Scan(&n.ID, &n.Channel, &n.Destination, &n.State, &n.Attempts, &n.Bytes, &created, &next, &expires, &n.LastError, &n.ProviderState, &n.PlatformSegments, &n.PlatformPrice, &n.PriceUnit, &n.HTTPStatus, &n.APIErrorCode, &n.DispatchState, &first); err != nil {
			return nil, err
		}
		if first > 0 {
			n.FirstAttemptAt = time.UnixMilli(first).UTC()
		}
		n.CreatedAt = time.UnixMilli(created).UTC()
		n.NextAttempt = time.UnixMilli(next).UTC()
		n.ExpiresAt = time.UnixMilli(expires).UTC()
		result = append(result, n)
	}
	return result, rows.Err()
}
