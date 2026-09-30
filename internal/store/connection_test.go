// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/model"
)

func physicalConnection(t *testing.T, db *sql.DB) driver.Conn {
	t.Helper()
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var physical driver.Conn
	if err := conn.Raw(func(c any) error { physical = c.(driver.Conn); return nil }); err != nil {
		t.Fatal(err)
	}
	return physical
}

func cancelRunningQuery(t *testing.T, s *Store) {
	t.Helper()
	before := physicalConnection(t, s.db)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	var value int64
	err := s.db.QueryRowContext(ctx, `WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<1000000000) SELECT SUM(x) FROM n`).Scan(&value)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("query did not run until cancellation: %v", err)
	}
	if after := physicalConnection(t, s.db); before == after {
		t.Fatal("cancellation did not replace the physical connection")
	}
}

func assertConnectionSettings(t *testing.T, s *Store, budget bool) {
	t.Helper()
	settings := map[string]int64{"foreign_keys": 1, "trusted_schema": 0, "busy_timeout": 5000, "secure_delete": 2, "synchronous": 1}
	mode, locking := "wal", "normal"
	if budget {
		settings["synchronous"] = 2
		settings["cache_spill"] = 0
		settings["temp_store"] = 2
		settings["max_page_count"] = s.budget.maxPages
		mode, locking = "delete", "exclusive"
	}
	for name, want := range settings {
		var got int64
		if err := s.db.QueryRow("PRAGMA " + name).Scan(&got); err != nil || got != want {
			t.Fatalf("%s=%d want %d: %v", name, got, want, err)
		}
	}
	for name, want := range map[string]string{"journal_mode": mode, "locking_mode": locking} {
		var got string
		if err := s.db.QueryRow("PRAGMA " + name).Scan(&got); err != nil || got != want {
			t.Fatalf("%s=%s want %s: %v", name, got, want, err)
		}
	}
}

func TestPhysicalConnectionCancellationPreservesInitializationAndCascade(t *testing.T) {
	for _, budget := range []bool{false, true} {
		name := "Open"
		if budget {
			name = "OpenWithBudget"
		}
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "real.db")
			var s *Store
			var err error
			if budget {
				s, err = OpenWithBudget(path, BudgetConfig{MaxBytes: 64 << 20})
			} else {
				s, err = Open(path)
			}
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			now := time.Now().UTC()
			event := model.Event{ID: "old", ObservedAt: now.Add(-8 * 24 * time.Hour), Kind: "test", Severity: model.SeverityInfo}
			if err := s.InsertEventNotification(context.Background(), event, nil); err != nil {
				t.Fatal(err)
			}
			var originalLinks int
			if err := s.db.QueryRow("SELECT COUNT(*) FROM event_notifications WHERE event_id='old'").Scan(&originalLinks); err != nil || originalLinks != 1 {
				t.Fatalf("fixture did not create a notification association: %d %v", originalLinks, err)
			}
			for i := 0; i < 3; i++ {
				cancelRunningQuery(t, s)
				assertConnectionSettings(t, s, budget)
			}
			if err := s.Prune(context.Background(), now); err != nil {
				t.Fatal(err)
			}
			var events, links int
			if err := s.db.QueryRow("SELECT COUNT(*) FROM events WHERE id='old'").Scan(&events); err != nil {
				t.Fatal(err)
			}
			if err := s.db.QueryRow("SELECT COUNT(*) FROM event_notifications WHERE event_id='old'").Scan(&links); err != nil {
				t.Fatal(err)
			}
			if events != 0 || links != 0 {
				t.Fatalf("prune left events=%d links=%d", events, links)
			}
			rows, err := s.db.Query("PRAGMA foreign_key_check")
			if err != nil {
				t.Fatal(err)
			}
			if rows.Next() {
				t.Fatal("foreign key violation after prune")
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			rows.Close()
			event.ID = "new"
			event.ObservedAt = now
			if err := s.InsertEvent(context.Background(), event); err != nil {
				t.Fatal(err)
			}
			if err := s.db.QueryRow("SELECT COUNT(*) FROM events WHERE id='new'").Scan(&events); err != nil || events != 1 {
				t.Fatalf("continued read/write failed: %d %v", events, err)
			}
		})
	}
}

func TestPhysicalConnectionBudgetsRemainStoreLocal(t *testing.T) {
	a, err := OpenWithBudget(filepath.Join(t.TempDir(), "a.db"), BudgetConfig{MaxBytes: 64 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := OpenWithBudget(filepath.Join(t.TempDir(), "b.db"), BudgetConfig{MaxBytes: 128 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	for i := 0; i < 2; i++ {
		cancelRunningQuery(t, a)
		cancelRunningQuery(t, b)
		assertConnectionSettings(t, a, true)
		assertConnectionSettings(t, b, true)
	}
	if a.budget.maxPages == b.budget.maxPages {
		t.Fatal("test did not exercise distinct budgets")
	}
}

func TestFailedPhysicalInitializationCannotEnterPool(t *testing.T) {
	s, err := OpenWithBudget(filepath.Join(t.TempDir(), "fail.db"), BudgetConfig{MaxBytes: 64 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.db.SetMaxIdleConns(0)
	s.connector.budget.Store(&BudgetConfig{MaxBytes: budgetFileOverhead})
	if err := s.db.PingContext(context.Background()); err == nil {
		t.Fatal("invalid initialization admitted a connection")
	}
	if s.db.Stats().OpenConnections != 0 {
		t.Fatal("failed connection retained in pool")
	}
	s.connector.budget.Store(&BudgetConfig{MaxBytes: 64 << 20})
	s.db.SetMaxIdleConns(1)
	if err := s.db.PingContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertConnectionSettings(t, s, true)
}
