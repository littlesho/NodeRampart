// SPDX-License-Identifier: MIT

package daemon

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/littlesho/NodeRampart/internal/billing"
)

// MonitorPeriodResult is the one previous recorded month, not a backlog of every
// period missed while offline. It survives advancing the current period.
type MonitorPeriodResult struct {
	Period    string    `json:"period"`
	Start     time.Time `json:"period_start_utc"`
	End       time.Time `json:"period_end_utc"`
	Evaluated bool      `json:"evaluated"`
	Reason    string    `json:"reason"`
	Milestone int       `json:"milestone"`
	Coverage  string    `json:"coverage"`
}

type monitorMonthPolicy struct {
	ThresholdBytes uint64         `json:"threshold_bytes,omitempty"`
	ThresholdCost  float64        `json:"threshold_cost,omitempty"`
	Currency       string         `json:"currency,omitempty"`
	UnitBytes      uint64         `json:"unit_bytes,omitempty"`
	FreeGB         float64        `json:"free_gb,omitempty"`
	Tiers          []billing.Tier `json:"tiers,omitempty"`
}

func copyMonthPolicy(p *monitorMonthPolicy) *monitorMonthPolicy {
	if p == nil {
		return nil
	}
	q := *p
	q.Tiers = append([]billing.Tier(nil), p.Tiers...)
	return &q
}

func (p monitorMonthPolicy) profile() billing.Profile {
	return billing.Profile{SchemaVersion: 1, Name: "Recorded monitor tariff", Provider: "custom", SourceRegion: "recorded", EffectiveDate: "1970-01-01", SourceURL: "https://example.invalid/recorded-tariff", Currency: p.Currency, UnitBytes: p.UnitBytes, FreeGB: p.FreeGB, InternetEgress: p.Tiers}
}

func (p monitorMonthPolicy) validate(key string) error {
	bad := errors.New("invalid recorded monthly policy")
	if key == "budget_month_bytes" {
		if p.ThresholdBytes == 0 || p.ThresholdBytes > math.MaxInt64 || p.ThresholdCost != 0 || p.Currency != "" || p.UnitBytes != 0 || p.FreeGB != 0 || len(p.Tiers) != 0 {
			return bad
		}
		return nil
	}
	if key != "budget_month_cost" || p.ThresholdBytes != 0 || p.ThresholdCost <= 0 || p.ThresholdCost > 1e12 || math.IsNaN(p.ThresholdCost) || math.IsInf(p.ThresholdCost, 0) {
		return bad
	}
	profile := p.profile()
	if profile.Validate() != nil {
		return bad
	}
	return nil
}

func validateMonthData(d monitorData, key string) error {
	if d.MonthPolicy != nil {
		if d.Period == "" || d.MonthPolicy.validate(key) != nil {
			return errors.New("invalid monthly policy state")
		}
	}
	if d.MonthFinalized && (d.Period == "" || (key != "budget_month_bytes" && key != "budget_month_cost")) {
		return errors.New("invalid finalized period")
	}
	return validatePeriodResult(d.PreviousPeriod)
}

func validatePeriodResult(p MonitorPeriodResult) error {
	if p == (MonitorPeriodResult{}) {
		return nil
	}
	at, err := time.Parse("2006-01", p.Period)
	if err != nil || at.Year() < 1970 || at.Year() > 9999 || !p.Start.Before(p.End) || p.End.Sub(p.Start) > 32*24*time.Hour || p.Start.Year() < 1970 || p.End.Year() > 9999 || (p.Milestone != 0 && p.Milestone != 80 && p.Milestone != 100) {
		return errors.New("invalid previous period result")
	}
	if p.Coverage != "unknown" && p.Coverage != "incomplete" && p.Coverage != "adequate_recorded" {
		return errors.New("invalid previous period coverage")
	}
	switch p.Reason {
	case "observed_estimate", "threshold_crossed":
		if !p.Evaluated {
			return errors.New("invalid evaluated period")
		}
	case "historical_policy_unavailable":
		if p.Evaluated {
			return errors.New("unavailable period cannot be evaluated")
		}
	default:
		return errors.New("invalid previous period reason")
	}
	return nil
}

