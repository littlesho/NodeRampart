// SPDX-License-Identifier: MIT

package daemon

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"time"

	"github.com/littlesho/NodeRampart/internal/api"
	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/model"
)

var monitorKeys = [...]string{"budget_month_bytes", "budget_month_cost", "budget_day_bytes", "budget_day_growth", "health_sensor", "health_interface_counter", "health_ssh_journal", "health_storage", "health_geoip_update"}

const monitorBasis = "Observed selected-interface guest TX estimate; private traffic and duplicated paths may be included; overlapping UTC-hour precision."

// MonitorStatus contains only fixed categories, numeric observations and times.
// Pending transitions are local memory and can be lost on abrupt restart.
type MonitorStatus struct {
	Enabled   bool                 `json:"enabled"`
	CheckedAt time.Time            `json:"checked_at_utc,omitzero"`
	Available bool                 `json:"available"`
	Pending   int                  `json:"pending"`
	Rules     []MonitorRuleStatus  `json:"rules"`
	Checks    []MonitorCheckStatus `json:"checks"`
	Note      string               `json:"note"`
}

type MonitorRuleStatus struct {
	Key                      string              `json:"key"`
	State                    string              `json:"state"`
	Reason                   string              `json:"reason"`
	Enabled                  bool                `json:"enabled"`
	Available                bool                `json:"available"`
	Pending                  bool                `json:"pending"`
	Period                   string              `json:"period,omitempty"`
	PeriodStart              time.Time           `json:"period_start_utc,omitzero"`
	PeriodEnd                time.Time           `json:"period_end_utc,omitzero"`
	ObservedBytes            uint64              `json:"observed_bytes,omitempty"`
	ThresholdBytes           uint64              `json:"threshold_bytes,omitempty"`
	ObservedCost             float64             `json:"observed_cost,omitempty"`
	ThresholdCost            float64             `json:"threshold_cost,omitempty"`
	Currency                 string              `json:"currency,omitempty"`
	GrowthRatio              float64             `json:"growth_ratio,omitempty"`
	BaselineMeanBytes        float64             `json:"baseline_mean_bytes,omitempty"`
	BaselineDays             int                 `json:"baseline_days,omitempty"`
	Milestone                int                 `json:"milestone,omitempty"`
	Basis                    string              `json:"basis,omitempty"`
	Coverage                 string              `json:"coverage,omitempty"`
	ConditionSince           time.Time           `json:"condition_since_utc,omitzero"`
	LastNotification         time.Time           `json:"last_notification_utc,omitzero"`
	PreviousPeriod           MonitorPeriodResult `json:"previous_period,omitzero"`
	LastPersistedObservation time.Time           `json:"last_persisted_observation_utc,omitzero"`
	PendingSince             time.Time           `json:"pending_since_utc,omitzero"`
	PendingAgeMS             int64               `json:"pending_age_milliseconds,omitempty"`
}

func (a *App) MonitorStatus() MonitorStatus {
	a.monitorMu.RLock()
	value := a.monitorStatus
	value.Rules = append([]MonitorRuleStatus(nil), value.Rules...)
	value.Checks = append([]MonitorCheckStatus(nil), value.Checks...)
	a.monitorMu.RUnlock()
	if value.Rules == nil {
		value = initialMonitorStatus(a.options.Config.Alerts)
	}
	updateMonitorAges(&value, time.Now().UTC())
	return value
}

func initialMonitorStatus(cfg config.AlertsConfig) MonitorStatus {
	result := MonitorStatus{Enabled: cfg.Budget.Enabled || cfg.Health.Enabled, Available: true, Rules: []MonitorRuleStatus{}, Note: "Pending observations are not durable and may be lost on abrupt restart. Availability is not proof of complete data or notification delivery."}
	result.Checks = []MonitorCheckStatus{{Group: "health", State: "not_checked"}, {Group: "budget", State: "not_checked"}}
	for _, key := range monitorKeys {
		enabled := monitorEnabled(key, cfg)
		state, reason := "disabled", "disabled"
		if enabled {
			state, reason = "unknown", "not_checked"
			result.Available = false
		}
		result.Rules = append(result.Rules, MonitorRuleStatus{Key: key, Enabled: enabled, State: state, Reason: reason, Available: !enabled})
	}
	return result
}

