// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/littlesho/NodeRampart/internal/api"
	"github.com/littlesho/NodeRampart/internal/model"
)

const MaxSilences = 128

type Silence struct {
	ID         string    `json:"id"`
	IncidentID string    `json:"incident_id,omitempty"`
	Kind       string    `json:"kind,omitempty"`
	Reason     string    `json:"reason,omitempty"`
	CreatedAt  time.Time `json:"created_at_utc"`
	ExpiresAt  time.Time `json:"expires_at_utc"`
	RevokedAt  time.Time `json:"revoked_at_utc,omitzero"`
	State      string    `json:"state"`
}

type SilenceResult struct {
	Silence    Silence `json:"silence"`
	Suppressed int64   `json:"suppressed_messages"`
	InFlight   int64   `json:"already_claimed_may_finish"`
}

// Legacy messages are resolved through the exact event dedupe key and the
// event primary key; no unindexed search through event evidence is required.
const silenceMessageMatch = `destination='telegram' AND (
 (event_kind<>'' AND (?='' OR incident_id=?) AND (?='' OR event_kind=?)) OR
 (event_kind='' AND EXISTS(SELECT 1 FROM events e WHERE e.id=substr(notification_outbox.dedupe_key,7,length(notification_outbox.dedupe_key)-15)
 AND notification_outbox.dedupe_key='event:'||e.id||':telegram' AND (?='' OR e.incident_id=?) AND (?='' OR e.kind=?))))`

