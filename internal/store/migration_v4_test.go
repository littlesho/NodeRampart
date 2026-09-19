// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

func legacyV3Database(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "legacy.db")
	s, err := Open(path)
	if err != nil {
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
	statements := []string{`DROP TABLE monitor_state`, `DROP TABLE retention_meta`, `DROP TABLE retention_ledger`, `DROP TABLE retention_totals`, `ALTER TABLE report_snapshots DROP COLUMN billing_json`, `DROP TABLE event_notifications`, `DROP TABLE notification_silences`, `DROP TABLE interface_detail_hourly`, `DROP INDEX events_incident_idx`, `DROP INDEX events_source_time_idx`, `DROP INDEX notification_incident_idx`}
	for _, column := range []string{"incident_id", "event_kind", "event_phase", "merged_count", "merge_until", "suppressed_at", "lease_until"} {
		statements = append(statements, `ALTER TABLE notification_outbox DROP COLUMN `+column)
	}
	statements = append(statements, `DELETE FROM schema_migrations WHERE version>=4`, `PRAGMA journal_mode=DELETE`)
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func TestMigrationBudgetRejectsBeforeSchemaChanges(t *testing.T) {
	path := legacyV3Database(t)
	available, err := availableDiskBytes(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if available >= 1<<40 {
		t.Skip("filesystem free space exceeds maximum configurable watermark")
	}
	s, err := OpenWithBudget(path, BudgetConfig{MaxBytes: 64 << 20, MinFreeBytes: int64(available)})
	if err == nil {
		s.Close()
		t.Fatal("migration ignored free-space admission headroom")
	}
	if !errors.Is(err, ErrStorageBudget) {
		t.Fatalf("wrong refusal: %v", err)
	}
	info, err := VerifyBackup(context.Background(), path)
	if err != nil || info.SchemaVersion != 3 {
		t.Fatalf("failed admission changed legacy schema: %+v %v", info, err)
	}
	s, err = OpenWithBudget(path, BudgetConfig{MaxBytes: 64 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var version int
	if err := s.db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil || version != schemaVersion {
		t.Fatal("migration did not resume")
	}
	budget, err := s.BudgetStatus(context.Background())
	if err != nil || budget.JournalMode != "delete" || budget.DatabaseBytes > budget.DatabaseLimitBytes {
		t.Fatal("migration escaped bounded journal mode")
	}
}

func TestMigrationFailureRollsBackDDLAndVersion(t *testing.T) {
	path := legacyV3Database(t)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	// A bounded fault at the final migration marker exercises rollback after
	// all schema-4 DDL, not just refusal before migration starts.
	if _, err := db.Exec(`CREATE TRIGGER fail_schema_four BEFORE INSERT ON schema_migrations WHEN NEW.version=4 BEGIN SELECT RAISE(ABORT,'fixture final migration failure'); END`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	s, err := OpenWithBudget(path, BudgetConfig{MaxBytes: 64 << 20})
	if err == nil {
		s.Close()
		t.Fatal("migration failure not propagated")
	}
	db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DROP TRIGGER fail_schema_four`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	info, err := VerifyBackup(context.Background(), path)
	if err != nil || info.SchemaVersion != 3 {
		t.Fatalf("migration partially committed: %+v %v", info, err)
	}
}
