// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/littlesho/NodeRampart/internal/api"
)

func validNotificationTarget(channel, destination string) bool {
	if destination == channel+":unknown" {
		return true
	}
	if len(destination) != len(channel)+1+64 || !strings.HasPrefix(destination, channel+":") {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(destination, channel+":"))
	return err == nil
}

// MaxNotificationChannels is a fixed product limit, not a plugin registry.
const MaxNotificationChannels = 8

const notificationChannelsSQL = "'telegram','webhook','feishu','wecom','discord','slack','teams','google_chat'"

var notificationChannels = [...]string{"telegram", "webhook", "feishu", "wecom", "discord", "slack", "teams", "google_chat"}

func notificationChannel(channel string) bool {
	switch channel {
	case "telegram", "webhook", "feishu", "wecom", "discord", "slack", "teams", "google_chat":
		return true
	default:
		return false
	}
}

func notificationTargetMatches(ctx context.Context, tx *sql.Tx, message OutboxMessage) (bool, error) {
	if message.Channel == "" {
		return true, nil
	}
	var bound bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM notification_targets WHERE channel=? AND destination=? AND privacy_mode=? AND destination<>?)`, message.Channel, message.Destination, message.PrivacyMode, message.Channel+":unknown").Scan(&bound)
	return bound, err
}

// ConfigureNotificationTarget binds one channel to an immutable, credential-free
// receiver identity. Switching back never revives previously isolated messages.
// Disabled same-target messages remain retained and paused. A stricter or
// incomparable privacy mode isolates older bodies instead of rewriting history.
func (s *Store) ConfigureNotificationTarget(ctx context.Context, channel, destination, privacyMode string, enabled bool, now time.Time) error {
	if !notificationChannel(channel) || !api.ValidID(destination) || !validNotificationTarget(channel, destination) || now.IsZero() ||
		(privacyMode != "full" && privacyMode != "prefix" && privacyMode != "hash") {
		return errors.New("invalid notification target policy")
	}
	release, err := s.beginWrite(ctx, writeCritical)
	if err != nil {
		return err
	}
	defer release()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var previousDestination, previousPrivacy string
	var previousEnabled bool
	var activated int64
	err = tx.QueryRowContext(ctx, `SELECT destination,privacy_mode,enabled,activated_at FROM notification_targets WHERE channel=?`, channel).Scan(&previousDestination, &previousPrivacy, &previousEnabled, &activated)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	isolatePrivacy := previousPrivacy != "" && previousPrivacy != privacyMode && privacyMode != "full"
	if _, err := tx.ExecContext(ctx, `UPDATE notification_outbox SET isolated_at=?,lease_until=NULL,last_error='notification target or privacy policy changed; retained in isolation'
 WHERE channel=? AND sent_at IS NULL AND suppressed_at IS NULL AND isolated_at IS NULL AND (destination<>? OR ?)`, now.UnixMilli(), channel, destination, isolatePrivacy); err != nil {
		return err
	}
	if destination == channel+":unknown" {
		enabled = false
	}
	if channel != "telegram" && channel != "webhook" && (previousDestination != destination || enabled && (!previousEnabled || activated == 0)) {
		activated = now.UnixMilli()
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO notification_targets(channel,destination,privacy_mode,enabled,activated_at) VALUES (?,?,?,?,?)
 ON CONFLICT(channel) DO UPDATE SET destination=excluded.destination,privacy_mode=excluded.privacy_mode,enabled=excluded.enabled,activated_at=excluded.activated_at`, channel, destination, privacyMode, boolInt(enabled), activated); err != nil {
		return err
	}
	return tx.Commit()
}

// NotificationTargetActivatedAt is the persisted boundary for automatic native
// daily summaries. It does not revoke old queued bodies or change report dates.
// An absent/mismatched target is an error, never permission to send history.
func (s *Store) NotificationTargetActivatedAt(ctx context.Context, channel, destination string) (time.Time, error) {
	if !notificationChannel(channel) || !validNotificationTarget(channel, destination) {
		return time.Time{}, errors.New("invalid notification target")
	}
	var activated int64
	if err := s.db.QueryRowContext(ctx, `SELECT activated_at FROM notification_targets WHERE channel=? AND destination=?`, channel, destination).Scan(&activated); err != nil {
		return time.Time{}, err
	}
	if activated == 0 {
		return time.Time{}, nil
	}
	return time.UnixMilli(activated).UTC(), nil
}

// NotificationDeliveryAllowed rechecks an already claimed row immediately before
// dispatch. An in-flight HTTP request may finish at its original receiver; its
// immutable sender identity can never redirect it to a newly configured target.
func (s *Store) NotificationDeliveryAllowed(ctx context.Context, id, destination string) (bool, error) {
	var allowed bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM notification_outbox o WHERE id=? AND destination=? AND sent_at IS NULL
 AND isolated_at IS NULL AND suppressed_at IS NULL AND quarantined_at IS NULL AND lease_until>? AND expires_at>?
 AND (channel='' OR EXISTS(SELECT 1 FROM notification_targets t WHERE t.channel=o.channel AND t.destination=o.destination AND t.enabled=1)))`, id, destination, time.Now().UTC().UnixMilli(), time.Now().UTC().UnixMilli()).Scan(&allowed)
	return allowed, err
}

// DiscardIsolatedNotifications is an explicit, bounded-scope operator action.
// Sent history and the active target's queue are preserved. It does not migrate
// bodies or remove channel cooldowns and cannot revoke an in-flight HTTP request.
func (s *Store) DiscardIsolatedNotifications(ctx context.Context, channel string, now time.Time) (int64, error) {
	if !notificationChannel(channel) || now.IsZero() {
		return 0, errors.New("invalid notification channel")
	}
	release, err := s.beginWrite(ctx, writeCritical)
	if err != nil {
		return 0, err
	}
	defer release()
	// Retain the message record but erase only explicitly selected isolated bodies.
	result, err := s.db.ExecContext(ctx, `UPDATE notification_outbox SET body='',suppressed_at=?,last_error='isolated notification explicitly discarded'
 WHERE channel=? AND isolated_at IS NOT NULL AND sent_at IS NULL AND suppressed_at IS NULL`, now.UnixMilli(), channel)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
