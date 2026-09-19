// SPDX-License-Identifier: MIT

package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/api"
	"github.com/littlesho/NodeRampart/internal/billing"
	"github.com/littlesho/NodeRampart/internal/model"
	"github.com/littlesho/NodeRampart/internal/store"
)

const rolloverGB = uint64(1_000_000_000)

func rolloverApp(t *testing.T, bytes, cost bool) *App {
	t.Helper()
	a := eventTestApp(t)
	a.options.Config.Alerts.Budget.Enabled = true
	a.options.Config.Alerts.Health.Enabled = false
	a.options.Config.Notifications.Telegram.Enabled = false
	a.report.Location = time.UTC
	if bytes {
		a.options.Config.Alerts.Budget.MonthlyBytes = 100 * rolloverGB
	}
	if cost {
		a.options.Config.Alerts.Budget.MonthlyCost = 100
	}
	a.options.Billing = &billing.Profile{SchemaVersion: 1, Name: "synthetic-private-tariff", Provider: "custom", SourceRegion: "synthetic-region", EffectiveDate: "2026-08-01", SourceURL: "https://example.invalid/synthetic-private-source", Currency: "USD", UnitBytes: rolloverGB, InternetEgress: []billing.Tier{{PricePerGB: 1}}}
	return a
}

func rolloverAddTX(t *testing.T, a *App, at time.Time, amount uint64) {
	t.Helper()
	if err := a.options.Store.AddInterface(context.Background(), model.InterfaceTotals{HourUTC: at, TXBytes: amount}); err != nil {
		t.Fatal(err)
	}
}

func rolloverState(t *testing.T, a *App, key string) (store.MonitorState, monitorData) {
	t.Helper()
	rows, err := a.options.Store.MonitorStates(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.Key != key {
			continue
		}
		data, err := decodeMonitorData(row.Data, key)
		if err != nil {
			t.Fatal(err)
		}
		return row, data
	}
	t.Fatalf("missing durable state for %s", key)
	return store.MonitorState{}, monitorData{}
}

func assertRolloverPrevious(t *testing.T, state monitorData, period string, start, end time.Time, milestone int, evaluated bool) {
	t.Helper()
	p := state.PreviousPeriod
	if p.Period != period || !p.Start.Equal(start) || !p.End.Equal(end) || p.Milestone != milestone || p.Evaluated != evaluated || state.Status.PreviousPeriod != p {
		t.Fatalf("wrong previous-period result: %+v", p)
	}
	if evaluated && p.Reason != "threshold_crossed" || !evaluated && p.Reason != "historical_policy_unavailable" {
		t.Fatalf("wrong closing reason: %+v", p)
	}
}

