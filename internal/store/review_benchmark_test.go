// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/model"
)

// These use the daemon's file-backed DELETE/FULL budget mode, not Open's
// WAL/NORMAL defaults. Setup and migration are outside the measured interval.
func reviewBudgetStore(b *testing.B) *Store {
	b.Helper()
	s, err := OpenWithBudget(filepath.Join(b.TempDir(), "state.db"), BudgetConfig{MaxBytes: 64 << 20})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		if err := s.Close(); err != nil {
			b.Error(err)
		}
	})
	return s
}

func BenchmarkReviewBudgetStatus(b *testing.B) {
	s := reviewBudgetStore(b)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		status, err := s.BudgetStatus(ctx)
		if err != nil || !status.Configured || status.State != "running" || status.JournalMode != "delete" {
			b.Fatalf("budget inspection failed: %+v, %v", status, err)
		}
	}
}

func BenchmarkReviewBudgetWriteAdmission(b *testing.B) {
	s := reviewBudgetStore(b)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		release, err := s.beginWrite(ctx, writeTraffic)
		if err != nil {
			b.Fatal(err)
		}
		release()
	}
}

func BenchmarkReviewBudgetTrafficTransaction(b *testing.B) {
	s := reviewBudgetStore(b)
	ctx := context.Background()
	now := time.Now().UTC()
	traffic := make([]model.Traffic, 64)
	for i := range traffic {
		traffic[i] = model.Traffic{HourUTC: now, Direction: model.DirectionInbound, Country: fmt.Sprintf("fixture%02d", i), Bytes: 1200, Packets: 1}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if err := s.AddTrafficBatch(ctx, traffic); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	var bytes, packets uint64
	if err := s.db.QueryRowContext(ctx, `SELECT SUM(bytes), SUM(packets) FROM traffic_hourly`).Scan(&bytes, &packets); err != nil {
		b.Fatal(err)
	}
	if bytes != uint64(b.N)*64*1200 || packets != uint64(b.N)*64 {
		b.Fatalf("committed totals mismatch: bytes=%d packets=%d", bytes, packets)
	}
}
