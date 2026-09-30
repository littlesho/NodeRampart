// SPDX-License-Identifier: MIT

package daemon

import (
	"context"
	"time"

	"github.com/littlesho/NodeRampart/internal/collector"
	"github.com/littlesho/NodeRampart/internal/model"
	"github.com/littlesho/NodeRampart/internal/store"
)

const authHistoryObservation = 7 * 24 * time.Hour

func (a *App) addAuthHistoryHints(ctx context.Context, observation collector.AuthObservation, event *model.Event) {
	if !a.options.Config.Auth.HistoryHintsEnabled || observation.Kind != collector.AuthSuccess {
		return
	}
	if event.Evidence == nil {
		event.Evidence = make(map[string]string)
	}
	event.Evidence["history_hint_state"] = "observing"
	if a.report == nil || a.report.Location == nil || a.options.StorePrivacy == nil {
		event.Evidence["history_hint_state"] = "history_unavailable"
		return
	}
	start := observation.ObservedAt.Add(-authHistoryObservation)
	if a.started.After(start) {
		return
	}
	child, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	view, err := a.options.Store.Integrity(child, store.IntegrityQuery{Start: start, End: observation.ObservedAt, Limit: 100})
	if err != nil || !authHistoryCoverage(view, start, observation.ObservedAt) {
		event.Evidence["history_hint_state"] = "history_incomplete"
		return
	}
	entries, truncated, err := a.options.Store.SSHLoginHistory(child, start, observation.ObservedAt)
	if err != nil || truncated {
		event.Evidence["history_hint_state"] = "history_unavailable"
		return
	}
	if len(entries) < 20 {
		return
	}
	days := make(map[string]struct{})
	hours := [24]int{}
	identity, prefix := a.options.StorePrivacy.IP(observation.SourceIP.String())
	known := false
	for _, entry := range entries {
		local := entry.ObservedAt.In(a.report.Location)
		days[local.Format("2006-01-02")] = struct{}{}
		hours[local.Hour()]++
		if identity != "" {
			known = known || entry.SourceIP == identity
		} else {
			known = known || entry.SourceRange == prefix
		}
	}
	if len(days) < 3 {
		return
	}
	event.Evidence["history_hint_state"] = "available"
	event.Evidence["history_basis"] = "current_process_retained_successes_7d"
	if !known && prefix != "" {
		if identity == "" {
			event.Evidence["history_source_hint"] = "first_observed_prefix"
		} else {
			event.Evidence["history_source_hint"] = "first_observed_source"
		}
	}
	if hours[observation.ObservedAt.In(a.report.Location).Hour()] == 0 {
		event.Evidence["history_time_hint"] = "unseen_local_hour"
	}
}

func authHistoryCoverage(view store.IntegrityView, start, end time.Time) bool {
	if view.HistoryTruncated || view.GapsTruncated || view.Retention == nil || view.Retention.TrackingStarted.After(start) || view.Retention.TrackingStarted.IsZero() {
		return false
	}
	for _, gap := range view.Gaps {
		if gap.Name == "ssh_journal" || gap.Name == "auth_detection" {
			return false
		}
	}
	for _, entry := range view.Retention.Entries {
		if entry.Dataset == "events" || entry.Dataset == "coverage_intervals" || entry.Dataset == "coverage_gaps" {
			return false
		}
	}
	if view.Retention.EvictedEntries > 0 {
		for _, total := range view.Retention.Totals {
			if (total.Dataset == "events" || total.Dataset == "coverage_intervals" || total.Dataset == "coverage_gaps") && total.DataStart.Before(end) && !total.DataEnd.Before(start) {
				return false
			}
		}
	}
	journalCovered := false
	for _, component := range view.Components {
		if component.Name == "ssh_journal" {
			journalCovered = component.RunningMS >= end.Sub(start).Milliseconds() && component.DegradedMS == 0 && component.UnknownMS == 0 && component.ConflictMS == 0
		}
		if component.Name == "auth_detection" && (component.DegradedMS > 0 || component.UnknownMS > 0 || component.ConflictMS > 0) {
			return false
		}
	}
	return journalCovered
}