func monitorEnabled(key string, cfg config.AlertsConfig) bool {
	switch key {
	case "budget_month_bytes":
		return cfg.Budget.Enabled && cfg.Budget.MonthlyBytes > 0
	case "budget_month_cost":
		return cfg.Budget.Enabled && cfg.Budget.MonthlyCost > 0
	case "budget_day_bytes":
		return cfg.Budget.Enabled && cfg.Budget.DailyBytes > 0
	case "budget_day_growth":
		return cfg.Budget.Enabled && cfg.Budget.DailyGrowthRatio > 0
	default:
		return cfg.Health.Enabled
	}
}

// Payload v2 adds bounded monthly closing inputs. Legacy v1 remains readable;
// missing historical tariffs stay unavailable rather than using today's tariff.
type monitorData struct {
	SchemaVersion    int                 `json:"schema_version"`
	ObservedAt       time.Time           `json:"observed_at_utc"`
	Period           string              `json:"period,omitempty"`
	PeriodStart      time.Time           `json:"period_start_utc,omitzero"`
	PeriodEnd        time.Time           `json:"period_end_utc,omitzero"`
	Milestone        int                 `json:"milestone,omitempty"`
	Evaluated        bool                `json:"evaluated"`
	IncidentID       string              `json:"incident_id,omitempty"`
	Active           bool                `json:"active"`
	ConditionSince   time.Time           `json:"condition_since_utc,omitzero"`
	RecoverySince    time.Time           `json:"recovery_since_utc,omitzero"`
	LastNotification time.Time           `json:"last_notification_utc,omitzero"`
	GeoScheduled     bool                `json:"geo_scheduled"`
	Status           MonitorRuleStatus   `json:"status"`
	MonthPolicy      *monitorMonthPolicy `json:"month_policy,omitempty"`
	MonthFinalized   bool                `json:"month_finalized,omitempty"`
	PreviousPeriod   MonitorPeriodResult `json:"previous_period,omitzero"`
}

func decodeMonitorData(raw []byte, key string) (monitorData, error) {
	var data monitorData
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if len(raw) > 16384 || decoder.Decode(&data) != nil || decoder.Decode(new(any)) != io.EOF || (data.SchemaVersion != 1 && data.SchemaVersion != 2) || data.Status.Key != key {
		return data, errors.New("unsupported monitor state")
	}
	if data.Milestone != 0 && data.Milestone != 80 && data.Milestone != 100 || data.Active && !api.ValidID(data.IncidentID) || data.IncidentID != "" && !api.ValidID(data.IncidentID) {
		return data, errors.New("invalid monitor milestone or identity")
	}
	if data.Milestone > 0 && (data.Period == "" || !api.ValidID(data.IncidentID)) || data.Evaluated && (!strings.HasPrefix(key, "budget_day_") || data.Period == "") || data.Active && data.ConditionSince.IsZero() {
		return data, errors.New("invalid monitor transition state")
	}
	if data.Period != "" {
		layout := "2006-01"
		if strings.HasPrefix(key, "budget_day_") {
			layout = "2006-01-02"
		}
		if _, err := time.Parse(layout, data.Period); err != nil {
			return data, errors.New("invalid monitor period")
		}
		if !data.PeriodStart.Before(data.PeriodEnd) {
			return data, errors.New("invalid monitor period bounds")
		}
	}
	for _, at := range []time.Time{data.ObservedAt, data.PeriodStart, data.PeriodEnd, data.ConditionSince, data.RecoverySince, data.LastNotification, data.Status.ConditionSince, data.Status.LastNotification} {
		if !at.IsZero() && (at.Year() < 1970 || at.Year() > 9999) {
			return data, errors.New("invalid monitor time")
		}
	}
	if data.ObservedAt.IsZero() || data.ConditionSince.After(data.ObservedAt) || data.RecoverySince.After(data.ObservedAt) || data.LastNotification.After(data.ObservedAt) {
		return data, errors.New("invalid monitor observation order")
	}
	if err := validateMonitorStatus(data.Status); err != nil {
		return data, err
	}
	if err := validateMonthData(data, key); err != nil {
		return data, err
	}
	return data, nil
}

