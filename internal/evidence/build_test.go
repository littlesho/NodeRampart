// SPDX-License-Identifier: MIT

package evidence

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/api"
	"github.com/littlesho/NodeRampart/internal/model"
	"github.com/littlesho/NodeRampart/internal/store"
)

func rawFixture() store.EvidenceSnapshot {
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	e := store.TimelineEvent{ID: "private_event_id", IncidentID: "private_incident_id", ObservedAt: now, Kind: "ssh_brute_force", Phase: "start", Severity: model.SeverityHigh, Summary: "private_summary_token", SourceRange: "198.51.100.0/24", Count: 7, Delivery: store.DeliveryOutcome{Decision: "queued", NotificationID: "private_notification_id", State: "pending", Attempts: 2, MergedEvents: 1, SilenceID: "private_silence_id"}}
	return store.EvidenceSnapshot{Start: now.Add(-time.Hour), End: now.Add(time.Hour), AsOf: now, Timeline: store.TimelinePage{Events: []store.TimelineEvent{e}, HistoryNote: "private_history_note"}, Incident: &store.IncidentView{Incident: store.IncidentSummary{ID: e.IncidentID, Kind: e.Kind, FirstObservedAt: now, LastObservedAt: now, Events: 1, HasStart: true}, RelatedSSH: []store.TimelineEvent{e}, SourceKeys: []string{"private_context_key"}, ContextStart: now.Add(-5 * time.Minute), ContextEnd: now.Add(5 * time.Minute), Correlation: "private_correlation"}, Integrity: store.IntegrityView{Components: []store.IntegrityComponent{{Name: "private_component", RunningMS: 100}}, Segments: []store.IntegritySegment{{Name: "sensor_feed", State: "running", Start: now, End: now.Add(time.Second)}}, Gaps: []store.CoverageGap{{Name: "private_gap_name", Reason: "private_gap_reason", Start: now, End: now, Count: 1}}, Notes: []string{"private_integrity_note"}}, Retention: store.RetentionView{TrackingStarted: now, Entries: []store.RetentionEntry{{ID: 991, Dataset: "events", Reason: "time_expiry", ActionDay: now.Truncate(24 * time.Hour), Operations: 1, AffectedRows: 2, AggregateSurvives: "private_survivor"}}, Notes: []string{"private_retention_note"}}, Monitors: []store.MonitorState{{Key: "health_storage", Revision: 1, UpdatedAt: now, Data: json.RawMessage(`{"schema_version":1,"active":true,"incident_id":"private_monitor_incident","unexpected":"private_monitor_secret"}`)}}}
}

func TestProjectionExcludesFreeTextAndRekeysEveryIdentity(t *testing.T) {
	raw := rawFixture()
	b, err := Build(raw)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(b)
	for _, secret := range []string{"private_", "198.51.100.0/24", "991"} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("excluded fixture string appeared: %q", secret)
		}
	}
	if b.Events[0].Alias != b.RelatedSSH[0].Alias || b.Events[0].SourceAlias != b.RelatedSSH[0].SourceAlias || b.Events[0].IncidentAlias != b.Incident.Alias {
		t.Fatal("within-bundle references were not preserved")
	}
	if b.Events[0].Count != 7 || b.Events[0].Delivery.Attempts != 2 || b.Monitors[0].State != "incident_active" || b.Coverage.Components[0].Name != "unknown" || b.Coverage.Gaps[0].Reason != "unknown" {
		t.Fatal("allowlisted evidence was lost or unknown text retained")
	}
	next, err := Build(raw)
	if err != nil || next.Events[0].Alias == b.Events[0].Alias || next.Events[0].SourceAlias == b.Events[0].SourceAlias {
		t.Fatal("independent snapshots reused pseudonyms", err)
	}
	if raw.Timeline.Events[0].ID != "private_event_id" {
		t.Fatal("projection mutated raw input")
	}
}

