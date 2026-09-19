// SPDX-License-Identifier: MIT

package model

import (
	"errors"
	"math"
	"strconv"
	"strings"
	"time"
)

const AlertBasis = "selected_interface_guest_tx"

// LegacyAlertBasis is the exact v1 observation description. Only this known
// description and the fixed short category may enter the sharing projection.
const LegacyAlertBasis = "Observed selected-interface guest TX estimate; private traffic and duplicated paths may be included; overlapping UTC-hour precision."

// AlertContext contains only recorded, identity-free alert inputs. Pointer
// values distinguish a recorded zero from an absent or rejected observation.
// It never borrows values from current configuration or monitor state.
type AlertContext struct {
	SchemaVersion     int        `json:"schema_version"`
	Availability      string     `json:"availability"`
	Metric            string     `json:"metric"`
	Reason            string     `json:"reason,omitempty"`
	Period            string     `json:"period,omitempty"`
	PeriodStart       *time.Time `json:"period_start_utc,omitempty"`
	PeriodEnd         *time.Time `json:"period_end_utc,omitempty"`
	ObservedBytes     *uint64    `json:"observed_bytes,omitempty"`
	ThresholdBytes    *uint64    `json:"threshold_bytes,omitempty"`
	ObservedCost      *float64   `json:"observed_cost,omitempty"`
	ThresholdCost     *float64   `json:"threshold_cost,omitempty"`
	Currency          string     `json:"currency,omitempty"`
	Milestone         *int       `json:"milestone,omitempty"`
	BaselineMeanBytes *float64   `json:"baseline_mean_bytes,omitempty"`
	BaselineDays      *int       `json:"baseline_days,omitempty"`
	GrowthRatio       *float64   `json:"growth_ratio,omitempty"`
	Coverage          string     `json:"coverage,omitempty"`
	Basis             string     `json:"basis,omitempty"`
	ConditionSince    *time.Time `json:"condition_since_utc,omitempty"`
}

func alertKind(kind string) bool {
	switch kind {
	case "budget_month_bytes", "budget_month_cost", "budget_day_bytes", "budget_day_growth",
		"health_sensor", "health_interface_counter", "health_ssh_journal", "health_storage", "health_geoip_update":
		return true
	}
	return false
}

func alertReason(reason string) bool {
	switch reason {
	case "disabled", "not_checked", "read_failed", "state_invalid", "clock_rollback", "insufficient_coverage", "baseline_unavailable", "zero_baseline", "billing_unavailable", "calendar_unavailable", "observed_estimate", "threshold_crossed", "healthy", "component_unavailable", "sensor_stale", "interface_stale", "journal_unavailable", "storage_write_failed", "storage_pressure", "storage_busy", "storage_unconfigured", "metadata_unavailable", "update_failed", "update_stale", "schedule_failed", "startup_grace", "check_unknown":
		return true
	}
	return false
}

func alertCoverage(coverage string) bool {
	return coverage == "unknown" || coverage == "incomplete" || coverage == "adequate_recorded"
}

func alertPeriod(kind, period string) bool {
	layout := "2006-01"
	if strings.HasPrefix(kind, "budget_day_") {
		layout = "2006-01-02"
	}
	parsed, err := time.Parse(layout, period)
	return err == nil && parsed.Year() >= 1970 && parsed.Year() <= 9999 && parsed.Format(layout) == period
}

func alertTime(at *time.Time) bool {
	if at == nil {
		return true
	}
	return !at.IsZero() && at.Year() >= 1970 && at.Year() <= 9999 && at.Location() == time.UTC
}

func alertCurrency(currency string) bool {
	return len(currency) == 3 && strings.IndexFunc(currency, func(r rune) bool { return r < 'A' || r > 'Z' }) < 0
}

func alertAmount(value *float64, maximum float64) bool {
	return value == nil || !math.IsNaN(*value) && !math.IsInf(*value, 0) && *value >= 0 && *value <= maximum
}

func (a *AlertContext) hasFields() bool {
	return a.Reason != "" || a.Period != "" || a.PeriodStart != nil || a.PeriodEnd != nil || a.ObservedBytes != nil || a.ThresholdBytes != nil || a.ObservedCost != nil || a.ThresholdCost != nil || a.Currency != "" || a.Milestone != nil || a.BaselineMeanBytes != nil || a.BaselineDays != nil || a.GrowthRatio != nil || a.Coverage != "" || a.Basis != "" || a.ConditionSince != nil
}

