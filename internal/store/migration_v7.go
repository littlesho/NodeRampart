// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"database/sql"
	"fmt"
)

func migrateV7(ctx context.Context, tx *sql.Tx) error {
	// Old gaps cannot distinguish unresolved loss from recovered history.
	// Seed no known pending state, without changing any historical evidence.
	for _, statement := range []string{
		`CREATE TABLE journal_recovery (id INTEGER PRIMARY KEY CHECK(id=1), pending INTEGER NOT NULL CHECK(pending IN (0,1)))`,
		`INSERT INTO journal_recovery(id,pending) VALUES (1,0)`,
		`INSERT INTO schema_migrations(version,applied_at) VALUES (7,unixepoch())`,
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("apply database migration 7: %w", err)
		}
	}
	return nil
}
