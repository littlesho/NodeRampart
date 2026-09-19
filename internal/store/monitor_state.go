// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/littlesho/NodeRampart/internal/api"
	"github.com/littlesho/NodeRampart/internal/model"
)

var ErrMonitorConflict = errors.New("monitor state revision conflict")

type MonitorState struct {
	Key       string          `json:"key"`
	Revision  int64           `json:"revision"`
	UpdatedAt time.Time       `json:"updated_at_utc"`
	Data      json.RawMessage `json:"data"`
}

func validMonitorKey(key string) bool {
	switch key {
	case "budget_month_bytes", "budget_month_cost", "budget_day_bytes", "budget_day_growth",
		"health_sensor", "health_interface_counter", "health_ssh_journal", "health_storage", "health_geoip_update":
		return true
	}
	return false
}

func (s *Store) MonitorStates(ctx context.Context) ([]MonitorState, error) {
	return readMonitorStates(ctx, s.db)
}

func readMonitorStates(ctx context.Context, db timelineReader) ([]MonitorState, error) {
	rows, err := db.QueryContext(ctx, `SELECT key,revision,updated_at,CASE WHEN length(CAST(data AS BLOB))<=16384 THEN data ELSE NULL END FROM monitor_state ORDER BY key LIMIT 33`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	states := []MonitorState{}
	for rows.Next() {
		var state MonitorState
		var at int64
		var data string
		if err := rows.Scan(&state.Key, &state.Revision, &at, &data); err != nil {
			return nil, err
		}
		if len(states) >= 32 || !validMonitorKey(state.Key) || state.Revision < 1 || !json.Valid([]byte(data)) {
			return nil, errors.New("invalid persisted monitor state")
		}
		state.UpdatedAt = time.UnixMilli(at).UTC()
		state.Data = json.RawMessage(data)
		states = append(states, state)
	}
	return states, rows.Err()
}

// CommitMonitorState treats an absent key as revision zero. The next revision
// is expectedRevision+1 regardless of the caller's next.Revision field. A
// successful return is the only acknowledgement of the state and its events.
func (s *Store) CommitMonitorState(ctx context.Context, next MonitorState, expectedRevision int64, events []model.Event, messages []*OutboxMessage) error {
	if !validMonitorKey(next.Key) || expectedRevision < 0 || expectedRevision == math.MaxInt64 || next.UpdatedAt.IsZero() || len(next.Data) > 16384 || !json.Valid(next.Data) || len(events) > 2 || len(events) != len(messages) {
		return errors.New("invalid bounded monitor transition")
	}
	seen := make(map[string]bool, len(events))
	for _, event := range events {
		if !api.ValidID(event.ID) || seen[event.ID] || event.ObservedAt.IsZero() || event.Kind != next.Key {
			return errors.New("invalid monitor transition event")
		}
		seen[event.ID] = true
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
	var previous, previousAt int64
	err = tx.QueryRowContext(ctx, `SELECT revision,updated_at FROM monitor_state WHERE key=?`, next.Key).Scan(&previous, &previousAt)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if previous != expectedRevision {
		return ErrMonitorConflict
	}
	if next.UpdatedAt.UnixMilli() < previousAt {
		return errors.New("monitor state time cannot move backwards")
	}
	if previous == 0 {
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM monitor_state`).Scan(&count); err != nil {
			return err
		}
		if count >= 32 {
			return errors.New("monitor state capacity reached")
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO monitor_state(key,revision,updated_at,data) VALUES (?,?,?,?) ON CONFLICT(key) DO UPDATE SET revision=excluded.revision,updated_at=excluded.updated_at,data=excluded.data`, next.Key, expectedRevision+1, next.UpdatedAt.UnixMilli(), string(next.Data)); err != nil {
		return err
	}
	for i, event := range events {
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM events WHERE id=?)`, event.ID).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return errors.New("monitor event identity already exists")
		}
		if err := insertEvent(ctx, tx, event); err != nil {
			return err
		}
		// The observation remains frozen across retries, but silence and merge
		// eligibility are decided when this transaction actually admits it.
		if err := recordEventNotification(ctx, tx, event, messages[i], time.Now().UTC(), s.mergeWindow); err != nil {
			return err
		}
	}
	return tx.Commit()
}
