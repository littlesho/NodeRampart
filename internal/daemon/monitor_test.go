// SPDX-License-Identifier: MIT

package daemon

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/assets"
	"github.com/littlesho/NodeRampart/internal/billing"
	"github.com/littlesho/NodeRampart/internal/collector"
	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/model"
	"github.com/littlesho/NodeRampart/internal/report"
	"github.com/littlesho/NodeRampart/internal/store"
)

func monthObservation(now time.Time, amount, threshold uint64) monitorObservation {
	start, _ := report.MonthStart(now, time.UTC)
	end, _ := report.MonthStart(start.AddDate(0, 1, 5), time.UTC)
	return monitorObservation{Now: now, Status: MonitorRuleStatus{Key: "budget_month_bytes", Enabled: true, Available: true, State: "observed", Reason: "observed_estimate", Period: now.Format("2006-01"), PeriodStart: start, PeriodEnd: end, ObservedBytes: amount, ThresholdBytes: threshold, Coverage: "incomplete", Basis: monitorBasis}}
}

func healthObservation(now time.Time, condition string) monitorObservation {
	reason := "healthy"
	if condition == "failed" {
		reason = "storage_pressure"
	}
	if condition == "unknown" {
		reason = "read_failed"
	}
	return monitorObservation{Now: now, Condition: condition, Status: MonitorRuleStatus{Key: "health_storage", Enabled: true, Available: condition != "unknown", State: "healthy", Reason: reason}}
}

