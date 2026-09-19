// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"database/sql"
	"fmt"
)

const schemaVersion = 6

func migrateV3(ctx context.Context, tx *sql.Tx) error {
	for _, statement := range []string{
		`ALTER TABLE notification_outbox ADD COLUMN quarantined_at INTEGER`,
		`ALTER TABLE notification_outbox ADD COLUMN expires_at INTEGER NOT NULL DEFAULT 0`,
		`UPDATE notification_outbox SET expires_at=created_at+604800000`,
		`CREATE TABLE notification_cooldowns (destination TEXT PRIMARY KEY, until_at INTEGER NOT NULL)`,
		`CREATE TABLE notification_counters (id INTEGER PRIMARY KEY CHECK(id=1), rejected INTEGER NOT NULL DEFAULT 0, expired INTEGER NOT NULL DEFAULT 0, last_sent_at INTEGER NOT NULL DEFAULT 0)`,
		`INSERT INTO notification_counters(id,last_sent_at) SELECT 1,COALESCE(MAX(sent_at),0) FROM notification_outbox`,
		`CREATE TABLE report_snapshots (report_date TEXT PRIMARY KEY, title TEXT NOT NULL, body TEXT NOT NULL, period_start INTEGER NOT NULL, period_end INTEGER NOT NULL, generated_at INTEGER NOT NULL)`,
		`CREATE TABLE coverage_intervals (id INTEGER PRIMARY KEY, name TEXT NOT NULL, state TEXT NOT NULL, started_at INTEGER NOT NULL, ended_at INTEGER)`,
		`CREATE INDEX coverage_period_idx ON coverage_intervals(started_at, ended_at)`,
		`CREATE UNIQUE INDEX coverage_open_idx ON coverage_intervals(name) WHERE ended_at IS NULL`,
		`CREATE TABLE coverage_gaps (id INTEGER PRIMARY KEY, name TEXT NOT NULL, reason TEXT NOT NULL, started_at INTEGER NOT NULL, ended_at INTEGER NOT NULL, count INTEGER NOT NULL)`,
		`CREATE TABLE collector_checkpoints (name TEXT PRIMARY KEY, cursor TEXT NOT NULL, observed_at INTEGER NOT NULL, updated_at INTEGER NOT NULL)`,
		`CREATE TABLE journal_seen (cursor_hash TEXT PRIMARY KEY, received_at INTEGER NOT NULL)`,
		`CREATE INDEX journal_seen_received_idx ON journal_seen(received_at)`,
		`INSERT INTO schema_migrations(version, applied_at) VALUES (3, unixepoch())`,
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("apply database migration 3: %w", err)
		}
	}
	return nil
}