func validateMonitorStatus(s MonitorRuleStatus) error {
	if err := validatePeriodResult(s.PreviousPeriod); err != nil {
		return err
	}
	validKey := false
	for _, key := range monitorKeys {
		if key == s.Key {
			validKey = true
		}
	}
	if !validKey {
		return errors.New("invalid monitor key")
	}
	if s.Period != "" {
		if !s.PeriodStart.Before(s.PeriodEnd) {
			return errors.New("invalid monitor status period bounds")
		}
		layout := "2006-01"
		if strings.HasPrefix(s.Key, "budget_day_") {
			layout = "2006-01-02"
		}
		if _, err := time.Parse(layout, s.Period); err != nil {
			return errors.New("invalid monitor status period")
		}
	}
	if s.Milestone != 0 && s.Milestone != 80 && s.Milestone != 100 {
		return errors.New("invalid monitor status milestone")
	}
	for _, at := range []time.Time{s.PeriodStart, s.PeriodEnd, s.ConditionSince, s.LastNotification} {
		if !at.IsZero() && (at.Year() < 1970 || at.Year() > 9999) {
			return errors.New("invalid monitor status time")
		}
	}

	switch s.State {
	case "disabled", "unknown", "observed", "alert", "healthy", "debouncing", "recovering":
	default:
		return errors.New("invalid monitor state category")
	}
	switch s.Reason {
	case "disabled", "not_checked", "read_failed", "state_invalid", "clock_rollback", "insufficient_coverage", "baseline_unavailable", "zero_baseline", "billing_unavailable", "calendar_unavailable", "observed_estimate", "threshold_crossed", "healthy", "component_unavailable", "sensor_stale", "interface_stale", "journal_unavailable", "storage_write_failed", "storage_pressure", "storage_busy", "storage_unconfigured", "metadata_unavailable", "update_failed", "update_stale", "startup_grace", "check_unknown", "historical_policy_unavailable", "period_close_pending", "schedule_failed":
	default:
		return errors.New("invalid monitor reason")
	}
	switch s.Coverage {
	case "", "unknown", "incomplete", "adequate_recorded":
	default:
		return errors.New("invalid monitor coverage")
	}
	if s.Basis != "" && s.Basis != monitorBasis || s.BaselineDays < 0 || s.BaselineDays > 30 || s.Currency != "" && (len(s.Currency) != 3 || strings.IndexFunc(s.Currency, func(r rune) bool { return r < 'A' || r > 'Z' }) >= 0) {
		return errors.New("invalid monitor status fields")
	}
	for _, v := range []float64{s.ObservedCost, s.ThresholdCost, s.GrowthRatio, s.BaselineMeanBytes} {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
			return errors.New("invalid monitor numeric value")
		}
	}
	return nil
}

type monitorObservation struct {
	Now          time.Time
	Status       MonitorRuleStatus
	Condition    string // healthy, failed, unknown or disabled; fixed health categories.
	Since        time.Time
	GeoKnown     bool
	GeoScheduled bool
	MonthPolicy  *monitorMonthPolicy
	Closing      bool
}