func TestUnknownCategoriesAndMonitorStateDoNotLeak(t *testing.T) {
	raw := rawFixture()
	e := &raw.Timeline.Events[0]
	e.Kind, e.Phase = "private_kind", "private_phase"
	e.Severity = "private_severity"
	e.Delivery.Decision, e.Delivery.State = "private_decision", "private_state"
	raw.Monitors[0].Data = json.RawMessage(`{"schema_version":99,"period":"private_period","active":true}`)
	b, err := Build(raw)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(b)
	if strings.Contains(string(data), "private_") || b.Monitors[0].State != "unknown" || b.Events[0].Kind != "unknown" {
		t.Fatal("unknown categories or state escaped projection")
	}
	raw.Monitors[0].Key = "budget_day_bytes"
	raw.Monitors[0].Data = json.RawMessage(`{"schema_version":1,"period":"2026-09-11","milestone":100}`)
	b, err = Build(raw)
	if err != nil || b.Monitors[0].State != "milestone_recorded" {
		t.Fatal("daily milestone mislabeled as inactive", err)
	}
}

func TestKnownHealthCategoriesRemainUseful(t *testing.T) {
	for _, value := range []string{"sensor_feed", "ssh_journal", "interface_counter", "network_events", "sensor_socket", "control_socket", "notification_worker", "report_scheduler"} {
		if component(value) != value {
			t.Fatal("known component lost")
		}
	}
	for _, value := range []string{"event_queue_capacity", "process_restart_pending_unknown", "interface_selection_changed", "backfill_count_limit", "persist_failed", "start_failed", "read_failed", "untrusted_origin", "malformed_record"} {
		if gapReason(value) != value {
			t.Fatal("known loss reason lost")
		}
	}
}

func TestProjectionBoundsPreserveTruncationAndUTC(t *testing.T) {
	raw := rawFixture()
	e := raw.Timeline.Events[0]
	raw.Timeline.Events = make([]store.TimelineEvent, 101)
	for i := range raw.Timeline.Events {
		raw.Timeline.Events[i] = e
	}
	raw.Incident.RelatedSSH = raw.Timeline.Events
	raw.Integrity.Segments = make([]store.IntegritySegment, 101)
	raw.Integrity.Gaps = make([]store.CoverageGap, 101)
	raw.Integrity.LossHours = make([]store.IntegrityLoss, 101)
	raw.Retention.Entries = make([]store.RetentionEntry, 101)
	raw.Start = raw.Start.In(time.FixedZone("private_timezone", 3600))
	b, err := Build(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Events) != 100 || !b.EventsTruncated || !b.RelatedTruncated || !b.Coverage.SegmentsTruncated || !b.Coverage.GapsTruncated || !b.Coverage.LossHoursTruncated || !b.Retention.Truncated {
		t.Fatal("truncation was concealed")
	}
	if b.Start.Location() != time.UTC {
		t.Fatal("export did not normalize UTC")
	}
	raw.Integrity.Components = make([]store.IntegrityComponent, 129)
	if _, err := Build(raw); err == nil {
		t.Fatal("unbounded components accepted")
	}
}

func TestStoredSensitiveEventDoesNotEnterSharingDTO(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "fixture.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().UTC().Truncate(time.Millisecond)
	e := model.Event{ID: "synthetic_sensitive_id", IncidentID: "synthetic_sensitive_incident", ObservedAt: now, Kind: "ssh_login_success", Phase: "observed", Severity: model.SeverityInfo, Summary: "synthetic_secret_summary", SourceIP: "2001:db8::123", SourceRange: "2001:db8::/48", Target: "ssh user=synthetic_private_user", Geo: model.Geo{City: "synthetic_private_city", ASNOrg: "synthetic_private_org"}, Evidence: map[string]string{"synthetic_private_key": "synthetic_secret_evidence"}}
	if err := s.InsertEvent(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	raw, err := s.Evidence(context.Background(), api.EvidenceArgs{Start: now.Add(-time.Hour), End: now.Add(time.Hour), IncidentID: e.IncidentID})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Build(raw)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(b)
	if strings.Contains(string(data), "synthetic_") || strings.Contains(string(data), "2001:db8") || len(b.Events) != 1 {
		t.Fatal("stored private event fields reached sharing DTO")
	}
}