func monitorEvents(t *testing.T, a *App, now time.Time) []model.Event {
	t.Helper()
	events, err := a.options.Store.Events(context.Background(), store.EventQuery{Start: now.AddDate(0, -2, 0), End: now.AddDate(0, 2, 0), Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	return events
}

func TestMonitorMonthlyMilestonesSurviveRestartEditsAndClockRollback(t *testing.T) {
	a := eventTestApp(t)
	a.options.Config.Alerts.Budget.Enabled = true
	a.options.Config.Alerts.Budget.MonthlyBytes = 100
	now := time.Now().UTC()
	m := newMonitorRuntime(a)
	ctx := context.Background()
	m.pass(ctx, now, []monitorObservation{monthObservation(now, 80, 100)})
	events := monitorEvents(t, a, now)
	if len(events) != 1 || events[0].Evidence["milestone"] != "80" || events[0].Evidence["coverage"] != "incomplete" {
		t.Fatalf("missing qualified 80%% event: %+v", events)
	}
	incident := events[0].IncidentID
	// Reload all durable state into a new worker, exactly as after daemon restart.
	m = newMonitorRuntime(a)
	m.pass(ctx, now.Add(time.Minute), []monitorObservation{monthObservation(now.Add(time.Minute), 70, 100)})
	m.pass(ctx, now.Add(2*time.Minute), []monitorObservation{monthObservation(now.Add(2*time.Minute), 25, 20)})
	m.pass(ctx, now.Add(3*time.Minute), []monitorObservation{monthObservation(now.Add(3*time.Minute), 1, 1)})
	if events = monitorEvents(t, a, now); len(events) != 2 || events[0].IncidentID != incident || events[0].Phase != "update" {
		t.Fatalf("edit/restart replayed milestone: %+v", events)
	}
	m.pass(ctx, now.Add(-time.Hour), []monitorObservation{monthObservation(now.Add(-time.Hour), 200, 100)})
	if a.MonitorStatus().Rules[0].Reason != "clock_rollback" || len(monitorEvents(t, a, now)) != 2 {
		t.Fatal("clock rollback emitted older transition")
	}
	nextMonth := now.AddDate(0, 1, 0)
	m.pass(ctx, nextMonth, []monitorObservation{monthObservation(nextMonth, 100, 100)})
	events = monitorEvents(t, a, nextMonth)
	if len(events) != 3 || events[0].IncidentID == incident || events[0].Phase != "start" || events[0].Evidence["milestone"] != "100" {
		t.Fatalf("new period did not jump directly to 100%%: %+v", events)
	}
}

func TestMonitorByteThresholdDoesNotRoundLargeIntegers(t *testing.T) {
	cfg := config.Defaults().Alerts
	now := time.Now().UTC()
	threshold := uint64(math.MaxInt64)
	boundary := threshold/5*4 + (threshold%5*4+4)/5
	for _, pair := range []struct {
		amount uint64
		events int
	}{{boundary - 1, 0}, {boundary, 1}, {threshold, 1}} {
		_, events := evaluateMonitor(monitorData{}, monthObservation(now, pair.amount, threshold), cfg, now)
		if len(events) != pair.events {
			t.Fatalf("amount %d wrong integer threshold", pair.amount)
		}
	}
}

func TestMonitorHealthDebounceUnknownRecoveryReminderAndDisabled(t *testing.T) {
	cfg := config.Defaults().Alerts
	cfg.Health.Enabled = true
	cfg.Health.GracePeriod.Duration = time.Minute
	cfg.Health.RecoveryPeriod.Duration = time.Minute
	cfg.Health.ReminderInterval.Duration = time.Hour
	now := time.Now().UTC()
	started := now.Add(-time.Hour)
	state := monitorData{}
	step := func(at time.Time, condition string, want string, count int) []model.Event {
		t.Helper()
		var events []model.Event
		state, events = evaluateMonitor(state, healthObservation(at, condition), cfg, started)
		if state.Status.State != want || len(events) != count {
			t.Fatalf("%s after %v: state %s events %d", condition, at.Sub(now), state.Status.State, len(events))
		}
		return events
	}
	step(now, "failed", "debouncing", 0)
	step(now.Add(30*time.Second), "unknown", "unknown", 0)
	step(now.Add(time.Minute), "failed", "debouncing", 0)
	first := step(now.Add(2*time.Minute), "failed", "alert", 1)[0]
	if first.Phase != "start" || !state.ConditionSince.Equal(now.Add(time.Minute)) {
		t.Fatal("unknown interval was counted as continuous failure")
	}
	step(now.Add(3*time.Minute), "healthy", "recovering", 0)
	step(now.Add(4*time.Minute), "unknown", "unknown", 0)
	step(now.Add(5*time.Minute), "healthy", "recovering", 0)
	recovered := step(now.Add(6*time.Minute), "healthy", "healthy", 1)[0]
	if recovered.Phase != "recovery" || recovered.IncidentID != first.IncidentID {
		t.Fatal("recovery identity changed")
	}
	step(now.Add(7*time.Minute), "failed", "debouncing", 0)
	start := step(now.Add(8*time.Minute), "failed", "alert", 1)[0]
	reminder := step(now.Add(68*time.Minute), "failed", "alert", 1)[0]
	if reminder.Phase != "update" || reminder.IncidentID != start.IncidentID || start.IncidentID == first.IncidentID {
		t.Fatal("health incident or reminder identity incorrect")
	}
	step(now.Add(69*time.Minute), "disabled", "disabled", 0)
}

func TestMonitorStartupGraceDoesNotRecoverPreviousIncident(t *testing.T) {
	cfg := config.Defaults().Alerts
	cfg.Health.Enabled = true
	now := time.Now().UTC()
	state := monitorData{SchemaVersion: 1, Active: true, IncidentID: "monitor_synthetic", ConditionSince: now.Add(-time.Hour), LastNotification: now.Add(-time.Hour)}
	next, events := evaluateMonitor(state, healthObservation(now, "healthy"), cfg, now)
	if len(events) != 0 || !next.Active || !next.RecoverySince.IsZero() || next.Status.Reason != "startup_grace" {
		t.Fatal("startup temporarily claimed recovery")
	}
}

func TestMonitorLowSpaceDebounceRetainsStartBeforeRecoveryWithoutRecursion(t *testing.T) {
	a := eventTestApp(t)
	a.options.Config.Alerts.Health.Enabled = true
	a.options.Config.Alerts.Health.GracePeriod.Duration = time.Minute
	a.options.Config.Alerts.Health.RecoveryPeriod.Duration = time.Minute
	now := time.Now().UTC()
	a.started = now.Add(-time.Hour)
	m := newMonitorRuntime(a)
	ctx := context.Background()
	eventTestBudget(t, a, true)
	for i := 0; i < 4; i++ {
		at := now.Add(time.Duration(i) * time.Minute)
		m.pass(ctx, at, []monitorObservation{healthObservation(at, "failed")})
	}
	pending := m.pending["health_storage"]
	if pending == nil || len(pending.events) != 1 || pending.events[0].Phase != "start" || !pending.next.ConditionSince.Equal(now) || a.MonitorStatus().Pending != 1 {
		t.Fatal("blocked debounce discarded the sustained failure")
	}
	eventID := pending.events[0].ID
	if a.monitorStorageFailure() || len(a.pendingEvents) != 0 {
		t.Fatal("monitor failure recursed into ingestion or storage-health queue")
	}
	if events := monitorEvents(t, a, now); len(events) != 0 {
		t.Fatal("failed transaction partially persisted event")
	}
	eventTestBudget(t, a, false)
	m.pass(ctx, now.Add(4*time.Minute), []monitorObservation{healthObservation(now.Add(4*time.Minute), "healthy")})
	m.pass(ctx, now.Add(5*time.Minute), []monitorObservation{healthObservation(now.Add(5*time.Minute), "healthy")})
	events := monitorEvents(t, a, now)
	if len(events) != 2 || events[1].ID != eventID || events[1].Phase != "start" || events[0].Phase != "recovery" || events[0].IncidentID != events[1].IncidentID || a.MonitorStatus().Pending != 0 {
		t.Fatalf("lost or reordered pending evidence: %+v", events)
	}
}

type monitorRepositoryHook struct {
	monitorRepository
	before        func()
	unknownCommit bool
}

func (r *monitorRepositoryHook) CommitMonitorState(ctx context.Context, next store.MonitorState, expected int64, events []model.Event, messages []*store.OutboxMessage) error {
	if r.before != nil {
		hook := r.before
		r.before = nil
		hook()
	}
	err := r.monitorRepository.CommitMonitorState(ctx, next, expected, events, messages)
	if err == nil && r.unknownCommit {
		r.unknownCommit = false
		return context.DeadlineExceeded
	}
	return err
}

func TestMonitorCASConflictAndUnknownCommitDoNotDuplicate(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(map[bool]string{false: "conflict", true: "unknown commit"}[unknown], func(t *testing.T) {
			a := eventTestApp(t)
			a.options.Config.Alerts.Budget.Enabled = true
			a.options.Config.Alerts.Budget.MonthlyBytes = 100
			now := time.Now().UTC()
			ctx := context.Background()
			m := newMonitorRuntime(a)
			repository := &monitorRepositoryHook{monitorRepository: a.options.Store, unknownCommit: unknown}
			m.repository = repository
			if !unknown {
				repository.before = func() {
					other := newMonitorRuntime(a)
					other.pass(ctx, now, []monitorObservation{monthObservation(now, 80, 100)})
				}
			}
			m.pass(ctx, now, []monitorObservation{monthObservation(now, 80, 100)})
			m.pass(ctx, now.Add(time.Minute), []monitorObservation{monthObservation(now.Add(time.Minute), 80, 100)})
			if events := monitorEvents(t, a, now); len(events) != 1 || a.MonitorStatus().Pending != 0 {
				t.Fatalf("duplicate transition or retained pending after acknowledgement: %d", len(events))
			}
		})
	}
}

func TestMonitorSilenceRetainsLocalEventAndNoDelivery(t *testing.T) {
	a := eventTestApp(t)
	a.options.Config.Alerts.Budget.Enabled = true
	a.options.Config.Alerts.Budget.MonthlyBytes = 100
	now := time.Now().UTC()
	ctx := context.Background()
	if _, err := a.options.Store.AddSilence(ctx, store.Silence{Kind: "budget_month_bytes", ExpiresAt: now.Add(time.Hour)}, now); err != nil {
		t.Fatal(err)
	}
	m := newMonitorRuntime(a)
	m.pass(ctx, now, []monitorObservation{monthObservation(now, 100, 100)})
	if len(monitorEvents(t, a, now)) != 1 {
		t.Fatal("silence removed local budget evidence")
	}
	pending, err := a.options.Store.Pending(ctx, now.Add(time.Minute), 100)
	if err != nil || len(pending) != 0 {
		t.Fatal("silence bypassed by monitor notification")
	}
}

func TestMonitorCoverageRequiresTrackedUnprunedNonconflictingHistory(t *testing.T) {
	start := time.Now().UTC()
	end := start.Add(24 * time.Hour)
	healthy := func() (store.IntegrityView, store.RetentionView) {
		return store.IntegrityView{Components: []store.IntegrityComponent{{Name: "interface_counter", RunningMS: 86400000}}}, store.RetentionView{TrackingStarted: start.Add(-time.Hour)}
	}
	i, r := healthy()
	if recordedMonitorCoverage(start, end, i, r) != "adequate_recorded" {
		t.Fatal("adequate recorded interval rejected")
	}
	cases := map[string]func(*store.IntegrityView, *store.RetentionView){
		"unknown": func(i *store.IntegrityView, r *store.RetentionView) {
			i.Components[0].RunningMS -= 1000000
			i.Components[0].UnknownMS = 1000000
		},
		"conflict": func(i *store.IntegrityView, r *store.RetentionView) { i.Components[0].ConflictMS = 1000000 },
		"gap": func(i *store.IntegrityView, r *store.RetentionView) {
			i.Gaps = []store.CoverageGap{{Name: "interface_counter"}}
		},
		"history truncated": func(i *store.IntegrityView, r *store.RetentionView) { i.HistoryTruncated = true },
		"gaps truncated":    func(i *store.IntegrityView, r *store.RetentionView) { i.GapsTruncated = true },
		"predates ledger":   func(i *store.IntegrityView, r *store.RetentionView) { r.TrackingStarted = start.Add(time.Second) },
		"retention": func(i *store.IntegrityView, r *store.RetentionView) {
			r.Entries = []store.RetentionEntry{{Dataset: "interface_hourly"}}
		},
		"evicted ledger": func(i *store.IntegrityView, r *store.RetentionView) {
			r.EvictedEntries = 1
			r.Totals = []store.RetentionTotal{{Dataset: "interface_hourly", DataStart: start, DataEnd: end}}
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			i, r := healthy()
			change(&i, &r)
			if recordedMonitorCoverage(start, end, i, r) == "adequate_recorded" {
				t.Fatal("incomplete baseline accepted")
			}
		})
	}
}