func evaluateMonitor(previous monitorData, in monitorObservation, cfg config.AlertsConfig, started time.Time) (monitorData, []model.Event) {
	next := previous
	next.SchemaVersion = 2
	if in.Now.Before(previous.ObservedAt) || in.Status.Period != "" && previous.Period != "" && in.Status.Period < previous.Period {
		return previous, nil
	}
	next.ObservedAt = in.Now
	next.Status = in.Status
	if strings.HasPrefix(in.Status.Key, "budget_") {
		return evaluateBudget(next, previous, in)
	}
	if in.GeoKnown {
		next.GeoScheduled = in.GeoScheduled
	}
	if !in.Status.Enabled || in.Condition == "disabled" {
		next.Active = false
		next.ConditionSince = time.Time{}
		next.RecoverySince = time.Time{}
		next.IncidentID = ""
		next.Status.State = "disabled"
		next.Status.Reason = "disabled"
		next.Status.Available = true
		return next, nil
	}
	if in.Condition == "unknown" {
		next.RecoverySince = time.Time{}
		if !next.Active {
			next.ConditionSince = time.Time{}
		}
		next.Status.State = "unknown"
		next.Status.Available = false
		return healthStatus(next), nil
	}
	h := cfg.Health
	startup := in.Now.Before(started.Add(h.GracePeriod.Duration))
	if in.Condition == "failed" {
		next.RecoverySince = time.Time{}
		if next.ConditionSince.IsZero() {
			next.ConditionSince = in.Now
			if !in.Since.IsZero() && !in.Since.After(in.Now) {
				next.ConditionSince = in.Since
			}
		}
		if !next.Active {
			next.Status.State = "debouncing"
			if startup || in.Now.Sub(next.ConditionSince) < h.GracePeriod.Duration {
				return healthStatus(next), nil
			}
			next.Active = true
			next.IncidentID = model.NewID("monitor")
			next.LastNotification = in.Now
			next.Status.State = "alert"
			return healthStatus(next), []model.Event{healthEvent(next, in.Now, "start")}
		}
		next.Status.State = "alert"
		if !startup && h.ReminderInterval.Duration > 0 && in.Now.Sub(next.LastNotification) >= h.ReminderInterval.Duration {
			next.LastNotification = in.Now
			return healthStatus(next), []model.Event{healthEvent(next, in.Now, "update")}
		}
		return healthStatus(next), nil
	}
	if startup {
		next.RecoverySince = time.Time{}
		next.Status.State = "unknown"
		next.Status.Reason = "startup_grace"
		next.Status.Available = false
		return healthStatus(next), nil
	}
	if next.Active {
		if next.RecoverySince.IsZero() {
			next.RecoverySince = in.Now
		}
		next.Status.State = "recovering"
		if in.Now.Sub(next.RecoverySince) < h.RecoveryPeriod.Duration {
			return healthStatus(next), nil
		}
		next.Active = false
		next.LastNotification = in.Now
		next.Status.State = "healthy"
		event := healthEvent(next, in.Now, "recovery")
		next.ConditionSince = time.Time{}
		next.RecoverySince = time.Time{}
		return healthStatus(next), []model.Event{event}
	}
	next.ConditionSince = time.Time{}
	next.RecoverySince = time.Time{}
	next.Status.State = "healthy"
	return healthStatus(next), nil
}

func healthStatus(data monitorData) monitorData {
	data.Status.ConditionSince = data.ConditionSince
	data.Status.LastNotification = data.LastNotification
	return data
}

func healthEvent(data monitorData, now time.Time, phase string) model.Event {
	severity := model.SeverityMedium
	summary := "Monitoring health condition requires attention"
	if phase == "recovery" {
		severity = model.SeverityInfo
		summary = "Monitoring health condition recovered"
	}
	return model.Event{ID: model.NewID("evt"), IncidentID: data.IncidentID, ObservedAt: now, Kind: data.Status.Key, Phase: phase, Severity: severity, Summary: summary, Evidence: map[string]string{"reason": data.Status.Reason, "condition_since_utc": data.ConditionSince.Format(time.RFC3339Nano), "observation": "recorded component health; does not guarantee complete collection or notification delivery"}}
}