func (a *AlertContext) complete() bool {
	if strings.HasPrefix(a.Metric, "health_") {
		return a.Reason != "" && a.ConditionSince != nil
	}
	if a.Period == "" || a.PeriodStart == nil || a.PeriodEnd == nil || a.ObservedBytes == nil || a.Milestone == nil || a.Coverage == "" || a.Basis == "" {
		return false
	}
	switch a.Metric {
	case "budget_month_bytes", "budget_day_bytes":
		return a.ThresholdBytes != nil
	case "budget_month_cost":
		return a.ObservedCost != nil && a.ThresholdCost != nil && a.Currency != ""
	case "budget_day_growth":
		return a.BaselineMeanBytes != nil && a.BaselineDays != nil && a.GrowthRatio != nil
	}
	return false
}

// Validate rechecks every scalar and the per-kind shape before public export.
// Partial means the retained event did not provide a complete valid context;
// it can retain useful fields even when another original value was rejected.
func (a *AlertContext) Validate(kind string) error {
	bad := errors.New("invalid recorded alert context")
	if a == nil || a.SchemaVersion != 1 || !alertKind(kind) || a.Metric != kind {
		return bad
	}
	if a.Reason != "" && !alertReason(a.Reason) || a.Period != "" && !alertPeriod(kind, a.Period) || !alertTime(a.PeriodStart) || !alertTime(a.PeriodEnd) || !alertTime(a.ConditionSince) {
		return bad
	}
	if (a.PeriodStart == nil) != (a.PeriodEnd == nil) || a.PeriodStart != nil && !a.PeriodStart.Before(*a.PeriodEnd) {
		return bad
	}
	if !alertAmount(a.ObservedCost, 1e120) || !alertAmount(a.ThresholdCost, 1e12) || !alertAmount(a.BaselineMeanBytes, float64(math.MaxUint64)) || !alertAmount(a.GrowthRatio, 100) || a.Currency != "" && !alertCurrency(a.Currency) {
		return bad
	}
	if a.Milestone != nil && *a.Milestone != 0 && *a.Milestone != 80 && *a.Milestone != 100 || a.BaselineDays != nil && (*a.BaselineDays < 0 || *a.BaselineDays > 30) || a.Coverage != "" && !alertCoverage(a.Coverage) || a.Basis != "" && a.Basis != AlertBasis {
		return bad
	}
	if strings.HasPrefix(kind, "health_") {
		if a.Period != "" || a.PeriodStart != nil || a.ObservedBytes != nil || a.ThresholdBytes != nil || a.ObservedCost != nil || a.ThresholdCost != nil || a.Currency != "" || a.Milestone != nil || a.BaselineMeanBytes != nil || a.BaselineDays != nil || a.GrowthRatio != nil || a.Coverage != "" || a.Basis != "" {
			return bad
		}
	} else {
		if a.ConditionSince != nil || kind != "budget_month_cost" && (a.ObservedCost != nil || a.ThresholdCost != nil || a.Currency != "") || kind != "budget_day_growth" && (a.BaselineMeanBytes != nil || a.BaselineDays != nil || a.GrowthRatio != nil) || kind != "budget_month_bytes" && kind != "budget_day_bytes" && a.ThresholdBytes != nil {
			return bad
		}
		if a.ThresholdBytes != nil && *a.ThresholdBytes == 0 || a.ThresholdCost != nil && *a.ThresholdCost == 0 || a.GrowthRatio != nil && *a.GrowthRatio <= 1 {
			return bad
		}
	}
	switch a.Availability {
	case "recorded":
		if !a.complete() {
			return bad
		}
	case "partial":
		if !a.hasFields() {
			return bad
		}
	case "unavailable":
		if a.hasFields() {
			return bad
		}
	default:
		return bad
	}
	return nil
}

