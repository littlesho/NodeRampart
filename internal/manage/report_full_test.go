// SPDX-License-Identifier: MIT

package manage

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/api"
	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/report"
	"github.com/littlesho/NodeRampart/internal/store"
)

func TestTUIReportUsesFullDocumentOrExplicitBoundedPreview(t *testing.T) {
	for _, size := range []int{6000, 40 << 10} {
		r := savedBillingReport(t)
		doc := &report.Document{SchemaVersion: 1, Date: r.Date, Title: r.Title, Body: strings.Repeat("中", size/3) + "hash_source", PeriodStart: r.PeriodStart, PeriodEnd: r.PeriodEnd, GeneratedAt: r.GeneratedAt}
		var err error
		r.Document, err = report.EncodeDocument(doc)
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		out, err := formatReport(data, true)
		if err != nil || len(out) > maxReportDisplayBytes {
			t.Fatal("bounded full report display failed", err)
		}
		if size < 32<<10 && !strings.Contains(out, "hash_source") {
			t.Fatal("full saved content not displayed")
		}
		if size > 32<<10 && (!strings.Contains(out, "bounded TUI preview") || !strings.Contains(out, r.Body)) {
			t.Fatal("large report silently truncated or rejected")
		}
	}
}

func TestPricePreviewUsesConfiguredCycle(t *testing.T) {
	m, _ := fixtureManager(t)
	cfg, err := config.Load(m.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Billing.CycleStartDay, cfg.Reports.Timezone = 15, "Asia/Kathmandu"
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(m.ConfigPath, data, 0o640); err != nil {
		t.Fatal(err)
	}
	called := false
	m.Request = func(_ context.Context, command string, args any) (json.RawMessage, error) {
		q, ok := args.(api.HealthArgs)
		if command != "health" || !ok {
			t.Fatal("unexpected price query")
		}
		loc, err := time.LoadLocation(cfg.Reports.Timezone)
		if err != nil {
			t.Fatal(err)
		}
		start, _, ok := report.BillingCycle(q.End, loc, 15)
		if !ok || !q.Start.Equal(start) {
			t.Fatal("price estimate uses calendar month instead of cycle")
		}
		called = true
		return json.Marshal(map[string]any{"history": store.IntegrityView{}, "interface_traffic": store.InterfaceBreakdown{Start: q.Start, End: q.End}})
	}
	text, err := m.showPrices(context.Background())
	if err != nil || !called || !strings.Contains(text, "cycle-to-date") {
		t.Fatal("cycle price preview unavailable", err)
	}
}
