// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/littlesho/NodeRampart/internal/model"
)

type JournalWrite struct {
	Cursor       string
	ReceivedAt   time.Time
	ObservedAt   time.Time
	Kind         string
	SourceRange  string
	Event        *model.Event
	Notification *OutboxMessage
	// Trusted is set only after journal decoding and source validation. A
	// new trusted checkpoint clears pending recovery in this same transaction.
	Trusted bool
}

type Checkpoint struct {
	Cursor          string
	ObservedAt      time.Time
	RecoveryPending bool
}

var ErrJournalRecoveryUnavailable = errors.New("journal recovery state unavailable")

func (s *Store) JournalCheckpoint(ctx context.Context) (Checkpoint, error) {
	var c Checkpoint
	var observed, pending int64
	// The singleton exists even before the first checkpoint. Read readiness
	// and cursor from one snapshot; a missing/corrupt marker is never ready.
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(c.cursor,''),COALESCE(c.observed_at,0),r.pending
 FROM journal_recovery r LEFT JOIN collector_checkpoints c ON c.name='ssh_journal' WHERE r.id=1`).Scan(&c.Cursor, &observed, &pending)
	if err != nil {
		// Driver errors may include storage details. Startup reports this fixed
		// reason instead of exposing the database or filesystem error text.
		return Checkpoint{}, ErrJournalRecoveryUnavailable
	}
	if pending != 0 && pending != 1 {
		return Checkpoint{}, ErrJournalRecoveryUnavailable
	}
	c.RecoveryPending = pending == 1
	if c.Cursor != "" {
		c.ObservedAt = time.UnixMicro(observed).UTC()
	}
	return c, nil
}

func setJournalRecovery(ctx context.Context, tx executor, pending bool) error {
	result, err := tx.ExecContext(ctx, `UPDATE journal_recovery SET pending=? WHERE id=1`, pending)
	if err != nil {
		return err
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		return ErrJournalRecoveryUnavailable
	}
	return nil
}

func cursorHash(cursor string) string {
	sum := sha256.Sum256([]byte(cursor))
	return hex.EncodeToString(sum[:])
}

func (s *Store) JournalSeen(ctx context.Context, cursor string) (bool, error) {
	var exists int
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM journal_seen WHERE cursor_hash=?)`, cursorHash(cursor)).Scan(&exists)
	return exists == 1, err
}

// CommitJournal atomically stores one normalized observation, its event and
// notification (when queue capacity permits), and the acknowledged cursor.
// The bounded overlap cache prevents duplicate accounting during cursor recovery.
func (s *Store) CommitJournal(ctx context.Context, write JournalWrite) (bool, error) {
	release, admitErr := s.beginWrite(ctx, writeCritical)
	if admitErr != nil {
		return false, admitErr
	}
	defer release()

	if write.Cursor == "" || len(write.Cursor) > 4096 || strings.IndexFunc(write.Cursor, func(r rune) bool { return r <= 0x20 || r == 0x7f }) >= 0 || write.ReceivedAt.IsZero() {
		return false, errors.New("invalid journal checkpoint")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO journal_seen(cursor_hash,received_at) VALUES (?,?)`, cursorHash(write.Cursor), write.ReceivedAt.UnixMilli())
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if count > 0 {
		// A cursor already committed before degradation is not new recovery
		// evidence, even when it reappears during bounded replay.
		if write.Trusted {
			if err := setJournalRecovery(ctx, tx, false); err != nil {
				return false, err
			}
		}
		if write.Kind != "" {
			if err := addAuth(ctx, tx, write.ObservedAt, write.Kind, write.SourceRange); err != nil {
				return false, err
			}
		}
		if write.Event != nil {
			if err := insertEvent(ctx, tx, *write.Event); err != nil {
				return false, err
			}
			if err := recordEventNotification(ctx, tx, *write.Event, write.Notification, time.Now().UTC(), s.mergeWindow); err != nil {
				return false, err
			}
		}
		if write.Event == nil && write.Notification != nil {
			if _, err := enqueue(ctx, tx, *write.Notification); err != nil && !errors.Is(err, ErrOutboxFull) {
				return false, err
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO collector_checkpoints(name,cursor,observed_at,updated_at) VALUES ('ssh_journal',?,?,?)
 ON CONFLICT(name) DO UPDATE SET cursor=excluded.cursor,observed_at=MAX(observed_at,excluded.observed_at),updated_at=excluded.updated_at`, write.Cursor, write.ReceivedAt.UnixMicro(), time.Now().UTC().UnixMilli()); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM journal_seen WHERE rowid<=(SELECT MAX(rowid)-10000 FROM journal_seen)`); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return count > 0, nil
}

// InsertEventNotification closes the crash window between event persistence and
// outbox insertion. Queue exhaustion is counted without discarding the event.
func (s *Store) InsertEventNotification(ctx context.Context, event model.Event, message *OutboxMessage) error {
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
	if err := insertEvent(ctx, tx, event); err != nil {
		return err
	}
	if err := recordEventNotification(ctx, tx, event, message, time.Now().UTC(), s.mergeWindow); err != nil {
		return err
	}
	return tx.Commit()
}
