// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"database/sql"
	"fmt"
)

func migrateV8(ctx context.Context, tx *sql.Tx) error {
	for _, statement := range []string{
		`ALTER TABLE notification_outbox ADD COLUMN channel TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE notification_outbox ADD COLUMN isolated_at INTEGER`,
		`CREATE TABLE notification_targets (channel TEXT PRIMARY KEY, destination TEXT NOT NULL, privacy_mode TEXT NOT NULL, enabled INTEGER NOT NULL CHECK(enabled IN (0,1)))`,
		// Channel-only historical rows have no reliable bot/chat attribution.
		// Retain their bodies for diagnosis; never adopt the next configured target.
		`UPDATE notification_outbox SET channel='telegram', isolated_at=CASE WHEN sent_at IS NULL THEN unixepoch()*1000 ELSE NULL END, lease_until=NULL WHERE destination='telegram'`,
		`INSERT INTO schema_migrations(version,applied_at) VALUES (8,unixepoch())`,
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("apply database migration 8: %w", err)
		}
	}
	return nil
}
