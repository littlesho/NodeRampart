// SPDX-License-Identifier: MIT

package report

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/littlesho/NodeRampart/internal/billing"
)

type ForecastScenario struct {
	Days               int               `json:"complete_days"`
	MeanDailyTXBytes   float64           `json:"mean_daily_tx_bytes"`
	RateBytesPerSecond float64           `json:"rate_bytes_per_second"`
	ProjectedTXBytes   uint64            `json:"projected_cycle_tx_bytes"`
	ProjectedCost      *billing.Estimate `json:"projected_cost,omitempty"`
	ByteThresholdAt    *time.Time        `json:"byte_threshold_at_utc"`
	CostThresholdAt    *time.Time        `json:"cost_threshold_at_utc"`
}

type ForecastResult struct {
	Available       bool               `json:"available"`
	Reason          string             `json:"reason"`
	PeriodStart     time.Time          `json:"period_start_utc"`
	PeriodEnd       time.Time          `json:"period_end_utc"`
	AsOf            time.Time          `json:"as_of_utc"`
	Timezone        string             `json:"timezone"`
	CycleStartDay   int                `json:"cycle_start_day"`
	ObservedTXBytes uint64             `json:"observed_tx_bytes"`
	Coverage        string             `json:"coverage"`
	ThresholdBytes  uint64             `json:"threshold_bytes"`
	ThresholdCost   float64            `json:"threshold_cost"`
	CurrentCost     *billing.Estimate  `json:"current_cost,omitempty"`
	Scenarios       []ForecastScenario `json:"scenarios"`
	MinimumTXBytes  *uint64            `json:"minimum_cycle_tx_bytes"`
	MaximumTXBytes  *uint64            `json:"maximum_cycle_tx_bytes"`
	MinimumCost     *float64           `json:"minimum_cycle_cost"`
	MaximumCost     *float64           `json:"maximum_cycle_cost"`
	Currency        string             `json:"currency,omitempty"`
	Notes           []string           `json:"notes"`
}