// ProjectAlertContext reads only the fixed evidence keys emitted by monitor v1.
// Non-monitor events have no alert context. Invalid/missing known fields are
// omitted with an explicit availability qualification, never copied as text.
func ProjectAlertContext(kind string, fields map[string]string) *AlertContext {
	if !alertKind(kind) {
		return nil
	}
	a := &AlertContext{SchemaVersion: 1, Metric: kind, Availability: "unavailable"}
	rejected := false
	text := func(key string, valid func(string) bool) string {
		value := fields[key]
		if value == "" {
			return ""
		}
		if len(value) > 256 || !valid(value) {
			rejected = true
			return ""
		}
		return value
	}
	a.Reason = text("reason", alertReason)
	parseTime := func(key string) *time.Time {
		value := fields[key]
		if value == "" {
			return nil
		}
		if len(value) <= 40 {
			at, err := time.Parse(time.RFC3339Nano, value)
			if err == nil && !at.IsZero() && at.Year() >= 1970 && at.Year() <= 9999 {
				at = at.UTC()
				return &at
			}
		}
		rejected = true
		return nil
	}
	if strings.HasPrefix(kind, "health_") {
		a.ConditionSince = parseTime("condition_since_utc")
	} else {
		a.Period = text("period", func(value string) bool { return alertPeriod(kind, value) })
		a.PeriodStart, a.PeriodEnd = parseTime("period_start_utc"), parseTime("period_end_utc")
		if (a.PeriodStart == nil) != (a.PeriodEnd == nil) || a.PeriodStart != nil && !a.PeriodStart.Before(*a.PeriodEnd) {
			a.PeriodStart, a.PeriodEnd = nil, nil
			rejected = true
		}
		unsigned := func(key string) *uint64 {
			value := fields[key]
			if value == "" {
				return nil
			}
			if len(value) <= 20 && strings.IndexFunc(value, func(r rune) bool { return r < '0' || r > '9' }) < 0 {
				parsed, err := strconv.ParseUint(value, 10, 64)
				if err == nil {
					return &parsed
				}
			}
			rejected = true
			return nil
		}
		amount := func(key string, maximum float64) *float64 {
			value := fields[key]
			if value == "" {
				return nil
			}
			if len(value) <= 128 && strings.IndexFunc(value, func(r rune) bool {
				return (r < '0' || r > '9') && r != '.' && r != 'e' && r != 'E' && r != '+' && r != '-'
			}) < 0 {
				parsed, err := strconv.ParseFloat(value, 64)
				if err == nil && alertAmount(&parsed, maximum) {
					return &parsed
				}
			}
			rejected = true
			return nil
		}
		a.ObservedBytes = unsigned("observed_bytes")
		if milestone := unsigned("milestone"); milestone != nil {
			if *milestone == 0 || *milestone == 80 || *milestone == 100 {
				value := int(*milestone)
				a.Milestone = &value
			} else {
				rejected = true
			}
		}
		a.Coverage = text("coverage", alertCoverage)
		if basis := text("basis", func(value string) bool { return value == LegacyAlertBasis || value == AlertBasis }); basis != "" {
			a.Basis = AlertBasis
		}
		switch kind {
		case "budget_month_bytes", "budget_day_bytes":
			a.ThresholdBytes = unsigned("threshold_bytes")
			if a.ThresholdBytes != nil && *a.ThresholdBytes == 0 {
				a.ThresholdBytes = nil
				rejected = true
			}
		case "budget_month_cost":
			a.ObservedCost, a.ThresholdCost = amount("observed_cost", 1e120), amount("threshold_cost", 1e12)
			if a.ThresholdCost != nil && *a.ThresholdCost == 0 {
				a.ThresholdCost = nil
				rejected = true
			}
			a.Currency = text("currency", alertCurrency)
		case "budget_day_growth":
			a.BaselineMeanBytes, a.GrowthRatio = amount("baseline_mean_bytes", float64(math.MaxUint64)), amount("growth_ratio", 100)
			if a.GrowthRatio != nil && *a.GrowthRatio <= 1 {
				a.GrowthRatio = nil
				rejected = true
			}
			if days := unsigned("baseline_days"); days != nil {
				if *days <= 30 {
					value := int(*days)
					a.BaselineDays = &value
				} else {
					rejected = true
				}
			}
		}
	}
	if a.hasFields() {
		a.Availability = "partial"
		if !rejected && a.complete() {
			a.Availability = "recorded"
		}
	}
	return a
}
