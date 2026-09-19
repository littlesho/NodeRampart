// SPDX-License-Identifier: MIT

package report

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/store"
)

func TestBackfillCancellationWhileArchiveBusy(t *testing.T) {
	b, _ := backfillBuilder(t, "UTC")
	now := time.Now().UTC()
	date := now.AddDate(0, 0, -2).Format(time.DateOnly)
	release, err := b.lockArchive(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	type response struct {
		result BackfillResult
		err    error
	}
	done := make(chan response, 1)
	go func() { result, err := b.Backfill(ctx, date, date, now); done <- response{result, err} }()
	<-ctx.Done()
	select {
	case got := <-done:
		if !errors.Is(got.err, context.DeadlineExceeded) || got.result.Complete || got.result.NextDate != date || got.result.StoppedReason != "cancelled" {
			t.Fatalf("deadline lost progress: %+v %v", got.result, got.err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("backfill kept waiting after its deadline")
	}
	if _, err := b.Store.Report(context.Background(), date); !store.IsNotFound(err) {
		t.Fatal("cancelled waiter created a snapshot")
	}
}

func TestSchedulerConflictStillBackfillsOtherDates(t *testing.T) {
	for _, delivered := range []bool{false, true} {
		t.Run(map[bool]string{false: "not-delivered", true: "already-delivered"}[delivered], func(t *testing.T) {
			b, _ := backfillBuilder(t, "UTC")
			now := time.Now().UTC().Truncate(24 * time.Hour).Add(12 * time.Hour)
			yesterday := now.AddDate(0, 0, -1).Format(time.DateOnly)
			if _, err := b.Backfill(context.Background(), yesterday, yesterday, now); err != nil {
				t.Fatal(err)
			}
			before, err := b.Store.Report(context.Background(), yesterday)
			if err != nil {
				t.Fatal(err)
			}
			if delivered {
				if err := b.Store.MarkReportGenerated(context.Background(), yesterday, "telegram", now); err != nil {
					t.Fatal(err)
				}
			}
			b.Location = time.FixedZone("synthetic-plus-one", 3600)
			scheduler := Scheduler{Store: b.Store, Builder: b, DailyAt: "09:00", Destination: "telegram", BackfillDays: 7}
			if err := scheduler.check(context.Background(), now); !errors.Is(err, store.ErrReportPeriodConflict) {
				t.Fatalf("previous conflict was hidden: %v", err)
			}
			for _, offset := range []int{-7, -6} {
				if _, err := b.Store.Report(context.Background(), now.AddDate(0, 0, offset).Format(time.DateOnly)); err != nil {
					t.Fatalf("unrelated backfill blocked: %v", err)
				}
			}
			reports, err := b.Store.Reports(context.Background(), "", 100)
			if err != nil || len(reports) != 3 {
				t.Fatalf("automatic backfill exceeded two creations: %d %v", len(reports), err)
			}
			after, err := b.Store.Report(context.Background(), yesterday)
			if err != nil || before != after {
				t.Fatal("conflicting archive changed")
			}
			queue, err := b.Store.QueueStatus(context.Background(), now)
			if err != nil || queue.Pending != 0 {
				t.Fatal("historical backfill generated notifications")
			}
		})
	}
}

func TestSchedulerCancellationDoesNotWaitForArchive(t *testing.T) {
	b, _ := backfillBuilder(t, "UTC")
	release, err := b.lockArchive(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	now := time.Now().UTC().Truncate(24 * time.Hour).Add(12 * time.Hour)
	scheduler := Scheduler{Store: b.Store, Builder: b, DailyAt: "09:00", BackfillDays: 7}
	done := make(chan error, 1)
	go func() { done <- scheduler.check(ctx, now) }()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("scheduler did not respect cancellation")
	}
}
