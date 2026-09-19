// SPDX-License-Identifier: MIT

package evidence

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/littlesho/NodeRampart/internal/api"
	"github.com/littlesho/NodeRampart/internal/store"
)

const coverageNotice = "Running heartbeats do not guarantee complete capture or persistence. Unrecorded and expired intervals are unknown. Loss counters use overlapping UTC hours; gap counts describe original intervals, not prorated losses."
const retentionNotice = "Lifetime totals are separate from the requested period. Tracking does not reconstruct prior pruning. Affected rows and min/max time spans do not measure lost packets or uninterrupted loss; compaction may preserve aggregates. Remaining row bounds do not prove continuous coverage."

// Build is the sole raw-data projection. The random HMAC key is never retained
// in the bundle; aliases are stable only within this particular export.
func Build(raw store.EvidenceSnapshot) (Bundle, error) {
	if !api.ValidTimeRange(raw.Start, raw.End, 8) {
		return Bundle{}, errors.New("invalid evidence period")
	}
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		return Bundle{}, errors.New("evidence pseudonyms unavailable")
	}
	alias := func(kind, value string) string {
		if value == "" {
			return ""
		}
		h := hmac.New(sha256.New, key[:])
		h.Write([]byte(kind))
		h.Write([]byte{0})
		h.Write([]byte(value))
		return kind + "_" + hex.EncodeToString(h.Sum(nil)[:16])
	}
	b := Bundle{FormatVersion: FormatVersion, Kind: "diagnostic", Start: raw.Start.UTC(), End: raw.End.UTC(), SnapshotAt: raw.AsOf.UTC(), Privacy: PrivacyNotice, Consistency: SnapshotNotice, Events: []Event{}, RelatedSSH: []Event{}, Monitors: []Monitor{}}
	b.Producer = producer()
	b.RuleFingerprintScope = RuleFingerprintNotice
	projectEvent := func(e store.TimelineEvent) Event {
		return Event{Alert: projectAlert(e.Kind, e.Alert), Alias: alias("event", e.ID), IncidentAlias: alias("incident", e.IncidentID), SourceAlias: alias("source", e.SourceRange), ObservedAt: e.ObservedAt.UTC(), Kind: kind(e.Kind), Phase: category(e.Phase, "observed", "start", "update", "recovery"), Severity: category(string(e.Severity), "info", "low", "medium", "high", "critical"), Count: e.Count, Delivery: Delivery{Decision: category(e.Delivery.Decision, "legacy", "queued", "merged", "silenced", "ineligible", "rejected"), NotificationAlias: alias("notification", e.Delivery.NotificationID), State: category(e.Delivery.State, "sent", "silenced", "ineligible", "rejected", "history_unavailable", "expired", "quarantined", "sending", "pending"), Attempts: e.Delivery.Attempts, MergedEvents: e.Delivery.MergedEvents, SilenceAlias: alias("silence", e.Delivery.SilenceID), SentAt: e.Delivery.SentAt.UTC()}}
	}
	for _, e := range raw.Timeline.Events[:min(len(raw.Timeline.Events), MaxRecords)] {
		b.Events = append(b.Events, projectEvent(e))
	}
	b.EventsTruncated = raw.Timeline.More || len(raw.Timeline.Events) > MaxRecords
	if v := raw.Incident; v != nil {
		b.Kind = "incident"
		b.Incident = &Incident{Alias: alias("incident", v.Incident.ID), Kind: kind(v.Incident.Kind), FirstObservedAt: v.Incident.FirstObservedAt.UTC(), LastObservedAt: v.Incident.LastObservedAt.UTC(), RetainedEvents: v.Incident.Events, HasStart: v.Incident.HasStart, HasRecovery: v.Incident.HasRecovery, ContextStart: v.ContextStart.UTC(), ContextEnd: v.ContextEnd.UTC(), SourcesTruncated: v.SourceKeysTruncated, Correlation: CorrelationNotice}
		for _, e := range v.RelatedSSH[:min(len(v.RelatedSSH), MaxRecords)] {
			b.RelatedSSH = append(b.RelatedSSH, projectEvent(e))
		}
		b.RelatedTruncated = v.RelatedTruncated || len(v.RelatedSSH) > MaxRecords
	}
	v := raw.Integrity
	b.Coverage = Coverage{Components: []Component{}, Segments: []Segment{}, Gaps: []Gap{}, LossHours: []Loss{}, SegmentsTruncated: v.More || len(v.Segments) > MaxRecords, HistoryTruncated: v.HistoryTruncated, GapsTruncated: v.GapsTruncated || len(v.Gaps) > MaxRecords, LossHoursTruncated: v.LossHoursTruncated || len(v.LossHours) > MaxRecords, Qualification: coverageNotice}
	if len(v.Components) > 128 || len(raw.Monitors) > 32 {
		return Bundle{}, errors.New("evidence section exceeds row bounds")
	}
	for _, c := range v.Components {
		b.Coverage.Components = append(b.Coverage.Components, Component{Name: component(c.Name), RunningMS: c.RunningMS, DegradedMS: c.DegradedMS, DisabledMS: c.DisabledMS, UnknownMS: c.UnknownMS, ConflictMS: c.ConflictMS})
	}
	for _, s := range v.Segments[:min(len(v.Segments), MaxRecords)] {
		b.Coverage.Segments = append(b.Coverage.Segments, Segment{Name: component(s.Name), State: category(s.State, "running", "degraded", "disabled", "conflicting"), Start: s.Start.UTC(), End: s.End.UTC(), ConflictingEvidence: s.ConflictingEvidence})
	}
	for _, g := range v.Gaps[:min(len(v.Gaps), MaxRecords)] {
		b.Coverage.Gaps = append(b.Coverage.Gaps, Gap{Name: component(g.Name), Reason: gapReason(g.Reason), Start: g.Start.UTC(), End: g.End.UTC(), Count: g.Count})
	}
	for _, l := range v.LossHours[:min(len(v.LossHours), MaxRecords)] {
		b.Coverage.LossHours = append(b.Coverage.LossHours, Loss{Hour: l.Hour.UTC(), OverflowPackets: l.OverflowPackets, OverflowBytes: l.OverflowBytes, ParseErrors: l.ParseErrors, KernelDrops: l.KernelDrops, KernelStatsErrors: l.KernelStatsErrors, IPCDroppedBatches: l.IPCDroppedBatches, IPCDroppedPackets: l.IPCDroppedPackets, IPCDroppedBytes: l.IPCDroppedBytes, Saturations: l.Saturations})
	}
	r := raw.Retention
	b.Retention = Retention{TrackingStarted: r.TrackingStarted.UTC(), Entries: []RetentionEntry{}, Totals: []RetentionTotal{}, Retained: []RetainedDataset{}, Truncated: r.More || len(r.Entries) > MaxRecords, EvictedEntries: r.EvictedEntries, DetailLimit: r.DetailLimit, Qualification: retentionNotice}
	if len(r.Totals) > 55 || len(r.Retained) > 11 {
		return Bundle{}, errors.New("evidence retention exceeds row bounds")
	}
	for _, e := range r.Entries[:min(len(r.Entries), MaxRecords)] {
		b.Retention.Entries = append(b.Retention.Entries, RetentionEntry{ActionDay: e.ActionDay.UTC(), RetentionTotal: projectRetention(e.Dataset, e.Reason, e.AggregateSurvives, e.Operations, e.AffectedRows, e.FirstAction, e.LastAction, e.DataStart, e.DataEnd)})
	}
	for _, e := range r.Totals {
		b.Retention.Totals = append(b.Retention.Totals, projectRetention(e.Dataset, e.Reason, e.AggregateSurvives, e.Operations, e.AffectedRows, e.FirstAction, e.LastAction, e.DataStart, e.DataEnd))
	}
	for _, e := range r.Retained {
		b.Retention.Retained = append(b.Retention.Retained, RetainedDataset{Dataset: dataset(e.Dataset), Rows: e.Rows, Start: e.Start.UTC(), End: e.End.UTC()})
	}
	for _, m := range raw.Monitors {
		b.Monitors = append(b.Monitors, projectMonitor(m, alias))
	}
	if err := b.Validate(); err != nil {
		return Bundle{}, err
	}
	return b, nil
}

