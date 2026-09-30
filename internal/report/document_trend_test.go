// SPDX-License-Identifier: MIT

package report

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/billing"
	"github.com/littlesho/NodeRampart/internal/model"
	"github.com/littlesho/NodeRampart/internal/store"
)

func TestFullArchiveRetainsLongChineseBodyAndPrivacy(t *testing.T) {
	b, _ := backfillBuilder(t, "UTC")
	b.Hostname = "中文<&>"
	now := time.Now().UTC()
	date, start, _ := PreviousDay(now, time.UTC)
	for i := range 120 {
		kind := fmt.Sprintf("中文审查来源%03d%s", i, strings.Repeat("长", 15))
		if i == 119 {
			kind = "<script>synthetic()</script>"
		}
		if err := b.Store.InsertEvent(context.Background(), model.Event{ID: fmt.Sprintf("evt_%d", i), ObservedAt: start.Add(time.Hour), Kind: kind, Severity: model.SeverityInfo, SourceRange: "ip_synthetic_privacy_hash"}); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, _, err := b.archiveDate(context.Background(), date, now)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := DecodeDocument(snapshot.Document)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Body) > 4096 || len(doc.Body) <= 4096 || strings.Contains(doc.Body, "… truncated") || !strings.Contains(doc.Body, "ip_synthetic_privacy_hash") || !strings.Contains(doc.Body, "&lt;script&gt;") || strings.Contains(doc.Body, "<script>") || !strings.Contains(doc.Body, "中文&lt;&amp;&gt;") {
		t.Fatal("full report was truncated, escaped incorrectly or lost privacy-transformed history")
	}
	b.Hostname = "new host"
	again, _, err := b.archiveDate(context.Background(), date, now.Add(time.Hour))
	if err != nil || !reflect.DeepEqual(snapshot, again) {
		t.Fatal("original full snapshot changed", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := b.StructuredRange(ctx, "cancel", start, now, now); err == nil {
		t.Fatal("cancelled report was generated")
	}
}

func TestLegacySnapshotRemainsLegacyAndDocumentBounds(t *testing.T) {
	b, _ := backfillBuilder(t, "UTC")
	now := time.Now().UTC()
	date, start, end := PreviousDay(now, time.UTC)
	old := store.ReportSnapshot{Date: date, Title: "Legacy", Body: "Original short body", PeriodStart: start, PeriodEnd: end, GeneratedAt: now.Truncate(time.Millisecond)}
	if err := b.Store.SaveReport(context.Background(), old); err != nil {
		t.Fatal(err)
	}
	got, state, err := b.archiveDate(context.Background(), date, now)
	if err != nil || state != "already_present" || !reflect.DeepEqual(old, got) || len(got.Document) != 0 {
		t.Fatal("legacy snapshot was rewritten", err)
	}
	if _, err := DecodeDocument(got.Document); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatal("legacy full content claimed available")
	}
	doc := &Document{SchemaVersion: 1, Title: "x", PeriodStart: start, PeriodEnd: end, GeneratedAt: now, Body: strings.Repeat("中", MaxFullBodyBytes/3+1)}
	if _, err := EncodeDocument(doc); err == nil {
		t.Fatal("oversized full body accepted")
	}
	doc.Body, doc.SchemaVersion = "x", 2
	if _, err := EncodeDocument(doc); err == nil {
		t.Fatal("future document accepted")
	}
}

func completeUsageBuilder(t *testing.T) (*Builder, string, time.Time) {
	t.Helper()
	b, path := backfillBuilder(t, "UTC")
	now := time.Now().UTC().Add(-time.Hour).Truncate(time.Hour)
	from := now.AddDate(0, 0, -40)
	ctx := context.Background()
	if err := b.Store.SetComponentStatus(ctx, "interface_counter", "running", from); err != nil {
		t.Fatal(err)
	}
	if err := b.Store.SetComponentStatus(ctx, "interface_counter", "running", now); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE retention_meta SET tracking_started=? WHERE id=1`, from.UnixMilli()); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	today := civilDate(now, time.UTC)
	for offset := 30; offset > 0; offset-- {
		for hour := range 24 {
			if err := b.Store.AddInterface(ctx, model.InterfaceTotals{HourUTC: today.AddDate(0, 0, -offset).Add(time.Duration(hour) * time.Hour), TXBytes: 3600, RXBytes: 7200}); err != nil {
				t.Fatal(err)
			}
		}
	}
	for at := today; at.Before(now); at = at.Add(time.Hour) {
		if err := b.Store.AddInterface(ctx, model.InterfaceTotals{HourUTC: at}); err != nil {
			t.Fatal(err)
		}
	}
	return b, path, now
}

func TestTrendsDistinguishCompletePartialMissingAndPruned(t *testing.T) {
	b, path, now := completeUsageBuilder(t)
	trend, err := b.Trend(context.Background(), 7, now)
	if err != nil || len(trend.Dates) != 7 {
		t.Fatal(err)
	}
	for _, day := range trend.Dates {
		if day.State != "complete" || day.TXBytes == nil || *day.TXBytes != 86400 {
			t.Fatalf("bad complete day: %+v", day)
		}
	}
	start, end := trend.Dates[0].Start, trend.Dates[0].End
	if err := b.Store.RecordCoverageGap(context.Background(), store.CoverageGap{Name: "interface_counter", Reason: "fixture", Start: start, End: start.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	trend, err = b.Trend(context.Background(), 7, now)
	if err != nil || trend.Dates[0].State != "partial" {
		t.Fatal("coverage gap hidden", err)
	}
	view := store.IntegrityView{Components: []store.IntegrityComponent{{Name: "interface_counter", RunningMS: end.Sub(start).Milliseconds()}}}
	retention := store.RetentionView{TrackingStarted: start.Add(-time.Hour), Entries: []store.RetentionEntry{{Dataset: "interface_hourly"}}}
	if state, _ := usageCoverage(start, end, view, retention); state != "pruned" {
		t.Fatal("pruning hidden")
	}
	empty, _ := backfillBuilder(t, "UTC")
	missing, err := empty.Trend(context.Background(), 7, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, day := range missing.Dates {
		if day.State != "missing" || day.RXBytes != nil || day.TXBytes != nil {
			t.Fatal("missing data filled with zero")
		}
	}
	if _, err := empty.Trend(context.Background(), 8, now); err == nil {
		t.Fatal("unsupported trend accepted")
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM interface_hourly WHERE hour_utc>=? AND hour_utc<?`, trend.Dates[1].Start.Unix(), trend.Dates[1].End.Unix()); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM interface_hourly WHERE hour_utc=?`, trend.Dates[2].Start.Unix()); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()
	withoutRows, err := b.Trend(context.Background(), 7, now)
	if err != nil || withoutRows.Dates[1].State != "missing" || withoutRows.Dates[1].TXBytes != nil || withoutRows.Dates[2].State != "partial" {
		t.Fatal("heartbeat masked missing hourly records", err)
	}
}

func TestTrendOriginalSnapshotSurvivesLaterCoverageChanges(t *testing.T) {
	b, _, now := completeUsageBuilder(t)
	date, start, _ := PreviousDay(now, time.UTC)
	before, _, err := b.archiveDate(context.Background(), date, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Store.RecordCoverageGap(context.Background(), store.CoverageGap{Name: "interface_counter", Reason: "later_fixture", Start: start, End: start.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	trend, err := b.Trend(context.Background(), 7, now)
	if err != nil {
		t.Fatal(err)
	}
	day := trend.Dates[len(trend.Dates)-1]
	if day.Source != "original_full_snapshot" || day.State != "complete" || day.TXBytes == nil || *day.TXBytes != 86400 {
		t.Fatalf("original snapshot rebuilt: %+v", day)
	}
	after, err := b.Store.Report(context.Background(), date)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("trend changed archive", err)
	}
}

func TestForecastCompleteDayScenariosQuotaAndZeroRate(t *testing.T) {
	b, path, now := completeUsageBuilder(t)
	b.CycleStartDay, b.ThresholdBytes, b.ThresholdCost = 28, 1_000_000, 1
	b.Billing = &billing.Profile{SchemaVersion: 1, Name: "fixture", Provider: "custom", SourceRegion: "test", Currency: "USD", EffectiveDate: "2026-01-01", SourceURL: "https://example.invalid/prices", UnitBytes: 1_000_000_000, FreeGB: .001, InternetEgress: []billing.Tier{{PricePerGB: 1}}}
	value, err := b.Forecast(context.Background(), now)
	if err != nil || !value.Available || len(value.Scenarios) != 2 || value.MinimumTXBytes == nil || value.MaximumTXBytes == nil || value.CurrentCost == nil || value.PeriodStart.In(time.UTC).Day() != 28 {
		t.Fatalf("forecast unavailable: %+v %v", value, err)
	}
	for _, scenario := range value.Scenarios {
		if scenario.RateBytesPerSecond != 1 || scenario.ProjectedTXBytes < value.ObservedTXBytes || scenario.ProjectedCost == nil || scenario.CostThresholdAt != nil {
			t.Fatalf("wrong quota scenario: %+v", scenario)
		}
	}
	if b.Billing.Estimate(value.ObservedTXBytes) != *value.CurrentCost {
		t.Fatal("free allowance/current cycle differs")
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE interface_hourly SET rx_bytes=0,tx_bytes=0`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()
	zero, err := b.Forecast(context.Background(), now)
	if err != nil || !zero.Available || len(zero.Scenarios) != 2 {
		t.Fatal("complete observed zero rate treated as missing", err)
	}
	for _, scenario := range zero.Scenarios {
		if scenario.RateBytesPerSecond != 0 || scenario.ProjectedTXBytes != 0 || scenario.ByteThresholdAt != nil || scenario.CostThresholdAt != nil {
			t.Fatal("zero rate invented crossing")
		}
	}
	if err := b.Store.RecordCoverageGap(context.Background(), store.CoverageGap{Name: "interface_counter", Reason: "fixture", Start: zero.PeriodStart, End: now}); err != nil {
		t.Fatal(err)
	}
	incomplete, err := b.Forecast(context.Background(), now)
	if err != nil || incomplete.Available || incomplete.Reason != "current_cycle_coverage_incomplete" {
		t.Fatal("incomplete cycle predicted", err)
	}
}