func evaluateBudget(next, previous monitorData, in monitorObservation) (monitorData, []model.Event) {
	s := in.Status
	if in.Closing && previous.MonthFinalized {
		return previous, nil
	}
	next.Status.PreviousPeriod = previous.PreviousPeriod
	if in.Closing && !s.Available && s.Reason == "historical_policy_unavailable" {
		next.MonthFinalized = true
		next.PreviousPeriod = MonitorPeriodResult{Period: previous.Period, Start: previous.PeriodStart, End: previous.PeriodEnd, Reason: s.Reason, Milestone: previous.Milestone, Coverage: s.Coverage}
		next.Status.PreviousPeriod = next.PreviousPeriod
		next.Status.State = "unknown"
		return next, nil
	}
	if !s.Enabled {
		next.Status.State = "disabled"
		next.Status.Reason = "disabled"
		next.Status.Available = true
		return next, nil
	}
	if !s.Available {
		if s.Period == previous.Period {
			next.Status.Milestone = previous.Milestone
			next.Status.LastNotification = previous.LastNotification
		}
		next.Status.State = "unknown"
		return next, nil
	}
	daily := strings.HasPrefix(s.Key, "budget_day_")
	if previous.Period == s.Period && daily && previous.Evaluated {
		next.Status = previous.Status
		return next, nil
	}
	if previous.Period != s.Period {
		next.Period = s.Period
		next.PeriodStart = s.PeriodStart
		next.PeriodEnd = s.PeriodEnd
		next.Milestone = 0
		next.Evaluated = false
		next.IncidentID = ""
		next.LastNotification = time.Time{}
		next.MonthFinalized = false
		next.MonthPolicy = nil
	}
	if !daily && !in.Closing {
		next.PeriodStart, next.PeriodEnd = s.PeriodStart, s.PeriodEnd
		next.MonthPolicy = copyMonthPolicy(in.MonthPolicy)
		if next.MonthPolicy == nil && s.Key == "budget_month_bytes" && s.ThresholdBytes > 0 {
			next.MonthPolicy = &monitorMonthPolicy{ThresholdBytes: s.ThresholdBytes}
		}
	}
	milestone := 0
	switch s.Key {
	case "budget_month_bytes":
		if s.ObservedBytes >= s.ThresholdBytes {
			milestone = 100
		} else if s.ObservedBytes >= s.ThresholdBytes/5*4+(s.ThresholdBytes%5*4+4)/5 {
			milestone = 80
		}
	case "budget_month_cost":
		if s.ObservedCost >= s.ThresholdCost {
			milestone = 100
		} else if s.ObservedCost >= s.ThresholdCost*0.8 {
			milestone = 80
		}
	case "budget_day_bytes":
		if s.ObservedBytes >= s.ThresholdBytes {
			milestone = 100
		}
	case "budget_day_growth":
		if float64(s.ObservedBytes) >= s.BaselineMeanBytes*s.GrowthRatio {
			milestone = 100
		}
	}
	next.Evaluated = daily
	next.Status.State = "observed"
	next.Status.Reason = "observed_estimate"
	var events []model.Event
	if milestone > next.Milestone {
		phase := "update"
		if next.Milestone == 0 {
			phase = "start"
			next.IncidentID = model.NewID("monitor")
		}
		next.Milestone = milestone
		next.LastNotification = in.Now
		severity := model.SeverityMedium
		if milestone == 100 {
			severity = model.SeverityHigh
		}
		events = []model.Event{{ID: model.NewID("evt"), IncidentID: next.IncidentID, ObservedAt: in.Now, Kind: s.Key, Phase: phase, Severity: severity, Summary: fmt.Sprintf("Observed guest TX estimate reached %d%% of the configured threshold", milestone), Evidence: map[string]string{"period": s.Period, "period_start_utc": s.PeriodStart.UTC().Format(time.RFC3339Nano), "period_end_utc": s.PeriodEnd.UTC().Format(time.RFC3339Nano), "reason": "threshold_crossed", "observed_bytes": fmt.Sprint(s.ObservedBytes), "threshold_bytes": fmt.Sprint(s.ThresholdBytes), "observed_cost": fmt.Sprint(s.ObservedCost), "threshold_cost": fmt.Sprint(s.ThresholdCost), "currency": s.Currency, "baseline_mean_bytes": fmt.Sprint(s.BaselineMeanBytes), "baseline_days": fmt.Sprint(s.BaselineDays), "growth_ratio": fmt.Sprint(s.GrowthRatio), "milestone": fmt.Sprint(milestone), "coverage": s.Coverage, "basis": monitorBasis}}}
	}
	if next.Milestone > 0 {
		next.Status.State = "alert"
		next.Status.Reason = "threshold_crossed"
	}
	next.Status.Milestone = next.Milestone
	next.Status.LastNotification = next.LastNotification
	if in.Closing {
		next.MonthFinalized = true
		next.PreviousPeriod = MonitorPeriodResult{Period: next.Period, Start: next.PeriodStart, End: next.PeriodEnd, Evaluated: true, Reason: next.Status.Reason, Milestone: next.Milestone, Coverage: s.Coverage}
		next.Status.PreviousPeriod = next.PreviousPeriod
	}
	return next, events
}
