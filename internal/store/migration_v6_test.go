// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func legacyV5Database(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "v5.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, query := range []string{`DROP TABLE journal_recovery`, `DROP TABLE monitor_state`, `DROP TABLE retention_meta`, `DROP TABLE retention_ledger`, `DROP TABLE retention_totals`, `DELETE FROM schema_migrations WHERE version>=6`, `PRAGMA journal_mode=DELETE`} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func TestMigrationV6RollbackAndUnknownPriorHistory(t *testing.T) {
	ctx := context.Background()
	path := legacyV5Database(t)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER fail_six BEFORE INSERT ON schema_migrations WHEN NEW.version=6 BEGIN SELECT RAISE(ABORT,'synthetic migration failure'); END`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if s, err := OpenWithBudget(path, BudgetConfig{MaxBytes: 64 << 20}); err == nil {
		s.Close()
		t.Fatal("migration fault ignored")
	}
	db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DROP TRIGGER fail_six`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if info, err := VerifyBackup(ctx, path); err != nil || info.SchemaVersion != 5 {
		t.Fatal("partial schema 6", info, err)
	}
	before := time.Now().UTC().Add(-time.Second)
	s, err := OpenWithBudget(path, BudgetConfig{MaxBytes: 64 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	view, err := s.Retention(ctx, RetentionQuery{Start: before.AddDate(0, -1, 0), End: time.Now().UTC(), Limit: 10})
	if err != nil || len(view.Entries) != 0 || len(view.Totals) != 0 || view.TrackingStarted.Before(before) {
		t.Fatal("invented prior history", view, err)
	}
	if _, err := s.db.Exec(`INSERT INTO monitor_state(key,revision,updated_at,data) VALUES (NULL,1,0,'{}')`); err == nil {
		t.Fatal("NULL key bypassed fixed monitor capacity")
	}
	if _, err := s.db.Exec(`INSERT INTO retention_totals VALUES ('arbitrary','time_expiry',1,1,0,0,0,0)`); err == nil {
		t.Fatal("arbitrary ledger category stored")
	}
	if _, err := s.Backup(ctx, filepath.Join(t.TempDir(), "six.db")); err != nil {
		t.Fatal(err)
	}
}

func TestMigrationV6PageLimitLeavesSchemaFive(t *testing.T) {
	path := legacyV5Database(t)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`VACUUM`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	var pages int64
	if err := db.QueryRow(`PRAGMA page_count`).Scan(&pages); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(fmt.Sprintf(`PRAGMA max_page_count=%d`, pages)); err != nil {
		db.Close()
		t.Fatal(err)
	}
	s := &Store{db: db, path: path}
	if err := s.migrate(context.Background()); err == nil {
		db.Close()
		t.Fatal("schema 6 exceeded page cap")
	}
	db.Close()
	if info, err := VerifyBackup(context.Background(), path); err != nil || info.SchemaVersion != 5 {
		t.Fatal("page limit left partial migration", info, err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal("migration cannot retry", err)
	}
	s.Close()
}
