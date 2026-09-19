// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOutboxUTF8ByteQuota(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	body := strings.Repeat("界", 1333)
	now := time.Now().UTC()
	// A valid retained quarantine consumes the same bytes as a pending message.
	if _, err := db.db.ExecContext(ctx, `WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<8390)
 INSERT INTO notification_outbox(id,dedupe_key,destination,body,next_attempt,created_at,expires_at,quarantined_at)
 SELECT x,x,'telegram',?,?,?, ?,? FROM n`, body, now.UnixMilli(), now.UnixMilli(), now.Add(OutboxTTL).UnixMilli(), now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	// 8,390 * 3,999 = 33,551,610: 2,822 bytes remain beneath the 32 MiB cap.
	inserted, err := db.Enqueue(ctx, OutboxMessage{DedupeKey: "extra", Destination: "telegram", Body: body})
	if inserted || err == nil {
		t.Fatalf("UTF-8 quota accepted body: %v %v", inserted, err)
	}
	status, err := db.QueueStatus(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if status.PendingBytes != 8390*3999 || status.Rejected != 1 || status.Quarantined != 8390 {
		t.Fatalf("incorrect queue accounting: %+v", status)
	}
}

func TestQueueCooldownSurvivesRestartAndRetry(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	now := time.Now().UTC()
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"one", "two"} {
		if _, err := db.Enqueue(ctx, OutboxMessage{ID: id, DedupeKey: id, Destination: "telegram", Body: id, NextAttempt: now}); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.MarkRateLimited(ctx, "one", "telegram", now.Add(time.Hour), "rate limited"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.RetryNotification(ctx, "one", now); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Pending(ctx, now.Add(time.Minute), 20)
	if err != nil || len(rows) != 0 {
		t.Fatalf("cooldown bypassed: %v %v", rows, err)
	}
	rows, err = db.Pending(ctx, now.Add(time.Hour+time.Second), 20)
	if err != nil || len(rows) != 2 {
		t.Fatalf("cooldown did not end: %v %v", rows, err)
	}
	for i := 0; i < MaxDeliveryAttempts; i++ {
		if err := db.MarkFailed(ctx, "two", now, "transient failure"); err != nil {
			t.Fatal(err)
		}
	}
	status, err := db.QueueStatus(ctx, now)
	if err != nil || status.Quarantined != 1 {
		t.Fatalf("retry bound lost: %+v %v", status, err)
	}
	if err := db.MarkSent(ctx, "one", now); err != nil {
		t.Fatal(err)
	}
	if err := db.ExpireNotifications(ctx, now.Add(OutboxTTL+time.Second)); err != nil {
		t.Fatal(err)
	}
	status, err = db.QueueStatus(ctx, now.Add(OutboxTTL+time.Second))
	if err != nil || status.Pending != 0 || status.Expired != 1 || !status.LastSent.Equal(now.Truncate(time.Millisecond)) {
		t.Fatalf("expiry/delivery accounting lost: %+v %v", status, err)
	}
	if err := db.RetryNotification(ctx, "two", now.Add(OutboxTTL+time.Second)); err == nil {
		t.Fatal("expired body was resurrected")
	}
}