// A closed private synthetic database models installation before these days.
// Real Integrity deliberately refuses to credit future running intervals.
func monitorHistoricalStore(t *testing.T, a *App) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "historical.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = raw.Exec(`UPDATE retention_meta SET tracking_started=? WHERE id=1`, time.Now().UTC().AddDate(0, 0, -10).UnixMilli())
	closeErr := raw.Close()
	if err != nil {
		t.Fatal(err)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	db, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	a.options.Store = db
}

func TestMonitorCompletedDayBaselineAndOncePerDay(t *testing.T) {
	a := eventTestApp(t)
	a.options.Config.Alerts.Budget.Enabled = true
	a.options.Config.Alerts.Budget.DailyGrowthRatio = 2
	a.options.Config.Alerts.Budget.BaselineDays = 3
	monitorHistoricalStore(t, a)
	now := time.Now().UTC()
	a.report.Location = time.UTC
	ctx := context.Background()
	_, start, end := report.PreviousDay(now, time.UTC)
	if err := a.options.Store.SetComponentStatus(ctx, "interface_counter", "running", start.AddDate(0, 0, -3)); err != nil {
		t.Fatal(err)
	}
	if err := a.options.Store.HeartbeatCoverage(ctx, end); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		amount := uint64(100)
		if i == 0 {
			amount = 300
		}
		if err := a.options.Store.AddInterface(ctx, model.InterfaceTotals{HourUTC: start.AddDate(0, 0, -i), TXBytes: amount}); err != nil {
			t.Fatal(err)
		}
	}
	m := newMonitorRuntime(a)
	m.pass(ctx, now, nil)
	state := m.states["budget_day_growth"]
	if !state.Evaluated || state.Milestone != 100 || state.Status.BaselineDays != 3 || state.Status.BaselineMeanBytes != 100 || state.Status.ObservedBytes != 300 {
		t.Fatalf("incorrect completed-day baseline: %+v", state.Status)
	}
	if err := a.options.Store.AddInterface(ctx, model.InterfaceTotals{HourUTC: start, TXBytes: 500}); err != nil {
		t.Fatal(err)
	}
	a.options.Config.Alerts.Budget.Enabled = false
	m.pass(ctx, now.Add(30*time.Second), nil)
	if a.MonitorStatus().Rules[3].State != "disabled" {
		t.Fatal("disabled budget stayed enabled")
	}
	a.options.Config.Alerts.Budget.Enabled = true
	a.options.Config.Alerts.Budget.DailyGrowthRatio = 3
	m = newMonitorRuntime(a)
	m.pass(ctx, now.Add(time.Minute), nil)
	if len(monitorEvents(t, a, now)) != 1 || m.states["budget_day_growth"].Status.ObservedBytes != 300 || m.states["budget_day_growth"].Status.GrowthRatio != 2 {
		t.Fatal("same completed day replayed after late data/config edit/restart")
	}
}