func TestMonitorRolloverClosesLastMinuteBytesAndCostWithRecordedPolicy(t *testing.T) {
	a := rolloverApp(t, true, true)
	ctx := context.Background()
	end := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	start := end.AddDate(0, -1, 0)
	last := end.Add(-30 * time.Second)
	rolloverAddTX(t, a, last, 79*rolloverGB)
	m := newMonitorRuntime(a)
	m.pass(ctx, last, nil)
	for _, key := range []string{"budget_month_bytes", "budget_month_cost"} {
		row, state := rolloverState(t, a, key)
		if state.Period != "2026-08" || state.Milestone != 0 || state.MonthPolicy == nil || state.SchemaVersion != 2 {
			t.Fatal("old policy was not saved below first milestone")
		}
		if strings.Contains(string(row.Data), "synthetic-private") {
			t.Fatal("monthly policy retained tariff identity")
		}
	}
	if len(monitorEvents(t, a, end)) != 0 {
		t.Fatal("79 percent unexpectedly alerted")
	}
	// The last counter flush arrives after the old month's last monitor pass.
	rolloverAddTX(t, a, last, 21*rolloverGB)
	a.options.Config.Alerts.Budget.MonthlyBytes = 1000 * rolloverGB
	a.options.Config.Alerts.Budget.MonthlyCost = 9000
	a.options.Billing.Currency, a.options.Billing.UnitBytes, a.options.Billing.FreeGB = "EUR", 1<<30, 5
	a.options.Billing.InternetEgress[0].PricePerGB = 99
	m = newMonitorRuntime(a)
	m.pass(ctx, end.Add(time.Second), nil)
	events := monitorEvents(t, a, end)
	if len(events) != 2 {
		t.Fatalf("expected one old-month 100%% event per metric, got %d", len(events))
	}
	ids := map[string]string{}
	for _, e := range events {
		ids[e.Kind] = e.ID
		if e.Evidence["period"] != "2026-08" || e.Evidence["milestone"] != "100" || e.Phase != "start" || e.Evidence["observed_bytes"] != "100000000000" || e.Evidence["period_start_utc"] != start.Format(time.RFC3339Nano) || e.Evidence["period_end_utc"] != end.Format(time.RFC3339Nano) || e.ObservedAt.Before(end) {
			t.Fatalf("wrong closing evidence: %+v", e)
		}
		if e.Kind == "budget_month_cost" && (e.Evidence["observed_cost"] != "100" || e.Evidence["threshold_cost"] != "100" || e.Evidence["currency"] != "USD") {
			t.Fatal("closing used new month's tariff or threshold")
		}
		_, state := rolloverState(t, a, e.Kind)
		if state.Period != "2026-09" || state.MonthFinalized || state.Milestone != 0 {
			t.Fatal("current month did not advance independently")
		}
		assertRolloverPrevious(t, state, "2026-08", start, end, 100, true)
		// The monitor already advanced; each public incident must still carry its
		// own historical amount and bounds, without borrowing the current status.
		bundle, err := a.evidenceSnapshot(ctx, api.EvidenceArgs{Start: end.Add(-time.Minute), End: end.Add(time.Hour), IncidentID: e.IncidentID})
		if err != nil {
			t.Fatal(err)
		}
		if len(bundle.Events) != 1 || bundle.Events[0].Alert == nil {
			t.Fatal("closing incident lost its typed alert context")
		}
		alert := bundle.Events[0].Alert
		if alert.Availability != "recorded" || alert.Period != "2026-08" || alert.PeriodStart == nil || !alert.PeriodStart.Equal(start) || alert.PeriodEnd == nil || !alert.PeriodEnd.Equal(end) || alert.ObservedBytes == nil || *alert.ObservedBytes != 100*rolloverGB || alert.Milestone == nil || *alert.Milestone != 100 {
			t.Fatalf("public context borrowed current values: %+v", alert)
		}
		if e.Kind == "budget_month_cost" && (alert.ObservedCost == nil || *alert.ObservedCost != 100 || alert.ThresholdCost == nil || *alert.ThresholdCost != 100 || alert.Currency != "USD") {
			t.Fatal("public context lost saved cost evidence")
		}
	}
	m = newMonitorRuntime(a)
	m.pass(ctx, end.Add(2*time.Minute), nil)
	events = monitorEvents(t, a, end)
	if len(events) != 2 {
		t.Fatal("restart replayed monthly closing")
	}
	for _, e := range events {
		if ids[e.Kind] != e.ID {
			t.Fatal("restart replaced closing identities")
		}
	}
}

