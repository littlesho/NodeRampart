// SPDX-License-Identifier: MIT

package evidence

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/api"
	"github.com/littlesho/NodeRampart/internal/model"
	"github.com/littlesho/NodeRampart/internal/store"
)

func diagnosticEventFields(kind string) map[string]string {
	fields := map[string]string{
		"reason": "journal_unavailable", "condition_since_utc": "2026-10-03T07:01:25Z",
		"component_reason": "process_exited", "journal_state": "retrying", "failure_cause": "process_exited", "journal_exit_code": "0",
		"diagnostic_scope": "last_failure", "diagnostic_at_utc": "2026-10-03T07:01:25Z",
	}
	if kind == "health_geoip_update" {
		fields["reason"], fields["component_reason"], fields["diagnostic_scope"] = "update_failed", "download_failed", "current"
		fields["failure_stage"], fields["failure_cause"], fields["geoip_edition"] = "validation", "validator_privilege_drop_failed", "City"
		delete(fields, "journal_state")
		delete(fields, "journal_exit_code")
	}
	return fields
}

func TestStoredDiagnosticsSurviveStrictJSONAndHTMLExport(t *testing.T) {
	for _, tc := range []struct {
		name, kind string
		changes    map[string]string
		want       map[string]any
		html       []string
	}{
		{
			name: "journal exit zero and last failure", kind: "health_ssh_journal",
			want: map[string]any{"component_reason": "process_exited", "journal_state": "retrying", "failure_cause": "process_exited", "journal_exit_code": json.Number("0"), "diagnostic_scope": "last_failure", "diagnostic_at_utc": "2026-10-03T07:01:25Z"},
			html: []string{"Component result: process_exited", "Failure cause: process_exited", "journalctl exit code: 0", "Diagnostic scope: last_failure", "Diagnostic observed at (UTC):"},
		},
		{
			name: "MMDB privilege drop", kind: "health_geoip_update",
			want: map[string]any{"component_reason": "download_failed", "failure_stage": "validation", "failure_cause": "validator_privilege_drop_failed", "geoip_edition": "City", "diagnostic_scope": "current", "diagnostic_at_utc": "2026-10-03T07:01:25Z"},
			html: []string{"Failure stage: validation", "Failure cause: validator_privilege_drop_failed", "GeoIP database: City"},
		},
		{
			name: "HTTP status", kind: "health_geoip_update",
			changes: map[string]string{"failure_stage": "download", "failure_cause": "http_status", "http_status": "403", "geoip_edition": "ASN"},
			want:    map[string]any{"component_reason": "download_failed", "failure_stage": "download", "failure_cause": "http_status", "geoip_edition": "ASN", "http_status": json.Number("403"), "diagnostic_scope": "current", "diagnostic_at_utc": "2026-10-03T07:01:25Z"},
			html:    []string{"Failure stage: download", "Failure cause: http_status", "HTTP status: 403", "GeoIP database: ASN"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := store.Open(filepath.Join(t.TempDir(), "fixture.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			now := time.Date(2026, 10, 3, 7, 4, 23, 0, time.UTC)
			fields := diagnosticEventFields(tc.kind)
			for key, value := range tc.changes {
				fields[key] = value
			}
			fields["failure_detail"] = "https://synthetic.invalid/?key=synthetic_private_token"
			fields["raw_error"], fields["path"] = "synthetic_private_error", "/synthetic_private_path"
			e := model.Event{ID: "synthetic_private_event", IncidentID: "synthetic_private_incident", Kind: tc.kind, ObservedAt: now, Phase: "update", Severity: model.SeverityMedium, Summary: "synthetic_private_summary", Evidence: fields}
			if err := s.InsertEvent(context.Background(), e); err != nil {
				t.Fatal(err)
			}
			raw, err := s.Evidence(context.Background(), api.EvidenceArgs{Start: now.Add(-time.Hour), End: now.Add(time.Hour), IncidentID: e.IncidentID})
			if err != nil {
				t.Fatal(err)
			}
			// A later monitor disagrees and contains an arbitrary private field.
			// Export must retain the event's saved diagnostic, never fill it from
			// this current-state JSON or copy that JSON into the public bundle.
			raw.Monitors = []store.MonitorState{{Key: tc.kind, Revision: 2, UpdatedAt: now.Add(time.Minute), Data: json.RawMessage(`{"schema_version":2,"active":false,"diagnostic":{"failure_cause":"synthetic_private_later_cause"},"status":{"reason":"healthy"}}`)}}
			b, err := Build(raw)
			if err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(b)
			if err != nil {
				t.Fatal(err)
			}
			assertDiagnosticWire(t, data, tc.want)
			if b.Events[0].Alert.SchemaVersion != 2 || b.FormatVersion != 1 {
				t.Fatal("nested diagnostic was not explicitly versioned")
			}
			// Build and alias renewal must independently clone the nested
			// diagnostic and each scalar pointer, including recorded zero.
			d := raw.Timeline.Events[0].Alert.Diagnostic
			d.ComponentReason = "synthetic_private_mutation"
			*d.DiagnosticAt = d.DiagnosticAt.Add(time.Hour)
			if d.HTTPStatus != nil {
				*d.HTTPStatus = 999
			}
			if d.JournalExitCode != nil {
				*d.JournalExitCode = 999
			}
			after, err := json.Marshal(b)
			if err != nil || !bytes.Equal(data, after) || b.Validate() != nil {
				t.Fatal("public diagnostic shares mutable raw input")
			}
			renewed, err := renewAliases(b)
			if err != nil {
				t.Fatal(err)
			}
			d = renewed.Events[0].Alert.Diagnostic
			d.ComponentReason = "synthetic_private_renewed_mutation"
			*d.DiagnosticAt = d.DiagnosticAt.Add(time.Hour)
			if d.HTTPStatus != nil {
				*d.HTTPStatus = 999
			}
			if d.JournalExitCode != nil {
				*d.JournalExitCode = 999
			}
			after, err = json.Marshal(b)
			if err != nil || !bytes.Equal(data, after) || b.Validate() != nil {
				t.Fatal("renewed diagnostic shares mutable caller input")
			}
			for _, format := range []string{"json", "html"} {
				path := filepath.Join(t.TempDir(), "evidence."+format)
				if err := Export(context.Background(), b, path, format); err != nil {
					t.Fatal(err)
				}
				out, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(out), "synthetic_private") || strings.Contains(string(out), "failure_detail") || strings.Contains(string(out), "https://synthetic.invalid") {
					t.Fatal("unprojected diagnostic text entered artifact")
				}
				if format == "json" {
					assertDiagnosticWire(t, out, tc.want)
				} else {
					for _, expected := range tc.html {
						if !strings.Contains(string(out), expected) {
							t.Fatalf("HTML lost recorded diagnostic field %q", expected)
						}
					}
				}
			}
		})
	}
}

// Inspect the exact public JSON, rather than decoding through a DTO which
// could silently discard an unexpected private field added by a regression.
func assertDiagnosticWire(t *testing.T, data []byte, want map[string]any) {
	t.Helper()
	if strings.Contains(string(data), "synthetic_private") || strings.Contains(string(data), "failure_detail") || strings.Contains(string(data), "raw_error") {
		t.Fatal("private diagnostic data reached wire JSON")
	}
	var document map[string]any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&document); err != nil {
		t.Fatal(err)
	}
	events, ok := document["events"].([]any)
	if !ok || len(events) != 1 {
		t.Fatal("unexpected event count")
	}
	alert := events[0].(map[string]any)["alert"].(map[string]any)
	if !reflect.DeepEqual(alert["diagnostic"], want) {
		t.Fatalf("recorded diagnostic wire differs from explicit allowlist: got %v, want %v", alert["diagnostic"], want)
	}
}

