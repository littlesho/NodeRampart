// SPDX-License-Identifier: MIT

package daemon

import (
	"context"
	"errors"
	"math"
	"os"
	"time"

	"github.com/littlesho/NodeRampart/internal/assets"
	"github.com/littlesho/NodeRampart/internal/model"
	"github.com/littlesho/NodeRampart/internal/report"
	"github.com/littlesho/NodeRampart/internal/store"
)

func (a *App) observeMonitor(ctx context.Context, now time.Time, states map[string]monitorData) []monitorObservation {
	cfg := a.options.Config.Alerts
	observations := make([]monitorObservation, 0, len(monitorKeys))
	for _, key := range monitorKeys {
		enabled := monitorEnabled(key, cfg)
		observations = append(observations, monitorObservation{Now: now, Status: MonitorRuleStatus{Key: key, Enabled: enabled, Available: !enabled, State: "disabled", Reason: "disabled"}, Condition: "disabled"})
	}
	if cfg.Health.Enabled {
		a.observeHealth(ctx, now, observations[4:], states)
	}
	if cfg.Budget.Enabled {
		a.observeBudgets(ctx, now, observations[:4], states)
	}
	return observations
}

func (a *App) observeBudgets(ctx context.Context, now time.Time, values []monitorObservation, states map[string]monitorData) {
	cfg := a.options.Config.Alerts.Budget
	for i := range values {
		if values[i].Status.Enabled {
			values[i].Status.State = "unknown"
			values[i].Status.Reason = "read_failed"
			values[i].Status.Coverage = "unknown"
			values[i].Status.Basis = monitorBasis
		}
	}
	values[0].Status.ThresholdBytes = cfg.MonthlyBytes
	values[1].Status.ThresholdCost = cfg.MonthlyCost
	values[2].Status.ThresholdBytes = cfg.DailyBytes
	values[3].Status.GrowthRatio = cfg.DailyGrowthRatio
	loc := a.report.Location
	if cfg.MonthlyBytes > 0 || cfg.MonthlyCost > 0 {
		start, ok := report.MonthStart(now, loc)
		// Resolve the next month's first real instant using a civil midday safely
		// inside it; Add(24h) would be wrong across calendar offset transitions.
		local := now.In(loc)
		end, okEnd := report.MonthStart(time.Date(local.Year(), local.Month()+1, 15, 12, 0, 0, 0, loc), loc)
		if !ok || !okEnd || start.After(now) {
			values[0].Status.Reason = "calendar_unavailable"
			values[1].Status.Reason = "calendar_unavailable"
		} else {
			var amount model.InterfaceTotals
			var err error
			coverage := "incomplete"
			if start.Before(now) {
				amount, err = a.options.Store.InterfaceSummary(ctx, start, now)
				coverage = a.monitorCoverage(ctx, start, now)
			}
			for i := 0; i < 2; i++ {
				s := &values[i].Status
				s.Period = local.Format("2006-01")
				s.PeriodStart = start
				s.PeriodEnd = end
				s.ObservedBytes = amount.TXBytes
				s.Coverage = coverage
				if err == nil && s.Enabled {
					s.Available = true
					s.Reason = "observed_estimate"
				}
			}
			if cfg.MonthlyCost > 0 {
				values[1].Status.Available = false
				values[1].Status.Reason = "billing_unavailable"
				if err == nil && a.options.Billing != nil {
					estimate := a.options.Billing.Estimate(amount.TXBytes)
					if !estimate.Unavailable {
						values[1].Status.Available = true
						values[1].Status.Reason = "observed_estimate"
						values[1].Status.ObservedCost = estimate.Cost
						values[1].Status.Currency = estimate.Currency
					}
				}
			}
			for i := 0; i < 2; i++ {
				values[i].MonthPolicy = a.currentMonthPolicy(values[i].Status)
			}
		}
	}
	if cfg.DailyBytes == 0 && cfg.DailyGrowthRatio == 0 {
		return
	}
	period, start, end := report.PreviousDay(now, loc)
	for i := 2; i < 4; i++ {
		values[i].Status.Period = period
		values[i].Status.PeriodStart = start
		values[i].Status.PeriodEnd = end
	}
	if period == "" || !start.Before(end) {
		values[2].Status.Reason = "calendar_unavailable"
		values[3].Status.Reason = "calendar_unavailable"
		return
	}
	needDay := false
	for i := 2; i < 4; i++ {
		s := &values[i].Status
		if previous := states[s.Key]; s.Enabled && previous.Period == period && previous.Evaluated {
			*s = previous.Status // Keep the exact completed-day evaluation across edits/restart.
		} else if s.Enabled {
			needDay = true
		}
	}
	if !needDay {
		return
	}
	amount, err := a.options.Store.InterfaceSummary(ctx, start, end)
	coverage := a.monitorCoverage(ctx, start, end)
	for i := 2; i < 4; i++ {
		s := &values[i].Status
		if previous := states[s.Key]; previous.Period == period && previous.Evaluated {
			continue
		}
		s.ObservedBytes = amount.TXBytes
		s.Coverage = coverage
		if err == nil && s.Enabled {
			s.Available = true
			s.Reason = "observed_estimate"
		}
	}
	s := &values[3].Status
	if !s.Enabled || states[s.Key].Period == period && states[s.Key].Evaluated {
		return
	}
	s.Available = false
	if err != nil {
		return
	}
	if coverage != "adequate_recorded" {
		s.Reason = "insufficient_coverage"
		return
	}
	s.Reason = "baseline_unavailable"
	cursor := start
	total := float64(0)
	for i := 0; i < cfg.BaselineDays; i++ {
		_, from, through := report.PreviousDay(cursor, loc)
		if !from.Before(through) || ctx.Err() != nil {
			return
		}
		cursor = from
		sample, readErr := a.options.Store.InterfaceSummary(ctx, from, through)
		if readErr != nil {
			return
		}
		if a.monitorCoverage(ctx, from, through) != "adequate_recorded" {
			continue
		}
		total += float64(sample.TXBytes)
		s.BaselineDays++
	}
	if s.BaselineDays < 3 {
		return
	}
	s.BaselineMeanBytes = total / float64(s.BaselineDays)
	if s.BaselineMeanBytes <= 0 || math.IsNaN(s.BaselineMeanBytes) || math.IsInf(s.BaselineMeanBytes, 0) {
		s.Reason = "zero_baseline"
		return
	}
	s.Available = true
	s.Reason = "observed_estimate"
}

