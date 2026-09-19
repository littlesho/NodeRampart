// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"database/sql"
	"fmt"
)

func migrateV5(ctx context.Context, tx *sql.Tx) error {
	for _, statement := range []string{
		`ALTER TABLE report_snapshots ADD COLUMN billing_json TEXT NOT NULL DEFAULT '' CHECK(length(CAST(billing_json AS BLOB))<=16384)`,
		`INSERT INTO schema_migrations(version,applied_at) VALUES (5,unixepoch())`,
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("apply database migration 5: %w", err)
		}
	}
	return nil
}
