// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"database/sql"
	"fmt"
)

// Rebuild the only existing channel CHECK constraint and add an activation
// boundary for native daily summaries. Bodies, presentation and sensor commits
// remain unchanged; legacy target activation defaults preserve old behavior.
func migrateV13(ctx context.Context, tx *sql.Tx) error {
	for _, statement := range []string{
		`ALTER TABLE notification_targets ADD COLUMN activated_at INTEGER NOT NULL DEFAULT 0`,
		`DROP INDEX event_notifications_message_idx`,
		`ALTER TABLE event_notifications RENAME TO event_notifications_v12`,
		`CREATE TABLE event_notifications (event_id TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
		 channel TEXT NOT NULL DEFAULT 'telegram' CHECK(channel IN (` + notificationChannelsSQL + `)),
		 notification_id TEXT NOT NULL DEFAULT '', decision TEXT NOT NULL CHECK(decision IN ('queued','merged','silenced','ineligible','rejected')),
		 silence_id TEXT NOT NULL DEFAULT '', recorded_at INTEGER NOT NULL, PRIMARY KEY(event_id,channel))`,
		`INSERT INTO event_notifications(event_id,channel,notification_id,decision,silence_id,recorded_at)
		 SELECT event_id,channel,notification_id,decision,silence_id,recorded_at FROM event_notifications_v12`,
		`DROP TABLE event_notifications_v12`,
		`CREATE INDEX event_notifications_message_idx ON event_notifications(notification_id)`,
		`INSERT INTO schema_migrations(version,applied_at) VALUES(13,unixepoch())`,
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("apply database migration 13: %w", err)
		}
	}
	return nil
}