func (a *App) monitorCoverage(ctx context.Context, start, end time.Time) string {
	if a.monitorStorageFailure() {
		return "incomplete"
	}
	integrity, retention, err := a.options.Store.MonitorCoverage(ctx, start, end)
	if err != nil {
		return "unknown"
	}
	return recordedMonitorCoverage(start, end, integrity, retention)
}

func recordedMonitorCoverage(start, end time.Time, integrity store.IntegrityView, retention store.RetentionView) string {
	if integrity.HistoryTruncated || integrity.GapsTruncated || retention.TrackingStarted.IsZero() || retention.TrackingStarted.After(start) {
		return "incomplete"
	}
	if len(retention.Entries) > 0 {
		return "incomplete"
	}
	if retention.EvictedEntries > 0 {
		// Details can be retired while their bounded lifetime totals survive.
		// An unrelated retirement does not invalidate all future baselines.
		// Intersecting bounding spans stay conservative, without asserting
		// that every instant within the span actually lost data.
		from, through := monitorRetentionRange(start, end)
		for _, total := range retention.Totals {
			if total.Dataset != "interface_hourly" {
				continue
			}
			if total.DataStart.IsZero() || !total.DataStart.Before(total.DataEnd) || total.DataStart.Year() < 1970 || total.DataEnd.Year() > 9999 || total.DataStart.Before(through) && total.DataEnd.After(from) {
				return "incomplete"
			}
		}
	}
	for _, gap := range integrity.Gaps {
		if gap.Name == "interface_counter" {
			return "incomplete"
		}
	}
	duration := end.Sub(start).Milliseconds()
	if duration <= 0 {
		return "unknown"
	}
	for _, component := range integrity.Components {
		if component.Name == "interface_counter" {
			// Float comparisons avoid overflowing near the accepted range boundary.
			if float64(component.RunningMS) >= float64(duration)*0.99 && float64(component.DegradedMS)+float64(component.UnknownMS)+float64(component.ConflictMS) <= float64(duration)*0.01 {
				return "adequate_recorded"
			}
			return "incomplete"
		}
	}
	return "unknown"
}

