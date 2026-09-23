// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/model"
)

func rewriteSnapshotTable(t *testing.T, db *sql.DB, table, from, to string) {
	t.Helper()
	var definition string
	if err := db.QueryRow(`SELECT sql FROM sqlite_schema WHERE type='table' AND name=?`, table).Scan(&definition); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(definition, from) {
		t.Fatalf("fixture SQL lacks %q", from)
	}
	var indexes []string
	rows, err := db.Query(`SELECT sql FROM sqlite_schema WHERE type='index' AND tbl_name=? AND sql IS NOT NULL`, table)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var index string
		if err := rows.Scan(&index); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		indexes = append(indexes, index)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	rows.Close()
	if _, err := db.Exec(`DROP TABLE ` + table); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(strings.Replace(definition, from, to, 1)); err != nil {
		t.Fatal(err)
	}
	for _, index := range indexes {
		if _, err := db.Exec(index); err != nil {
			t.Fatal(err)
		}
	}
}

func TestBackupRejectsMissingOrChangedSchemaConstraints(t *testing.T) {
	cases := []struct{ name, table, from, to string }{
		{"primary_key", "interface_hourly", "hour_utc INTEGER PRIMARY KEY", "hour_utc INTEGER"},
		{"outbox_deduplication", "notification_outbox", "dedupe_key TEXT NOT NULL UNIQUE", "dedupe_key TEXT NOT NULL"},
		{"affinity", "interface_hourly", "rx_bytes INTEGER NOT NULL", "rx_bytes TEXT NOT NULL"},
		{"not_null", "events", "severity TEXT NOT NULL", "severity TEXT"},
		{"default", "notification_outbox", "attempts INTEGER NOT NULL DEFAULT 0", "attempts INTEGER NOT NULL DEFAULT 1"},
		{"unique_collation", "notification_outbox", "dedupe_key TEXT NOT NULL UNIQUE", "dedupe_key TEXT COLLATE NOCASE NOT NULL UNIQUE"},
		{"extra_check", "interface_hourly", "rx_bytes INTEGER NOT NULL", "rx_bytes INTEGER NOT NULL CHECK(rx_bytes=0)"},
		{"extra_foreign_key", "interface_hourly", "rx_bytes INTEGER NOT NULL", "rx_bytes INTEGER NOT NULL REFERENCES events(id)"},
		{"missing_official_check", "notification_counters", "CHECK(id=1)", ""},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			s := budgetStore(t)
			path := filepath.Join(t.TempDir(), "incompatible.db")
			ctx := context.Background()
			if _, err := s.Backup(ctx, path); err != nil {
				t.Fatal(err)
			}
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			rewriteSnapshotTable(t, db, item.table, item.from, item.to)
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := VerifyBackup(ctx, path); err == nil {
				t.Fatal("incompatible constraint accepted")
			}
			output := filepath.Join(t.TempDir(), "restored.db")
			if _, err := RestoreBackup(ctx, path, output); err == nil {
				t.Fatal("incompatible constraint restored successfully")
			}
		})
	}
}

func TestBackupRejectsChangedPartialUniquePredicate(t *testing.T) {
	s := budgetStore(t)
	path := filepath.Join(t.TempDir(), "predicate.db")
	ctx := context.Background()
	if _, err := s.Backup(ctx, path); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DROP INDEX coverage_open_idx; CREATE UNIQUE INDEX coverage_open_idx ON coverage_intervals(name) WHERE ended_at IS NOT NULL`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()
	if _, err := VerifyBackup(ctx, path); err == nil {
		t.Fatal("wrong partial uniqueness predicate accepted")
	}
}

func TestBackupRejectsPartialIndexCommentPredicateSpoof(t *testing.T) {
	s := budgetStore(t)
	path := filepath.Join(t.TempDir(), "comment-predicate.db")
	ctx := context.Background()
	if _, err := s.Backup(ctx, path); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DROP INDEX coverage_open_idx; CREATE UNIQUE INDEX coverage_open_idx ON coverage_intervals(name) WHERE 0 -- WHERE ENDED_AT IS NULL`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()
	if _, err := VerifyBackup(ctx, path); err == nil {
		t.Fatal("SQL comment disguised an ineffective unique index")
	}
}

func TestSchemaTokensPreserveStringLiteralContents(t *testing.T) {
	withSpace, err := schemaTokens(`CREATE TABLE t(v TEXT DEFAULT 'a b')`)
	if err != nil {
		t.Fatal(err)
	}
	withoutSpace, err := schemaTokens(`CREATE TABLE t(v TEXT DEFAULT 'ab')`)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Equal(withSpace, withoutSpace) {
		t.Fatal("literal whitespace was erased")
	}
	tokens, err := schemaTokens(`CREATE TABLE t(v TEXT DEFAULT 'a''b -- CHECK(1)')`)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, token := range tokens {
		if token.Kind == 's' && token.Text == "a'b -- CHECK(1)" {
			found = true
		}
	}
	if !found {
		t.Fatal("escaped literal contents were changed or parsed as SQL")
	}
}

