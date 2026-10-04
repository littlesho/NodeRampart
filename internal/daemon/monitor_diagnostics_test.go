// SPDX-License-Identifier: MIT

package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/assets"
	"github.com/littlesho/NodeRampart/internal/collector"
	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/model"
)

func diagnosticJournalObservation(now time.Time, cause string) monitorObservation {
	status := collector.JournalStatus{State: "degraded", Reason: "untrusted_origin", Since: now.Add(-time.Hour), At: now, Cause: cause, DiagnosticScope: "current", DiagnosticAt: now}
	return monitorObservation{Now: now, Since: status.Since, Condition: "failed", Status: MonitorRuleStatus{Key: "health_ssh_journal", State: "healthy", Reason: "journal_unavailable", Enabled: true, Available: true}, Diagnostic: monitorDiagnostic{journal: &status}}
}

func TestMonitorJournalFailureDetailsFollowTheObservedFailure(t *testing.T) {
	a := eventTestApp(t)
	a.options.Config.Alerts.Health.Enabled = true
	now := time.Now().UTC().Truncate(time.Microsecond)
	a.started = now.Add(-2 * time.Hour)
	a.journalStatus = collector.JournalStatus{State: "retrying", Reason: "start_failed", At: now, Since: now.Add(-time.Hour), Cause: "permission_denied", Detail: "SYNTHETIC_SECRET", DiagnosticScope: "current", DiagnosticAt: now}
	values := make([]monitorObservation, 5)
	a.observeHealth(context.Background(), now, values, nil)
	in := values[2]
	in.Now, in.Status.Key, in.Status.Enabled = now, "health_ssh_journal", true
	next, events := evaluateMonitor(monitorData{}, in, a.options.Config.Alerts, a.started)
	if len(events) != 1 || events[0].Phase != "start" {
		t.Fatalf("underlying failure did not produce its debounced event: %+v", events)
	}
	evidence := events[0].Evidence
	for key, want := range map[string]string{"reason": "journal_unavailable", "component_reason": "start_failed", "failure_cause": "permission_denied", "journal_state": "retrying", "diagnostic_scope": "current", "diagnostic_at_utc": now.Format(time.RFC3339Nano)} {
		if evidence[key] != want {
			t.Fatalf("%s=%q, want %q", key, evidence[key], want)
		}
	}
	if !next.ConditionSince.Equal(a.journalStatus.Since) {
		t.Fatal("diagnostics changed the existing condition start")
	}
	encoded, err := json.Marshal(events[0])
	if err != nil || bytes.Contains(encoded, []byte("SYNTHETIC_SECRET")) {
		t.Fatal("free-form diagnostic text reached an event")
	}
	// Optional event details must not make existing persisted monitor states
	// unreadable or create a new incident merely by upgrading the daemon.
	raw, err := json.Marshal(next)
	if err != nil || bytes.Contains(raw, []byte("diagnostic")) || bytes.Contains(raw, []byte("permission_denied")) {
		t.Fatal("event diagnostics leaked into the monitor-state format")
	}
	if _, err := decodeMonitorData(raw, in.Status.Key); err != nil {
		t.Fatalf("diagnostic event broke the existing monitor-state contract: %v", err)
	}
}

func TestMonitorGeoFailureIncludesActualStageAndLegacyClassification(t *testing.T) {
	a := eventTestApp(t)
	a.options.Config.Alerts.Health.Enabled = true
	now := time.Now().UTC().Truncate(time.Microsecond)
	for _, diagnostic := range []*assets.GeoDiagnostic{
		nil,
		{Stage: "validation", Reason: "validator_privilege_drop_failed", Edition: "City"},
		{Stage: "download", Reason: "http_status", Edition: "ASN", HTTPStatus: 403},
	} {
		health := assets.Health{SchemaVersion: 1, Result: "download_failed", Scheduled: true, CheckedAt: now.Add(-time.Minute), LastSuccessAt: now.Add(-48 * time.Hour), ConsecutiveFailures: 13, Diagnostic: diagnostic}
		in := a.observeGeoHealth(now, health, nil, true)
		in.Since = now.Add(-time.Hour)
		_, events := evaluateMonitor(monitorData{}, in, a.options.Config.Alerts, now.Add(-2*time.Hour))
		if len(events) != 1 || events[0].Evidence["reason"] != "update_failed" || events[0].Evidence["component_reason"] != "download_failed" {
			t.Fatalf("update classification changed or disappeared: %+v", events)
		}
		evidence := events[0].Evidence
		if diagnostic == nil {
			if evidence["failure_cause"] != "" || evidence["failure_stage"] != "" {
				t.Fatal("legacy metadata acquired an invented specific cause")
			}
		} else if evidence["failure_cause"] != diagnostic.Reason || evidence["failure_stage"] != diagnostic.Stage || evidence["geoip_edition"] != diagnostic.Edition {
			t.Fatalf("GeoIP stage-specific diagnostic was lost: %+v", evidence)
		}
		if diagnostic != nil && diagnostic.HTTPStatus == 403 && evidence["http_status"] != "403" {
			t.Fatal("HTTP failure status was discarded")
		}
	}
}

