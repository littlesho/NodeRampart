// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"database/sql"
	"time"
)

// A deferred SQLite transaction is not a snapshot until its first real read.
// Record the observation time only after acquiring both the connection and that
// snapshot, so a writer we waited for cannot appear newer than our own AsOf.
func (s *Store) beginReadSnapshot(ctx context.Context) (*sql.Tx, time.Time, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, time.Time{}, err
	}
	var version int
	if err := tx.QueryRowContext(ctx, `SELECT version FROM schema_migrations ORDER BY version DESC LIMIT 1`).Scan(&version); err != nil {
		tx.Rollback()
		return nil, time.Time{}, err
	}
	return tx, time.Now().UTC(), nil
}
