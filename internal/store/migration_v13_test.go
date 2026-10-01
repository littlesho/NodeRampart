// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func schemaTwelveFixture(t *testing.T, oversized bool) string {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "twelve.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	target := nativeTestPolicy(t, s, "telegram", "A", "prefix", true)
	nativeTestPolicy(t, s, "webhook", "A", "prefix", false)
	event, _ := alertFixture("retained", "inc_retained", "start")
	if err := s.InsertEventNotification(ctx, event, nativeTestChain([]string{"telegram", "webhook"}, event.ID)); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	if _, ok, err := s.ClaimNotification(ctx, "msg_retained_telegram", now.Add(time.Second)); err != nil || !ok {
		t.Fatal(err)
	}
	if err := s.MarkRateLimited(ctx, "msg_retained_telegram", target, now.Add(time.Hour), "synthetic server hold"); err != nil {
		t.Fatal(err)
	}
	if oversized {
		if _, err := s.db.Exec(`WITH RECURSIVE n(v) AS (SELECT 1 UNION ALL SELECT v+1 FROM n WHERE v<1500)
 INSERT INTO notification_outbox(id,dedupe_key,channel,destination,body,created_at,next_attempt,expires_at)
 SELECT 'old_'||v,'old_'||v,'telegram',?,'retained',?,?,? FROM n`, target, now.UnixMilli(), now.UnixMilli(), now.Add(OutboxTTL).UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}
	// Use the published v11/v12 table DDL independently of the new migration.
	for _, statement := range []string{
		`ALTER TABLE notification_targets DROP COLUMN activated_at`,
		`DROP INDEX event_notifications_message_idx`,
		`ALTER TABLE event_notifications RENAME TO fixture_current_decisions`,
		`CREATE TABLE event_notifications (event_id TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
		 channel TEXT NOT NULL DEFAULT 'telegram' CHECK(channel IN ('telegram','webhook')),
		 notification_id TEXT NOT NULL DEFAULT '', decision TEXT NOT NULL CHECK(decision IN ('queued','merged','silenced','ineligible','rejected')),
		 silence_id TEXT NOT NULL DEFAULT '', recorded_at INTEGER NOT NULL, PRIMARY KEY(event_id,channel))`,
		`INSERT INTO event_notifications SELECT event_id,channel,notification_id,decision,silence_id,recorded_at FROM fixture_current_decisions`,
		`DROP TABLE fixture_current_decisions`,
		`CREATE INDEX event_notifications_message_idx ON event_notifications(notification_id)`,
		`DELETE FROM schema_migrations WHERE version>12`,
	} {
		if _, err := s.db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestNativeChannelSchemaMigrationPreservesQueuesTargetsAndPresentation(t *testing.T) {
	ctx := context.Background()
	path := schemaTwelveFixture(t, false)
	if info, err := VerifyBackup(ctx, path); err != nil || info.SchemaVersion != 12 {
		t.Fatal("published schema 12 snapshot rejected", info, err)
	}
	s, err := OpenWithBudget(path, BudgetConfig{MaxBytes: 64 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var version, decisions, targets, pending, attempts, cooldowns int
	var body, language, timezone string
	if err := s.db.QueryRow(`SELECT (SELECT MAX(version) FROM schema_migrations),(SELECT COUNT(*) FROM event_notifications),
 (SELECT COUNT(*) FROM notification_targets),(SELECT COUNT(*) FROM notification_outbox),attempts,body,language,presentation_timezone,
 (SELECT COUNT(*) FROM notification_cooldowns) FROM notification_outbox WHERE id='msg_retained_telegram'`).Scan(&version, &decisions, &targets, &pending, &attempts, &body, &language, &timezone, &cooldowns); err != nil {
		t.Fatal(err)
	}
	if version != 13 || decisions != 2 || targets != 2 || pending != 2 || attempts != 1 || body != "合成摘要 · retained" || language != "zh" || timezone != "Asia/Shanghai|+08:00" || cooldowns != 1 {
		t.Fatal("schema migration changed retained state", version, decisions, targets, pending, attempts, body, language, timezone, cooldowns)
	}
	for _, channel := range nativeTestChannels {
		if _, err := s.db.Exec(`INSERT INTO event_notifications(event_id,channel,decision,recorded_at) VALUES ('retained',?,'ineligible',0)`, channel); err != nil {
			t.Fatal("native decision rejected", channel, err)
		}
	}
	if _, err := s.db.Exec(`INSERT INTO event_notifications(event_id,channel,decision,recorded_at) VALUES ('retained','unapproved','ineligible',0)`); err == nil {
		t.Fatal("unbounded channel accepted")
	}
	backup := filepath.Join(t.TempDir(), "thirteen.db")
	if info, err := s.Backup(ctx, backup); err != nil || info.SchemaVersion != 13 {
		t.Fatal(info, err)
	}
	restoredPath := filepath.Join(t.TempDir(), "restored.db")
	if _, err := RestoreBackup(ctx, backup, restoredPath); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(restoredPath)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if err := restored.db.QueryRow(`SELECT COUNT(*) FROM event_notifications WHERE event_id='retained'`).Scan(&decisions); err != nil || decisions != 8 {
		t.Fatal("restored channel decisions lost", decisions, err)
	}
	if status, err := restored.ForeignKeys(ctx); err != nil || len(status.Violations) != 0 {
		t.Fatal("schema 13 foreign keys invalid", status, err)
	}
	// Cascade still removes only decisions, not historical outbox bodies.
	if _, err := restored.db.Exec(`DELETE FROM events WHERE id='retained'`); err != nil {
		t.Fatal(err)
	}
	if err := restored.db.QueryRow(`SELECT (SELECT COUNT(*) FROM event_notifications),(SELECT COUNT(*) FROM notification_outbox)`).Scan(&decisions, &pending); err != nil || decisions != 0 || pending != 2 {
		t.Fatal("foreign-key behavior changed", decisions, pending, err)
	}
}

func TestNativeChannelMigrationFailureRollsBackExactPublishedSchema(t *testing.T) {
	ctx := context.Background()
	path := schemaTwelveFixture(t, false)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER reject_thirteen BEFORE INSERT ON schema_migrations WHEN NEW.version=13 BEGIN SELECT RAISE(ABORT,'synthetic migration failure'); END`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if s, err := OpenWithBudget(path, BudgetConfig{MaxBytes: 64 << 20}); err == nil {
		s.Close()
		t.Fatal("failed migration succeeded")
	}
	db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version, decisions, bodies, oldTable int
	var ddl string
	if err := db.QueryRow(`SELECT (SELECT MAX(version) FROM schema_migrations),(SELECT COUNT(*) FROM event_notifications),
 (SELECT COUNT(*) FROM notification_outbox WHERE body<>''),(SELECT COUNT(*) FROM sqlite_schema WHERE name='event_notifications_v12'),
 (SELECT sql FROM sqlite_schema WHERE name='event_notifications')`).Scan(&version, &decisions, &bodies, &oldTable, &ddl); err != nil || version != 12 || decisions != 2 || bodies != 2 || oldTable != 0 || strings.Contains(ddl, "feishu") {
		t.Fatal("failed migration left partial DDL/data", version, decisions, bodies, oldTable, err)
	}
	if _, err := db.Exec(`DROP TRIGGER reject_thirteen`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if info, err := VerifyBackup(ctx, path); err != nil || info.SchemaVersion != 12 {
		t.Fatal("rollback no longer validates as published schema 12", info, err)
	}
}

func TestNativeChannelMigrationGrandfathersLargeLegacyQueueWithoutStarvingNewTargets(t *testing.T) {
	ctx := context.Background()
	path := schemaTwelveFixture(t, true)
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	target := nativeTestTarget("telegram", "A")
	now := time.Now().UTC()
	status, err := s.QueueStatus(ctx, now)
	if err != nil || status.Pending != 1502 {
		t.Fatal("migration deleted preexisting over-share bodies", status, err)
	}
	message := nativeTestMessage("telegram", "A", "new_legacy")
	if ok, err := s.Enqueue(ctx, message); ok || !errors.Is(err, ErrOutboxFull) {
		t.Fatal("legacy over-share queue admitted more bodies", ok, err)
	}
	if err := s.ResumeDestination(ctx, target); err != nil {
		t.Fatal(err)
	}
	pending, err := s.PendingDestination(ctx, now.Add(time.Second), 100, target)
	if err != nil || len(pending) != 100 {
		t.Fatal("grandfathered pending messages cannot drain", len(pending), err)
	}
	for _, channel := range nativeTestChannels {
		nativeTestPolicy(t, s, channel, "A", "prefix", true)
		if ok, err := s.Enqueue(ctx, nativeTestMessage(channel, "A", "fresh")); err != nil || !ok {
			t.Fatal("grandfathered receiver starved new channel", channel, ok, err)
		}
	}
}
