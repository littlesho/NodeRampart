// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/model"
)

func budgetStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "budget.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.ConfigureBudget(context.Background(), BudgetConfig{MaxBytes: 64 << 20, MinFreeBytes: 0}); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestStorageBudgetBoundsDatabaseAndJournalDuringLargeWrite(t *testing.T) {
	s := budgetStore(t)
	ctx := context.Background()
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE budget_payload(id INTEGER PRIMARY KEY,payload BLOB)`); err != nil {
		t.Fatal(err)
	}
	full := false
	for i := 0; i < 40; i++ {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO budget_payload(payload) VALUES (zeroblob(1048576))`); err != nil {
			full = true
			break
		}
	}
	if !full {
		t.Fatal("SQLite page count did not enforce the cap")
	}
	status, err := s.BudgetStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.JournalMode != "delete" || status.WALBytes != 0 || status.DatabaseBytes > status.DatabaseLimitBytes {
		t.Fatalf("wrong bounded mode: %#v", status)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE budget_payload SET payload=randomblob(length(payload))`); err != nil {
		t.Fatal(err)
	}
	// Inspect real active files while the uncommitted journal is at its peak.
	var used int64
	for _, suffix := range []string{"", "-journal", "-wal", "-shm"} {
		info, err := fileSizeIfPresent(s.path + suffix)
		if err != nil {
			t.Fatal(err)
		}
		used += info
	}
	if used > 64<<20 {
		t.Fatalf("active files grew past hard budget during transaction: %d", used)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var value int
	if err := s.db.QueryRowContext(ctx, `SELECT SUM(substr(payload,1,1)<>x'00') FROM budget_payload`).Scan(&value); err != nil || value != 0 {
		t.Fatalf("failed rollback after capped write: value=%d error=%v", value, err)
	}
}

func TestBudgetLowDiskRejectsWritesButKeepsDiagnostics(t *testing.T) {
	s := budgetStore(t)
	ctx := context.Background()
	s.budget.freeSpace = func(string) (uint64, error) { return 0, nil }
	err := s.InsertEvent(ctx, model.Event{ID: "should-not-persist", ObservedAt: time.Now().UTC(), Kind: "test", Severity: model.SeverityInfo})
	if !errors.Is(err, ErrStorageBudget) {
		t.Fatalf("low disk write admitted: %v", err)
	}
	status, err := s.BudgetStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != "degraded" || status.Reason != "low_disk_space" || status.RejectedWrites != 1 {
		t.Fatalf("low disk not visible: %#v", status)
	}
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM events`).Scan(&count); err != nil || count != 0 {
		t.Fatal("rejected event persisted")
	}
}

func TestBudgetDiagnosticsAndWriteWaitRespectBusyOperation(t *testing.T) {
	s := budgetStore(t)
	s.budgetMu.Lock()
	defer s.budgetMu.Unlock()
	status, err := s.BudgetStatus(context.Background())
	if err != nil || status.State != "busy" {
		t.Fatalf("busy diagnostics blocked or lied: %#v %v", status, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := s.beginWrite(ctx, writeCritical); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("writer wait ignored deadline: %v", err)
	}
}

func TestBudgetPruningPreservesEventsUntilTrafficRemoved(t *testing.T) {
	s := budgetStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := s.InsertEvent(ctx, model.Event{ID: "retain", ObservedAt: now, Kind: "test", Severity: model.SeverityInfo}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 300; i++ {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO traffic_hourly VALUES (?,'inbound','','',0,'',0,1,1)`, now.Add(time.Duration(i)*time.Hour).Unix()); err != nil {
			t.Fatal(err)
		}
	}
	// Force the pressure threshold while preserving the real SQLite page cap.
	s.budget.status.DatabaseLimitBytes = 1
	if err := s.MaintainBudget(ctx, now); err != nil {
		t.Fatal(err)
	}
	var events, traffic int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM events`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM traffic_hourly`).Scan(&traffic); err != nil {
		t.Fatal(err)
	}
	if events != 1 || traffic != 44 || s.budget.status.PrunedTrafficRows != 256 {
		t.Fatalf("priority/chunk budget failed: events=%d traffic=%d", events, traffic)
	}
}

func TestBudgetExclusiveOwnershipBlocksSecondSQLiteWriter(t *testing.T) {
	s := budgetStore(t)
	other, err := sql.Open("sqlite", s.path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if _, err := other.Exec(`PRAGMA busy_timeout=20`); err != nil {
		t.Fatal(err)
	}
	if _, err := other.Exec(`INSERT INTO events(id,observed_at,kind,severity,summary) VALUES ('external',0,'test','info','')`); err == nil {
		t.Fatal("second writer bypassed configured page cap")
	}
}

func TestBudgetCriticalWriteCannotHideLowSpaceForNormalWrites(t *testing.T) {
	s := budgetStore(t)
	ctx := context.Background()
	s.budget.freeSpace = func(string) (uint64, error) { return 2 << 20, nil }
	if release, err := s.beginWrite(ctx, writeNormal); !errors.Is(err, ErrStorageBudget) {
		if release != nil {
			release()
		}
		t.Fatalf("normal write unexpectedly admitted: %v", err)
	}
	if err := s.InsertEvent(ctx, model.Event{ID: "reserved-critical", ObservedAt: time.Now().UTC(), Kind: "test", Severity: model.SeverityInfo}); err != nil {
		t.Fatal(err)
	}
	status, err := s.BudgetStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != "degraded" || status.Reason != "low_disk_space" {
		t.Fatalf("critical write hid normal ingestion degradation: %#v", status)
	}
}
