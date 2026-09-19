// SPDX-License-Identifier: MIT

package report

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/billing"
	"github.com/littlesho/NodeRampart/internal/model"
	"github.com/littlesho/NodeRampart/internal/store"
)

func TestArchiveKeepsPricingInputsAfterTariffAndCountersChange(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(24 * time.Hour).Add(12 * time.Hour)
	date, start, end := PreviousDay(now, time.UTC)
	p := billing.Profile{SchemaVersion: 1, Name: "fixture", Provider: "custom", SourceRegion: "fixture", Currency: "USD", EffectiveDate: "2026-01-01", SourceURL: "https://example.com/pricing", UnitBytes: 1 << 30, FreeGB: 1, InternetEgress: []billing.Tier{{PricePerGB: .5}}}
	if err := db.AddInterface(ctx, model.InterfaceTotals{HourUTC: start, TXBytes: 3 << 30}); err != nil {
		t.Fatal(err)
	}
	b := Builder{Store: db, Hostname: "fixture", Location: time.UTC, TopN: 5, Billing: &p}
	first, state, err := b.archiveDate(ctx, date, now)
	if err != nil || state != "generated" {
		t.Fatal("archive failed", state, err)
	}
	if first.Billing == nil || first.Billing.Estimate.Cost != 1 || !first.Billing.PeriodEnd.Equal(end) || !strings.Contains(first.Body, "1.00 USD") {
		t.Fatal("body and snapshot disagree")
	}
	encoded, err := billing.EncodeSnapshot(first.Billing)
	if err != nil {
		t.Fatal(err)
	}
	p.InternetEgress[0].PricePerGB = 50
	p.FreeGB = 0
	if err := db.AddInterface(ctx, model.InterfaceTotals{HourUTC: start, TXBytes: 100 << 30}); err != nil {
		t.Fatal(err)
	}
	again, state, err := b.archiveDate(ctx, date, now.Add(time.Hour))
	if err != nil || state != "already_present" {
		t.Fatal("archive changed", state, err)
	}
	after, err := billing.EncodeSnapshot(again.Billing)
	if err != nil || encoded != after || first.Body != again.Body {
		t.Fatal("historic pricing changed")
	}
	list, err := db.Reports(ctx, "", 20)
	if err != nil || len(list) != 1 || list[0].Billing != nil {
		t.Fatal("report list contains full pricing snapshot")
	}
}