func TestBackupAcceptsEquivalentRenamedUniqueIndex(t *testing.T) {
	s := budgetStore(t)
	path := filepath.Join(t.TempDir(), "renamed-index.db")
	ctx := context.Background()
	if _, err := s.Backup(ctx, path); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DROP INDEX coverage_open_idx; CREATE UNIQUE INDEX renamed_coverage_open ON coverage_intervals(name) WHERE ended_at IS NULL`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()
	if _, err := VerifyBackup(ctx, path); err != nil {
		t.Fatalf("equivalent index name changed compatibility: %v", err)
	}
}

func TestBackupCreateTableAsSelectCannotRemoveRestoreKeys(t *testing.T) {
	s := budgetStore(t)
	path := filepath.Join(t.TempDir(), "missing-pk.db")
	ctx := context.Background()
	if _, err := s.Backup(ctx, path); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{`ALTER TABLE interface_hourly RENAME TO interface_original`, `CREATE TABLE interface_hourly AS SELECT * FROM interface_original`, `DROP TABLE interface_original`} {
		if _, err := db.Exec(statement); err != nil {
			db.Close()
			t.Fatal(err)
		}
	}
	db.Close()
	if _, err := VerifyBackup(ctx, path); err == nil {
		t.Fatal("CREATE TABLE AS SELECT snapshot without UPSERT keys verified")
	}
}

func TestBackupAcceptsAndMigratesAllSupportedSchemaVersions(t *testing.T) {
	for version := 1; version <= schemaVersion; version++ {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			s := budgetStore(t)
			dir := t.TempDir()
			path := filepath.Join(dir, "legacy.db")
			ctx := context.Background()
			if _, err := s.Backup(ctx, path); err != nil {
				t.Fatal(err)
			}
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			var statements []string
			if version < 7 {
				statements = append(statements, `DROP TABLE journal_recovery`)
			}
			if version < 6 {
				statements = append(statements, `DROP TABLE monitor_state`, `DROP TABLE retention_meta`, `DROP TABLE retention_ledger`, `DROP TABLE retention_totals`)
			}
			if version < 5 {
				statements = append(statements, `ALTER TABLE report_snapshots DROP COLUMN billing_json`)
			}
			if version < 4 {
				statements = append(statements, `DROP TABLE event_notifications`, `DROP TABLE notification_silences`, `DROP TABLE interface_detail_hourly`,
					`DROP INDEX events_incident_idx`, `DROP INDEX events_source_time_idx`, `DROP INDEX notification_incident_idx`)
				for _, column := range []string{"incident_id", "event_kind", "event_phase", "merged_count", "merge_until", "suppressed_at", "lease_until"} {
					statements = append(statements, `ALTER TABLE notification_outbox DROP COLUMN `+column)
				}
			}
			if version < 3 {
				for _, table := range []string{"notification_cooldowns", "notification_counters", "report_snapshots", "coverage_intervals", "coverage_gaps", "collector_checkpoints", "journal_seen"} {
					statements = append(statements, `DROP TABLE `+table)
				}
				statements = append(statements, `ALTER TABLE notification_outbox DROP COLUMN quarantined_at`, `ALTER TABLE notification_outbox DROP COLUMN expires_at`)
			}
			if version < 2 {
				for _, column := range []string{"ipc_dropped_batches", "ipc_dropped_packets", "ipc_dropped_bytes", "health_counter_saturations"} {
					statements = append(statements, `ALTER TABLE collector_health_hourly DROP COLUMN `+column)
				}
			}
			for _, statement := range statements {
				if _, err := db.Exec(statement); err != nil {
					db.Close()
					t.Fatal(err)
				}
			}
			if _, err := db.Exec(`DELETE FROM schema_migrations WHERE version>?`, version); err != nil {
				db.Close()
				t.Fatal(err)
			}
			db.Close()
			info, err := VerifyBackup(ctx, path)
			if err != nil || info.SchemaVersion != version {
				t.Fatalf("valid version %d rejected: %#v %v", version, info, err)
			}
			target := filepath.Join(dir, "restored.db")
			if _, err := RestoreBackup(ctx, path, target); err != nil {
				t.Fatal(err)
			}
			restored, err := Open(target)
			if err != nil {
				t.Fatal(err)
			}
			defer restored.Close()
			if err := restored.AddInterface(ctx, model.InterfaceTotals{HourUTC: time.Now().UTC(), RXBytes: 7}); err != nil {
				t.Fatalf("restored UPSERT failed: %v", err)
			}
		})
	}
}
