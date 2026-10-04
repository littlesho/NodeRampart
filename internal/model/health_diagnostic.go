// SPDX-License-Identifier: MIT

package model

import (
	"errors"
	"strconv"
	"strings"
	"time"
)

// HealthDiagnostic contains recorded, identity-free classifications only.
// It deliberately has no message, error, path, URL, or arbitrary map field.
// The owning collector/assets packages cannot be imported here because they
// depend on model; contract tests keep these fixed categories aligned.
type HealthDiagnostic struct {
	ComponentReason string     `json:"component_reason,omitempty"`
	FailureStage    string     `json:"failure_stage,omitempty"`
	FailureCause    string     `json:"failure_cause,omitempty"`
	GeoIPEdition    string     `json:"geoip_edition,omitempty"`
	HTTPStatus      *int       `json:"http_status,omitempty"`
	JournalState    string     `json:"journal_state,omitempty"`
	JournalExitCode *int       `json:"journal_exit_code,omitempty"`
	JournalSignal   string     `json:"journal_signal,omitempty"`
	DiagnosticAt    *time.Time `json:"diagnostic_at_utc,omitempty"`
	DiagnosticScope string     `json:"diagnostic_scope,omitempty"`
}

func (d *HealthDiagnostic) hasFields() bool {
	return d.ComponentReason != "" || d.FailureStage != "" || d.FailureCause != "" || d.GeoIPEdition != "" || d.HTTPStatus != nil || d.JournalState != "" || d.JournalExitCode != nil || d.JournalSignal != "" || d.DiagnosticAt != nil || d.DiagnosticScope != ""
}

// Validate checks both the field allowlists and their component-specific
// relationships. A timestamp or component result alone remains useful for a
// legacy update; an exit code or HTTP status must have its associated cause.
func (d *HealthDiagnostic) Validate(kind string) error {
	bad := errors.New("invalid recorded health diagnostic")
	if d == nil || !d.hasFields() || !alertTime(d.DiagnosticAt) || d.DiagnosticScope != "" && d.DiagnosticScope != "current" && d.DiagnosticScope != "last_failure" {
		return bad
	}
	if d.FailureCause != "" && d.DiagnosticScope == "" {
		return bad
	}
	switch kind {
	case "health_ssh_journal":
		if d.ComponentReason != "" && !journalComponentReason(d.ComponentReason) || d.JournalState != "" && !journalDiagnosticState(d.JournalState) || d.FailureCause != "" && !journalDiagnosticCause(d.FailureCause) {
			return bad
		}
		if d.FailureStage != "" || d.GeoIPEdition != "" || d.HTTPStatus != nil || d.JournalExitCode != nil && (*d.JournalExitCode < 0 || *d.JournalExitCode > 255) || d.JournalSignal != "" && !journalDiagnosticSignal(d.JournalSignal) {
			return bad
		}
		if d.FailureCause == "" && (d.JournalExitCode != nil || d.JournalSignal != "") {
			return bad
		}
	case "health_geoip_update":
		if d.ComponentReason != "" && !geoComponentReason(d.ComponentReason) || d.JournalState != "" || d.JournalExitCode != nil || d.JournalSignal != "" {
			return bad
		}
		if (d.FailureStage != "" || d.FailureCause != "" || d.GeoIPEdition != "" || d.HTTPStatus != nil) && !geoDiagnosticFailure(d) {
			return bad
		}
	default:
		return bad
	}
	return nil
}

func journalComponentReason(value string) bool {
	switch value {
	case "process_started", "record_persisted", "start_failed", "read_failed", "process_exited", "persist_failed",
		"cursor_unavailable", "backfill_time_limit", "backfill_count_limit", "invalid_cursor", "malformed_record",
		"untrusted_origin", "recovery_state_unavailable", "worker_stopped":
		return true
	}
	return false
}

func journalDiagnosticState(value string) bool {
	switch value {
	case "starting", "running", "retrying", "degraded", "gap":
		return true
	}
	return false
}

func journalDiagnosticCause(value string) bool {
	switch value {
	case "executable_not_found", "executable_invalid", "permission_denied", "file_descriptor_limit", "memory_allocation_failed",
		"resource_unavailable", "no_space", "io_error", "journal_files_unavailable", "unsupported_option", "journal_format_unsupported",
		"journal_corrupt", "process_signaled", "process_exited", "process_start_failed", "stream_read_failed", "cursor_unavailable",
		"backfill_time_limit", "backfill_count_limit", "invalid_cursor", "malformed_record", "untrusted_origin", "persist_failed", "recovery_pending",
		"untrusted_uid", "untrusted_executable", "untrusted_unit", "untrusted_transport", "missing_origin_metadata":
		return true
	}
	return false
}

func journalDiagnosticSignal(value string) bool {
	switch value {
	case "SIGHUP", "SIGINT", "SIGQUIT", "SIGILL", "SIGTRAP", "SIGABRT", "SIGBUS", "SIGFPE", "SIGKILL", "SIGUSR1",
		"SIGSEGV", "SIGUSR2", "SIGPIPE", "SIGALRM", "SIGTERM", "SIGXCPU", "SIGXFSZ", "SIGVTALRM", "SIGPROF", "SIGSYS":
		return true
	}
	return false
}

