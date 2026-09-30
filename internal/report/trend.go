// SPDX-License-Identifier: MIT

package report

import (
	"context"
	"errors"
	"time"

	"github.com/littlesho/NodeRampart/internal/store"
)

type TrendDay struct {
	Date    string    `json:"date"`
	Start   time.Time `json:"start_utc"`
	End     time.Time `json:"end_utc"`
	State   string    `json:"state"`
	Reason  string    `json:"reason"`
	Source  string    `json:"source"`
	RXBytes *uint64   `json:"rx_bytes"`
	TXBytes *uint64   `json:"tx_bytes"`
}

type TrendResult struct {
	Days     int        `json:"days"`
	Timezone string     `json:"timezone"`
	AsOf     time.Time  `json:"as_of_utc"`
	Dates    []TrendDay `json:"dates"`
	Notes    []string   `json:"notes"`
}

// usageCoverage concerns the interface counters used for quota estimates.
// Capture losses are separate: guest counters and packet attribution differ.
func usageCoverage(start, end time.Time, integrity store.IntegrityView, retention store.RetentionView) (string, string) {
	for _, entry := range retention.Entries {
		if entry.Dataset == "interface_hourly" {
			return "pruned", "interface_history_pruned"
		}
	}
	if retention.EvictedEntries > 0 {
		for _, total := range retention.Totals {
			if total.Dataset == "interface_hourly" && (total.DataStart.IsZero() || !total.DataStart.Before(total.DataEnd) || total.DataStart.Before(end) && total.DataEnd.After(start)) {
				return "pruned", "retention_detail_expired"
			}
		}
	}
	running := int64(0)
	for _, component := range integrity.Components {
		if component.Name == "interface_counter" {
			running = component.RunningMS
			break
		}
	}
	if running == 0 {
		return "missing", "interface_coverage_unknown"
	}
	if integrity.HistoryTruncated || integrity.GapsTruncated || retention.TrackingStarted.IsZero() || retention.TrackingStarted.After(start) {
		return "partial", "coverage_history_incomplete"
	}
	for _, gap := range integrity.Gaps {
		if gap.Name == "interface_counter" {
			return "partial", "interface_coverage_gap"
		}
	}
	duration := end.Sub(start).Milliseconds()
	for _, component := range integrity.Components {
		if component.Name != "interface_counter" {
			continue
		}
		if component.RunningMS == 0 {
			return "missing", "interface_coverage_unknown"
		}
		if duration > 0 && component.RunningMS >= duration && component.DegradedMS == 0 && component.UnknownMS == 0 && component.ConflictMS == 0 {
			return "complete", "recorded_interface_coverage"
		}
		return "partial", "interface_coverage_incomplete"
	}
	return "missing", "interface_coverage_unknown"
}

func usageRecords(start, end time.Time, records int64, state, reason string) (string, string) {
	if records == 0 {
		if state == "pruned" {
			return state, reason
		}
		return "missing", "interface_observations_missing"
	}
	endHour := end.UTC().Truncate(time.Hour)
	if end.After(endHour) {
		endHour = endHour.Add(time.Hour)
	}
	expected := int64(endHour.Sub(start.UTC().Truncate(time.Hour)) / time.Hour)
	if state == "complete" && records < expected {
		return "partial", "interface_hourly_observations_missing"
	}
	return state, reason
}

func (b *Builder) Trend(ctx context.Context, days int, now time.Time) (TrendResult, error) {
	result := TrendResult{Days: days, Timezone: b.location().String(), AsOf: now.UTC(), Dates: []TrendDay{}, Notes: []string{"Seven or thirty preceding civil days; missing totals are null, never zero-filled.", "Complete means full recorded interface-counter coverage, not proof of a provider bill or lossless packet capture.", "Totals use overlapping UTC-hour aggregates, including non-hour timezone boundaries. Original full snapshots take precedence; legacy short snapshots are not reconstructed as original full content."}}
	if days != 7 && days != 30 || now.IsZero() {
		return result, errors.New("trend requires 7 or 30 days and a valid time")
	}
	work, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	today := civilDate(now, b.location())
	for offset := days; offset > 0; offset-- {
		civil := today.AddDate(0, 0, -offset)
		start, okStart := resolveCivilTime(civil, b.location())
		end, okEnd := resolveCivilTime(civil.AddDate(0, 0, 1), b.location())
		if !okStart || !okEnd || end.Before(start) {
			return result, errors.New("trend calendar boundary unavailable")
		}
		day := TrendDay{Date: civil.Format(time.DateOnly), Start: start, End: end, Source: "retained_aggregates"}
		if start.Equal(end) {
			day.State, day.Reason, day.Source = "missing", "nonexistent_civil_date", "not_applicable"
			result.Dates = append(result.Dates, day)
			continue
		}
		archived, err := b.Store.Report(work, day.Date)
		if err == nil && len(archived.Document) > 0 {
			doc, err := DecodeDocument(archived.Document)
			if err != nil {
				return result, err
			}
			if doc.PeriodStart.Equal(start) && doc.PeriodEnd.Equal(end) && doc.Integrity.Retention != nil {
				day.Source = "original_full_snapshot"
				day.State, day.Reason = usageCoverage(start, end, doc.Integrity, *doc.Integrity.Retention)
				var records int64
				for _, retained := range doc.Integrity.Retention.Retained {
					if retained.Dataset == "interface_hourly" {
						records = retained.Rows
						break
					}
				}
				day.State, day.Reason = usageRecords(start, end, records, day.State, day.Reason)
				rx, tx := doc.Summary.Interface.RXBytes, doc.Summary.Interface.TXBytes
				if day.State != "missing" && records > 0 {
					day.RXBytes, day.TXBytes = &rx, &tx
				}
				result.Dates = append(result.Dates, day)
				continue
			}
		} else if err != nil && !store.IsNotFound(err) {
			return result, err
		}
		amount, records, err := b.Store.InterfaceSummaryWithRecords(work, start, end)
		if err != nil {
			return result, err
		}
		integrity, retention, err := b.Store.MonitorCoverage(work, start, end)
		if err != nil {
			return result, err
		}
		day.State, day.Reason = usageCoverage(start, end, integrity, retention)
		day.State, day.Reason = usageRecords(start, end, records, day.State, day.Reason)
		if day.State == "missing" && (amount.RXBytes > 0 || amount.TXBytes > 0) {
			day.State, day.Reason = "partial", "observations_without_complete_coverage"
		}
		if day.State != "missing" && records > 0 {
			rx, tx := amount.RXBytes, amount.TXBytes
			day.RXBytes, day.TXBytes = &rx, &tx
		}
		result.Dates = append(result.Dates, day)
	}
	return result, work.Err()
}