func TestMonitorRolloverUsesLatestSameMonthPolicyWithoutReplayingMilestones(t *testing.T) {
	a := rolloverApp(t, true, true)
	ctx := context.Background()
	end := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	last := end.Add(-3 * time.Minute)
	rolloverAddTX(t, a, last, 80*rolloverGB)
	m := newMonitorRuntime(a)
	m.pass(ctx, last, nil)
	initial := monitorEvents(t, a, end)
	if len(initial) != 2 {
		t.Fatal("initial 80 percent milestone missing")
	}
	// Byte budget increases; cost threshold and rate both double. Neither edit
	// repeats an already-recorded 80 percent alert, but both policies are saved.
	a.options.Config.Alerts.Budget.MonthlyBytes = 200 * rolloverGB
	a.options.Config.Alerts.Budget.MonthlyCost = 200
	a.options.Billing.InternetEgress[0].PricePerGB = 2
	m.pass(ctx, last.Add(time.Minute), nil)
	_, b := rolloverState(t, a, "budget_month_bytes")
	_, c := rolloverState(t, a, "budget_month_cost")
	if b.MonthPolicy.ThresholdBytes != 200*rolloverGB || c.MonthPolicy.ThresholdCost != 200 || c.MonthPolicy.Tiers[0].PricePerGB != 2 || len(monitorEvents(t, a, end)) != 2 {
		t.Fatal("same-period edits were not saved without replay")
	}
	// Cost reaches 100 percent at 100 GB; bytes remain below their newly saved
	// 200 GB threshold. An obsolete original byte policy would falsely alert.
	rolloverAddTX(t, a, last, 20*rolloverGB)
	a.options.Config.Alerts.Budget.MonthlyBytes = 10 * rolloverGB
	a.options.Config.Alerts.Budget.MonthlyCost = 1
	a.options.Billing.InternetEgress[0].PricePerGB = 100
	m = newMonitorRuntime(a)
	m.pass(ctx, end.Add(time.Second), nil)
	events := monitorEvents(t, a, end)
	if len(events) != 3 {
		t.Fatalf("latest saved policy not used: got %d events", len(events))
	}
	var closed model.Event
	for _, e := range events {
		if !e.ObservedAt.Before(end) {
			closed = e
		}
	}
	if closed.Kind != "budget_month_cost" || closed.Phase != "update" || closed.Evidence["observed_cost"] != "200" || closed.Evidence["threshold_cost"] != "200" {
		t.Fatal("closing ignored latest saved cost policy")
	}
	for _, e := range initial {
		if e.Kind == closed.Kind && e.IncidentID != closed.IncidentID {
			t.Fatal("policy edit changed the existing incident")
		}
	}
	_, b = rolloverState(t, a, "budget_month_bytes")
	if b.PreviousPeriod.Milestone != 80 || !b.PreviousPeriod.Evaluated {
		t.Fatal("lower observed ratio erased already-recorded byte milestone")
	}
}

func TestMonitorRolloverSavedCostPreservesFreeAllowanceBinaryUnitAndTiers(t *testing.T) {
	a := rolloverApp(t, false, true)
	ctx := context.Background()
	end := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	last := end.Add(-time.Minute)
	// 79 GiB minus 10 free GiB costs 40*1.25 + 29*1 = 79 USD.
	// At 100 GiB the saved two-tier policy costs exactly 100 USD.
	a.options.Billing.UnitBytes = 1 << 30
	a.options.Billing.FreeGB = 10
	a.options.Billing.InternetEgress = []billing.Tier{{UpToGB: 40, PricePerGB: 1.25}, {PricePerGB: 1}}
	rolloverAddTX(t, a, last, 79*(1<<30))
	m := newMonitorRuntime(a)
	m.pass(ctx, last, nil)
	_, old := rolloverState(t, a, "budget_month_cost")
	if old.Status.ObservedCost != 79 || old.MonthPolicy == nil || old.MonthPolicy.UnitBytes != 1<<30 || old.MonthPolicy.FreeGB != 10 || len(old.MonthPolicy.Tiers) != 2 {
		t.Fatal("nontrivial closing tariff was not saved")
	}
	rolloverAddTX(t, a, last, 21*(1<<30))
	a.options.Config.Alerts.Budget.MonthlyCost = 9000
	a.options.Billing.Currency, a.options.Billing.UnitBytes, a.options.Billing.FreeGB = "EUR", rolloverGB, 0
	a.options.Billing.InternetEgress = []billing.Tier{{PricePerGB: 99}}
	m = newMonitorRuntime(a)
	m.pass(ctx, end.Add(time.Second), nil)
	events := monitorEvents(t, a, end)
	if len(events) != 1 || events[0].Evidence["observed_bytes"] != fmt.Sprint(uint64(100*(1<<30))) || events[0].Evidence["observed_cost"] != "100" || events[0].Evidence["currency"] != "USD" || events[0].Evidence["threshold_cost"] != "100" {
		t.Fatalf("saved free allowance/unit/tiers were changed: %+v", events)
	}
}

