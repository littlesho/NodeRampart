// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func legacyV4Report(t *testing.T) (string, ReportSnapshot) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "legacy.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	r := ReportSnapshot{Date: "2026-01-01", Title: "legacy", Body: "retained body", PeriodStart: now.Add(-time.Hour), PeriodEnd: now, GeneratedAt: now}
	if err := s.SaveReport(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range []string{`DROP TABLE journal_recovery`, `DROP TABLE monitor_state`, `DROP TABLE retention_meta`, `DROP TABLE retention_ledger`, `DROP TABLE retention_totals`, `ALTER TABLE report_snapshots DROP COLUMN billing_json`, `DELETE FROM schema_migrations WHERE version>=5`, `PRAGMA journal_mode=DELETE`} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	return path, r
}

func TestMigrationV5PreservesOldReportsAndBoundsPricing(t *testing.T) {
	path, original := legacyV4Report(t)
	ctx := context.Background()
	info, err := VerifyBackup(ctx, path)
	if err != nil || info.SchemaVersion != 4 {
		t.Fatal("legacy backup rejected", err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r, err := s.Report(ctx, original.Date)
	if err != nil || r.Billing != nil || r != original {
		t.Fatal("migration altered archive", err)
	}
	if _, err := s.db.Exec(`UPDATE report_snapshots SET billing_json=?`, strings.Repeat("x", 16385)); err == nil {
		t.Fatal("unbounded snapshot stored")
	}
	if _, err := s.db.Exec(`UPDATE report_snapshots SET billing_json='{}'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Report(ctx, original.Date); err == nil {
		t.Fatal("corrupt pricing silently ignored")
	}
	// A malformed historic snapshot is immutable, not silently rebuilt.
	if inserted, err := s.SaveReportIfAbsent(ctx, original); err != nil || inserted {
		t.Fatal("existing archive replaced", err)
	}
}

func TestMigrationV5FailureRollsBackColumnAndVersion(t *testing.T) {
	path, _ := legacyV4Report(t)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER fail_schema_five BEFORE INSERT ON schema_migrations WHEN NEW.version=5 BEGIN SELECT RAISE(ABORT,'synthetic migration failure'); END`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	s, err := Open(path)
	if err == nil {
		s.Close()
		t.Fatal("migration failure not propagated")
	}
	db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DROP TRIGGER fail_schema_five`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	info, err := VerifyBackup(context.Background(), path)
	if err != nil || info.SchemaVersion != 4 {
		t.Fatal("partial schema migration committed", err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal("migration cannot retry", err)
	}
	s.Close()
}
