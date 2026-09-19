// SPDX-License-Identifier: MIT

package report

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/store"
)

func backfillBuilder(t *testing.T, zone string) (*Builder, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	location, err := time.LoadLocation(zone)
	if err != nil {
		t.Fatal(err)
	}
	return &Builder{Store: db, Hostname: "synthetic", Location: location, TopN: 5}, path
}

func TestBackfillStorageAdmissionRefusalPreservesResumeDate(t *testing.T) {
	b, _ := backfillBuilder(t, "UTC")
	if err := b.Store.ConfigureBudget(context.Background(), store.BudgetConfig{MaxBytes: 64 << 20, MinFreeBytes: 1 << 40}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	date := now.AddDate(0, 0, -2).Format(time.DateOnly)
	result, err := b.Backfill(context.Background(), date, date, now)
	if err == nil || result.NextDate != date || result.Complete || len(result.Dates) != 1 || result.Dates[0].State != "unavailable" {
		t.Fatalf("storage refusal progress=%+v %v", result, err)
	}
	if _, err := b.Store.Report(context.Background(), date); !store.IsNotFound(err) {
		t.Fatal("refused snapshot persisted")
	}
}

func TestBackfillCalendarRetentionAndImmutableConflict(t *testing.T) {
	for _, tc := range []struct {
		zone, first, last, now string
		hours                  float64
		skipped                int
	}{
		{"America/New_York", "2026-03-08", "2026-03-08", "2026-03-10T12:00:00Z", 23, 0},
		{"Pacific/Apia", "2011-12-29", "2011-12-31", "2012-01-02T12:00:00Z", 24, 1},
	} {
		t.Run(tc.zone, func(t *testing.T) {
			b, _ := backfillBuilder(t, tc.zone)
			now, _ := time.Parse(time.RFC3339, tc.now)
			result, err := b.Backfill(context.Background(), tc.first, tc.last, now)
			if err != nil || !result.Complete {
				t.Fatalf("backfill=%+v %v", result, err)
			}
			skipped := 0
			for _, date := range result.Dates {
				if date.State == "nonexistent_date" {
					skipped++
					continue
				}
				if date.State != "generated" || date.PeriodEnd.Sub(date.PeriodStart).Hours() != tc.hours {
					t.Fatalf("invented civil interval: %+v", date)
				}
			}
			if skipped != tc.skipped {
				t.Fatal("skipped civil date not identified")
			}
			before, err := b.Store.Report(context.Background(), tc.first)
			if err != nil {
				t.Fatal(err)
			}
			b.Location = time.UTC
			again, err := b.Backfill(context.Background(), tc.first, tc.first, now)
			if err != nil || again.Dates[0].State != "conflict" {
				t.Fatalf("timezone conflict overwritten: %+v %v", again, err)
			}
			after, err := b.Store.Report(context.Background(), tc.first)
			if err != nil || before != after {
				t.Fatal("immutable archive changed")
			}
		})
	}
}

func TestBackfillConcurrentResumeNeverEnqueuesAndLabelsPrunedDetail(t *testing.T) {
	b, _ := backfillBuilder(t, "UTC")
	now := time.Now().UTC()
	date := now.AddDate(0, 0, -14).Format(time.DateOnly)
	var wg sync.WaitGroup
	states := make(chan string, 4)
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := b.Backfill(context.Background(), date, date, now)
			if err != nil {
				t.Error(err)
				return
			}
			states <- result.Dates[0].State
		}()
	}
	wg.Wait()
	close(states)
	generated := 0
	for state := range states {
		if state == "generated" {
			generated++
		} else if state != "already_present" {
			t.Error(state)
		}
	}
	if generated != 1 {
		t.Fatalf("generated %d snapshots", generated)
	}
	snapshot, err := b.Store.Report(context.Background(), date)
	if err != nil || !strings.Contains(snapshot.Body, "may be pruned") || !strings.Contains(snapshot.Body, "unknown 24h0m0s") {
		t.Fatalf("missing historical limitations: %v %s", err, snapshot.Body)
	}
	queue, err := b.Store.QueueStatus(context.Background(), now)
	if err != nil || queue.Pending != 0 {
		t.Fatal("historical reports entered notification queue")
	}
}

func TestBackfillBoundedAutomaticWorkCancellationAndPartialFailure(t *testing.T) {
	b, path := backfillBuilder(t, "UTC")
	now := time.Now().UTC().Truncate(24 * time.Hour).Add(12 * time.Hour)
	s := Scheduler{Store: b.Store, Builder: b, DailyAt: "09:00", Destination: "telegram", BackfillDays: 7}
	if err := s.check(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	reports, err := b.Store.Reports(context.Background(), "", 100)
	if err != nil || len(reports) != 3 {
		t.Fatalf("automatic pass unbounded: %d %v", len(reports), err)
	}
	queue, err := b.Store.QueueStatus(context.Background(), now)
	if err != nil || queue.Pending != 1 {
		t.Fatal("only normal previous-day delivery should queue")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	date := now.AddDate(0, 0, -10).Format(time.DateOnly)
	result, err := b.Backfill(ctx, date, date, now)
	if err == nil || result.NextDate != date || result.StoppedReason != "cancelled" || len(result.Dates) != 0 {
		t.Fatalf("cancelled progress=%+v %v", result, err)
	}
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	// Fail the second date after the first was durably archived.
	second := now.AddDate(0, 0, -9).Format(time.DateOnly)
	if _, err := raw.Exec(`CREATE TRIGGER backfill_failure BEFORE INSERT ON report_snapshots WHEN NEW.report_date='` + second + `' BEGIN SELECT RAISE(ABORT,'fixture storage failure'); END`); err != nil {
		t.Fatal(err)
	}
	result, err = b.Backfill(context.Background(), date, second, now)
	if err == nil || result.Complete || result.NextDate != second || len(result.Dates) != 2 || result.Dates[0].State != "generated" {
		t.Fatalf("partial progress lost: %+v %v", result, err)
	}
	if _, err := raw.Exec(`DROP TRIGGER backfill_failure`); err != nil {
		t.Fatal(err)
	}
	result, err = b.Backfill(context.Background(), date, second, now)
	if err != nil || !result.Complete || result.Dates[0].State != "already_present" || result.Dates[1].State != "generated" {
		t.Fatalf("resume failed: %+v %v", result, err)
	}
	for _, pair := range [][2]string{{"2020-01-01", "2020-01-01"}, {now.Format(time.DateOnly), now.Format(time.DateOnly)}, {now.AddDate(0, 0, -33).Format(time.DateOnly), now.AddDate(0, 0, -1).Format(time.DateOnly)}} {
		if _, err := b.Backfill(context.Background(), pair[0], pair[1], now); err == nil {
			t.Fatalf("invalid dates admitted: %v", pair)
		}
	}
}
