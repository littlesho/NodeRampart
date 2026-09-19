// SPDX-License-Identifier: MIT

package notify

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/store"
)

type senderFunc func(context.Context, string) error

func (f senderFunc) Send(ctx context.Context, body string) error { return f(ctx, body) }

func openNotifyStore(t *testing.T) *store.Store {
	t.Helper()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func enqueueSynthetic(t *testing.T, db *store.Store, id, destination string, next time.Time) {
	t.Helper()
	inserted, err := db.Enqueue(context.Background(), store.OutboxMessage{
		ID: id, DedupeKey: id, Destination: destination, Body: "synthetic message", NextAttempt: next,
	})
	if err != nil || !inserted {
		t.Fatalf("enqueue succeeded = %v, error = %v", inserted, err)
	}
}

func queueStatus(t *testing.T, db *store.Store) store.QueueStatus {
	t.Helper()
	status, err := db.QueueStatus(context.Background(), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	return status
}

func TestWorkerDestinationCooldownSurvivesPassAndRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "outbox.sqlite")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	now := time.Now().UTC()
	enqueueSynthetic(t, db, "first", "telegram", now.Add(-3*time.Second))
	enqueueSynthetic(t, db, "same-destination", "telegram", now.Add(-2*time.Second))
	enqueueSynthetic(t, db, "other-destination", "other", now.Add(-time.Second))
	calls := 0
	sender := senderFunc(func(context.Context, string) error {
		calls++
		if calls == 1 {
			return &DeliveryError{StatusCode: 429, RateLimited: true, RetryAfter: time.Hour}
		}
		return nil
	})
	worker := &Worker{Store: db, Sender: sender}
	if err := worker.process(ctx); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("sender called %d times; want rate-limited message and other destination only", calls)
	}
	status := queueStatus(t, db)
	if status.Pending != 2 || len(status.Cooldowns) != 1 || status.Cooldowns[0].Until.Before(now.Add(time.Hour)) {
		t.Fatalf("cooldown was not retained: %+v", status)
	}
	if err := worker.process(ctx); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal("subsequent pass bypassed destination cooldown")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	enqueueSynthetic(t, db, "new-after-restart", "telegram", now)
	worker = &Worker{Store: db, Sender: sender}
	if err := worker.process(ctx); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal("restart or newly enqueued message bypassed destination cooldown")
	}
	pending, err := db.Pending(ctx, now.Add(time.Hour+time.Minute), 20)
	if err != nil || len(pending) != 3 {
		t.Fatalf("messages available after cooldown = %d, error = %v", len(pending), err)
	}
	for _, message := range pending {
		wantAttempts := 0
		if message.ID == "first" {
			wantAttempts = 1
		}
		if message.Attempts != wantAttempts {
			t.Fatalf("message %s attempts = %d, want %d", message.ID, message.Attempts, wantAttempts)
		}
	}
}

func TestWorkerRateLimitWithoutHintPausesBatch(t *testing.T) {
	db := openNotifyStore(t)
	now := time.Now().UTC()
	enqueueSynthetic(t, db, "first", "telegram", now.Add(-time.Second))
	enqueueSynthetic(t, db, "second", "telegram", now)
	calls := 0
	worker := &Worker{Store: db, Sender: senderFunc(func(context.Context, string) error {
		calls++
		return &DeliveryError{StatusCode: 429, RateLimited: true}
	})}
	if err := worker.process(context.Background()); err != nil {
		t.Fatal(err)
	}
	status := queueStatus(t, db)
	if calls != 1 || len(status.Cooldowns) != 1 || status.Cooldowns[0].Until.Before(now.Add(30*time.Second)) {
		t.Fatal("429 without a retry hint must pause the destination for at least 30 seconds")
	}
}

func TestWorkerPermanentFailureQuarantinedUntilManualRetry(t *testing.T) {
	db := openNotifyStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	enqueueSynthetic(t, db, "permanent", "telegram", now.Add(-time.Second))
	enqueueSynthetic(t, db, "successful", "telegram", now)
	calls := 0
	worker := &Worker{Store: db, Sender: senderFunc(func(context.Context, string) error {
		calls++
		if calls == 1 {
			return &DeliveryError{StatusCode: 403, Permanent: true}
		}
		return nil
	})}
	if err := worker.process(ctx); err != nil {
		t.Fatal(err)
	}
	status := queueStatus(t, db)
	if status.Pending != 1 || status.Quarantined != 1 || status.LastSent.IsZero() {
		t.Fatalf("permanent failure was not quarantined: %+v", status)
	}
	if err := worker.process(ctx); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal("quarantined message was retried automatically")
	}
	if err := db.RetryNotification(ctx, "permanent", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := worker.process(ctx); err != nil {
		t.Fatal(err)
	}
	if calls != 3 || queueStatus(t, db).Pending != 0 {
		t.Fatal("manual retry did not deliver quarantined message")
	}
}