func (s *Store) AddSilence(ctx context.Context, rule Silence, now time.Time) (SilenceResult, error) {
	var result SilenceResult
	if rule.IncidentID == "" && rule.Kind == "" || rule.IncidentID != "" && !api.ValidID(rule.IncidentID) || rule.Kind != "" && !api.ValidID(rule.Kind) ||
		len(rule.Reason) > 256 || !utf8.ValidString(rule.Reason) || strings.IndexFunc(rule.Reason, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 ||
		now.IsZero() || !rule.ExpiresAt.After(now) || rule.ExpiresAt.Sub(now) > 7*24*time.Hour {
		return result, errors.New("silence requires an incident or kind, bounded reason and expiry within seven days")
	}
	rule.ID = model.NewID("sil")
	rule.CreatedAt, rule.ExpiresAt = now.UTC().Truncate(time.Millisecond), rule.ExpiresAt.UTC().Truncate(time.Millisecond)
	if !rule.ExpiresAt.After(rule.CreatedAt) {
		return result, errors.New("silence expiry must be after creation")
	}
	rule.RevokedAt = time.Time{}
	rule.State = "active"
	release, err := s.beginWrite(ctx, writeCritical)
	if err != nil {
		return result, err
	}
	defer release()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	if _, err := pruneRows(ctx, tx, "notification_silences", "time_expiry", `SELECT rowid FROM notification_silences WHERE expires_at<=?`, []any{now.UnixMilli()}, now); err != nil {
		return result, err
	}
	if _, err := pruneRows(ctx, tx, "notification_silences", "capacity_eviction", `SELECT rowid FROM notification_silences WHERE revoked_at IS NOT NULL`, nil, now); err != nil {
		return result, err
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM notification_silences`).Scan(&count); err != nil {
		return result, err
	}
	if count >= MaxSilences {
		return result, errors.New("silence capacity reached")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO notification_silences(id,incident_id,kind,reason,created_at,expires_at) VALUES (?,?,?,?,?,?)`, rule.ID, rule.IncidentID, rule.Kind, rule.Reason, rule.CreatedAt.UnixMilli(), rule.ExpiresAt.UnixMilli()); err != nil {
		return result, err
	}
	args := []any{rule.IncidentID, rule.IncidentID, rule.Kind, rule.Kind, rule.IncidentID, rule.IncidentID, rule.Kind, rule.Kind}
	query := `SELECT COUNT(*),COALESCE(SUM(lease_until>?),0) FROM notification_outbox WHERE sent_at IS NULL AND suppressed_at IS NULL AND expires_at>? AND ` + silenceMessageMatch
	countArgs := append([]any{now.UnixMilli(), now.UnixMilli()}, args...)
	if err := tx.QueryRowContext(ctx, query, countArgs...).Scan(&result.Suppressed, &result.InFlight); err != nil {
		return result, err
	}
	var firstBody, lastBody int64
	if result.Suppressed > 0 {
		if err := tx.QueryRowContext(ctx, `SELECT MIN(created_at),MAX(created_at) FROM notification_outbox WHERE sent_at IS NULL AND suppressed_at IS NULL AND expires_at>? AND `+silenceMessageMatch, append([]any{now.UnixMilli()}, args...)...).Scan(&firstBody, &lastBody); err != nil {
			return result, err
		}
		if err := recordRetention(ctx, tx, "notification_outbox", "silence_body_discard", result.Suppressed, firstBody, lastBody, now); err != nil {
			return result, err
		}
	}
	// Suppression also persists for a previously claimed row. The already
	// copied in-flight body may finish, but a failed/cancelled send cannot retry
	// after expiry/revocation and unexpectedly release the suppressed backlog.
	if _, err := tx.ExecContext(ctx, `UPDATE event_notifications SET decision='silenced',silence_id=? WHERE notification_id IN
 (SELECT id FROM notification_outbox WHERE sent_at IS NULL AND suppressed_at IS NULL AND expires_at>? AND `+silenceMessageMatch+`)`, append([]any{rule.ID, now.UnixMilli()}, args...)...); err != nil {
		return result, err
	}
	updateArgs := append([]any{now.UnixMilli(), now.UnixMilli()}, args...)
	if _, err := tx.ExecContext(ctx, `UPDATE notification_outbox SET suppressed_at=?,body='' WHERE sent_at IS NULL AND suppressed_at IS NULL AND expires_at>? AND `+silenceMessageMatch, updateArgs...); err != nil {
		return result, err
	}
	// Suppressed bodies are discarded; retain at most one outbox capacity of
	// body-free suppressed outcomes. Event decisions survive outbox pruning.
	if _, err := pruneRows(ctx, tx, "notification_outbox", "capacity_eviction", `SELECT rowid FROM notification_outbox WHERE suppressed_at IS NOT NULL AND sent_at IS NULL AND (lease_until IS NULL OR lease_until<=?)
 AND id NOT IN(SELECT id FROM notification_outbox WHERE suppressed_at IS NOT NULL AND sent_at IS NULL ORDER BY CASE WHEN lease_until>? THEN 0 ELSE 1 END,suppressed_at DESC,id DESC LIMIT ?)`, []any{now.UnixMilli(), now.UnixMilli(), maxPendingOutboxMessages}, now); err != nil {
		return result, err
	}
	if err := tx.Commit(); err != nil {
		return result, err
	}
	result.Silence = rule
	return result, nil
}

func (s *Store) Silences(ctx context.Context, now time.Time) ([]Silence, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,incident_id,kind,reason,created_at,expires_at,revoked_at FROM notification_silences ORDER BY created_at,id LIMIT ?`, MaxSilences)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Silence{}
	for rows.Next() {
		var rule Silence
		var created, expires int64
		var revoked *int64
		if err := rows.Scan(&rule.ID, &rule.IncidentID, &rule.Kind, &rule.Reason, &created, &expires, &revoked); err != nil {
			return nil, err
		}
		rule.CreatedAt, rule.ExpiresAt = time.UnixMilli(created).UTC(), time.UnixMilli(expires).UTC()
		rule.State = "active"
		if expires <= now.UnixMilli() {
			rule.State = "expired"
		}
		if revoked != nil {
			rule.State = "revoked"
			rule.RevokedAt = time.UnixMilli(*revoked).UTC()
		}
		result = append(result, rule)
	}
	return result, rows.Err()
}

func (s *Store) RemoveSilence(ctx context.Context, id string, now time.Time) error {
	if !api.ValidID(id) {
		return errors.New("invalid silence ID")
	}
	release, err := s.beginWrite(ctx, writeCritical)
	if err != nil {
		return err
	}
	defer release()
	result, err := s.db.ExecContext(ctx, `UPDATE notification_silences SET revoked_at=COALESCE(revoked_at,?) WHERE id=?`, now.UnixMilli(), id)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return errors.New("silence not found")
	}
	return nil
}
