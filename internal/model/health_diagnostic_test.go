// SPDX-License-Identifier: MIT

package model

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func diagnosticFields(kind string) map[string]string {
	fields := map[string]string{
		"reason": "journal_unavailable", "condition_since_utc": "2026-10-03T07:01:25Z",
		"component_reason": "untrusted_origin", "journal_state": "degraded", "failure_cause": "untrusted_unit",
		"diagnostic_scope": "current", "diagnostic_at_utc": "2026-10-03T07:01:25Z",
	}
	if kind == "health_geoip_update" {
		fields["reason"], fields["component_reason"] = "update_failed", "download_failed"
		fields["failure_stage"], fields["failure_cause"], fields["geoip_edition"] = "validation", "validator_privilege_drop_failed", "City"
		delete(fields, "journal_state")
	}
	return fields
}

func TestDiagnosticProjectionPreservesLegacyWireAndVersionsNewContext(t *testing.T) {
	legacy := ProjectAlertContext("health_ssh_journal", map[string]string{"reason": "journal_unavailable", "condition_since_utc": "2026-10-03T07:01:25Z"})
	data, err := json.Marshal(legacy)
	want := `{"schema_version":1,"availability":"recorded","metric":"health_ssh_journal","reason":"journal_unavailable","condition_since_utc":"2026-10-03T07:01:25Z"}`
	if err != nil || string(data) != want || legacy.Validate(legacy.Metric) != nil {
		t.Fatalf("legacy context wire changed: %s, %v", data, err)
	}
	for _, kind := range []string{"health_ssh_journal", "health_geoip_update"} {
		got := ProjectAlertContext(kind, diagnosticFields(kind))
		if got.SchemaVersion != 2 || got.Availability != "recorded" || got.Diagnostic == nil || got.Validate(kind) != nil {
			t.Fatal("new recorded diagnostic was lost or unversioned", kind)
		}
		got.SchemaVersion = 1
		if got.Validate(kind) == nil {
			t.Fatal("legacy schema accepted new diagnostic")
		}
		got.SchemaVersion, got.Diagnostic = 2, nil
		if got.Validate(kind) == nil {
			t.Fatal("new schema accepted an absent diagnostic")
		}
	}
	for _, kind := range []string{"health_sensor", "health_storage", "budget_month_bytes"} {
		got := ProjectAlertContext(kind, diagnosticFields("health_ssh_journal"))
		if got.SchemaVersion != 1 || got.Diagnostic != nil || got.Validate(kind) != nil {
			t.Fatal("foreign event acquired SSH diagnostics", kind)
		}
	}
}

func TestDiagnosticProjectionPreservesLegacyComponentWithoutInventingFailure(t *testing.T) {
	for _, kind := range []string{"health_ssh_journal", "health_geoip_update"} {
		fields := diagnosticFields(kind)
		for _, key := range []string{"failure_cause", "failure_stage", "geoip_edition", "diagnostic_scope"} {
			delete(fields, key)
		}
		got := ProjectAlertContext(kind, fields)
		if got.Diagnostic == nil || got.Diagnostic.ComponentReason != fields["component_reason"] || got.Diagnostic.FailureCause != "" || got.Diagnostic.DiagnosticScope != "" || got.Diagnostic.DiagnosticAt == nil || got.Validate(kind) != nil {
			t.Fatal("legacy component observation lost or supplied a fabricated cause", kind)
		}
	}
}

func TestDiagnosticProjectionRejectsUntrustedScalarText(t *testing.T) {
	for _, kind := range []string{"health_ssh_journal", "health_geoip_update"} {
		for _, key := range []string{"component_reason", "failure_stage", "failure_cause", "geoip_edition", "http_status", "journal_state", "journal_exit_code", "journal_signal", "diagnostic_at_utc", "diagnostic_scope"} {
			t.Run(kind+"/"+key, func(t *testing.T) {
				for _, untrusted := range []string{"synthetic_private_token", "https://synthetic.invalid/?key=synthetic_private_token", "<script>synthetic_private_token</script>", "permission_denied\nsynthetic_private_token", strings.Repeat("synthetic_private_token", 4000)} {
					fields := diagnosticFields(kind)
					fields[key] = untrusted
					fields["failure_detail"], fields["url"], fields["token"] = untrusted, untrusted, untrusted
					got := ProjectAlertContext(kind, fields)
					data, err := json.Marshal(got)
					if err != nil || got.Validate(kind) != nil || got.Availability != "partial" || len(data) > 4096 || strings.Contains(string(data), "synthetic_private") || strings.Contains(string(data), "failure_detail") {
						t.Fatal("untrusted diagnostic escaped projection")
					}
					if got.Reason != fields["reason"] || got.ConditionSince == nil {
						t.Fatal("invalid diagnostic discarded independent saved alert inputs")
					}
				}
			})
		}
	}
}

