// SPDX-License-Identifier: MIT

package report

import (
	"context"
	"database/sql"
	"github.com/littlesho/NodeRampart/internal/billing"
	"github.com/littlesho/NodeRampart/internal/model"
	"github.com/littlesho/NodeRampart/internal/store"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBillingMonthEndIncludesCompletedMonth(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	err = db.AddInterface(ctx, model.InterfaceTotals{HourUTC: time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC), TXBytes: 10_000_000_000})
	if err != nil {
		t.Fatal(err)
	}
	b := Builder{Store: db, Hostname: "lab", Location: time.UTC, TopN: 5, Billing: &billing.Profile{SchemaVersion: 1, Provider: "custom", SourceRegion: "fixture", EffectiveDate: "2026-01-01", SourceURL: "https://example.com/pricing", Name: "test", Currency: "USD", InternetEgress: []billing.Tier{{PricePerGB: 1}}}}
	date, body, err := b.Daily(ctx, time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("date=%s", date)
	for _, line := range strings.Split(body, "\n") {
		if strings.Contains(line, "billable GB") {
			t.Log(line)
		}
	}
	if !strings.Contains(body, "10.00 USD for 10.000 billable GB") {
		t.Fatal("completed-month usage disappeared from last-day daily report")
	}
}

func TestSchedulerSkipsAggregatesForGeneratedReport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	scheduler := Scheduler{Store: db, Builder: &Builder{Store: db, Hostname: "lab", Location: time.UTC, TopN: 5}, DailyAt: "09:00", Destination: "telegram", Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := scheduler.check(ctx, now); err != nil {
		t.Fatal(err)
	}
	exists, err := db.ReportGenerated(ctx, "2026-09-10", "telegram")
	if err != nil || !exists {
		t.Fatalf("generated=%t err=%v", exists, err)
	}
	// Fault only the source aggregates after the report has already been generated.
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.Exec(`DROP TABLE interface_hourly`); err != nil {
		t.Fatal(err)
	}
	err = scheduler.check(ctx, now.Add(30*time.Second))
	t.Logf("already generated=%t, next scheduler check=%v", exists, err)
	if err != nil {
		t.Fatal("scheduler rebuilt source aggregates for an already generated report")
	}
}
