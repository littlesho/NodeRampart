// SPDX-License-Identifier: MIT

package model_test

import (
	"strconv"
	"testing"

	"github.com/littlesho/NodeRampart/internal/assets"
	"github.com/littlesho/NodeRampart/internal/collector"
	"github.com/littlesho/NodeRampart/internal/model"
)

// model cannot import its producers. Exercise their real public validators
// against the projection to catch category drift without introducing a cycle.
func TestHealthDiagnosticProjectionMatchesGeoIPProducerContract(t *testing.T) {
	stages := []string{"configuration", "credentials", "download", "staging", "archive", "validation", "activation", "cleanup", "synthetic_private_stage"}
	causes := []string{"unavailable", "invalid", "failed", "dns_lookup_failed", "network_unreachable", "connection_refused", "timeout", "cancelled", "tls_failed", "redirect_rejected", "http_status", "response_read_failed", "response_too_large", "temporary_directory_unavailable", "temporary_directory_unsafe", "storage_full", "permission_denied", "validation_rejected", "resource_budget", "validator_privilege_drop_failed", "validator_setup_failed", "validator_failed", "validator_response_invalid", "source_changed", "recovery_pending", "metadata_save_failed", "synthetic_private_cause"}
	for _, stage := range stages {
		for _, cause := range causes {
			for _, edition := range []string{"", "City", "ASN", "synthetic_private_edition"} {
				for _, status := range []int{0, 100, 200, 451, 599, 600} {
					producer := assets.GeoDiagnostic{Stage: stage, Reason: cause, Edition: edition, HTTPStatus: status}
					fields := map[string]string{"component_reason": "download_failed", "diagnostic_scope": "current", "failure_stage": stage, "failure_cause": cause, "geoip_edition": edition}
					if status != 0 {
						fields["http_status"] = strconv.Itoa(status)
					}
					got := model.ProjectAlertContext("health_geoip_update", fields)
					if got.Validate(got.Metric) != nil || got.Diagnostic == nil {
						t.Fatal("projected diagnostic is not independently valid")
					}
					accepted := producer.Validate() == nil
					d := got.Diagnostic
					if accepted {
						if d.FailureStage != stage || d.FailureCause != cause || d.GeoIPEdition != edition || (status != 0) != (d.HTTPStatus != nil) || d.HTTPStatus != nil && *d.HTTPStatus != status {
							t.Fatalf("supported GeoIP diagnostic lost: %s/%s/%s/%d", stage, cause, edition, status)
						}
					} else if d.FailureStage != "" || d.FailureCause != "" || d.GeoIPEdition != "" || d.HTTPStatus != nil {
						t.Fatal("producer-invalid GeoIP tuple entered projection")
					}
				}
			}
		}
	}
}

func TestHealthDiagnosticProjectionMatchesJournalProducerContract(t *testing.T) {
	causes := []string{"executable_not_found", "executable_invalid", "permission_denied", "file_descriptor_limit", "memory_allocation_failed", "resource_unavailable", "no_space", "io_error", "journal_files_unavailable", "unsupported_option", "journal_format_unsupported", "journal_corrupt", "process_signaled", "process_exited", "process_start_failed", "stream_read_failed", "cursor_unavailable", "backfill_time_limit", "backfill_count_limit", "invalid_cursor", "malformed_record", "untrusted_origin", "persist_failed", "recovery_pending", "untrusted_uid", "untrusted_executable", "untrusted_unit", "untrusted_transport", "missing_origin_metadata", "synthetic_private_cause"}
	for _, cause := range causes {
		producer := (collector.JournalStatus{Cause: cause, DiagnosticScope: "last_failure"}).SafeDiagnostic()
		got := model.ProjectAlertContext("health_ssh_journal", map[string]string{"component_reason": "process_exited", "journal_state": "retrying", "diagnostic_scope": "last_failure", "failure_cause": cause})
		if got.Validate(got.Metric) != nil || got.Diagnostic == nil || got.Diagnostic.FailureCause != producer.Cause {
			t.Fatalf("journal cause contract drifted: %s", cause)
		}
	}
	for _, reason := range []string{"process_started", "record_persisted", "start_failed", "read_failed", "process_exited", "persist_failed", "cursor_unavailable", "backfill_time_limit", "backfill_count_limit", "invalid_cursor", "malformed_record", "untrusted_origin", "recovery_state_unavailable", "worker_stopped", "recovery_pending", "stdout_failed", "pipe_failed", "unknown", "synthetic_private_reason"} {
		got := model.ProjectAlertContext("health_ssh_journal", map[string]string{"component_reason": reason, "journal_state": "retrying"})
		if got.Validate(got.Metric) != nil || got.Diagnostic == nil || got.Diagnostic.ComponentReason != collector.SafeJournalReason(reason) {
			t.Fatalf("journal component reason contract drifted: %s", reason)
		}
	}
	for _, state := range []string{"starting", "running", "retrying", "degraded", "gap", "disabled", "synthetic_private_state"} {
		got := model.ProjectAlertContext("health_ssh_journal", map[string]string{"component_reason": "process_started", "journal_state": state})
		if got.Validate(got.Metric) != nil || got.Diagnostic == nil || got.Diagnostic.JournalState != collector.SafeJournalState(state) {
			t.Fatalf("journal state contract drifted: %s", state)
		}
	}
	for _, signal := range []string{"SIGHUP", "SIGINT", "SIGQUIT", "SIGILL", "SIGTRAP", "SIGABRT", "SIGBUS", "SIGFPE", "SIGKILL", "SIGUSR1", "SIGSEGV", "SIGUSR2", "SIGPIPE", "SIGALRM", "SIGTERM", "SIGXCPU", "SIGXFSZ", "SIGVTALRM", "SIGPROF", "SIGSYS", "SIGSTOP", "synthetic_private_signal"} {
		producer := (collector.JournalStatus{Cause: "process_signaled", DiagnosticScope: "current", Signal: signal}).SafeDiagnostic()
		got := model.ProjectAlertContext("health_ssh_journal", map[string]string{"failure_cause": "process_signaled", "diagnostic_scope": "current", "journal_signal": signal})
		if got.Validate(got.Metric) != nil || got.Diagnostic == nil || got.Diagnostic.JournalSignal != producer.Signal {
			t.Fatalf("journal signal contract drifted: %s", signal)
		}
	}
}