func TestDiagnosticProjectionIntegerBoundsAndRecordedZero(t *testing.T) {
	for _, tc := range []struct {
		kind, key string
		accepted  map[string]int
		value     func(*HealthDiagnostic) *int
	}{
		{"health_ssh_journal", "journal_exit_code", map[string]int{"0": 0, "1": 1, "99": 99, "100": 100, "199": 199, "200": 200, "201": 201, "255": 255}, func(d *HealthDiagnostic) *int { return d.JournalExitCode }},
		{"health_geoip_update", "http_status", map[string]int{"100": 100, "199": 199, "201": 201, "255": 255, "256": 256, "403": 403, "451": 451, "599": 599}, func(d *HealthDiagnostic) *int { return d.HTTPStatus }},
	} {
		for _, input := range []string{"0", "1", "255", "256", "99", "100", "199", "200", "201", "403", "451", "599", "600", "-1", "+1", "1.5", "1e2", " 1", "١", "18446744073709551616"} {
			t.Run(tc.key+"/"+input, func(t *testing.T) {
				fields := diagnosticFields(tc.kind)
				fields[tc.key] = input
				if tc.key == "http_status" {
					fields["failure_stage"], fields["failure_cause"] = "download", "http_status"
				} else {
					fields["failure_cause"] = "process_exited"
				}
				got := ProjectAlertContext(tc.kind, fields)
				value := tc.value(got.Diagnostic)
				want, accepted := tc.accepted[input]
				if got.Validate(tc.kind) != nil || accepted && (value == nil || *value != want || got.Availability != "recorded") || !accepted && (value != nil || got.Availability != "partial") {
					t.Fatal("diagnostic integer did not respect its domain")
				}
				if input == "0" && tc.key == "journal_exit_code" {
					data, _ := json.Marshal(got)
					if !strings.Contains(string(data), `"journal_exit_code":0`) {
						t.Fatal("recorded exit status zero became absent")
					}
				}
			})
		}
	}
}

func TestDiagnosticProjectionKeepsFailureTuplesAndScopeConsistent(t *testing.T) {
	for name, changes := range map[string]map[string]string{
		"wrong stage and cause":       {"failure_stage": "download", "failure_cause": "validator_privilege_drop_failed"},
		"status on validation":        {"http_status": "403"},
		"status without status cause": {"failure_stage": "download", "failure_cause": "tls_failed", "http_status": "403"},
		"HTTP cause without status":   {"failure_stage": "download", "failure_cause": "http_status"},
		"invalid edition":             {"geoip_edition": "synthetic_private_database"},
		"missing scope":               {"diagnostic_scope": ""},
	} {
		t.Run(name, func(t *testing.T) {
			fields := diagnosticFields("health_geoip_update")
			for key, value := range changes {
				fields[key] = value
			}
			got := ProjectAlertContext("health_geoip_update", fields)
			d := got.Diagnostic
			if got.Validate(got.Metric) != nil || got.Availability != "partial" || d == nil || d.ComponentReason != "download_failed" || d.FailureStage != "" || d.FailureCause != "" || d.GeoIPEdition != "" || d.HTTPStatus != nil {
				t.Fatal("invalid failure tuple was partly reinterpreted")
			}
		})
	}
	fields := diagnosticFields("health_ssh_journal")
	fields["failure_cause"], fields["journal_exit_code"], fields["journal_signal"] = "", "0", "SIGKILL"
	got := ProjectAlertContext("health_ssh_journal", fields)
	if got.Availability != "partial" || got.Diagnostic.JournalExitCode != nil || got.Diagnostic.JournalSignal != "" || got.Validate(got.Metric) != nil {
		t.Fatal("orphaned journal process outcome acquired a cause")
	}
	fields = diagnosticFields("health_ssh_journal")
	fields["diagnostic_at_utc"], fields["diagnostic_scope"] = "2026-10-03T15:01:25+08:00", "last_failure"
	got = ProjectAlertContext("health_ssh_journal", fields)
	if got.Diagnostic.DiagnosticAt == nil || got.Diagnostic.DiagnosticAt.Format(time.RFC3339) != "2026-10-03T07:01:25Z" || got.Diagnostic.DiagnosticScope != "last_failure" || got.Validate(got.Metric) != nil {
		t.Fatal("saved diagnostic time/scope was replaced or not normalized")
	}
}

func TestHealthDiagnosticValidateRejectsMutatedTypedValues(t *testing.T) {
	for _, kind := range []string{"health_ssh_journal", "health_geoip_update"} {
		for _, field := range []string{"ComponentReason", "FailureStage", "FailureCause", "GeoIPEdition", "JournalState", "JournalSignal", "DiagnosticScope"} {
			t.Run(kind+"/"+field, func(t *testing.T) {
				got := ProjectAlertContext(kind, diagnosticFields(kind))
				reflect.ValueOf(got.Diagnostic).Elem().FieldByName(field).SetString("synthetic_private_token")
				if got.Validate(kind) == nil {
					t.Fatal("mutated diagnostic text passed independent validation")
				}
			})
		}
		for _, mutate := range []func(*HealthDiagnostic){
			func(d *HealthDiagnostic) { n := -1; d.JournalExitCode = &n },
			func(d *HealthDiagnostic) { n := 256; d.JournalExitCode = &n },
			func(d *HealthDiagnostic) { n := 200; d.HTTPStatus = &n },
			func(d *HealthDiagnostic) { n := 600; d.HTTPStatus = &n },
			func(d *HealthDiagnostic) {
				*d.DiagnosticAt = d.DiagnosticAt.In(time.FixedZone("synthetic_private_zone", 0))
			},
			func(d *HealthDiagnostic) { *d.DiagnosticAt = time.Time{} },
		} {
			got := ProjectAlertContext(kind, diagnosticFields(kind))
			mutate(got.Diagnostic)
			if got.Validate(kind) == nil {
				t.Fatal("mutated diagnostic bounds passed independent validation")
			}
		}
		got := ProjectAlertContext(kind, diagnosticFields(kind))
		got.Metric = "health_storage"
		if got.Validate("health_storage") == nil {
			t.Fatal("foreign health monitor accepted component diagnostics")
		}
	}
}