func TestMonitorInsufficientAndZeroBaselineAreUnavailable(t *testing.T) {
	for _, zero := range []bool{false, true} {
		t.Run(map[bool]string{false: "new installation", true: "zero baseline"}[zero], func(t *testing.T) {
			a := eventTestApp(t)
			a.options.Config.Alerts.Budget.Enabled = true
			a.options.Config.Alerts.Budget.DailyGrowthRatio = 2
			a.options.Config.Alerts.Budget.BaselineDays = 3
			a.report.Location = time.UTC
			monitorHistoricalStore(t, a)
			now := time.Now().UTC()
			_, start, end := report.PreviousDay(now, time.UTC)
			ctx := context.Background()
			if zero {
				if err := a.options.Store.SetComponentStatus(ctx, "interface_counter", "running", start.AddDate(0, 0, -3)); err != nil {
					t.Fatal(err)
				}
				if err := a.options.Store.HeartbeatCoverage(ctx, end); err != nil {
					t.Fatal(err)
				}
			}
			if err := a.options.Store.AddInterface(ctx, model.InterfaceTotals{HourUTC: start, TXBytes: 300}); err != nil {
				t.Fatal(err)
			}
			m := newMonitorRuntime(a)
			m.pass(ctx, now, nil)
			s := m.states["budget_day_growth"].Status
			reason := "insufficient_coverage"
			if zero {
				reason = "zero_baseline"
			}
			if s.Available || s.Reason != reason || len(monitorEvents(t, a, now)) != 0 {
				t.Fatalf("unavailable baseline became a verdict: %+v", s)
			}
		})
	}
}