func TestWorkerQuarantinesExhaustedRetries(t *testing.T) {
	for _, rateLimited := range []bool{false, true} {
		name := "transient"
		if rateLimited {
			name = "rate limited"
		}
		t.Run(name, func(t *testing.T) {
			db := openNotifyStore(t)
			ctx := context.Background()
			now := time.Now().UTC()
			enqueueSynthetic(t, db, "exhausted", "telegram", now.Add(-time.Second))
			for i := 0; i < store.MaxDeliveryAttempts-1; i++ {
				if err := db.MarkFailed(ctx, "exhausted", now.Add(-time.Second), "synthetic transient failure"); err != nil {
					t.Fatal(err)
				}
			}
			calls := 0
			worker := &Worker{Store: db, Sender: senderFunc(func(context.Context, string) error {
				calls++
				if rateLimited {
					return &DeliveryError{StatusCode: 429, RateLimited: true, RetryAfter: time.Hour}
				}
				return errors.New("synthetic private transport failure")
			})}
			if err := worker.process(ctx); err != nil {
				t.Fatal(err)
			}
			status := queueStatus(t, db)
			if status.Pending != 1 || status.Quarantined != 1 {
				t.Fatal("exhausted retry was not retained in quarantine")
			}
			if rateLimited && len(status.Cooldowns) != 1 {
				t.Fatal("last allowed attempt lost destination cooldown")
			}
			if err := worker.process(ctx); err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatal("exhausted retry was sent again")
			}
		})
	}
}

func TestWorkerOnlyMarksConfirmedDeliverySent(t *testing.T) {
	for _, tc := range []struct {
		name    string
		body    string
		success bool
	}{
		{"valid confirmation", `{"ok":true,"result":{"message_id":1}}`, true},
		{"business failure", `{"ok":false,"error_code":500}`, false},
		{"invalid response", `untrusted`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := openNotifyStore(t)
			enqueueSynthetic(t, db, "message", "telegram", time.Now().UTC())
			worker := &Worker{Store: db, Sender: syntheticTelegram(200, strings.NewReader(tc.body), "")}
			if err := worker.process(context.Background()); err != nil {
				t.Fatal(err)
			}
			status := queueStatus(t, db)
			if (status.Pending == 0) != tc.success || (!status.LastSent.IsZero()) != tc.success {
				t.Fatalf("delivery confirmation mismatch: %+v", status)
			}
		})
	}
}

func TestWorkerExcessiveWaitRequiresDestinationResume(t *testing.T) {
	db := openNotifyStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	enqueueSynthetic(t, db, "first", "telegram", now.Add(-time.Second))
	enqueueSynthetic(t, db, "second", "telegram", now)
	calls := 0
	worker := &Worker{Store: db, Sender: senderFunc(func(context.Context, string) error {
		calls++
		return &DeliveryError{StatusCode: 429, RateLimited: true, SuspendDestination: true}
	})}
	if err := worker.process(ctx); err != nil {
		t.Fatal(err)
	}
	status := queueStatus(t, db)
	if calls != 1 || len(status.Cooldowns) != 1 || status.Cooldowns[0].Until.Year() != 9999 {
		t.Fatal("excessive server wait did not persist a destination hold")
	}
	if err := db.RetryNotification(ctx, "first", now); err != nil {
		t.Fatal(err)
	}
	if err := worker.process(ctx); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("message retry bypassed destination hold")
	}
}

func TestWorkerCancellationDoesNotConsumeAttempt(t *testing.T) {
	db := openNotifyStore(t)
	now := time.Now().UTC()
	enqueueSynthetic(t, db, "message", "telegram", now)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	worker := &Worker{Store: db, Sender: senderFunc(func(context.Context, string) error {
		cancel()
		return context.Canceled
	})}
	if err := worker.process(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("expected cancellation")
	}
	pending, err := db.Pending(context.Background(), now.Add(time.Minute), 20)
	if err != nil || len(pending) != 1 || pending[0].Attempts != 0 {
		t.Fatal("shutdown cancellation consumed a delivery attempt")
	}
}