type rolloverCommitHook struct {
	monitorRepository
	hook func(store.MonitorState, monitorData) error
}

func (r *rolloverCommitHook) CommitMonitorState(ctx context.Context, next store.MonitorState, expected int64, events []model.Event, messages []*store.OutboxMessage) error {
	data, err := decodeMonitorData(next.Data, next.Key)
	if err != nil {
		return err
	}
	if r.hook != nil {
		if err := r.hook(next, data); err != nil {
			return err
		}
	}
	return r.monitorRepository.CommitMonitorState(ctx, next, expected, events, messages)
}

func TestMonitorRolloverRestartAfterCloseCommitBeforeAdvancement(t *testing.T) {
	a := rolloverApp(t, true, false)
	ctx := context.Background()
	end := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	last := end.Add(-time.Minute)
	rolloverAddTX(t, a, last, 80*rolloverGB)
	m := newMonitorRuntime(a)
	m.pass(ctx, last, nil)
	rolloverAddTX(t, a, last, 20*rolloverGB)
	m.repository = &rolloverCommitHook{monitorRepository: a.options.Store, hook: func(_ store.MonitorState, next monitorData) error {
		if next.Period == "2026-09" {
			return errors.New("synthetic interruption before current-period commit")
		}
		return nil
	}}
	m.pass(ctx, end.Add(time.Second), nil)
	_, durable := rolloverState(t, a, "budget_month_bytes")
	if durable.Period != "2026-08" || !durable.MonthFinalized || durable.Milestone != 100 || len(monitorEvents(t, a, end)) != 2 {
		t.Fatal("did not stop after an atomic old-month close")
	}
	if m.pending["budget_month_bytes"] == nil {
		t.Fatal("current-period interruption fixture did not fire")
	}
	before := monitorEvents(t, a, end)
	m = newMonitorRuntime(a)
	m.pass(ctx, end.Add(time.Minute), nil)
	_, durable = rolloverState(t, a, "budget_month_bytes")
	after := monitorEvents(t, a, end)
	if durable.Period != "2026-09" || len(after) != 2 || before[0].ID != after[0].ID || before[1].ID != after[1].ID {
		t.Fatal("restart repeated the committed closing event")
	}
	assertRolloverPrevious(t, durable, "2026-08", end.AddDate(0, -1, 0), end, 100, true)
}

