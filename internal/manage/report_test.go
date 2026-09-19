// SPDX-License-Identifier: MIT

package manage

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/littlesho/NodeRampart/internal/api"
	"github.com/littlesho/NodeRampart/internal/billing"
	"github.com/littlesho/NodeRampart/internal/store"
)

func savedBillingReport(t *testing.T) store.ReportSnapshot {
	t.Helper()
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	p := billing.Profile{SchemaVersion: 1, Name: "saved-custom-rate", Provider: "custom", SourceRegion: "saved-region", Currency: "USD", EffectiveDate: "2026-09-12", SourceURL: "https://example.invalid/pricing?a=1&b=2", FreeGB: 10, UnitBytes: 1 << 30, InternetEgress: []billing.Tier{{UpToGB: 100, PricePerGB: 0.1}, {PricePerGB: 0.2}}}
	s, err := billing.NewSnapshot(p, 160*(1<<30), start, start.Add(10*24*time.Hour), start.Add(11*24*time.Hour), "UTC")
	if err != nil {
		t.Fatal(err)
	}
	return store.ReportSnapshot{Date: "2026-09-10", Title: "Archived day", Body: "<b>Original archived report</b>\nPreserved content", PeriodStart: start.Add(9 * 24 * time.Hour), PeriodEnd: s.PeriodEnd, GeneratedAt: s.GeneratedAt, Billing: s}
}

func TestManagementReportShowsSavedBillingInputsWithoutActiveProfile(t *testing.T) {
	report := savedBillingReport(t)
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	m := &Manager{ConfigPath: "/unreadable/synthetic/config.json", Request: func(_ context.Context, command string, args any) (json.RawMessage, error) {
		q, ok := args.(api.DateArgs)
		if command != "report_show" || !ok || q.Date != report.Date {
			t.Fatal("unexpected request")
		}
		return encoded, nil
	}}
	out, err := m.Action(context.Background(), "report_show", map[string]string{"date": report.Date})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, report.Title+"\n"+report.Body) {
		t.Fatal("archived body changed")
	}
	for _, wanted := range []string{"Saved billing snapshot", "saved-custom-rate", "saved-region", "2026-09-12", "https://example.invalid/pricing?a=1&amp;b=2", report.Billing.ProfileSHA256, "171798691840 bytes", "1073741824 bytes", "10 pricing GB", "0–100 pricing GB: 0.1 USD/GB", "100+ pricing GB: 0.2 USD/GB", "20 USD; 150 billable pricing GB", billing.SnapshotBasis, "2026-09-01T00:00:00Z", "2026-09-11T00:00:00Z", "2026-09-12T00:00:00Z"} {
		if !strings.Contains(out, wanted) {
			t.Fatalf("saved input omitted: %q", wanted)
		}
	}
	if len(out) > maxReportDisplayBytes {
		t.Fatal("display exceeds bound")
	}
}

func TestManagementReportLabelsMissingInvalidAndLivePricing(t *testing.T) {
	for _, tc := range []struct {
		name     string
		archived bool
		data     string
		want     string
	}{
		{"legacy", true, `{"title":"Old","body":"Keep old body"}`, "Unavailable for this archived report"},
		{"nil", true, `{"body":"Keep old body","billing":null}`, "Unavailable for this archived report"},
		{"invalid", true, `{"body":"Keep old body","billing":{"schema_version":999}}`, "failed validation"},
		{"live", false, `{"report":"Keep old body","billing":{"schema_version":999}}`, "Current report"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := formatReport([]byte(tc.data), tc.archived)
			if err != nil || !strings.Contains(out, "Keep old body") || !strings.Contains(out, tc.want) {
				t.Fatalf("missing explicit report basis: %v %s", err, out)
			}
		})
	}
}

func TestManagementReportBoundsAndTerminalSafety(t *testing.T) {
	r := savedBillingReport(t)
	r.Title = "Title\x1b[2J\u202e"
	r.Body = "Body\x07\u2066\n< b>"
	r.Billing.Profile.Name = "saved\u202ename <b>[red] literal"
	// Exercise normal IPC HTML escaping beyond the canonical snapshot's limit.
	r.Billing.Profile.SourceURL = "https://example.invalid/?" + strings.Repeat("&", 4000)
	var err error
	r.Billing, err = billing.NewSnapshot(r.Billing.Profile, r.Billing.OutboundBytes, r.Billing.PeriodStart, r.Billing.PeriodEnd, r.Billing.GeneratedAt, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	out, err := formatReport(data, true)
	if err != nil {
		t.Fatal(err)
	}
	if strings.IndexFunc(out, func(r rune) bool { return r != '\n' && r != '\t' && unicode.IsControl(r) || unicode.Is(unicode.Cf, r) }) >= 0 {
		t.Fatal("terminal controls escaped display sanitization")
	}
	if !strings.Contains(out, "savedname &lt;b&gt;[red] literal") || !strings.Contains(out, "20 USD; 150 billable pricing GB") {
		t.Fatal("bounded valid snapshot lost during escaping")
	}
	for _, data := range [][]byte{[]byte(strings.Repeat(" ", maxReportDisplayBytes+1)), []byte(`{"body":`), []byte(`{"body":"` + strings.Repeat("x", 4097) + `"}`)} {
		if _, err := formatReport(data, true); err == nil {
			t.Fatal("invalid or unbounded report accepted")
		}
	}
}
