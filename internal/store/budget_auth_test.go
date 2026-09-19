// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/model"
)

func seedAuthDetail(t *testing.T, s *Store, hour time.Time, count int) {
	t.Helper()
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for i := 0; i < count; i++ {
		if _, err := tx.Exec(`INSERT INTO auth_hourly VALUES (?,'failure',?,2)`, hour.Unix(), fmt.Sprintf("2001:db8:%x::/48", i+1)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Exec(`INSERT INTO auth_cardinality_hourly VALUES (?,?)`, hour.Unix(), count); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func compactAuthForTest(t *testing.T, s *Store) (bool, error) {
	t.Helper()
	s.budgetMu.Lock()
	defer s.budgetMu.Unlock()
	return s.compactAuthChunkLocked(context.Background())
}

func TestAuthPressurePreservesHourlyTotalsAndDurableLoss(t *testing.T) {
	s := budgetStore(t)
	ctx := context.Background()
	hour := time.Now().UTC().Truncate(time.Hour)
	seedAuthDetail(t, s, hour, 512)
	if _, err := s.db.Exec(`INSERT INTO auth_hourly VALUES (?,'failure','_overflow',9)`, hour.Unix()); err != nil {
		t.Fatal(err)
	}
	for pass := 0; pass < 4; pass++ {
		if _, err := compactAuthForTest(t, s); err != nil {
			t.Fatal(err)
		}
	}
	var total, rows, keys, bucket int
	if err := s.db.QueryRow(`SELECT SUM(count),COUNT(*) FROM auth_hourly`).Scan(&total, &rows); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT keys FROM auth_cardinality_hourly WHERE hour_utc=?`, hour.Unix()).Scan(&keys); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT count FROM auth_hourly WHERE source_range=?`, authPressureBucket).Scan(&bucket); err != nil {
		t.Fatal(err)
	}
	if total != 1033 || bucket != 1024 || rows != 2 || keys != 1 || s.budget.status.CompactedAuthRows != 512 {
		t.Fatalf("compaction changed totals, bounds or cardinality: total=%d bucket=%d rows=%d keys=%d status=%#v", total, bucket, rows, keys, s.budget.status)
	}
	gaps, err := s.CoverageGaps(ctx, hour, hour.Add(time.Hour), 100)
	if err != nil || len(gaps) != 1 || gaps[0].Reason != authPressureReason || gaps[0].Count != 512 {
		t.Fatalf("loss was not coalesced and persisted: %#v %v", gaps, err)
	}
	path := s.path
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := reopened.ConfigureBudget(ctx, BudgetConfig{MaxBytes: 64 << 20}); err != nil {
		t.Fatal(err)
	}
	if _, err := compactAuthForTest(t, reopened); err != nil {
		t.Fatal(err)
	}
	gaps, err = reopened.CoverageGaps(ctx, hour, hour.Add(time.Hour), 100)
	if err != nil || len(gaps) != 1 || gaps[0].Count != 512 || reopened.budget.status.CompactedAuthRows != 0 {
		t.Fatalf("restart recounted summary buckets: %#v %v", gaps, err)
	}
	if err := reopened.AddAuth(ctx, hour, "failure", "2001:db8:ffff::/48"); err != nil {
		t.Fatal(err)
	}
	if err := reopened.db.QueryRow(`SELECT keys FROM auth_cardinality_hourly WHERE hour_utc=?`, hour.Unix()).Scan(&keys); err != nil || keys != 2 {
		t.Fatalf("new source admission did not reuse released cardinality: %d %v", keys, err)
	}
}

func TestAuthPressureRollsBackDetailAndLossTogether(t *testing.T) {
	for _, overflow := range []bool{false, true} {
		t.Run(fmt.Sprint(overflow), func(t *testing.T) {
			s := budgetStore(t)
			hour := time.Now().UTC().Truncate(time.Hour)
			seedAuthDetail(t, s, hour, 2)
			if overflow {
				if _, err := s.db.Exec(`INSERT INTO auth_hourly VALUES (?,'failure',?,?)`, hour.Unix(), authPressureBucket, int64(math.MaxInt64-1)); err != nil {
					t.Fatal(err)
				}
			} else if _, err := s.db.Exec(`CREATE TRIGGER refuse_loss BEFORE INSERT ON coverage_gaps BEGIN SELECT RAISE(ABORT,'loss marker unavailable'); END`); err != nil {
				t.Fatal(err)
			}
			if _, err := compactAuthForTest(t, s); err == nil {
				t.Fatal("compaction accepted unavailable loss marker or overflowing total")
			}
			var details, keys, gaps int
			if err := s.db.QueryRow(`SELECT COUNT(*) FROM auth_hourly WHERE source_range<>?`, authPressureBucket).Scan(&details); err != nil {
				t.Fatal(err)
			}
			if err := s.db.QueryRow(`SELECT keys FROM auth_cardinality_hourly`).Scan(&keys); err != nil {
				t.Fatal(err)
			}
			if err := s.db.QueryRow(`SELECT COUNT(*) FROM coverage_gaps`).Scan(&gaps); err != nil {
				t.Fatal(err)
			}
			if details != 2 || keys != 2 || gaps != 0 || s.budget.status.CompactedAuthRows != 0 {
				t.Fatalf("rollback lost evidence or counted loss: details=%d keys=%d gaps=%d", details, keys, gaps)
			}
		})
	}
}

func TestAuthPressureWalkResumesPastSummaryBuckets(t *testing.T) {
	s := budgetStore(t)
	hour := time.Now().UTC().Truncate(time.Hour)
	seedAuthDetail(t, s, hour, 512)
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for i := 1; i <= 300; i++ {
		if _, err := tx.Exec(`INSERT INTO auth_hourly VALUES (?,'failure',?,1)`, hour.Add(-time.Duration(i)*time.Hour).Unix(), authPressureBucket); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if progressed, err := compactAuthForTest(t, s); err != nil || !progressed || s.budget.status.CompactedAuthRows != 0 {
		t.Fatalf("one step must not scan beyond its 256 summary candidates: progressed=%v error=%v", progressed, err)
	}
	for i := 0; i < 6; i++ {
		before := s.budget.status.CompactedAuthRows
		if _, err := compactAuthForTest(t, s); err != nil {
			t.Fatal(err)
		}
		if s.budget.status.CompactedAuthRows-before > 256 {
			t.Fatal("one compaction exceeded the bounded row count")
		}
	}
	var total, summaries int
	if err := s.db.QueryRow(`SELECT SUM(count),COUNT(*) FROM auth_hourly`).Scan(&total, &summaries); err != nil {
		t.Fatal(err)
	}
	if total != 1324 || summaries != 301 || s.budget.status.CompactedAuthRows != 512 {
		t.Fatalf("walk lost sources or re-compacted summaries: total=%d summaries=%d compacted=%d", total, summaries, s.budget.status.CompactedAuthRows)
	}
}

func TestAuthPressureCompletedWalkSurvivesCriticalWrites(t *testing.T) {
	s := budgetStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Hour)
	if _, err := s.db.Exec(`INSERT INTO auth_hourly VALUES (?,'failure',?,1)`, now.Unix(), authPressureBucket); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 128; i++ {
		if err := s.InsertEvent(ctx, model.Event{ID: fmt.Sprintf("old_%03d", i), ObservedAt: now.Add(-time.Hour), Kind: "port_scan", Severity: model.SeverityMedium}); err != nil {
			t.Fatal(err)
		}
	}
	// Force pressure without changing SQLite's real page cap. Each critical
	// heartbeat lands between maintenance's bucket visit and its end-of-walk
	// read, which formerly restarted the scan without ever pruning events.
	s.budget.status.DatabaseLimitBytes = 1
	for i := 0; i < 4; i++ {
		if err := s.MaintainBudget(ctx, now); err != nil {
			t.Fatal(err)
		}
		if err := s.HeartbeatCoverage(ctx, now); err != nil {
			t.Fatal(err)
		}
	}
	var events int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM events`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 0 || s.budget.status.PrunedEventRows != 128 {
		t.Fatalf("interleaved critical writes starved event reclamation: retained=%d pruned=%d", events, s.budget.status.PrunedEventRows)
	}
}

func TestAuthPressureRecoversJournalAtRealPageCap(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.ConfigureBudget(ctx, BudgetConfig{MaxBytes: 64 << 20}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Hour)
	// Establish the normal bounded replay cache through real atomic writes.
	for i := 0; i < 10_000; i++ {
		if _, err := s.CommitJournal(ctx, JournalWrite{Cursor: fmt.Sprintf("seen_%d", i), ReceivedAt: now, ObservedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	// Simulate an already-full database from the earlier implementation. Every
	// setup transaction has at most 256 rows, using default-shaped /48 sources
	// and no more than 60,000 distinct sources per hour. No large payloads.
	next := 0
	for ; next < 600_000; next += 256 {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		insert, err := tx.PrepareContext(ctx, `INSERT INTO auth_hourly VALUES (?,'failure',?,1)`)
		if err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
		for i := next; i < next+256; i++ {
			hour := now.Add(-time.Duration(i/60_000) * time.Hour)
			_, err = insert.ExecContext(ctx, hour.Unix(), fmt.Sprintf("2001:db8:%x::/48", i%60_000+1))
			if err != nil {
				break
			}
		}
		if closeErr := insert.Close(); closeErr != nil {
			_ = tx.Rollback()
			t.Fatal(closeErr)
		}
		if err != nil {
			_ = tx.Rollback()
			break
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	if next >= 600_000 {
		t.Fatal("fixture did not reach page cap")
	}
	if _, err := s.db.Exec(`INSERT INTO auth_cardinality_hourly SELECT hour_utc,COUNT(*) FROM auth_hourly GROUP BY hour_utc`); err != nil {
		t.Fatal(err)
	}
	for ; next < 600_000; next++ {
		hour := now.Add(-time.Duration(next/60_000) * time.Hour)
		if _, err := s.db.Exec(`INSERT INTO auth_hourly VALUES (?,'failure',?,1)`, hour.Unix(), fmt.Sprintf("2001:db8:%x::/48", next%60_000+1)); err != nil {
			break
		}
		if _, err := s.db.Exec(`UPDATE auth_cardinality_hourly SET keys=keys+1 WHERE hour_utc=?`, hour.Unix()); err != nil {
			t.Fatal(err)
		}
	}
	before, err := s.BudgetStatus(ctx)
	if err != nil || before.LiveBytes != before.DatabaseLimitBytes || before.Reason != "database_capacity_exhausted" {
		t.Fatalf("fixture must fill real pages: %#v %v", before, err)
	}
	write := JournalWrite{Cursor: "recovered", ReceivedAt: now, ObservedAt: now, Kind: "failure", SourceRange: "2001:db8:ffff::/48"}
	if added, err := s.CommitJournal(ctx, write); err != nil || !added {
		t.Fatalf("normal journal write failed after bounded reclamation: added=%v error=%v", added, err)
	}
	if rows := s.budget.status.CompactedAuthRows; rows == 0 || rows > maxAuthCompactionRows {
		t.Fatalf("one journal write exceeded the reclamation bound: %d", rows)
	}
	if added, err := s.CommitJournal(ctx, write); err != nil || added {
		t.Fatalf("journal replay changed accounting: added=%v error=%v", added, err)
	}
	var observations int
	if err := s.db.QueryRow(`SELECT SUM(count) FROM auth_hourly`).Scan(&observations); err != nil || observations != next+1 {
		t.Fatalf("compaction/replay changed hourly observations: got=%d want=%d error=%v", observations, next+1, err)
	}
	for i := 0; i < 20; i++ {
		event := model.Event{ID: model.NewID("evt"), ObservedAt: now, Kind: "port_scan", Phase: "start", Severity: model.SeverityMedium, SourceRange: "2001:db8:ffff::/48", Count: 20, Summary: "at least 20 unique local ports probed in 10s", Evidence: map[string]string{"packets": "20", "rule": "inbound_syn_or_unsolicited_udp", "threshold": "20", "window_seconds": "60"}}
		if err := s.InsertEventNotification(ctx, event, nil); err != nil {
			t.Fatalf("normal small scan event failed after auth pressure: %v", err)
		}
	}
	if err := s.db.QueryRow(`SELECT SUM(count) FROM auth_hourly`).Scan(&observations); err != nil || observations != next+1 {
		t.Fatalf("continued reclamation lost observations: got=%d want=%d error=%v", observations, next+1, err)
	}
	after, err := s.BudgetStatus(ctx)
	if err != nil || after.UsedBytes > after.MaxBytes || after.DatabaseBytes > after.DatabaseLimitBytes || after.CompactedAuthRows == 0 {
		t.Fatalf("recovery bypassed hard budget or omitted compaction: %#v %v", after, err)
	}
}