func TestMonitorRolloverFailedCloseRetriesFrozenEventBeforeAdvancing(t *testing.T) {
	a := rolloverApp(t, true, false)
	ctx := context.Background()
	end := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	last := end.Add(-time.Minute)
	rolloverAddTX(t, a, last, 79*rolloverGB)
	m := newMonitorRuntime(a)
	m.pass(ctx, last, nil)
	rolloverAddTX(t, a, last, 21*rolloverGB)
	fail := true
	m.repository = &rolloverCommitHook{monitorRepository: a.options.Store, hook: func(_ store.MonitorState, next monitorData) error {
		if fail && next.MonthFinalized {
			return errors.New("synthetic closing transaction failure")
		}
		return nil
	}}
	m.pass(ctx, end.Add(time.Second), nil)
	_, durable := rolloverState(t, a, "budget_month_bytes")
	pending := m.pending["budget_month_bytes"]
	if durable.MonthFinalized || durable.Period != "2026-08" || durable.Milestone != 0 || pending == nil || !pending.observation.Closing || len(pending.events) != 1 || len(monitorEvents(t, a, end)) != 0 {
		t.Fatal("failed closing did not remain pending without a durable event")
	}
	want := pending.events[0]
	// A later counter correction must not alter the already-frozen pending
	// decision. Only its eventual commit may advance the current month.
	rolloverAddTX(t, a, last, 100*rolloverGB)
	fail = false
	m.pass(ctx, end.Add(time.Minute), nil)
	events := monitorEvents(t, a, end)
	if len(events) != 1 || events[0].ID != want.ID || events[0].Evidence["observed_bytes"] != "100000000000" || !events[0].ObservedAt.Equal(want.ObservedAt.UTC().Truncate(time.Millisecond)) {
		t.Fatal("retry changed or duplicated frozen closing evidence")
	}
	_, durable = rolloverState(t, a, "budget_month_bytes")
	if durable.Period != "2026-09" || len(m.pending) != 0 {
		t.Fatal("successful retry did not advance current period")
	}
	m = newMonitorRuntime(a)
	m.pass(ctx, end.Add(2*time.Minute), nil)
	if len(monitorEvents(t, a, end)) != 1 {
		t.Fatal("restart replayed retried closing")
	}
}

