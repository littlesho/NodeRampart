// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"database/sql"
	"fmt"
)

func migrateV11(ctx context.Context, tx *sql.Tx) error {
	for _, statement := range []string{
		`DROP INDEX event_notifications_message_idx`,
		`ALTER TABLE event_notifications RENAME TO event_notifications_v10`,
		`CREATE TABLE event_notifications (event_id TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
		 channel TEXT NOT NULL DEFAULT 'telegram' CHECK(channel IN ('telegram','webhook')),
		 notification_id TEXT NOT NULL DEFAULT '', decision TEXT NOT NULL CHECK(decision IN ('queued','merged','silenced','ineligible','rejected')),
		 silence_id TEXT NOT NULL DEFAULT '', recorded_at INTEGER NOT NULL, PRIMARY KEY(event_id,channel))`,
		`INSERT INTO event_notifications(event_id,channel,notification_id,decision,silence_id,recorded_at)
		 SELECT event_id,'telegram',notification_id,decision,silence_id,recorded_at FROM event_notifications_v10`,
		`DROP TABLE event_notifications_v10`,
		`CREATE INDEX event_notifications_message_idx ON event_notifications(notification_id)`,
		`INSERT INTO schema_migrations(version,applied_at) VALUES(11,unixepoch())`,
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("apply database migration 11: %w", err)
		}
	}
	return nil
}
