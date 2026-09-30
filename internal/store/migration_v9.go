// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"database/sql"
	"fmt"
)

func migrateV9(ctx context.Context, tx *sql.Tx) error {
	for _, statement := range []string{
		`ALTER TABLE report_snapshots ADD COLUMN document_json TEXT NOT NULL DEFAULT ''`,
		`INSERT INTO schema_migrations(version,applied_at) VALUES(9,unixepoch())`,
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("apply database migration 9: %w", err)
		}
	}
	return nil
}