func TestMonitorGeoUnknownScheduledMissingAndUnchanged(t *testing.T) {
	a := eventTestApp(t)
	a.options.Config.Alerts.Health.Enabled = true
	now := time.Now().UTC()
	for _, previous := range []bool{false, true} {
		value := a.observeGeoHealth(now, assets.Health{}, errors.New("synthetic SECRET path"), previous)
		condition := "unknown"
		if previous {
			condition = "failed"
		}
		if value.Condition != condition || strings.Contains(value.Status.Reason, "SECRET") {
			t.Fatal("unsafe or false healthy missing metadata")
		}
	}
	health := assets.Health{SchemaVersion: 1, CheckedAt: now.Add(-time.Hour), LastSuccessAt: now.Add(-time.Hour), Result: "unchanged", Scheduled: true}
	if a.observeGeoHealth(now, health, nil, true).Condition != "healthy" {
		t.Fatal("unchanged successful check incorrectly stale")
	}
	health.LastSuccessAt = now.Add(-90 * time.Hour)
	health.CheckedAt = health.LastSuccessAt
	if a.observeGeoHealth(now, health, nil, true).Status.Reason != "update_stale" {
		t.Fatal("scheduled stale update ignored")
	}
	health.Scheduled = false
	if a.observeGeoHealth(now, health, nil, true).Condition != "healthy" {
		t.Fatal("manual unscheduled database age treated as updater failure")
	}
	health.Result = "download_failed"
	health.ConsecutiveFailures = 1
	if a.observeGeoHealth(now, health, nil, true).Condition != "unknown" {
		t.Fatal("failed check manufactured recovery")
	}
	health.ConsecutiveFailures = 2
	if a.observeGeoHealth(now, health, nil, true).Condition != "failed" {
		t.Fatal("failure streak ignored")
	}
}

func TestMonitorInvalidPersistedStateAndConcurrentStatus(t *testing.T) {
	a := eventTestApp(t)
	a.options.Config.Alerts.Budget.Enabled = true
	a.options.Config.Alerts.Budget.MonthlyBytes = 100
	now := time.Now().UTC()
	ctx := context.Background()
	invalid := json.RawMessage(`{"schema_version":1,"status":{"key":"budget_month_bytes","state":"observed","reason":"observed_estimate","period":"SYNTHETIC_SECRET"}}`)
	if err := a.options.Store.CommitMonitorState(ctx, store.MonitorState{Key: "budget_month_bytes", UpdatedAt: now, Data: invalid}, 0, nil, nil); err != nil {
		t.Fatal(err)
	}
	m := newMonitorRuntime(a)
	var readers sync.WaitGroup
	for range 4 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for range 30 {
				s := a.MonitorStatus()
				if len(s.Rules) > 0 {
					s.Rules[0].Reason = "caller mutation"
				}
			}
		}()
	}
	m.pass(ctx, now, []monitorObservation{monthObservation(now, 100, 100)})
	readers.Wait()
	data, _ := json.Marshal(a.MonitorStatus())
	if strings.Contains(string(data), "SYNTHETIC_SECRET") || a.MonitorStatus().Rules[0].Reason != "state_invalid" || len(monitorEvents(t, a, now)) != 0 {
		t.Fatal("invalid durable state reset or leaked")
	}
}

