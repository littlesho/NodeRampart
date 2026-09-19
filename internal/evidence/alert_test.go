// SPDX-License-Identifier: MIT

package evidence

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/api"
	"github.com/littlesho/NodeRampart/internal/model"
	"github.com/littlesho/NodeRampart/internal/store"
)

func savedAlertFields() map[string]string {
	return map[string]string{"reason": "threshold_crossed", "period": "2026-09", "period_start_utc": "2026-09-01T00:00:00Z", "period_end_utc": "2026-10-01T00:00:00Z", "observed_bytes": "9007199254740993", "threshold_bytes": "100", "observed_cost": "8", "threshold_cost": "10", "currency": "USD", "milestone": "80", "coverage": "incomplete", "basis": model.LegacyAlertBasis}
}

func TestStoredEventAlertContextRemainsHistoricalAndIdentityFree(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "fixture.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	fields := savedAlertFields()
	fields["path"], fields["profile"], fields["license_key"] = "synthetic_private_path", "synthetic_private_tariff", "synthetic_private_secret"
	e := model.Event{ID: "synthetic_private_event", IncidentID: "synthetic_private_incident", Kind: "budget_month_cost", ObservedAt: now, Phase: "start", Severity: model.SeverityMedium, Summary: "synthetic_private_summary", Evidence: fields}
	if err := s.InsertEvent(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	raw, err := s.Evidence(context.Background(), api.EvidenceArgs{Start: now.Add(-time.Hour), End: now.Add(time.Hour), IncidentID: e.IncidentID})
	if err != nil {
		t.Fatal(err)
	}
	// A later monitor's inputs deliberately disagree with the event. They must
	// never fill or replace event context, even with v2 policy fields available.
	raw.Monitors = []store.MonitorState{{Key: "budget_month_cost", Revision: 2, UpdatedAt: now, Data: json.RawMessage(`{"schema_version":2,"period":"2026-09","milestone":100,"policy":{"threshold_cost":999,"currency":"EUR","private":"synthetic_private_policy"},"status":{"observed_cost":999,"threshold_cost":999,"currency":"EUR"}}`)}}
	b, err := Build(raw)
	if err != nil {
		t.Fatal(err)
	}
	got := b.Events[0].Alert
	if got == nil || got.Availability != "recorded" || *got.ObservedBytes != 9007199254740993 || *got.ObservedCost != 8 || *got.ThresholdCost != 10 || got.Currency != "USD" || *got.Milestone != 80 || b.Monitors[0].State != "milestone_recorded" {
		t.Fatal("saved event was replaced by current monitor inputs")
	}
	data, err := json.Marshal(b)
	if err != nil || strings.Contains(string(data), "synthetic_private") || strings.Contains(string(data), "EUR") || strings.Contains(string(data), "999") {
		t.Fatal("private/current policy data entered historical event export")
	}
	*raw.Timeline.Events[0].Alert.ObservedCost = 777
	if *b.Events[0].Alert.ObservedCost != 8 {
		t.Fatal("typed projection shares mutable raw pointers")
	}
}

func TestEventAlertProjectionMarksMissingOrInvalidContextUnavailable(t *testing.T) {
	for _, supplied := range []bool{false, true} {
		raw := rawFixture()
		raw.Timeline.Events[0].Kind = "budget_month_cost"
		if supplied {
			raw.Timeline.Events[0].Alert = model.ProjectAlertContext("budget_month_cost", savedAlertFields())
			raw.Timeline.Events[0].Alert.Currency = "synthetic_private_currency"
		}
		b, err := Build(raw)
		if err != nil || b.Events[0].Alert == nil || b.Events[0].Alert.Availability != "unavailable" || b.Events[0].Alert.ObservedCost != nil {
			t.Fatal("missing or invalid context became a zero or current value", err)
		}
	}
	raw := rawFixture()
	raw.Timeline.Events[0].Alert = model.ProjectAlertContext("health_sensor", map[string]string{"reason": "sensor_stale", "condition_since_utc": "2026-09-12T00:00:00Z"})
	b, err := Build(raw)
	if err != nil || b.Events[0].Alert != nil {
		t.Fatal("SSH event retained a foreign monitor context", err)
	}
}

func TestEventAlertExportsShowZeroPartialAndHealthContext(t *testing.T) {
	raw := rawFixture()
	raw.Incident = nil
	e := raw.Timeline.Events[0]
	e.Kind = "budget_month_cost"
	fields := savedAlertFields()
	fields["observed_bytes"], fields["observed_cost"], fields["milestone"] = "0", "0", "0"
	e.Alert = model.ProjectAlertContext(e.Kind, fields)
	raw.Timeline.Events = []store.TimelineEvent{e}
	legacy := e
	legacy.ID += "_legacy"
	delete(fields, "observed_cost")
	delete(fields, "period_start_utc")
	delete(fields, "period_end_utc")
	legacy.Alert = model.ProjectAlertContext(e.Kind, fields)
	raw.Timeline.Events = append(raw.Timeline.Events, legacy)
	health := e
	health.ID += "_health"
	health.Kind = "health_storage"
	health.Alert = model.ProjectAlertContext(health.Kind, map[string]string{"reason": "storage_pressure", "condition_since_utc": "2026-09-12T00:00:00Z"})
	raw.Timeline.Events = append(raw.Timeline.Events, health)
	b, err := Build(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"json", "html"} {
		path := filepath.Join(t.TempDir(), "events."+format)
		if err := Export(context.Background(), b, path, format); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if format == "html" {
			page := string(data)
			for _, value := range []string{"Recorded alert explanation", "Context: partial", "Observed outgoing bytes: 0", "Estimated cost: 0 USD", "Condition observed since (UTC)", "storage_pressure"} {
				if !strings.Contains(page, value) {
					t.Fatalf("HTML lost recorded alert field %q", value)
				}
			}
			if strings.Count(page, "Estimated cost: 0 USD") != 1 || strings.Contains(page, "0x") {
				t.Fatal("HTML fabricated an absent cost or rendered a pointer")
			}
		} else {
			var exported Bundle
			if json.Unmarshal(data, &exported) != nil || exported.Validate() != nil || exported.Events[1].Alert.ObservedCost != nil || exported.Events[1].Alert.Availability != "partial" {
				t.Fatal("JSON lost missing-value semantics")
			}
		}
	}
}

func TestAlertExportIndependentlyRejectsMutatedTypedContext(t *testing.T) {
	for _, mutate := range []func(*model.AlertContext){
		func(a *model.AlertContext) { *a.ObservedCost = math.NaN() },
		func(a *model.AlertContext) { *a.ObservedCost = math.Inf(1) },
		func(a *model.AlertContext) { a.Currency = "synthetic_private_currency" },
		func(a *model.AlertContext) { a.Metric = "health_storage" },
		func(a *model.AlertContext) { a.Reason = "<script>synthetic</script>" },
	} {
		b := fixtureBundle(t)
		b.Events[0].Kind = "budget_month_cost"
		b.Events[0].Alert = model.ProjectAlertContext("budget_month_cost", savedAlertFields())
		mutate(b.Events[0].Alert)
		path := filepath.Join(t.TempDir(), "rejected.json")
		if b.Validate() == nil || Export(context.Background(), b, path, "json") == nil {
			t.Fatal("mutated alert escaped export validation")
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("invalid alert context created an artifact")
		}
	}
}
