// SPDX-License-Identifier: MIT

package daemon

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestMonthlyMonitorUsesCustomBillingCycleAndRecordsSwitch(t *testing.T) {
	a := rolloverApp(t, true, true)
	ctx := context.Background()
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	rolloverAddTX(t, a, now.Add(-time.Hour), 90*rolloverGB)
	m := newMonitorRuntime(a)
	m.pass(ctx, now, nil)
	_, before := rolloverState(t, a, "budget_month_bytes")
	if before.Period != "2026-09" || before.Milestone != 80 {
		t.Fatal("fixture not evaluated")
	}
	a.options.Config.Billing.CycleStartDay = 15
	m = newMonitorRuntime(a)
	m.pass(ctx, now.Add(time.Minute), nil)
	_, after := rolloverState(t, a, "budget_month_bytes")
	if after.Period != "2026-08" || after.PeriodStart.Day() != 15 || after.PeriodEnd.Day() != 15 || after.MonthPolicy == nil || after.MonthPolicy.CycleStartDay != 15 || after.PreviousPeriod.Reason != "billing_cycle_changed" || after.PreviousPeriod.Evaluated || !after.PreviousPeriod.Start.Equal(before.PeriodStart) || !after.PreviousPeriod.End.Equal(before.PeriodEnd) {
		t.Fatalf("cycle change lost original bounds or reused old month: %+v", after)
	}
	_, cost := rolloverState(t, a, "budget_month_cost")
	if !cost.PeriodStart.Equal(after.PeriodStart) || !cost.PeriodEnd.Equal(after.PeriodEnd) || cost.Status.ObservedCost != 90 {
		t.Fatal("cost/free allowance uses different cycle")
	}
	// A restart uses the durable custom-cycle policy, not legacy day-one state.
	m = newMonitorRuntime(a)
	m.pass(ctx, now.Add(2*time.Minute), nil)
	_, restarted := rolloverState(t, a, "budget_month_bytes")
	if restarted.Milestone != after.Milestone || restarted.IncidentID != after.IncidentID || restarted.MonthPolicy.CycleStartDay != 15 {
		t.Fatal("restart duplicated milestone or lost cycle")
	}
}

func TestMonthlyCycleSwitchPreservesUnknownLegacyCoverage(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	in := monthObservation(now, 90*rolloverGB, 100*rolloverGB)
	cfg := eventTestApp(t).options.Config.Alerts
	previous, _ := evaluateMonitor(monitorData{}, in, cfg, now.Add(-time.Hour))
	previous.MonthPolicy = nil
	previous.Status.Coverage = ""
	in.Now = now.Add(time.Minute)
	in.Status.PeriodStart = time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
	in.Status.PeriodEnd = time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	in.Status.Period = "2026-08"
	in.MonthPolicy = &monitorMonthPolicy{ThresholdBytes: 100 * rolloverGB, CycleStartDay: 15}
	next, _ := evaluateMonitor(previous, in, cfg, now.Add(-time.Hour))
	data, err := json.Marshal(next)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeMonitorData(data, "budget_month_bytes"); err != nil || next.PreviousPeriod.Coverage != "unknown" {
		t.Fatal("legacy unknown coverage made durable cycle invalid", err)
	}
}
