// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"database/sql"
	"fmt"
)

func migrateV4(ctx context.Context, tx *sql.Tx) error {
	for _, statement := range []string{
		`CREATE INDEX events_incident_idx ON events(incident_id,observed_at,id)`,
		`CREATE INDEX events_source_time_idx ON events(source_range,observed_at,id)`,
		`CREATE TABLE event_notifications (
		 event_id TEXT PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE,
		 notification_id TEXT NOT NULL DEFAULT '',
		 decision TEXT NOT NULL CHECK(decision IN ('queued','merged','silenced','ineligible','rejected')),
		 silence_id TEXT NOT NULL DEFAULT '', recorded_at INTEGER NOT NULL)`,
		`CREATE INDEX event_notifications_message_idx ON event_notifications(notification_id)`,
		`CREATE TABLE notification_silences (
		 id TEXT PRIMARY KEY, incident_id TEXT NOT NULL DEFAULT '', kind TEXT NOT NULL DEFAULT '',
		 reason TEXT NOT NULL DEFAULT '', created_at INTEGER NOT NULL, expires_at INTEGER NOT NULL,
		 revoked_at INTEGER, CHECK(incident_id<>'' OR kind<>''), CHECK(expires_at>created_at))`,
		`ALTER TABLE notification_outbox ADD COLUMN incident_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE notification_outbox ADD COLUMN event_kind TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE notification_outbox ADD COLUMN event_phase TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE notification_outbox ADD COLUMN merged_count INTEGER NOT NULL DEFAULT 1 CHECK(merged_count>=1)`,
		`ALTER TABLE notification_outbox ADD COLUMN merge_until INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE notification_outbox ADD COLUMN suppressed_at INTEGER`,
		`ALTER TABLE notification_outbox ADD COLUMN lease_until INTEGER`,
		`CREATE INDEX notification_incident_idx ON notification_outbox(incident_id,event_kind,destination,merge_until)`,
		`CREATE TABLE interface_detail_hourly (
		 hour_utc INTEGER NOT NULL, interface TEXT NOT NULL, rx_bytes INTEGER NOT NULL,
		 tx_bytes INTEGER NOT NULL, rx_packets INTEGER NOT NULL, tx_packets INTEGER NOT NULL,
		 PRIMARY KEY(hour_utc,interface))`,
		`INSERT INTO schema_migrations(version,applied_at) VALUES (4,unixepoch())`,
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("apply database migration 4: %w", err)
		}
	}
	return nil
}