func (a *App) currentMonthPolicy(s MonitorRuleStatus) *monitorMonthPolicy {
	if !s.Available {
		return nil
	}
	if s.Key == "budget_month_bytes" {
		return &monitorMonthPolicy{ThresholdBytes: s.ThresholdBytes}
	}
	if s.Key != "budget_month_cost" || a.options.Billing == nil {
		return nil
	}
	p := a.options.Billing
	return &monitorMonthPolicy{ThresholdCost: s.ThresholdCost, Currency: p.Currency, UnitBytes: p.UnitBytes, FreeGB: p.FreeGB, Tiers: append([]billing.Tier(nil), p.InternetEgress...)}
}

func needsMonthClose(previous monitorData, in monitorObservation) bool {
	return in.Status.Enabled && (in.Status.Key == "budget_month_bytes" || in.Status.Key == "budget_month_cost") && previous.Period != "" && in.Status.Period > previous.Period && !previous.MonthFinalized
}

func (a *App) closingObservation(ctx context.Context, now time.Time, previous monitorData) (monitorObservation, error) {
	// A timezone edit can advance the civil month before the old saved UTC
	// interval has ended. Keep its policy until that interval can be closed.
	if previous.PeriodEnd.After(now) {
		return monitorObservation{}, errors.New("recorded month has not ended")
	}
	s := previous.Status
	s.Period, s.PeriodStart, s.PeriodEnd = previous.Period, previous.PeriodStart, previous.PeriodEnd
	s.Enabled, s.Available, s.Pending = true, false, false
	s.LastPersistedObservation, s.PendingSince = time.Time{}, time.Time{}
	s.PendingAgeMS = 0
	s.Coverage, s.State, s.Reason = "unknown", "unknown", "historical_policy_unavailable"
	in := monitorObservation{Now: now, Status: s, Closing: true, MonthPolicy: copyMonthPolicy(previous.MonthPolicy)}
	if in.MonthPolicy == nil && previous.Status.Period == previous.Period && previous.Status.PeriodStart.Equal(previous.PeriodStart) && previous.Status.PeriodEnd.Equal(previous.PeriodEnd) && s.Key == "budget_month_bytes" && s.ThresholdBytes > 0 && s.ThresholdBytes <= math.MaxInt64 {
		in.MonthPolicy = &monitorMonthPolicy{ThresholdBytes: s.ThresholdBytes}
	}
	if in.MonthPolicy == nil || in.MonthPolicy.validate(s.Key) != nil {
		return in, nil
	}
	if !previous.PeriodStart.Before(previous.PeriodEnd) || previous.PeriodEnd.Sub(previous.PeriodStart) > 32*24*time.Hour {
		return in, errors.New("invalid closing range")
	}
	amount, err := a.options.Store.InterfaceSummary(ctx, previous.PeriodStart, previous.PeriodEnd)
	if err != nil {
		return in, err
	}
	in.Status.Coverage = a.monitorCoverage(ctx, previous.PeriodStart, previous.PeriodEnd)
	if ctx.Err() != nil {
		return in, ctx.Err()
	}
	in.Status.ObservedBytes = amount.TXBytes
	in.Status.ThresholdBytes = in.MonthPolicy.ThresholdBytes
	in.Status.ThresholdCost = in.MonthPolicy.ThresholdCost
	if s.Key == "budget_month_cost" {
		p := in.MonthPolicy.profile()
		estimate := p.Estimate(amount.TXBytes)
		if estimate.Unavailable {
			return in, errors.New("recorded monthly tariff unavailable")
		}
		in.Status.ObservedCost, in.Status.Currency = estimate.Cost, estimate.Currency
	}
	in.Status.Available = true
	in.Status.Reason = "observed_estimate"
	return in, nil
}