func TestMonitorCostUsesGuestTXTariffAndPreservesMilestonesAcrossTariffEdits(t *testing.T) {
	a := eventTestApp(t)
	cfg := &a.options.Config
	cfg.Alerts.Budget.Enabled = true
	cfg.Alerts.Budget.MonthlyCost = 10
	cfg.Billing.Enabled = true
	a.report.Location = time.UTC
	a.options.Billing = &billing.Profile{SchemaVersion: 1, Currency: "USD", UnitBytes: 1_000_000_000, FreeGB: 100, InternetEgress: []billing.Tier{{PricePerGB: 0.1}}}
	now := time.Now().UTC()
	ctx := context.Background()
	if err := a.options.Store.AddInterface(ctx, model.InterfaceTotals{HourUTC: now.Truncate(time.Hour), TXBytes: 180_000_000_000}); err != nil {
		t.Fatal(err)
	}
	m := newMonitorRuntime(a)
	m.pass(ctx, now, nil)
	state := m.states["budget_month_cost"]
	if state.Milestone != 80 || state.Status.ObservedCost != 8 || state.Status.Currency != "USD" || state.Status.Coverage == "adequate_recorded" {
		t.Fatalf("wrong tariff or coverage: %+v", state.Status)
	}
	a.options.Billing.InternetEgress[0].PricePerGB = 0.2
	m = newMonitorRuntime(a)
	m.pass(ctx, now.Add(time.Minute), nil)
	a.options.Billing.InternetEgress[0].PricePerGB = 0.4
	m = newMonitorRuntime(a)
	m.pass(ctx, now.Add(2*time.Minute), nil)
	if len(monitorEvents(t, a, now)) != 2 || m.states["budget_month_cost"].Milestone != 100 {
		t.Fatal("tariff change replayed milestone")
	}
	a.options.Billing = nil
	m.pass(ctx, now.Add(3*time.Minute), nil)
	status := m.states["budget_month_cost"].Status
	if status.Available || status.Reason != "billing_unavailable" || status.Milestone != 100 {
		t.Fatal("missing tariff erased durable crossed milestone")
	}
}

func TestMonitorAbsoluteDailyAlertAllowsQualifiedIncompleteDSTDay(t *testing.T) {
	a := eventTestApp(t)
	a.options.Config.Alerts.Budget.Enabled = true
	a.options.Config.Alerts.Budget.DailyBytes = 100
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	a.report.Location = loc
	now := time.Date(2026, 3, 9, 12, 0, 0, 0, loc).UTC()
	_, start, end := report.PreviousDay(now, loc)
	if end.Sub(start) != 23*time.Hour {
		t.Fatal("test fixture is not the spring transition")
	}
	if err := a.options.Store.AddInterface(context.Background(), model.InterfaceTotals{HourUTC: start.Truncate(time.Hour), TXBytes: 120}); err != nil {
		t.Fatal(err)
	}
	m := newMonitorRuntime(a)
	m.pass(context.Background(), now, nil)
	s := m.states["budget_day_bytes"].Status
	if s.Milestone != 100 || s.Period != "2026-03-08" || s.PeriodEnd.Sub(s.PeriodStart) != 23*time.Hour || s.Coverage != "incomplete" || len(monitorEvents(t, a, now)) != 1 {
		t.Fatalf("day precision or qualified threshold incorrect: %+v", s)
	}
}

func TestMonitorHealthSourcesRespectDisabledCollectorsAndQuietSSH(t *testing.T) {
	a := eventTestApp(t)
	a.options.Config.Alerts.Health.Enabled = true
	now := time.Now().UTC()
	a.started = now.Add(-time.Hour)
	a.interfaceCounterState = "running"
	a.interfaceCounterAt = now
	a.lastSensor = now
	a.journalStatus = collector.JournalStatus{State: "running", At: now.Add(-48 * time.Hour)}
	values := a.observeMonitor(context.Background(), now, nil)
	if values[4].Condition != "healthy" || values[5].Condition != "healthy" || values[6].Condition != "healthy" {
		t.Fatal("fresh feed or quiet healthy SSH was declared failed")
	}
	a.options.Config.Sensor.Enabled = false
	a.options.Config.Auth.Enabled = false
	a.lastSensor = time.Time{}
	a.journalStatus.State = "degraded"
	values = a.observeMonitor(context.Background(), now, nil)
	if values[4].Condition != "disabled" || values[6].Condition != "disabled" {
		t.Fatal("disabled collectors treated as faults")
	}
	a.recordWrite(errors.New("synthetic failure"), "component_status", false)
	if a.monitorStorageFailure() {
		t.Fatal("status read failure treated as durable ingest failure")
	}
	a.recordWrite(errors.New("synthetic failure"), "traffic", true)
	values = a.observeMonitor(context.Background(), now, nil)
	if values[7].Condition != "failed" || values[7].Status.Reason != "storage_write_failed" {
		t.Fatal("durable write failure ignored")
	}
	a.recordWrite(nil, "traffic", true)
	if err := a.options.Store.Close(); err != nil {
		t.Fatal(err)
	}
	values = a.observeMonitor(context.Background(), now, nil)
	if values[7].Condition != "unknown" || values[7].Status.Available {
		t.Fatal("database read failure declared recovery")
	}
}