func projectRetention(ds, reason, aggregate string, ops, rows int64, first, last, start, end time.Time) RetentionTotal {
	return RetentionTotal{Dataset: dataset(ds), Reason: retentionReason(reason), AggregateSurvives: category(aggregate, "totals_preserved", "event_decisions_have_separate_retention", "notification_outcome_preserved", "none_guaranteed"), Operations: ops, AffectedRows: rows, FirstAction: first.UTC(), LastAction: last.UTC(), DataStart: start.UTC(), DataEnd: end.UTC()}
}

func category(value string, allowed ...string) string {
	for _, candidate := range allowed {
		if value == candidate {
			return value
		}
	}
	return "unknown"
}

func kind(v string) string {
	return category(v, "syn_flood", "udp_flood", "icmp_flood", "bandwidth_spike", "port_scan", "ssh_login_success", "ssh_brute_force", "budget_month_bytes", "budget_month_cost", "budget_day_bytes", "budget_day_growth", "health_sensor", "health_interface_counter", "health_ssh_journal", "health_storage", "health_geoip_update")
}
func component(v string) string {
	return category(v, "sensor_feed", "ssh_journal", "interface_counter", "interface_discovery", "network_events", "storage", "geoip_update", "sensor_socket", "control_socket", "notification_worker", "report_scheduler")
}
func gapReason(v string) string {
	return category(v, "event_queue_capacity", "event_metadata_rejected", "process_restart_pending_unknown", "shutdown_unpersisted_events", "interface_selection_changed", "invalid_cursor", "backfill_time_limit", "backfill_record_limit", "process_exited", "process_start_failed", "malformed_record", "cursor_unavailable", "record_too_large", "unsupported_record", "invalid_source", "invalid_timestamp", "missing_cursor", "journal_read_failed", "consumer_failed", "persist_failed", "start_failed", "read_failed", "backfill_count_limit", "untrusted_origin", "unrecognized_message")
}
func dataset(v string) string {
	if store.ValidRetentionDataset(v) {
		return v
	}
	return "unknown"
}
func retentionReason(v string) string {
	if store.ValidRetentionReason(v) {
		return v
	}
	return "unknown"
}