func TestBillingCycleCivilMonthTimezoneDSTAndBoundaries(t *testing.T) {
	for _, tc := range []struct {
		zone, now, want string
		day             int
	}{
		{"UTC", "2026-09-14T12:00:00Z", "2026-08-15T00:00:00Z", 15},
		{"UTC", "2026-09-15T00:00:00Z", "2026-09-15T00:00:00Z", 15},
		{"Asia/Kathmandu", "2026-09-15T00:00:00Z", "2026-09-14T18:15:00Z", 15},
		{"America/New_York", "2026-03-08T12:00:00Z", "2026-03-08T05:00:00Z", 8},
		{"UTC", "2026-01-01T00:00:00Z", "2025-12-28T00:00:00Z", 28},
	} {
		t.Run(tc.zone+tc.now, func(t *testing.T) {
			loc, err := time.LoadLocation(tc.zone)
			if err != nil {
				t.Fatal(err)
			}
			now, _ := time.Parse(time.RFC3339, tc.now)
			start, end, ok := BillingCycle(now, loc, tc.day)
			if !ok || start.Format(time.RFC3339) != tc.want || start.After(now) || !now.Before(end) {
				t.Fatalf("wrong cycle %s/%s %t", start, end, ok)
			}
		})
	}
	if _, _, ok := BillingCycle(time.Now(), time.UTC, 29); ok {
		t.Fatal("unsupported cycle day accepted")
	}
}