func (a *App) monitorStorageFailure() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	// Status API read failures and this worker's own persistence failures cannot
	// recursively produce storage-write alerts.
	for _, operation := range []string{"interface", "sensor_health", "traffic", "event", "journal", "coverage", "coverage_gap", "interface_coverage_gap", "budget_maintenance", "event_queue_capacity", "event_metadata_rejected", "process_restart_pending_unknown"} {
		if a.storageFailures[operation] {
			return true
		}
	}
	return false
}

func (a *App) observeHealth(ctx context.Context, now time.Time, values []monitorObservation, states map[string]monitorData) {
	for i := range values {
		values[i].Condition = "healthy"
		values[i].Status.State = "healthy"
		values[i].Status.Reason = "healthy"
		values[i].Status.Available = true
	}
	a.mu.RLock()
	counterState, counterAt := a.interfaceCounterState, a.interfaceCounterAt
	journal := a.journalStatus
	lastFailure := a.storageHealth.LastFailure
	a.mu.RUnlock()
	if !a.options.Config.Sensor.Enabled {
		values[0].Condition = "disabled"
	} else if a.sensorCoverageState(now) != "running" {
		values[0].Condition = "failed"
		values[0].Status.Reason = "sensor_stale"
	}
	if counterState != "running" || counterAt.IsZero() || now.Sub(counterAt) > 10*time.Second || counterAt.After(now) {
		values[1].Condition = "failed"
		values[1].Status.Reason = "interface_stale"
	}
	if !a.options.Config.Auth.Enabled {
		values[2].Condition = "disabled"
	} else if journal.State != "running" {
		values[2].Condition = "failed"
		values[2].Status.Reason = "journal_unavailable"
		values[2].Since = journal.Since
	}
	budget, err := a.options.Store.BudgetStatus(ctx)
	values[3] = observeStorageHealth(now, budget, err, a.monitorStorageFailure(), lastFailure)
	health, healthErr := assets.Health{}, error(os.ErrNotExist)
	if a.options.AssetHealthPath != "" {
		health, healthErr = assets.ReadHealth(a.options.AssetHealthPath)
	}

	values[4] = a.observeGeoHealth(now, health, healthErr, states["health_geoip_update"].GeoScheduled)
}