func projectMonitor(m store.MonitorState, alias func(string, string) string) Monitor {
	r := Monitor{Key: category(m.Key, "budget_month_bytes", "budget_month_cost", "budget_day_bytes", "budget_day_growth", "health_sensor", "health_interface_counter", "health_ssh_journal", "health_storage", "health_geoip_update"), Revision: m.Revision, UpdatedAt: m.UpdatedAt.UTC(), State: "unknown"}
	// Deliberately decode only independent scalar fields, never replay opaque
	// monitor JSON or identifiers. Invalid/future state remains unknown.
	var v struct {
		SchemaVersion    int       `json:"schema_version"`
		IncidentID       string    `json:"incident_id"`
		Period           string    `json:"period"`
		Milestone        int       `json:"milestone"`
		Active           bool      `json:"active"`
		ObservedAt       time.Time `json:"observed_at_utc"`
		ConditionSince   time.Time `json:"condition_since_utc"`
		RecoverySince    time.Time `json:"recovery_since_utc"`
		LastNotification time.Time `json:"last_notification_utc"`
	}
	if len(m.Data) > 16<<10 || json.Unmarshal(m.Data, &v) != nil || v.SchemaVersion != 1 && v.SchemaVersion != 2 || !validPeriod(v.Period) || v.Milestone != 0 && v.Milestone != 80 && v.Milestone != 100 {
		return r
	}
	r.Period, r.Milestone, r.Active = v.Period, v.Milestone, v.Active
	if v.IncidentID != "" {
		r.IncidentAlias = alias("incident", v.IncidentID)
	}
	r.ObservedAt, r.ConditionSince, r.RecoverySince, r.LastNotification = v.ObservedAt.UTC(), v.ConditionSince.UTC(), v.RecoverySince.UTC(), v.LastNotification.UTC()
	if r.Key == "unknown" {
		return r
	}
	if len(r.Key) > 7 && r.Key[:7] == "budget_" {
		r.State = "no_milestone_recorded"
		if v.Milestone > 0 {
			r.State = "milestone_recorded"
		}
	} else {
		r.State = "no_active_incident"
		if v.Active {
			r.State = "incident_active"
		}
	}
	return r
}

func validPeriod(v string) bool {
	if v == "" {
		return true
	}
	for _, layout := range []string{"2006-01", "2006-01-02"} {
		if t, err := time.Parse(layout, v); err == nil && t.Year() >= 1970 && t.Format(layout) == v {
			return true
		}
	}
	return false
}
