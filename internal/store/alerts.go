// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/littlesho/NodeRampart/internal/api"
	"github.com/littlesho/NodeRampart/internal/model"
)

const mergeBodyReserve = 128

// ConfigureNotifications changes admission policy under the same guard as
// event transactions. Existing fixed merge windows are not moved on reload.
func (s *Store) ConfigureNotifications(window time.Duration) error {
	if window < 0 || window > 24*time.Hour {
		return errors.New("notification merge window must be 0..24h")
	}
	s.budgetMu.Lock()
	defer s.budgetMu.Unlock()
	s.mergeWindow = window
	return nil
}

func recordEventNotification(ctx context.Context, tx *sql.Tx, event model.Event, message *OutboxMessage, now time.Time, window time.Duration) error {
	if message == nil {
		return recordOneEventNotification(ctx, tx, event, nil, now, window)
	}
	// Validate before admission; this also bounds malformed cyclic chains.
	seen := make(map[string]bool, MaxNotificationChannels)
	seenDedupe := make(map[string]bool, MaxNotificationChannels)
	seenID := make(map[string]bool, MaxNotificationChannels)
	messages := make([]OutboxMessage, 0, MaxNotificationChannels)
	for current := message; current != nil; current = current.Secondary {
		if len(messages) >= MaxNotificationChannels || seen[current.Channel] || seenDedupe[current.DedupeKey] ||
			(current.ID != "" && seenID[current.ID]) ||
			(current.Channel != "" && !notificationChannel(current.Channel)) ||
			(message.Secondary != nil && current.Channel == "") {
			return errors.New("event supports at most one message per configured notification channel")
		}
		seen[current.Channel] = true
		seenDedupe[current.DedupeKey] = true
		seenID[current.ID] = true
		copy := *current
		copy.Secondary = nil
		messages = append(messages, copy)
	}
	for index := range messages {
		if err := recordOneEventNotification(ctx, tx, event, &messages[index], now, window); err != nil {
			return err
		}
	}
	return nil
}

func recordOneEventNotification(ctx context.Context, tx *sql.Tx, event model.Event, message *OutboxMessage, now time.Time, window time.Duration) error {
	channel := "telegram"
	if message != nil && message.Channel != "" {
		channel = message.Channel
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM event_notifications WHERE event_id=? AND channel=?)`, event.ID, channel).Scan(&exists); err != nil || exists {
		return err
	}
	decision, notificationID, silenceID := "ineligible", "", ""
	record := func() error {
		_, err := tx.ExecContext(ctx, `INSERT INTO event_notifications(event_id,channel,notification_id,decision,silence_id,recorded_at) VALUES (?,?,?,?,?,?)`, event.ID, channel, notificationID, decision, silenceID, now.UnixMilli())
		return err
	}
	if message == nil {
		return record()
	}
	copyPresentation := *message
	if err := normalizePresentation(&copyPresentation); err != nil {
		return err
	}
	message = &copyPresentation
	// An older daemon may have committed this exact event before migration.
	err := tx.QueryRowContext(ctx, `SELECT id FROM notification_outbox WHERE dedupe_key=?`, message.DedupeKey).Scan(&notificationID)
	if err == nil {
		decision = "queued"
		return record()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if notificationChannel(message.Channel) || message.Destination == "telegram" {
		err = tx.QueryRowContext(ctx, `SELECT id FROM notification_silences WHERE revoked_at IS NULL AND expires_at>?
 AND (incident_id='' OR incident_id=?) AND (kind='' OR kind=?) ORDER BY created_at,id LIMIT 1`, now.UnixMilli(), event.IncidentID, event.Kind).Scan(&silenceID)
		if err == nil {
			decision = "silenced"
			return record()
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	if event.Phase == "recovery" && event.IncidentID != "" {
		if _, err := tx.ExecContext(ctx, `UPDATE notification_outbox SET next_attempt=MIN(next_attempt,?),merge_until=0
 WHERE incident_id=? AND destination=? AND event_phase='update' AND attempts=0 AND sent_at IS NULL AND suppressed_at IS NULL AND lease_until IS NULL`, now.UnixMilli(), event.IncidentID, message.Destination); err != nil {
			return err
		}
	}
	bound, err := notificationTargetMatches(ctx, tx, *message)
	if err != nil {
		return err
	}
	// Native bodies have already been bounded and encoded for their platform.
	// Preserve them byte-for-byte rather than applying Telegram HTML merging.
	mergeable := (message.Channel == "" || message.Channel == "telegram" || message.Channel == "webhook") && bound && mergeablePresentation(*message) && window > 0 && event.Phase == "update" && event.IncidentID != "" && len(message.Body) <= 4096-mergeBodyReserve
	if mergeable {
		var count, previousBytes int64
		err := tx.QueryRowContext(ctx, `SELECT id,merged_count,LENGTH(CAST(body AS BLOB)) FROM notification_outbox
	 WHERE incident_id=? AND event_kind=? AND destination=? AND language=? AND presentation_timezone=? AND event_phase='update' AND merge_until>?
 AND sent_at IS NULL AND suppressed_at IS NULL AND isolated_at IS NULL AND quarantined_at IS NULL AND lease_until IS NULL AND attempts=0 AND expires_at>?
 ORDER BY merge_until,id LIMIT 1`, event.IncidentID, event.Kind, message.Destination, message.Language, message.Timezone, now.UnixMilli(), now.UnixMilli()).Scan(&notificationID, &count, &previousBytes)
		if err == nil {
			if count == 1<<63-1 {
				return errors.New("notification merge count exhausted")
			}
			body := mergedBodyLocalized(count+1, message.Body, message.Language)
			var pendingBytes, channelBytes int64
			if err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(LENGTH(CAST(body AS BLOB))),0),
 COALESCE(SUM(CASE WHEN channel=? THEN LENGTH(CAST(body AS BLOB)) ELSE 0 END),0)
 FROM notification_outbox WHERE sent_at IS NULL AND suppressed_at IS NULL`, message.Channel).Scan(&pendingBytes, &channelBytes); err != nil {
				return err
			}
			if pendingBytes-previousBytes+int64(len(body)) > maxPendingOutboxBytes ||
				message.Channel != "" && channelBytes-previousBytes+int64(len(body)) > maxChannelOutboxBytes {
				decision, notificationID = "rejected", ""
				if _, err := tx.ExecContext(ctx, `UPDATE notification_counters SET rejected=MIN(rejected,9223372036854775806)+1 WHERE id=1`); err != nil {
					return err
				}
				return record()
			}
			if _, err := tx.ExecContext(ctx, `UPDATE notification_outbox SET body=?,merged_count=merged_count+1 WHERE id=?`, body, notificationID); err != nil {
				return err
			}
			decision = "merged"
			return record()
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	copy := *message
	if copy.ID == "" {
		copy.ID = model.NewID("msg")
	}
	mergeUntil := int64(0)
	if mergeable {
		copy.NextAttempt = now.Add(window)
		copy.Body = mergedBodyLocalized(1, copy.Body, copy.Language)
		mergeUntil = copy.NextAttempt.UnixMilli()
	}
	inserted, err := enqueue(ctx, tx, copy)
	if errors.Is(err, ErrOutboxFull) {
		decision, notificationID = "rejected", ""
		return record()
	}
	if err != nil {
		return err
	}
	if !inserted {
		return errors.New("event notification identity conflict")
	}
	if _, err := tx.ExecContext(ctx, `UPDATE notification_outbox SET incident_id=?,event_kind=?,event_phase=?,merge_until=? WHERE id=?`, event.IncidentID, event.Kind, event.Phase, mergeUntil, copy.ID); err != nil {
		return err
	}
	decision, notificationID = "queued", copy.ID
	return record()
}