func (b *Builder) Forecast(ctx context.Context, now time.Time) (ForecastResult, error) {
	day := b.CycleStartDay
	if day == 0 {
		day = 1
	}
	result := ForecastResult{Reason: "insufficient_history", AsOf: now.UTC(), Timezone: b.location().String(), CycleStartDay: day, ThresholdBytes: b.ThresholdBytes, ThresholdCost: b.ThresholdCost, Scenarios: []ForecastScenario{}, Notes: []string{"Simple elapsed-time extrapolation from 7/30 complete recorded civil days; scenarios are not statistical confidence intervals.", "Guest TX is not a provider bill. Free allowance and current usage use this same fixed monthly cycle. No costs outside the configured tariff are included.", "Missing, partial or pruned days do not become zeros; no crossing within the cycle is represented by a null date."}}
	start, end, ok := BillingCycle(now, b.location(), day)
	if !ok {
		return result, errors.New("billing cycle unavailable")
	}
	result.PeriodStart, result.PeriodEnd = start, end
	work, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if start.Equal(now) {
		result.Coverage, result.Reason = "partial", "cycle_just_started"
		return result, nil
	}
	amount, records, err := b.Store.InterfaceSummaryWithRecords(work, start, now)
	if err != nil {
		return result, err
	}
	result.ObservedTXBytes = amount.TXBytes
	integrity, retention, err := b.Store.MonitorCoverage(work, start, now)
	if err != nil {
		return result, err
	}
	result.Coverage, result.Reason = usageCoverage(start, now, integrity, retention)
	result.Coverage, result.Reason = usageRecords(start, now, records, result.Coverage, result.Reason)
	if b.Billing != nil {
		estimate := b.Billing.Estimate(amount.TXBytes)
		if !estimate.Unavailable {
			result.CurrentCost = &estimate
		}
	}
	if result.Coverage != "complete" {
		result.Reason = "current_cycle_coverage_incomplete"
		return result, nil
	}
	trend, err := b.Trend(work, 30, now)
	if err != nil {
		return result, err
	}
	for _, window := range []int{7, 30} {
		var total, elapsed float64
		complete := true
		for _, sample := range trend.Dates[len(trend.Dates)-window:] {
			if sample.State != "complete" || sample.TXBytes == nil {
				complete = false
				break
			}
			total += float64(*sample.TXBytes)
			elapsed += sample.End.Sub(sample.Start).Seconds()
		}
		if !complete || elapsed <= 0 {
			continue
		}
		scenario := ForecastScenario{Days: window, MeanDailyTXBytes: total / float64(window), RateBytesPerSecond: total / elapsed}
		projected := float64(amount.TXBytes) + scenario.RateBytesPerSecond*end.Sub(now).Seconds()
		if math.IsInf(projected, 0) || projected < 0 || projected >= float64(math.MaxUint64) {
			result.Reason = "projection_exceeds_numeric_limit"
			continue
		}
		scenario.ProjectedTXBytes = uint64(math.Ceil(projected))
		scenario.ByteThresholdAt = thresholdTime(now, end, amount.TXBytes, b.ThresholdBytes, scenario.RateBytesPerSecond)
		if b.Billing != nil {
			estimate := b.Billing.Estimate(scenario.ProjectedTXBytes)
			if !estimate.Unavailable {
				scenario.ProjectedCost = &estimate
			}
			if b.ThresholdCost > 0 && estimate.Cost >= b.ThresholdCost && !estimate.Unavailable {
				low, high := amount.TXBytes, scenario.ProjectedTXBytes
				for low < high {
					mid := low + (high-low)/2
					if b.Billing.Estimate(mid).Cost >= b.ThresholdCost {
						high = mid
					} else {
						low = mid + 1
					}
				}
				scenario.CostThresholdAt = thresholdTime(now, end, amount.TXBytes, low, scenario.RateBytesPerSecond)
			}
		}
		result.Scenarios = append(result.Scenarios, scenario)
	}
	if len(result.Scenarios) == 0 {
		if result.Reason != "projection_exceeds_numeric_limit" {
			result.Reason = "insufficient_complete_days"
		}
		return result, nil
	}
	minimum, maximum := result.Scenarios[0].ProjectedTXBytes, result.Scenarios[0].ProjectedTXBytes
	for _, scenario := range result.Scenarios {
		minimum = min(minimum, scenario.ProjectedTXBytes)
		maximum = max(maximum, scenario.ProjectedTXBytes)
	}
	result.Available, result.Reason, result.MinimumTXBytes, result.MaximumTXBytes = true, "complete_day_scenarios", &minimum, &maximum
	if result.Scenarios[0].ProjectedCost != nil {
		minCost, maxCost := result.Scenarios[0].ProjectedCost.Cost, result.Scenarios[0].ProjectedCost.Cost
		allCosts := true
		for _, scenario := range result.Scenarios {
			if scenario.ProjectedCost == nil {
				allCosts = false
				break
			}
			minCost, maxCost = min(minCost, scenario.ProjectedCost.Cost), max(maxCost, scenario.ProjectedCost.Cost)
		}
		if allCosts {
			result.MinimumCost, result.MaximumCost, result.Currency = &minCost, &maxCost, result.Scenarios[0].ProjectedCost.Currency
		}
	}
	return result, work.Err()
}

func thresholdTime(now, end time.Time, current, threshold uint64, rate float64) *time.Time {
	if threshold == 0 {
		return nil
	}
	if current >= threshold {
		at := now.UTC()
		return &at
	}
	if rate <= 0 {
		return nil
	}
	seconds := float64(threshold-current) / rate
	if seconds > end.Sub(now).Seconds() || math.IsInf(seconds, 0) || math.IsNaN(seconds) {
		return nil
	}
	at := now.Add(time.Duration(seconds * float64(time.Second))).UTC()
	return &at
}
