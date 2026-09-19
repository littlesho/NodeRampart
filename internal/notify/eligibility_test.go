// SPDX-License-Identifier: MIT

package notify

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/store"
)

func TestWorkerRechecksPrefetchedNotificationEligibility(t *testing.T) {
	for _, state := range []string{"quarantined", "expired", "cooldown", "deferred", "sent"} {
		t.Run(state, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "queue.db")
			db, err := store.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			now := time.Now().UTC()
			enqueueSynthetic(t, db, "first", "telegram", now.Add(-2*time.Second))
			enqueueSynthetic(t, db, "second", "telegram", now.Add(-time.Second))
			calls := 0
			worker := &Worker{Store: db, Sender: senderFunc(func(context.Context, string) error {
				calls++
				if calls != 1 {
					return nil
				}
				// Both rows have been fetched. Change the second row while the
				// first request is in flight, before its own send can begin.
				switch state {
				case "quarantined":
					err = db.QuarantineNotification(ctx, "second", now)
				case "expired":
					var fixture *sql.DB
					fixture, err = sql.Open("sqlite", path)
					if err == nil {
						_, err = fixture.ExecContext(ctx, `UPDATE notification_outbox SET expires_at=? WHERE id='second'`, now.Add(-time.Second).UnixMilli())
						_ = fixture.Close()
					}
				case "cooldown":
					err = db.MarkRateLimited(ctx, "second", "telegram", now.Add(time.Hour), "synthetic cooldown")
				case "deferred":
					err = db.MarkFailed(ctx, "second", now.Add(time.Hour), "synthetic deferral")
				case "sent":
					err = db.MarkSent(ctx, "second", now)
				}
				if err != nil {
					t.Fatal(err)
				}
				return nil
			})}
			if err := worker.process(ctx); err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatalf("sent prefetched %s notification: calls=%d", state, calls)
			}
			items, err := db.Notifications(ctx, "", 20)
			if err != nil || len(items) != 2 {
				t.Fatalf("retained notification count=%d error=%v", len(items), err)
			}
			for _, item := range items {
				if item.ID == "second" && state != "sent" && item.State == "sent" {
					t.Fatal("skipped notification was incorrectly marked sent")
				}
			}
		})
	}
}

func TestNotificationEligibilityRejectsUnavailableState(t *testing.T) {
	db := openNotifyStore(t)
	now := time.Now().UTC()
	if eligible, err := db.NotificationEligible(context.Background(), "missing", now); err != nil || eligible {
		t.Fatalf("missing row eligibility=%v error=%v", eligible, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if eligible, err := db.NotificationEligible(ctx, "missing", now); err == nil || eligible {
		t.Fatal("cancelled eligibility query was admitted")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if eligible, err := db.NotificationEligible(context.Background(), "missing", now); err == nil || eligible {
		t.Fatal("unavailable storage was admitted")
	}
}
