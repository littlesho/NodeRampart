// SPDX-License-Identifier: MIT

package report

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/store"
)

func TestDailyArchiveWithoutTelegramIsImmutable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(24 * time.Hour).Add(12 * time.Hour)
	scheduler := Scheduler{Store: db, Builder: &Builder{Store: db, Hostname: "synthetic", Location: time.UTC, TopN: 5}, DailyAt: "09:00"}
	if err := scheduler.check(ctx, now); err != nil {
		t.Fatal(err)
	}
	date, _, _ := PreviousDay(now, time.UTC)
	snapshot, err := db.Report(ctx, date)
	if err != nil {
		t.Fatal(err)
	}
	queue, err := db.QueueStatus(ctx, now)
	if err != nil || queue.Pending != 0 {
		t.Fatalf("offline report enqueued: %+v %v", queue, err)
	}
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.Exec(`DROP TABLE interface_hourly`); err != nil {
		t.Fatal(err)
	}
	if err := scheduler.check(ctx, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	again, err := db.Report(ctx, date)
	if err != nil || again.Body != snapshot.Body || !again.GeneratedAt.Equal(snapshot.GeneratedAt) {
		t.Fatalf("archived body rebuilt: %v", err)
	}
}
