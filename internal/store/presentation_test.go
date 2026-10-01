// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestMergedBodyLocalizedCompleteGolden(t *testing.T) {
	const latest = "<b>safe &amp; latest</b>"
	for _, count := range []int64{1, 2, 1<<63 - 1} {
		for _, item := range []struct{ language, template string }{
			{"en", "<b>%d incident updates</b> · latest observation below; all events retained in the timeline.\n"},
			{"zh", "<b>%d 次事件更新</b> · 以下为最新观察，所有事件保留在时间线。\n"},
		} {
			want := fmt.Sprintf(item.template, count) + latest
			if got := mergedBodyLocalized(count, latest, item.language); got != want {
				t.Fatalf("complete merge text %s/%d differs: %q", item.language, count, got)
			}
		}
	}
}

func presentationFixtureV11(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "eleven.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	event, message := alertFixture("legacy", "legacy_incident", "start")
	message.Body = "<b>Original English body &amp; facts</b>"
	if err := s.InsertEventNotification(context.Background(), event, message); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkFailed(context.Background(), message.ID, time.Now().Add(time.Minute), "synthetic failure"); err != nil {
		t.Fatal(err)
	}
	if err := downgradeSnapshotSchemaReference(context.Background(), s.db, 11); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DELETE FROM schema_migrations WHERE version>11`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPresentationMigrationPreservesBodiesAndBackup(t *testing.T) {
	ctx := context.Background()
	path := presentationFixtureV11(t)
	if info, err := VerifyBackup(ctx, path); err != nil || info.SchemaVersion != 11 {
		t.Fatal(info, err)
	}
	s, err := OpenWithBudget(path, BudgetConfig{MaxBytes: 64 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	pending, err := s.Pending(ctx, time.Now().Add(2*time.Minute), 20)
	if err != nil || len(pending) != 1 {
		t.Fatal(pending, err)
	}
	old := pending[0]
	if old.Language != "en" || old.Timezone != "" || old.Attempts != 1 || old.Body != "<b>Original English body &amp; facts</b>" {
		t.Fatal("legacy presentation was rewritten", old)
	}
	message := OutboxMessage{ID: "zh_body", DedupeKey: "zh_body", Destination: "telegram", Body: "<b>中文事实 &amp; 内容</b>", Language: "zh", Timezone: "Asia/Shanghai|+08:00"}
	if ok, err := s.Enqueue(ctx, message); err != nil || !ok {
		t.Fatal(ok, err)
	}
	backup := filepath.Join(t.TempDir(), "twelve.db")
	if info, err := s.Backup(ctx, backup); err != nil || info.SchemaVersion != schemaVersion {
		t.Fatal(info, err)
	}
	restored := filepath.Join(t.TempDir(), "restored.db")
	if _, err := RestoreBackup(ctx, backup, restored); err != nil {
		t.Fatal(err)
	}
	copy, err := Open(restored)
	if err != nil {
		t.Fatal(err)
	}
	defer copy.Close()
	claimed, ok, err := copy.ClaimNotification(ctx, message.ID, time.Now().Add(time.Second))
	if err != nil || !ok || claimed.Body != message.Body || claimed.Language != "zh" || claimed.Timezone != message.Timezone {
		t.Fatal("backup lost presentation", claimed, ok, err)
	}
	if _, err := copy.db.Exec(`INSERT INTO schema_migrations(version,applied_at) VALUES (?,0)`, schemaVersion+1); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyBackup(ctx, restored); err == nil {
		t.Fatal("future schema accepted")
	}
	copy.Close()
	if future, err := Open(restored); err == nil {
		future.Close()
		t.Fatal("future database accepted")
	}
}

func TestPresentationMigrationFailureRollsBackDDL(t *testing.T) {
	path := presentationFixtureV11(t)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER fail_twelve BEFORE INSERT ON schema_migrations WHEN NEW.version=12 BEGIN SELECT RAISE(ABORT,'synthetic migration failure'); END`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if failed, err := OpenWithBudget(path, BudgetConfig{MaxBytes: 64 << 20}); err == nil {
		failed.Close()
		t.Fatal("migration failure accepted")
	}
	db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version, columns, bodies int
	for query, target := range map[string]*int{
		`SELECT MAX(version) FROM schema_migrations`: &version,
		`SELECT COUNT(*) FROM pragma_table_info('notification_outbox') WHERE name IN ('language','presentation_timezone')`:                  &columns,
		`SELECT COUNT(*) FROM notification_outbox WHERE id='msg_legacy' AND body='<b>Original English body &amp; facts</b>' AND attempts=1`: &bodies,
	} {
		if err := db.QueryRow(query).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	if version != 11 || columns != 0 || bodies != 1 {
		t.Fatal("partial migration survived", version, columns, bodies)
	}
	if _, err := db.Exec(`DROP TRIGGER fail_twelve`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err := VerifyBackup(context.Background(), path); err != nil {
		t.Fatal("rollback invalidated old backup", err)
	}
}

func TestMergeRequiresSavedLanguageAndTimezone(t *testing.T) {
	s := budgetStore(t)
	ctx := context.Background()
	if err := s.ConfigureNotifications(time.Hour); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct{ id, language, timezone, body string }{
		{"en_first", "en", "UTC|+00:00", "English one"},
		{"zh_first", "zh", "UTC|+00:00", "中文一"},
		{"zh_second", "zh", "UTC|+00:00", "中文二"},
		{"en_second", "en", "UTC|+00:00", "English two"},
		{"zh_zone", "zh", "Asia/Kathmandu|+05:45", "中文三"},
		{"legacy", "", "", "Legacy"},
	} {
		e, m := alertFixture(item.id, "one_incident", "update")
		m.Language, m.Timezone, m.Body = item.language, item.timezone, item.body
		if err := s.InsertEventNotification(ctx, e, m); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := s.db.Query(`SELECT language,presentation_timezone,body,merged_count FROM notification_outbox ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		count++
		var language, zone, body string
		var merged int
		if err := rows.Scan(&language, &zone, &body, &merged); err != nil {
			t.Fatal(err)
		}
		if language == "zh" {
			if !strings.Contains(body, "次事件更新") || strings.Contains(body, "incident updates") {
				t.Fatal("mixed merge language", body)
			}
		} else if zone != "" && !strings.Contains(body, "incident updates") {
			t.Fatal("English default lost", body)
		} else if zone == "" && body != "Legacy" {
			t.Fatal("unknown legacy body rewritten", body)
		}
		if zone == "UTC|+00:00" && merged != 2 {
			t.Fatal("same presentation did not merge", language, merged)
		}
	}
	if rows.Err() != nil || count != 4 {
		t.Fatal("cross presentation merged", count, rows.Err())
	}
	for _, language := range []string{"en", "zh"} {
		body := mergedBodyLocalized(1<<63-1, strings.Repeat("中", (4096-mergeBodyReserve)/3), language)
		if len(body) > 4096 || !utf8.ValidString(body) {
			t.Fatal("maximum count exhausted byte reserve", language, len(body))
		}
	}
}

func TestUnknownAndLocalPresentationNeverMerge(t *testing.T) {
	for i, zone := range []string{"", "Local", "Local|UTC+00:00", "Local|UTC+08:00"} {
		t.Run(fmt.Sprintf("context_%d", i), func(t *testing.T) {
			s := budgetStore(t)
			ctx := context.Background()
			if err := s.ConfigureNotifications(time.Hour); err != nil {
				t.Fatal(err)
			}
			for _, id := range []string{"first", "second"} {
				e, m := alertFixture(id, "unknown_context", "update")
				m.Timezone = zone
				if err := s.InsertEventNotification(ctx, e, m); err != nil {
					t.Fatal(err)
				}
			}
			var count, merged int
			if err := s.db.QueryRow(`SELECT COUNT(*),MAX(merged_count) FROM notification_outbox`).Scan(&count, &merged); err != nil {
				t.Fatal(err)
			}
			if count != 2 || merged != 1 {
				t.Fatalf("unproven presentation contexts merged: zone=%q messages=%d merged=%d", zone, count, merged)
			}
			pending, err := s.Pending(ctx, time.Now().Add(time.Second), 20)
			if err != nil || len(pending) != 2 {
				t.Fatal("unknown presentation changed original send eligibility", len(pending), err)
			}
			for _, m := range pending {
				if m.Language != "en" || m.Timezone != zone || strings.Contains(m.Body, "incident updates") {
					t.Fatal("unmergeable saved body rewritten", m)
				}
			}
		})
	}
}

func TestPresentationDoesNotChangeTargetClaimOrCooldown(t *testing.T) {
	s := budgetStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	destination := "telegram:" + strings.Repeat("a", 64)
	if err := s.ConfigureNotificationTarget(ctx, "telegram", destination, "prefix", true, now); err != nil {
		t.Fatal(err)
	}
	for _, language := range []string{"en", "zh"} {
		message := OutboxMessage{ID: language, DedupeKey: language, Channel: "telegram", Destination: destination, PrivacyMode: "prefix", Language: language, Body: language, NextAttempt: now.Add(-time.Second)}
		if ok, err := s.Enqueue(ctx, message); err != nil || !ok {
			t.Fatal(ok, err)
		}
	}
	claimed, ok, err := s.ClaimNotification(ctx, "en", now)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	// Reapplying the same recipient on a language-only restart must preserve the lease.
	if err := s.ConfigureNotificationTarget(ctx, "telegram", destination, "prefix", true, now); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.ClaimNotification(ctx, "en", now.Add(time.Second)); err != nil || ok {
		t.Fatal("language restart revoked claim", ok, err)
	}
	if err := s.MarkRateLimited(ctx, claimed.ID, destination, now.Add(time.Hour), "synthetic rate limit"); err != nil {
		t.Fatal(err)
	}
	if err := s.ConfigureNotificationTarget(ctx, "telegram", destination, "prefix", true, now); err != nil {
		t.Fatal(err)
	}
	pending, err := s.PendingDestination(ctx, now.Add(time.Minute), 20, destination)
	if err != nil || len(pending) != 0 {
		t.Fatal("language reset recipient cooldown", pending, err)
	}
	var isolated, cooldowns int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM notification_outbox WHERE isolated_at IS NOT NULL`).Scan(&isolated); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM notification_cooldowns`).Scan(&cooldowns); err != nil {
		t.Fatal(err)
	}
	if isolated != 0 || cooldowns != 1 {
		t.Fatal("language changed isolation/cooldown", isolated, cooldowns)
	}
	for _, bad := range []OutboxMessage{
		{Language: "fr"}, {Language: "zh", Channel: "webhook"}, {Language: "en", Timezone: strings.Repeat("a", 257)}, {Language: "zh", Timezone: "bad\nzone"},
	} {
		bad.ID, bad.DedupeKey, bad.Destination, bad.Body = "bad", "bad", "telegram", "body"
		if _, err := s.Enqueue(ctx, bad); err == nil {
			t.Fatal("invalid presentation accepted", bad)
		}
	}
}
