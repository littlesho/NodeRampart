// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func targetID(label string) string {
	return fmt.Sprintf("telegram:%x", sha256.Sum256([]byte(label)))
}

func targetPolicy(t *testing.T, s *Store, destination, privacy string, enabled bool) {
	t.Helper()
	if err := s.ConfigureNotificationTarget(context.Background(), "telegram", destination, privacy, enabled, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
}

func targetMessage(t *testing.T, s *Store, id, destination string) {
	t.Helper()
	var privacyMode string
	if err := s.db.QueryRow(`SELECT privacy_mode FROM notification_targets WHERE channel='telegram'`).Scan(&privacyMode); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.Enqueue(context.Background(), OutboxMessage{ID: id, DedupeKey: id, Channel: "telegram", PrivacyMode: privacyMode, Destination: destination, Body: "synthetic retained body"}); err != nil || !ok {
		t.Fatal("enqueue", ok, err)
	}
}

func TestNotificationTargetSwitchRetainsIsolatedClaimsRetriesAndCooldown(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "targets.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	targetPolicy(t, s, targetID("targetA"), "full", true)
	targetMessage(t, s, "old_claim", targetID("targetA"))
	targetMessage(t, s, "old_retry", targetID("targetA"))
	claim, ok, err := s.ClaimNotification(ctx, "old_claim", time.Now().UTC())
	if err != nil || !ok {
		t.Fatal("claim", ok, err)
	}
	if err := s.MarkRateLimited(ctx, "old_retry", targetID("targetA"), time.Now().Add(time.Hour), "synthetic hold"); err != nil {
		t.Fatal(err)
	}
	targetPolicy(t, s, targetID("targetB"), "full", true)
	allowed, err := s.NotificationDeliveryAllowed(ctx, claim.ID, claim.Destination)
	if err != nil || allowed {
		t.Fatal("old claim remained deliverable", allowed, err)
	}
	if err := s.RetryNotification(ctx, "old_retry", time.Now().UTC()); err == nil {
		t.Fatal("retry revived isolated body")
	}
	if err := s.ResumeDestination(ctx, targetID("targetA")); err != nil {
		t.Fatal(err)
	}
	targetMessage(t, s, "new_message", targetID("targetB"))
	pending, err := s.Pending(ctx, time.Now().Add(time.Second), 20)
	if err != nil || len(pending) != 1 || pending[0].ID != "new_message" {
		t.Fatal("old target leaked", pending, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate another manual configuration change on restart. Switching back is
	// not authorization to migrate previously isolated bodies.
	targetPolicy(t, s, targetID("targetA"), "full", true)
	pending, err = s.Pending(ctx, time.Now().Add(time.Second), 20)
	if err != nil || len(pending) != 0 {
		t.Fatal("restart adopted old backlog", pending, err)
	}
	status, err := s.QueueStatus(ctx, time.Now().UTC())
	if err != nil || status.Isolated != 3 {
		t.Fatal("isolation not retained", status, err)
	}
	var oldBody string
	if err := s.db.QueryRow(`SELECT body FROM notification_outbox WHERE id='old_claim'`).Scan(&oldBody); err != nil || oldBody == "" {
		t.Fatal("body unexpectedly erased", err)
	}
	count, err := s.DiscardIsolatedNotifications(ctx, "telegram", time.Now().UTC())
	if err != nil || count != 3 {
		t.Fatal("explicit discard", count, err)
	}
	if err := s.db.QueryRow(`SELECT body FROM notification_outbox WHERE id='old_claim'`).Scan(&oldBody); err != nil || oldBody != "" {
		t.Fatal("discard retained body", err)
	}
}

func TestNotificationSameTargetRotationAndDisablePause(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "pause.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	targetPolicy(t, s, targetID("stable"), "prefix", true)
	targetMessage(t, s, "retained", targetID("stable"))
	if err := s.MarkFailed(ctx, "retained", time.Now().Add(-time.Second), "synthetic retry"); err != nil {
		t.Fatal(err)
	}
	targetPolicy(t, s, targetID("stable"), "prefix", false)
	if pending, err := s.Pending(ctx, time.Now().UTC(), 20); err != nil || len(pending) != 0 {
		t.Fatal("disabled target sent", pending, err)
	}
	targetPolicy(t, s, targetID("stable"), "prefix", true)
	// A credential rotation produces the same target and retains retry history.
	targetPolicy(t, s, targetID("stable"), "prefix", true)
	pending, err := s.Pending(ctx, time.Now().UTC(), 20)
	if err != nil || len(pending) != 1 || pending[0].Attempts != 1 {
		t.Fatal("same-target retry history lost", pending, err)
	}
	status, err := s.QueueStatus(ctx, time.Now().UTC())
	if err != nil || status.Isolated != 0 {
		t.Fatal("same target isolated", status, err)
	}
}

func TestNotificationPrivacyTighteningAndUnknownIdentityFailClosed(t *testing.T) {
	for _, mode := range []string{"prefix", "hash"} {
		t.Run(mode, func(t *testing.T) {
			s, err := Open(filepath.Join(t.TempDir(), "privacy.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			targetPolicy(t, s, targetID("stable"), "full", true)
			targetMessage(t, s, "full_body", targetID("stable"))
			targetPolicy(t, s, targetID("stable"), mode, true)
			targetMessage(t, s, "private_body", targetID("stable"))
			if _, err := s.Enqueue(context.Background(), OutboxMessage{ID: "stale_full", DedupeKey: "stale_full", Channel: "telegram", PrivacyMode: "full", Destination: targetID("stable"), Body: "synthetic stale full body"}); err != nil {
				t.Fatal(err)
			}
			pending, err := s.Pending(context.Background(), time.Now().Add(time.Second), 20)
			if err != nil || len(pending) != 1 || pending[0].ID != "private_body" {
				t.Fatal("privacy tightening released old body", pending, err)
			}
			targetPolicy(t, s, "telegram:unknown", mode, true)
			targetMessage(t, s, "uncertain_body", "telegram:unknown")
			targetPolicy(t, s, targetID("stable"), mode, true)
			pending, err = s.Pending(context.Background(), time.Now().Add(time.Second), 20)
			if err != nil || len(pending) != 0 {
				t.Fatal("uncertain body adopted", pending, err)
			}
		})
	}
}

func TestNotificationTargetPolicyRejectsCredentialShapedDestination(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "safe-target.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.ConfigureNotificationTarget(context.Background(), "telegram", "telegram:123456789:"+strings.Repeat("A", 35), "prefix", true, time.Now().UTC()); err == nil {
		t.Fatal("credential-shaped target accepted")
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM notification_targets`).Scan(&count); err != nil || count != 0 {
		t.Fatal("invalid target stored", count, err)
	}
}

func legacyV7NotificationDatabase(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "v7.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Enqueue(context.Background(), OutboxMessage{ID: "legacy", DedupeKey: "legacy", Destination: "telegram", Body: "synthetic legacy private body"}); err != nil {
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
	if err := downgradeSnapshotSchemaReference(context.Background(), db, 7); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE journal_recovery SET pending=1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM schema_migrations WHERE version>7`); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestNotificationLegacyMigrationIsolationAndRollback(t *testing.T) {
	path := legacyV7NotificationDatabase(t)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER fail_eight BEFORE INSERT ON schema_migrations WHEN NEW.version=8 BEGIN SELECT RAISE(ABORT,'synthetic failure'); END`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if s, err := Open(path); err == nil {
		s.Close()
		t.Fatal("migration failure ignored")
	}
	db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	var columns int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('notification_outbox') WHERE name IN ('channel','isolated_at')`).Scan(&columns); err != nil || columns != 0 {
		t.Fatal("partial migration committed", columns, err)
	}
	if _, err := db.Exec(`DROP TRIGGER fail_eight`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var recoveryPending int
	if err := s.db.QueryRow(`SELECT pending FROM journal_recovery WHERE id=1`).Scan(&recoveryPending); err != nil || recoveryPending != 1 {
		t.Fatal("published schema 7 recovery state lost", recoveryPending, err)
	}
	targetPolicy(t, s, targetID("new"), "prefix", true)
	pending, err := s.Pending(context.Background(), time.Now().Add(time.Second), 20)
	if err != nil || len(pending) != 0 {
		t.Fatal("legacy body adopted new target", pending, err)
	}
	items, err := s.Notifications(context.Background(), "", 20)
	if err != nil || len(items) != 1 || items[0].State != "isolated" || !strings.Contains(items[0].Destination, "telegram") {
		t.Fatal("legacy body absent from diagnostics", items, err)
	}
}