func geoComponentReason(value string) bool {
	switch value {
	case "ok", "unchanged", "download_failed", "activation_failed", "schedule_failed", "credentials_unavailable", "unknown":
		return true
	}
	return false
}

func geoDiagnosticFailure(d *HealthDiagnostic) bool {
	if d.GeoIPEdition != "" && d.GeoIPEdition != "City" && d.GeoIPEdition != "ASN" {
		return false
	}
	if d.FailureCause == "http_status" {
		return d.FailureStage == "download" && d.HTTPStatus != nil && *d.HTTPStatus >= 100 && *d.HTTPStatus <= 599 && *d.HTTPStatus != 200
	}
	if d.HTTPStatus != nil {
		return false
	}
	switch d.FailureStage {
	case "configuration":
		return d.FailureCause == "unavailable"
	case "credentials":
		return d.FailureCause == "unavailable" || d.FailureCause == "invalid"
	case "download":
		switch d.FailureCause {
		case "failed", "dns_lookup_failed", "network_unreachable", "connection_refused", "timeout", "cancelled", "tls_failed", "redirect_rejected", "response_read_failed", "response_too_large":
			return true
		}
	case "staging":
		switch d.FailureCause {
		case "failed", "temporary_directory_unavailable", "temporary_directory_unsafe", "storage_full", "permission_denied":
			return true
		}
	case "archive":
		return d.FailureCause == "invalid"
	case "validation":
		switch d.FailureCause {
		case "validation_rejected", "resource_budget", "validator_privilege_drop_failed", "validator_setup_failed", "validator_failed", "validator_response_invalid", "source_changed", "timeout", "cancelled":
			return true
		}
	case "activation":
		return d.FailureCause == "failed" || d.FailureCause == "recovery_pending" || d.FailureCause == "metadata_save_failed"
	case "cleanup":
		return d.FailureCause == "failed"
	}
	return false
}

func projectHealthDiagnostic(kind string, fields map[string]string) (*HealthDiagnostic, bool) {
	if kind != "health_ssh_journal" && kind != "health_geoip_update" {
		return nil, false
	}
	d := &HealthDiagnostic{}
	rejected := false
	text := func(key string, valid func(string) bool) string {
		value := fields[key]
		if value != "" && (len(value) > 64 || !valid(value)) {
			rejected = true
			return ""
		}
		return value
	}
	number := func(key string, maximum int) *int {
		value := fields[key]
		if value == "" {
			return nil
		}
		if len(value) <= 3 && strings.IndexFunc(value, func(r rune) bool { return r < '0' || r > '9' }) < 0 {
			if parsed, err := strconv.Atoi(value); err == nil && parsed <= maximum {
				return &parsed
			}
		}
		rejected = true
		return nil
	}
	d.DiagnosticScope = text("diagnostic_scope", func(value string) bool { return value == "current" || value == "last_failure" })
	if value := fields["diagnostic_at_utc"]; value != "" {
		if len(value) <= 40 {
			if at, err := time.Parse(time.RFC3339Nano, value); err == nil && !at.IsZero() && at.Year() >= 1970 && at.Year() <= 9999 {
				at = at.UTC()
				d.DiagnosticAt = &at
			}
		}
		if d.DiagnosticAt == nil {
			rejected = true
		}
	}
	if kind == "health_ssh_journal" {
		d.ComponentReason = text("component_reason", journalComponentReason)
		d.JournalState = text("journal_state", journalDiagnosticState)
		d.FailureCause = text("failure_cause", journalDiagnosticCause)
		d.JournalExitCode = number("journal_exit_code", 255)
		d.JournalSignal = text("journal_signal", journalDiagnosticSignal)
		if d.FailureCause != "" && d.DiagnosticScope == "" || d.FailureCause == "" && (d.JournalExitCode != nil || d.JournalSignal != "") {
			d.FailureCause, d.JournalSignal, d.JournalExitCode = "", "", nil
			rejected = true
		}
		for _, key := range []string{"failure_stage", "geoip_edition", "http_status"} {
			rejected = rejected || fields[key] != ""
		}
	} else {
		d.ComponentReason = text("component_reason", geoComponentReason)
		// Treat the stage/cause/edition/status tuple as one unit. For example,
		// an invalid HTTP status must not turn a saved HTTP failure into a
		// different valid category or borrow a current value.
		failurePresent := false
		for _, key := range []string{"failure_stage", "failure_cause", "geoip_edition", "http_status"} {
			failurePresent = failurePresent || fields[key] != ""
		}
		if failurePresent {
			d.FailureStage, d.FailureCause, d.GeoIPEdition = fields["failure_stage"], fields["failure_cause"], fields["geoip_edition"]
			d.HTTPStatus = number("http_status", 599)
			if len(d.FailureStage) > 64 || len(d.FailureCause) > 64 || len(d.GeoIPEdition) > 64 || !geoDiagnosticFailure(d) || d.DiagnosticScope == "" || fields["http_status"] != "" && d.HTTPStatus == nil {
				d.FailureStage, d.FailureCause, d.GeoIPEdition, d.HTTPStatus = "", "", "", nil
				rejected = true
			}
		}
		for _, key := range []string{"journal_state", "journal_exit_code", "journal_signal"} {
			rejected = rejected || fields[key] != ""
		}
	}
	if !d.hasFields() {
		return nil, rejected
	}
	if d.Validate(kind) != nil {
		return nil, true
	}
	return d, rejected
}