func TestMonitorDiagnosticRemainsFrozenAcrossFailedCommitAndRecovery(t *testing.T) {
	a := eventTestApp(t)
	a.options.Config.Alerts.Health.Enabled = true
	a.options.Config.Alerts.Health.RecoveryPeriod.Duration = time.Minute
	now := time.Now().UTC()
	a.started = now.Add(-2 * time.Hour)
	m := newMonitorRuntime(a)
	ctx := context.Background()
	eventTestBudget(t, a, true)
	first := diagnosticJournalObservation(now, "untrusted_origin")
	m.pass(ctx, now, []monitorObservation{first})
	pending := m.pending[first.Status.Key]
	if pending == nil || len(pending.events) != 1 {
		t.Fatal("fixture did not retain the blocked start event")
	}
	id := pending.events[0].ID
	// A later symptom cannot rewrite the diagnostic attached to the first
	// observed event while its exact transaction is waiting for storage.
	changed := diagnosticJournalObservation(now.Add(time.Minute), "permission_denied")
	m.pass(ctx, changed.Now, []monitorObservation{changed})
	if m.pending[first.Status.Key].events[0].Evidence["failure_cause"] != "untrusted_origin" {
		t.Fatal("a newer observation replaced the frozen event's evidence")
	}
	eventTestBudget(t, a, false)
	healthy := changed
	healthy.Condition = "healthy"
	healthy.Status.Reason = "healthy"
	healthy.Diagnostic = monitorDiagnostic{}
	for _, offset := range []time.Duration{2 * time.Minute, 3 * time.Minute} {
		healthy.Now = now.Add(offset)
		m.pass(ctx, healthy.Now, []monitorObservation{healthy})
	}
	events := monitorEvents(t, a, now)
	if len(events) != 2 || events[1].ID != id || events[1].Evidence["failure_cause"] != "untrusted_origin" || events[0].Phase != "recovery" || events[0].IncidentID != events[1].IncidentID || m.pending[first.Status.Key] != nil {
		t.Fatalf("lost or relabeled the blocked failure/recovery transaction: %+v", events)
	}
	if events[0].Evidence["failure_cause"] != "" || events[0].Evidence["component_reason"] != "" {
		t.Fatal("recovery borrowed stale failure evidence")
	}
}

func TestMonitorDiagnosticsRejectUntrustedDataAndWrongComponent(t *testing.T) {
	now := time.Now().UTC()
	bad := collector.JournalStatus{State: "degraded", Reason: "SYNTHETIC_SECRET", Cause: "SYNTHETIC_SECRET", Detail: "SYNTHETIC_SECRET", DiagnosticScope: "current", Signal: "SYNTHETIC_SECRET", DiagnosticAt: now}
	geo := assets.Health{SchemaVersion: 1, Result: "download_failed", ConsecutiveFailures: 2, Diagnostic: &assets.GeoDiagnostic{Stage: "download", Reason: "SYNTHETIC_SECRET"}}
	for _, kind := range []string{"health_ssh_journal", "health_geoip_update", "health_storage"} {
		event := model.Event{Kind: kind, Phase: "start", ObservedAt: now, Evidence: map[string]string{}}
		(monitorDiagnostic{journal: &bad, geo: &geo}).addEvidence(&event)
		raw, err := json.Marshal(event)
		if err != nil || bytes.Contains(raw, []byte("SYNTHETIC_SECRET")) {
			t.Fatal("arbitrary diagnostic escaped its projection")
		}
		if kind == "health_storage" && len(event.Evidence) != 0 {
			t.Fatal("another component acquired SSH or GeoIP evidence")
		}
	}
	// A valid signal remains a signal; no source claims it was sent by OOM.
	bad.Reason, bad.Cause, bad.Signal = "process_exited", "process_signaled", "SIGKILL"
	event := model.Event{Kind: "health_ssh_journal", Phase: "start", ObservedAt: now, Evidence: map[string]string{}}
	(monitorDiagnostic{journal: &bad}).addEvidence(&event)
	if event.Evidence["failure_cause"] != "process_signaled" || event.Evidence["journal_signal"] != "SIGKILL" {
		t.Fatal("observed signal was discarded or reinterpreted")
	}
}