func TestDiagnosticExportRejectsMutatedPublicDTOBeforeCreatingArtifact(t *testing.T) {
	for _, kind := range []string{"health_ssh_journal", "health_geoip_update"} {
		for name, mutate := range map[string]func(*model.AlertContext){
			"legacy version": func(a *model.AlertContext) { a.SchemaVersion = 1 },
			"URL cause": func(a *model.AlertContext) {
				a.Diagnostic.FailureCause = "https://synthetic.invalid/?key=synthetic_private_token"
			},
			"raw component": func(a *model.AlertContext) { a.Diagnostic.ComponentReason = "synthetic_private_error" },
			"wrong stage":   func(a *model.AlertContext) { a.Diagnostic.FailureStage = "synthetic_private_stage" },
			"exit bound":    func(a *model.AlertContext) { n := 256; a.Diagnostic.JournalExitCode = &n },
			"HTTP bound":    func(a *model.AlertContext) { n := 600; a.Diagnostic.HTTPStatus = &n },
			"time zone": func(a *model.AlertContext) {
				*a.Diagnostic.DiagnosticAt = a.Diagnostic.DiagnosticAt.In(time.FixedZone("synthetic_private_zone", 0))
			},
		} {
			t.Run(kind+"/"+name, func(t *testing.T) {
				raw := rawFixture()
				raw.Incident = nil
				raw.Timeline.Events[0].Kind = kind
				raw.Timeline.Events[0].Alert = model.ProjectAlertContext(kind, diagnosticEventFields(kind))
				b, err := Build(raw)
				if err != nil {
					t.Fatal(err)
				}
				mutate(b.Events[0].Alert)
				path := filepath.Join(t.TempDir(), "rejected.json")
				if b.Validate() == nil || Export(context.Background(), b, path, "json") == nil {
					t.Fatal("mutated public diagnostic was accepted")
				}
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatal("rejected diagnostic created an artifact")
				}
			})
		}
	}
}

func TestLegacyDiagnosticExportDoesNotInventSpecificFailure(t *testing.T) {
	raw := rawFixture()
	raw.Incident = nil
	e := &raw.Timeline.Events[0]
	e.Kind = "health_geoip_update"
	e.Alert = model.ProjectAlertContext(e.Kind, map[string]string{"reason": "update_failed", "condition_since_utc": "2026-09-23T00:27:15Z", "component_reason": "download_failed"})
	b, err := Build(raw)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(b)
	page, err := renderHTML(context.Background(), b, data)
	if err != nil || !strings.Contains(string(page), "Specific failure cause was not recorded.") || strings.Contains(string(page), "Failure cause:") || strings.Contains(string(page), "Failure stage:") {
		t.Fatal("legacy update observation invented a detailed diagnosis", err)
	}
}
