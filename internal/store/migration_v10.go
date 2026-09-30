// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"database/sql"
	"fmt"
)

func migrateV10(ctx context.Context, tx *sql.Tx) error {
	for _, statement := range []string{
		`CREATE TABLE sensor_watermarks (session_id TEXT NOT NULL, interface TEXT NOT NULL, sequence INTEGER NOT NULL CHECK(sequence>0), sent_at_us INTEGER NOT NULL, committed_at INTEGER NOT NULL, events_complete INTEGER NOT NULL CHECK(events_complete IN(0,1)), notifications_complete INTEGER NOT NULL CHECK(notifications_complete IN(0,1)), complete INTEGER NOT NULL CHECK(complete IN(0,1)), reason TEXT NOT NULL, sequence_gaps INTEGER NOT NULL DEFAULT 0, duplicates INTEGER NOT NULL DEFAULT 0, health_json TEXT NOT NULL DEFAULT '{}', PRIMARY KEY(session_id,interface))`,
		`CREATE TABLE sensor_commit_state (id INTEGER PRIMARY KEY CHECK(id=1), retired_before_us INTEGER NOT NULL DEFAULT 0)`,
		`INSERT INTO sensor_commit_state(id) VALUES (1)`,
		`INSERT INTO schema_migrations(version,applied_at) VALUES (10,unixepoch())`,
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("apply database migration 10: %w", err)
		}
	}
	return nil
}