type blockingMonitorRepository struct{ monitorRepository }

func (r blockingMonitorRepository) CommitMonitorState(ctx context.Context, _ store.MonitorState, _ int64, _ []model.Event, _ []*store.OutboxMessage) error {
	<-ctx.Done()
	return ctx.Err()
}

func TestMonitorPassCancellationKeepsBoundedPending(t *testing.T) {
	a := eventTestApp(t)
	a.options.Config.Alerts.Budget.Enabled = true
	a.options.Config.Alerts.Budget.MonthlyBytes = 100
	m := newMonitorRuntime(a)
	m.repository = blockingMonitorRepository{a.options.Store}
	now := time.Now().UTC()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	m.pass(ctx, now, []monitorObservation{monthObservation(now, 100, 100)})
	if time.Since(now) > time.Second || a.MonitorStatus().Pending != 1 || len(m.pending) != 1 || len(m.pending["budget_month_bytes"].events) != 1 {
		t.Fatal("cancellation lost pending transition or exceeded bound")
	}
}

func TestMonitorStorageBusyAndUnconfiguredDoNotRecover(t *testing.T) {
	cfg := config.Defaults().Alerts
	cfg.Health.Enabled = true
	now := time.Now().UTC()
	previous := monitorData{SchemaVersion: 1, Active: true, IncidentID: "monitor_storage_fixture", ConditionSince: now.Add(-time.Hour), LastNotification: now.Add(-time.Hour), ObservedAt: now.Add(-time.Minute)}
	for _, budget := range []store.StorageBudgetStatus{{State: "busy"}, {State: "unconfigured"}, {Configured: true, State: "unexpected"}} {
		observation := observeStorageHealth(now, budget, nil, false, time.Time{})
		next, events := evaluateMonitor(previous, observation, cfg, now.Add(-time.Hour))
		if observation.Condition != "unknown" || observation.Status.Available || !next.Active || !next.RecoverySince.IsZero() || len(events) != 0 {
			t.Fatalf("%s manufactured storage recovery", budget.State)
		}
	}
	observation := observeStorageHealth(now, store.StorageBudgetStatus{Configured: true, State: "running"}, nil, false, time.Time{})
	if observation.Condition != "healthy" {
		t.Fatal("successful configured inspection rejected")
	}
}

func TestMonitorPendingViewKeepsDurableMilestoneAndNotificationTime(t *testing.T) {
	a := eventTestApp(t)
	a.options.Config.Alerts.Budget.Enabled = true
	a.options.Config.Alerts.Budget.MonthlyBytes = 100
	now := time.Now().UTC()
	ctx := context.Background()
	m := newMonitorRuntime(a)
	m.pass(ctx, now, []monitorObservation{monthObservation(now, 80, 100)})
	durable := m.states["budget_month_bytes"].LastNotification
	eventTestBudget(t, a, true)
	m.pass(ctx, now.Add(time.Minute), []monitorObservation{monthObservation(now.Add(time.Minute), 100, 100)})
	s := a.MonitorStatus().Rules[0]
	if !s.Pending || s.Available || s.Milestone != 80 || !s.LastNotification.Equal(durable) || m.pending[s.Key].next.Milestone != 100 {
		t.Fatalf("uncommitted transition was displayed as durable: %+v", s)
	}
	eventTestBudget(t, a, false)
	m.pass(ctx, now.Add(2*time.Minute), []monitorObservation{monthObservation(now.Add(2*time.Minute), 100, 100)})
	s = a.MonitorStatus().Rules[0]
	if s.Pending || s.Milestone != 100 || !s.LastNotification.Equal(now.Add(time.Minute)) {
		t.Fatal("committed pending transition missing durable metadata")
	}
}