func TestJournalDiagnosticLogsChangesWithoutRawTextOrRepeatedRecords(t *testing.T) {
	var output bytes.Buffer
	a := &App{options: Options{Logger: slog.New(slog.NewJSONHandler(&output, nil))}}
	now := time.Now().UTC()
	status := collector.JournalStatus{State: "degraded", Reason: "untrusted_origin", Cause: "untrusted_origin", Detail: "SYNTHETIC_SECRET", DiagnosticScope: "current", DiagnosticAt: now}
	a.logJournalTransition(collector.JournalStatus{}, status)
	later := status
	later.At, later.DiagnosticAt, later.Count = now.Add(time.Second), now.Add(time.Second), 3
	a.logJournalTransition(status, later)
	if strings.Count(strings.TrimSpace(output.String()), "\n") != 0 || strings.Contains(output.String(), "SYNTHETIC_SECRET") {
		t.Fatal("duplicate records flooded the log or raw detail escaped")
	}
	if !strings.Contains(output.String(), `"cause":"untrusted_origin"`) || !strings.Contains(output.String(), `"reason":"untrusted_origin"`) {
		t.Fatal("the log lost the source-quality failure category")
	}
	status.State, status.Reason, status.Cause = "running", "record_persisted", ""
	a.logJournalTransition(later, status)
	if strings.Count(strings.TrimSpace(output.String()), "\n") != 1 || !strings.Contains(output.String(), "SSH journal reader state changed") {
		t.Fatal("the next actual state change was not logged")
	}
}

func TestDiagnosticDetailDoesNotChangeHealthReminderCadence(t *testing.T) {
	cfg := config.Defaults().Alerts
	cfg.Health.Enabled = true
	cfg.Health.ReminderInterval.Duration = time.Hour
	now := time.Now().UTC()
	state, initial := evaluateMonitor(monitorData{}, diagnosticJournalObservation(now, "untrusted_origin"), cfg, now.Add(-2*time.Hour))
	if len(initial) != 1 {
		t.Fatal("missing initial event")
	}
	changed := diagnosticJournalObservation(now.Add(time.Minute), "permission_denied")
	state, events := evaluateMonitor(state, changed, cfg, now.Add(-2*time.Hour))
	if len(events) != 0 || state.IncidentID != initial[0].IncidentID {
		t.Fatal("a detail change bypassed cadence or forked the incident")
	}
	changed.Now = now.Add(time.Hour)
	_, events = evaluateMonitor(state, changed, cfg, now.Add(-2*time.Hour))
	if len(events) != 1 || events[0].Phase != "update" || events[0].Evidence["failure_cause"] != "permission_denied" || events[0].IncidentID != initial[0].IncidentID {
		t.Fatal("the scheduled reminder did not contain the latest observed cause")
	}
}

func TestStoppedJournalWorkerDoesNotReusePriorFailureCause(t *testing.T) {
	now := time.Now().UTC()
	a := &App{journalStatus: collector.JournalStatus{State: "running", Reason: "process_started", Cause: "permission_denied", DiagnosticScope: "last_failure", DiagnosticAt: now.Add(-time.Minute)}}
	a.monitorWorkerStopped("ssh_journal", now)
	if a.journalStatus.Reason != "worker_stopped" || a.journalStatus.State != "degraded" || a.journalStatus.SafeDiagnostic().Cause != "" {
		t.Fatal("an old subprocess diagnostic was attributed to a new worker exit")
	}
}
