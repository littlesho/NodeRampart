// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func legacyChannelFixtureV10(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "schema-ten.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	event, message := alertFixture("legacy_event", "legacy_incident", "update")
	event.ObservedAt = time.Now().UTC()
	if err := s.InsertEventNotification(context.Background(), event, message); err != nil {
		t.Fatal(err)
	}
	if err := downgradeSnapshotSchemaReference(context.Background(), s.db, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DELETE FROM schema_migrations WHERE version>10`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestChannelSchemaMigrationPreservesLegacyDecisionAndBackup(t *testing.T) {
	ctx := context.Background()
	path := legacyChannelFixtureV10(t)
	legacyBackup := filepath.Join(t.TempDir(), "legacy-ten.db")
	if _, err := RestoreBackup(ctx, path, legacyBackup); err != nil {
		t.Fatal(err)
	}
	if info, err := VerifyBackup(ctx, legacyBackup); err != nil || info.SchemaVersion != 10 {
		t.Fatal(info, err)
	}
	s, err := OpenWithBudget(path, BudgetConfig{MaxBytes: 64 << 20, MinFreeBytes: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var channel, decision, id string
	if err := s.db.QueryRow(`SELECT channel,decision,notification_id FROM event_notifications WHERE event_id='legacy_event'`).Scan(&channel, &decision, &id); err != nil || channel != "telegram" || decision != "queued" || id != "msg_legacy_event" {
		t.Fatal("legacy decision changed", channel, decision, id, err)
	}
	if _, err := s.db.Exec(`INSERT INTO event_notifications(event_id,channel,decision,recorded_at) VALUES('legacy_event','webhook','ineligible',0)`); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "current-eleven.db")
	if _, err := s.Backup(ctx, backup); err != nil {
		t.Fatal(err)
	}
	restored := filepath.Join(t.TempDir(), "restored-eleven.db")
	if _, err := RestoreBackup(ctx, backup, restored); err != nil {
		t.Fatal(err)
	}
	copy, err := Open(restored)
	if err != nil {
		t.Fatal(err)
	}
	defer copy.Close()
	var count int
	if err := copy.db.QueryRow(`SELECT COUNT(*) FROM event_notifications WHERE event_id='legacy_event'`).Scan(&count); err != nil || count != 2 {
		t.Fatal("backup lost channel decisions", count, err)
	}
	if rows, err := copy.db.Query(`PRAGMA foreign_key_check`); err != nil {
		t.Fatal(err)
	} else {
		defer rows.Close()
		if rows.Next() {
			t.Fatal("restored channels violated foreign keys")
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestChannelSchemaMigrationFailureRollsBackAllDDLAndData(t *testing.T) {
	path := legacyChannelFixtureV10(t)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER fail_eleven BEFORE INSERT ON schema_migrations WHEN NEW.version=11 BEGIN SELECT RAISE(ABORT,'synthetic final migration failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenWithBudget(path, BudgetConfig{MaxBytes: 64 << 20, MinFreeBytes: 0}); err == nil {
		t.Fatal("migration failure was ignored")
	}
	db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version, columns, renamed, count int
	for query, target := range map[string]*int{`SELECT MAX(version) FROM schema_migrations`: &version, `SELECT COUNT(*) FROM pragma_table_info('event_notifications') WHERE name='channel'`: &columns, `SELECT COUNT(*) FROM sqlite_schema WHERE name='event_notifications_v10'`: &renamed, `SELECT COUNT(*) FROM event_notifications WHERE event_id='legacy_event' AND notification_id='msg_legacy_event' AND decision='queued'`: &count} {
		if err := db.QueryRow(query).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	if version != 10 || columns != 0 || renamed != 0 || count != 1 {
		t.Fatalf("partial migration persisted version=%d columns=%d renamed=%d count=%d", version, columns, renamed, count)
	}
	if _, err := db.Exec(`DROP TRIGGER fail_eleven`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if info, err := VerifyBackup(context.Background(), path); err != nil || info.SchemaVersion != 10 {
		t.Fatal("rollback left invalid historical snapshot", info, err)
	}
	s, err := OpenWithBudget(path, BudgetConfig{MaxBytes: 64 << 20, MinFreeBytes: 0})
	if err != nil {
		t.Fatal("retry after repaired fixture failed", err)
	}
	defer s.Close()
}
