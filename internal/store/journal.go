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
}

type Checkpoint struct {
	Cursor     string
	ObservedAt time.Time
}

func (s *Store) JournalCheckpoint(ctx context.Context) (Checkpoint, error) {
	var c Checkpoint
	var observed int64
	err := s.db.QueryRowContext(ctx, `SELECT cursor,observed_at FROM collector_checkpoints WHERE name='ssh_journal'`).Scan(&c.Cursor, &observed)
	if IsNotFound(err) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	c.ObservedAt = time.UnixMicro(observed).UTC()
	return c, nil
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
