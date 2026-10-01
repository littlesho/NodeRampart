// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"database/sql"
	"fmt"
)

// Presentation belongs to the saved message, never to recipient identity.
// Existing bodies stay byte-for-byte unchanged and retain their English default.
func migrateV12(ctx context.Context, tx *sql.Tx) error {
	for _, statement := range []string{
		`ALTER TABLE notification_outbox ADD COLUMN language TEXT NOT NULL DEFAULT 'en' CHECK(language IN ('en','zh'))`,
		`ALTER TABLE notification_outbox ADD COLUMN presentation_timezone TEXT NOT NULL DEFAULT '' CHECK(length(CAST(presentation_timezone AS BLOB))<=256)`,
		`INSERT INTO schema_migrations(version,applied_at) VALUES(12,unixepoch())`,
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("apply database migration 12: %w", err)
		}
	}
	return nil
}