func (a *App) observeGeoHealth(now time.Time, health assets.Health, err error, previouslyScheduled bool) monitorObservation {
	health = health.NormalizeLegacy()
	value := monitorObservation{Now: now, Status: MonitorRuleStatus{Key: "health_geoip_update", Enabled: a.options.Config.Alerts.Health.Enabled, State: "healthy", Reason: "healthy", Available: true}, Condition: "healthy"}
	if err != nil || health.Validate() != nil {
		if errors.Is(err, os.ErrNotExist) && !previouslyScheduled && a.options.Config.Geo.CityMMDB == "" && a.options.Config.Geo.ASNMMDB == "" {
			value.Condition = "disabled"
			value.Status.State = "disabled"
			value.Status.Reason = "disabled"
			return value
		}
		value.Condition = "unknown"
		value.Status.Available = false
		value.Status.Reason = "metadata_unavailable"
		if previouslyScheduled {
			value.Condition = "failed"
			value.Status.Available = true
		}
		return value
	}
	value.GeoKnown = true
	value.GeoScheduled = health.Scheduled
	if health.CheckedAt.After(now) || health.LastSuccessAt.After(now) || health.Schedule != nil && health.Schedule.CheckedAt.After(now) {
		value.Condition = "unknown"
		value.Status.Available = false
		value.Status.Reason = "check_unknown"
		return value
	}
	if health.Schedule != nil && int(health.Schedule.ConsecutiveFailures) >= a.options.Config.Alerts.Health.GeoIPFailureThreshold {
		value.Condition = "failed"
		value.Status.Reason = "schedule_failed"
		return value
	}
	if health.Result == "unknown" {
		value.Condition = "unknown"
		value.Status.Available = false
		value.Status.Reason = "check_unknown"
		return value
	}
	if int(health.ConsecutiveFailures) >= a.options.Config.Alerts.Health.GeoIPFailureThreshold {
		value.Condition = "failed"
		value.Status.Reason = "update_failed"
		return value
	}
	if health.Scheduled && (health.LastSuccessAt.IsZero() || now.Sub(health.LastSuccessAt) > a.options.Config.Alerts.Health.GeoIPStaleAfter.Duration) {
		value.Condition = "failed"
		value.Status.Reason = "update_stale"
		return value
	}
	// A below-threshold failed update is still not evidence of recovery.
	if health.Result != "ok" && health.Result != "unchanged" || health.Schedule != nil && health.Schedule.Result != "ok" {
		value.Condition = "unknown"
		value.Status.Available = false
		value.Status.Reason = "check_unknown"
	}
	return value
}

func observeStorageHealth(now time.Time, budget store.StorageBudgetStatus, err error, failed bool, since time.Time) monitorObservation {
	value := monitorObservation{Now: now, Condition: "healthy", Status: MonitorRuleStatus{Key: "health_storage", Enabled: true, Available: true, State: "healthy", Reason: "healthy"}}
	switch {
	case failed:
		value.Condition = "failed"
		value.Status.Reason = "storage_write_failed"
		value.Since = since
	case err != nil:
		value.Condition = "unknown"
		value.Status.Reason = "read_failed"
	case budget.State == "degraded":
		value.Condition = "failed"
		value.Status.Reason = "storage_pressure"
	case budget.State == "busy":
		value.Condition = "unknown"
		value.Status.Reason = "storage_busy"
	case !budget.Configured:
		value.Condition = "unknown"
		value.Status.Reason = "storage_unconfigured"
	case budget.State != "running":
		value.Condition = "unknown"
		value.Status.Reason = "check_unknown"
	}
	if value.Condition == "unknown" {
		value.Status.Available = false
		value.Status.State = "unknown"
	}
	return value
}

// A worker's final error must invalidate the same live observation read by the
// health monitor, even if persisting its component status then blocks/fails.
func (a *App) monitorWorkerStopped(name string, now time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	switch name {
	case "ssh_journal":
		if a.journalStatus.State == "running" || a.journalStatus.Since.IsZero() {
			a.journalStatus.Since = now
		}
		a.journalStatus.State = "degraded"
		a.journalStatus.Reason = "worker_stopped"
		a.journalStatus.At = now
	case "interface_counter":
		a.interfaceCounterState = "degraded"
	}
}

// Compare lifetime bounding spans against all UTC hours used by the estimate.
// Detailed ledger rows already encode their full hour span for SQL overlap.
func monitorRetentionRange(start, end time.Time) (time.Time, time.Time) {
	from, through := start.UTC().Truncate(time.Hour), end.UTC().Truncate(time.Hour)
	if through.Before(end) {
		through = through.Add(time.Hour)
	}
	return from, through
}