func TestMonitorOptionalGeoMetadataMissingIsDisabledOnlyWhenUnconfigured(t *testing.T) {
	a := eventTestApp(t)
	a.options.Config.Alerts.Health.Enabled = true
	now := time.Now().UTC()
	if a.observeGeoHealth(now, assets.Health{}, os.ErrNotExist, false).Condition != "disabled" {
		t.Fatal("unused optional Geo made monitor unavailable")
	}
	a.options.Config.Geo.CityMMDB = "/synthetic/geo.mmdb"
	if a.observeGeoHealth(now, assets.Health{}, os.ErrNotExist, false).Condition != "unknown" {
		t.Fatal("configured missing Geo metadata claimed disabled")
	}
	a.options.Config.Geo.CityMMDB = ""
	if a.observeGeoHealth(now, assets.Health{}, os.ErrNotExist, true).Condition != "failed" {
		t.Fatal("previously scheduled missing metadata was ignored")
	}
}

func TestMonitorUnexpectedWorkerStopInvalidatesLiveSSHAndCounterState(t *testing.T) {
	a := eventTestApp(t)
	a.options.Config.Alerts.Health.Enabled = true
	now := time.Now().UTC()
	a.journalStatus = collector.JournalStatus{State: "running", At: now.Add(-time.Hour)}
	a.interfaceCounterState = "running"
	a.interfaceCounterAt = now
	a.monitorWorkerStopped("ssh_journal", now)
	a.monitorWorkerStopped("interface_counter", now)
	observations := a.observeMonitor(context.Background(), now, nil)
	if observations[5].Condition != "failed" || observations[6].Condition != "failed" || !observations[6].Since.Equal(now) {
		t.Fatal("stopped worker retained its previous healthy live state")
	}
}

func TestMonitorRetentionCoversUTCOverlapAndExactMonthStart(t *testing.T) {
	start := time.Date(2026, 9, 10, 18, 15, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	from, through := monitorRetentionRange(start, end)
	removedBoundaryHour := start.Truncate(time.Hour)
	if removedBoundaryHour.Before(from) || !removedBoundaryHour.Before(through) || !from.Equal(removedBoundaryHour) || through.Sub(from) != 25*time.Hour {
		t.Fatal("retention query missed a UTC hour used by the estimate")
	}
	a := eventTestApp(t)
	a.options.Config.Alerts.Budget.Enabled = true
	a.options.Config.Alerts.Budget.MonthlyBytes = 100
	a.report.Location = time.UTC
	now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	observations := a.observeMonitor(context.Background(), now, nil)
	if observations[0].Status.Period != "2026-10" || !observations[0].Status.Available || observations[0].Status.ObservedBytes != 0 || observations[0].Status.Coverage != "incomplete" {
		t.Fatal("exact new month failed to identify the empty estimate period")
	}
}

func TestMonitorRetiredDetailsUseRelevantLifetimeBounds(t *testing.T) {
	start := time.Date(2026, 9, 10, 18, 15, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	integrity := store.IntegrityView{Components: []store.IntegrityComponent{{Name: "interface_counter", RunningMS: 86400000}}}
	cases := []struct {
		name     string
		totals   []store.RetentionTotal
		adequate bool
	}{
		{"no relevant lifetime removal", nil, true},
		{"unrelated dataset", []store.RetentionTotal{{Dataset: "events", DataStart: start, DataEnd: end}}, true},
		{"older hourly removal", []store.RetentionTotal{{Dataset: "interface_hourly", DataStart: start.Add(-48 * time.Hour), DataEnd: start.Truncate(time.Hour)}}, true},
		{"overlapping retired hourly detail", []store.RetentionTotal{{Dataset: "interface_hourly", DataStart: start.Truncate(time.Hour), DataEnd: start.Truncate(time.Hour).Add(time.Hour)}}, false},
		{"unknown bounds", []store.RetentionTotal{{Dataset: "interface_hourly"}}, false},
		{"reversed bounds", []store.RetentionTotal{{Dataset: "interface_hourly", DataStart: end, DataEnd: start}}, false},
		{"broad bounding span", []store.RetentionTotal{{Dataset: "interface_hourly", DataStart: start.Add(-48 * time.Hour), DataEnd: end.Add(48 * time.Hour)}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			retention := store.RetentionView{TrackingStarted: start.Add(-72 * time.Hour), EvictedEntries: 1, Totals: tc.totals}
			if got := recordedMonitorCoverage(start, end, integrity, retention) == "adequate_recorded"; got != tc.adequate {
				t.Fatal("retired detail was interpreted without its authoritative lifetime bounds")
			}
		})
	}
}