func TestMonitorRolloverLegacyBytesFallbackAndUnavailableCostPolicy(t *testing.T) {
	a := rolloverApp(t, true, true)
	ctx := context.Background()
	end := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	last := end.Add(-time.Minute)
	rolloverAddTX(t, a, last, 79*rolloverGB)
	m := newMonitorRuntime(a)
	m.pass(ctx, last, nil)
	for _, key := range []string{"budget_month_bytes", "budget_month_cost"} {
		row, data := rolloverState(t, a, key)
		data.SchemaVersion, data.MonthPolicy = 1, nil
		encoded, err := json.Marshal(data)
		if err != nil {
			t.Fatal(err)
		}
		if err := a.options.Store.CommitMonitorState(ctx, store.MonitorState{Key: key, UpdatedAt: data.ObservedAt, Data: encoded}, row.Revision, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	rolloverAddTX(t, a, last, 21*rolloverGB)
	a.options.Config.Alerts.Budget.MonthlyBytes = 1000 * rolloverGB
	a.options.Config.Alerts.Budget.MonthlyCost = 1
	a.options.Billing.Currency = "EUR"
	m = newMonitorRuntime(a)
	m.pass(ctx, end.Add(time.Second), nil)
	events := monitorEvents(t, a, end)
	if len(events) != 1 || events[0].Kind != "budget_month_bytes" || events[0].Evidence["threshold_bytes"] != "100000000000" {
		t.Fatal("legacy policy fallback fabricated cost or lost known byte threshold")
	}
	_, b := rolloverState(t, a, "budget_month_bytes")
	_, c := rolloverState(t, a, "budget_month_cost")
	assertRolloverPrevious(t, b, "2026-08", end.AddDate(0, -1, 0), end, 100, true)
	assertRolloverPrevious(t, c, "2026-08", end.AddDate(0, -1, 0), end, 0, false)
	if c.Period != "2026-09" || c.PreviousPeriod.Coverage != "unknown" || c.SchemaVersion != 2 {
		t.Fatal("legacy missing policy did not advance with explicit uncertainty")
	}
}

func TestMonitorRolloverTimezoneAdvanceWaitsForSavedOldEnd(t *testing.T) {
	a := rolloverApp(t, true, false)
	ctx := context.Background()
	oldLoc, err := time.LoadLocation("Etc/GMT+12")
	if err != nil {
		t.Fatal(err)
	}
	newLoc, err := time.LoadLocation("Etc/GMT-14")
	if err != nil {
		t.Fatal(err)
	}
	a.report.Location = oldLoc
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, oldLoc).UTC()
	end := time.Date(2026, 10, 1, 0, 0, 0, 0, oldLoc).UTC()
	early := time.Date(2026, 10, 1, 0, 0, 30, 0, newLoc).UTC()
	last := early.Add(-time.Minute)
	rolloverAddTX(t, a, last, 79*rolloverGB)
	m := newMonitorRuntime(a)
	m.pass(ctx, last, nil)
	_, before := rolloverState(t, a, "budget_month_bytes")
	if before.Period != "2026-09" || !before.PeriodEnd.Equal(end) || !end.After(early) {
		t.Fatal("timezone fixture lacks overlapping old and new months")
	}
	a.report.Location = newLoc
	a.options.Config.Alerts.Budget.MonthlyBytes = 1000 * rolloverGB
	m = newMonitorRuntime(a)
	m.pass(ctx, early, nil)
	_, waiting := rolloverState(t, a, "budget_month_bytes")
	if waiting.Period != "2026-09" || !waiting.PeriodEnd.Equal(end) || waiting.MonthPolicy.ThresholdBytes != 100*rolloverGB || waiting.MonthFinalized || len(monitorEvents(t, a, end)) != 0 {
		t.Fatal("new calendar label discarded unfinished saved old month")
	}
	found := false
	for _, rule := range a.MonitorStatus().Rules {
		if rule.Key == "budget_month_bytes" {
			found = rule.Reason == "period_close_pending" && !rule.Available
		}
	}
	if !found {
		t.Fatal("waiting for old UTC end was not visible")
	}
	rolloverAddTX(t, a, end.Add(-time.Minute), 21*rolloverGB)
	m = newMonitorRuntime(a)
	m.pass(ctx, end.Add(time.Minute), nil)
	_, done := rolloverState(t, a, "budget_month_bytes")
	if done.Period != "2026-10" {
		t.Fatal("closing did not resume after saved UTC end")
	}
	assertRolloverPrevious(t, done, "2026-09", start, end, 100, true)
	events := monitorEvents(t, a, end)
	if len(events) != 1 || events[0].Evidence["period_start_utc"] != start.Format(time.RFC3339Nano) || events[0].Evidence["period_end_utc"] != end.Format(time.RFC3339Nano) || events[0].Evidence["observed_bytes"] != "100000000000" {
		t.Fatal("closing reinterpreted old UTC range under new timezone")
	}
}

func TestMonitorRolloverLongOutageClosesOnlyOneRecordedMonth(t *testing.T) {
	a := rolloverApp(t, true, false)
	ctx := context.Background()
	end := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	last := end.Add(-time.Minute)
	rolloverAddTX(t, a, last, 79*rolloverGB)
	m := newMonitorRuntime(a)
	m.pass(ctx, last, nil)
	rolloverAddTX(t, a, last, 21*rolloverGB)
	for month := 1; month <= 2; month++ {
		rolloverAddTX(t, a, end.AddDate(0, month, 5), 1000*rolloverGB)
	}
	now := time.Date(2026, 12, 15, 12, 0, 0, 0, time.UTC)
	a.options.Config.Alerts.Budget.MonthlyBytes = 1000 * rolloverGB
	m = newMonitorRuntime(a)
	m.pass(ctx, now, nil)
	_, state := rolloverState(t, a, "budget_month_bytes")
	if state.Period != "2026-12" {
		t.Fatal("long outage did not reach current period")
	}
	assertRolloverPrevious(t, state, "2026-08", end.AddDate(0, -1, 0), end, 100, true)
	events := monitorEvents(t, a, now)
	if len(events) != 1 || events[0].Evidence["period"] != "2026-08" || events[0].Evidence["observed_bytes"] != fmt.Sprint(100*rolloverGB) {
		t.Fatal("outage enumerated missing months or combined unrelated intervals")
	}
}