func mergedBody(count int64, latest string) string {
	return mergedBodyLocalized(count, latest, "en")
}

func mergedBodyLocalized(count int64, latest, language string) string {
	if language == "zh" {
		return fmt.Sprintf("<b>%d 次事件更新</b> · 以下为最新观察，所有事件保留在时间线。\n%s", count, latest)
	}
	return fmt.Sprintf("<b>%d incident updates</b> · latest observation below; all events retained in the timeline.\n%s", count, latest)
}

// ClaimNotification reloads the final body and establishes a bounded lease in
// one transaction. A stale prefetched body can never race with coalescing.
func (s *Store) ClaimNotification(ctx context.Context, id string, now time.Time) (OutboxMessage, bool, error) {
	if !api.ValidID(id) || now.IsZero() {
		return OutboxMessage{}, false, errors.New("invalid notification claim")
	}
	release, err := s.beginWrite(ctx, writeCritical)
	if err != nil {
		return OutboxMessage{}, false, err
	}
	defer release()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return OutboxMessage{}, false, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE notification_outbox AS o SET lease_until=? WHERE id=?
 AND sent_at IS NULL AND quarantined_at IS NULL AND isolated_at IS NULL AND suppressed_at IS NULL AND expires_at>? AND next_attempt<=?
 AND (channel='' OR EXISTS(SELECT 1 FROM notification_targets t WHERE t.channel=o.channel AND t.destination=o.destination AND t.enabled=1))
 AND (lease_until IS NULL OR lease_until<=?)
 AND NOT EXISTS(SELECT 1 FROM notification_cooldowns c WHERE c.destination=o.destination AND c.until_at>?)`,
		now.Add(2*time.Minute).UnixMilli(), id, now.UnixMilli(), now.UnixMilli(), now.UnixMilli(), now.UnixMilli())
	if err != nil {
		return OutboxMessage{}, false, err
	}
	count, err := result.RowsAffected()
	if err != nil || count == 0 {
		return OutboxMessage{}, false, err
	}
	var message OutboxMessage
	var next int64
	if err := tx.QueryRowContext(ctx, `SELECT id,dedupe_key,channel,destination,body,attempts,next_attempt,language,presentation_timezone FROM notification_outbox WHERE id=?`, id).Scan(&message.ID, &message.DedupeKey, &message.Channel, &message.Destination, &message.Body, &message.Attempts, &next, &message.Language, &message.Timezone); err != nil {
		return OutboxMessage{}, false, err
	}
	message.NextAttempt = time.UnixMilli(next).UTC()
	if err := tx.Commit(); err != nil {
		return OutboxMessage{}, false, err
	}
	return message, true, nil
}

// ReleaseNotificationClaim is used only when cancellation interrupts delivery;
// it does not consume a delivery attempt or revive suppressed/expired messages.
func (s *Store) ReleaseNotificationClaim(ctx context.Context, id string) error {
	release, err := s.beginWrite(ctx, writeCritical)
	if err != nil {
		return err
	}
	defer release()
	_, err = s.db.ExecContext(ctx, `UPDATE notification_outbox SET lease_until=NULL WHERE id=? AND sent_at IS NULL`, id)
	return err
}
