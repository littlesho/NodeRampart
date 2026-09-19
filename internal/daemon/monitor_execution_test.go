// SPDX-License-Identifier: MIT

package daemon

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/assets"
	"github.com/littlesho/NodeRampart/internal/store"
)

func TestMonitorPersistsHealthBeforeBlockedBudgetAndPublishesProgress(t *testing.T) {
	a := eventTestApp(t)
	now := time.Now().UTC()
	a.started = now.Add(-time.Hour)
	a.options.Config.Sensor.Enabled = false
	a.options.Config.Auth.Enabled = false
	a.options.Config.Alerts.Health.Enabled = true
	a.options.Config.Alerts.Budget.Enabled = true
	a.options.Config.Alerts.Budget.MonthlyBytes = 100
	eventTestBudget(t, a, false)
	m := newMonitorRuntime(a)
	first := healthObservation(now.Add(-3*time.Minute), "failed")
	first.Status.Key, first.Status.Reason = "health_interface_counter", "interface_stale"
	m.pass(context.Background(), first.Now, []monitorObservation{first})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	entered := make(chan struct{})
	m.budgetObserver = func(ctx context.Context, _ time.Time, values []monitorObservation, _ map[string]monitorData) {
		close(entered)
		<-ctx.Done()
		for i := range values {
			if values[i].Status.Enabled {
				values[i].Status.Available = false
				values[i].Status.Reason = "read_failed"
			}
		}
	}
	done := make(chan struct{})
	go func() { defer close(done); m.pass(ctx, now, nil) }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("budget phase was not reached")
	}
	// This is a real store read while the single monitor worker is still blocked
	// in historical work. Urgent health persistence must already have completed.
	events, err := a.options.Store.Events(ctx, store.EventQuery{Start: now.Add(-time.Minute), End: now.Add(time.Minute), Kind: "health_interface_counter", Limit: 10})
	if err != nil || len(events) != 1 || events[0].Phase != "start" {
		t.Fatalf("health not persisted before budget: %d / %v", len(events), err)
	}
	status := a.MonitorStatus()
	if len(status.Checks) != 2 || status.Checks[0].InProgress || status.Checks[0].LastCompletedAt.IsZero() || !status.Checks[1].InProgress || status.Checks[1].State != "running" {
		t.Fatalf("live check stages missing: %+v", status.Checks)
	}
	if status.Checks[0].LastSuccessfulAt.IsZero() {
		t.Fatal("known failed component was confused with failed check execution")
	}
	// Readers receive independent slices while execution is changing underneath.
	var readers sync.WaitGroup
	for i := 0; i < 4; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for j := 0; j < 50; j++ {
				s := a.MonitorStatus()
				s.Checks[0].State = "caller mutation"
				s.Rules[0].Reason = "caller mutation"
			}
		}()
	}
	readers.Wait()
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("monitor did not honor canceled budget")
	}
	status = a.MonitorStatus()
	if status.Checks[0].State == "caller mutation" || status.Checks[1].State != "timeout" || !status.Checks[1].LastSuccessfulAt.IsZero() {
		t.Fatalf("bad completion status: %+v", status.Checks)
	}
}

func TestMonitorPendingAgeAndLastDurableObservation(t *testing.T) {
	a := eventTestApp(t)
	a.options.Config.Alerts.Budget.Enabled = true
	a.options.Config.Alerts.Budget.MonthlyBytes = 100
	now := time.Now().UTC()
	first := now.Add(-3 * time.Minute)
	m := newMonitorRuntime(a)
	eventTestBudget(t, a, true)
	m.pass(context.Background(), first, []monitorObservation{monthObservation(first, 80, 100)})
	status := a.MonitorStatus()
	r := status.Rules[0]
	if !r.Pending || !r.PendingSince.Equal(first) || r.PendingAgeMS < 2*time.Minute.Milliseconds() || !r.LastPersistedObservation.IsZero() || r.Milestone != 0 {
		t.Fatalf("pending observation was claimed durable: %+v", r)
	}
	if !status.Checks[1].LastSuccessfulAt.IsZero() {
		t.Fatal("failed persistence claimed successful check")
	}
	eventTestBudget(t, a, false)
	m.pass(context.Background(), now, []monitorObservation{monthObservation(now, 80, 100)})
	r = a.MonitorStatus().Rules[0]
	if r.Pending || !r.PendingSince.IsZero() || r.PendingAgeMS != 0 || r.LastPersistedObservation.IsZero() || r.Milestone != 80 {
		t.Fatalf("pending recovery missing: %+v", r)
	}
	restarted, err := New(a.options)
	if err != nil {
		t.Fatal(err)
	}
	fresh := restarted.MonitorStatus()
	for _, check := range fresh.Checks {
		if !check.LastSuccessfulAt.IsZero() || !check.LastAttemptAt.IsZero() {
			t.Fatal("new process fabricated previous execution success")
		}
	}
}

func TestMonitorGeoScheduleFailureAndMixedRecovery(t *testing.T) {
	a := eventTestApp(t)
	a.options.Config.Alerts.Health.Enabled = true
	now := time.Now().UTC()
	health := assets.Health{SchemaVersion: 1, CheckedAt: now.Add(-time.Hour), LastSuccessAt: now.Add(-time.Hour), Result: "unchanged", Scheduled: true, Schedule: &assets.ScheduleHealth{CheckedAt: now.Add(-time.Minute), Result: "failed", ConsecutiveFailures: 2}}
	got := a.observeGeoHealth(now, health, nil, true)
	if got.Condition != "failed" || got.Status.Reason != "schedule_failed" {
		t.Fatal("schedule failure not observed")
	}
	health.Schedule.Result, health.Schedule.ConsecutiveFailures = "ok", 0
	if a.observeGeoHealth(now, health, nil, true).Condition != "healthy" {
		t.Fatal("repaired pure schedule failure did not recover")
	}
	health.Result, health.ConsecutiveFailures = "download_failed", 2
	if a.observeGeoHealth(now, health, nil, true).Condition != "failed" {
		t.Fatal("schedule repair cleared download failure")
	}
	health.Schedule.CheckedAt = now.Add(time.Minute)
	if a.observeGeoHealth(now, health, nil, true).Condition != "unknown" {
		t.Fatal("future schedule check claimed a known state")
	}
}

func TestMonitorLiveAgesClampRollbackAndExposeDelay(t *testing.T) {
	now := time.Now().UTC()
	s := MonitorStatus{Rules: []MonitorRuleStatus{{Pending: true, PendingSince: now.Add(-2 * time.Minute)}}, Checks: []MonitorCheckStatus{{State: "running", LastAttemptAt: now.Add(-3 * time.Minute), InProgress: true}}}
	updateMonitorAges(&s, now)
	if s.Rules[0].PendingAgeMS != 120000 || s.Checks[0].DelayMS != 120000 || s.Checks[0].DurationMS != 180000 {
		t.Fatalf("bad live ages: %+v", s)
	}
	updateMonitorAges(&s, now.Add(-time.Hour))
	if s.Rules[0].PendingAgeMS != 0 || s.Checks[0].DelayMS != 0 || s.Checks[0].DurationMS != 0 {
		t.Fatal("clock rollback produced negative ages")
	}
}
