// SPDX-License-Identifier: MIT

package collector

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

// JournalDiagnostic is bounded, non-sensitive evidence about an observed
// failure. Scope distinguishes a current failure from one retained after a
// quiet subprocess restart. At is the diagnostic observation time.
type JournalDiagnostic struct {
	Cause    string    `json:"cause,omitempty"`
	Detail   string    `json:"detail,omitempty"`
	Scope    string    `json:"scope,omitempty"`
	At       time.Time `json:"at_utc,omitzero"`
	ExitCode *int      `json:"exit_code,omitempty"`
	Signal   string    `json:"signal,omitempty"`
}

// JournalCauseDescription is the single allowlist for public diagnostic
// causes. Descriptions are fixed application text; raw stderr is never output.
func JournalCauseDescription(cause string) string {
	switch cause {
	case "executable_not_found":
		return "The configured journalctl executable could not be found; check its installation and configured path."
	case "executable_invalid":
		return "The configured journalctl executable could not be executed in this environment; check its format and architecture."
	case "permission_denied":
		return "Access was denied while starting or reading journalctl; check service-user journal permissions and security policy."
	case "file_descriptor_limit":
		return "An open-file limit prevented journal access; check service and system file-descriptor limits."
	case "memory_allocation_failed":
		return "A memory allocation failed while accessing the journal; check host and service memory pressure."
	case "resource_unavailable":
		return "A required system resource was temporarily unavailable; check process limits and host resource pressure."
	case "no_space":
		return "Journal access reported no space left; check filesystem space, inode capacity and inotify watch limits."
	case "io_error":
		return "Journal access reported an input/output error; inspect journal and kernel storage diagnostics."
	case "journal_files_unavailable":
		return "No journal files were available to journalctl; check journald storage and the service's journal visibility."
	case "unsupported_option":
		return "journalctl rejected a command-line option; check the installed systemd version and configured executable."
	case "journal_format_unsupported":
		return "journalctl reported an unsupported journal format or feature; check reader and journal format compatibility."
	case "journal_corrupt":
		return "journalctl reported damaged or truncated journal data; inspect journald diagnostics and verify retained journal files."
	case "process_signaled":
		return "journalctl was terminated by a signal; the signal alone does not identify its sender or establish an out-of-memory event."
	case "process_exited":
		return "The journalctl follow process exited; inspect the exit status and local journald diagnostics."
	case "process_start_failed":
		return "The journalctl process could not be started; the available evidence does not identify a more specific cause."
	case "stream_read_failed":
		return "Reading the journalctl output stream failed; the available evidence does not identify a more specific cause."
	case "cursor_unavailable":
		return "The saved journal cursor was unavailable or no longer matched; bounded replay resumed with an explicit coverage gap."
	case "backfill_time_limit":
		return "Journal replay reached the configured time boundary; older coverage is recorded as a gap."
	case "backfill_count_limit":
		return "Journal replay reached the configured record limit; skipped coverage is recorded as a gap."
	case "invalid_cursor":
		return "The saved journal cursor failed validation; bounded replay resumed with an explicit coverage gap."
	case "malformed_record":
		return "A journal record was malformed, oversized, or lacked a readable message; recovery requires a new trusted record to be persisted."
	case "untrusted_origin":
		return "A journal record failed SSH-origin checks; inspect its UID, executable, service or session unit, and transport metadata."
	case "missing_origin_metadata":
		return "A journal record lacked required UID, executable or transport metadata; its SSH origin could not be verified."
	case "untrusted_uid":
		return "A journal record did not have the required root sender UID; its SSH origin was rejected."
	case "untrusted_executable":
		return "A journal record's executable was outside the approved OpenSSH paths; its SSH origin was rejected."
	case "untrusted_unit":
		return "A journal record carried an unsupported or invalid explicit system unit, or a user-service unit; its SSH origin was rejected."
	case "untrusted_transport":
		return "A journal record used an unsupported transport; SSH-origin verification requires the journal or syslog transport."
	case "persist_failed":
		return "The journal checkpoint or recovery marker could not be persisted; inspect NodeRampart storage health."
	case "recovery_pending":
		return "A previously recorded journal quality failure still awaits a new trusted record to be persisted."
	}
	return ""
}

// SafeJournalReason returns only a collector or daemon-owned reason code.
// It is also used at notification and evidence-export boundaries.
func SafeJournalReason(reason string) string {
	switch reason {
	case "process_started", "record_persisted", "start_failed", "read_failed", "process_exited", "persist_failed",
		"cursor_unavailable", "backfill_time_limit", "backfill_count_limit", "invalid_cursor", "malformed_record",
		"untrusted_origin", "recovery_state_unavailable", "worker_stopped":
		return reason
	}
	return ""
}

// SafeJournalState returns only a collector-owned state code.
func SafeJournalState(state string) string {
	switch state {
	case "starting", "running", "retrying", "degraded", "gap":
		return state
	}
	return ""
}

// SafeDiagnostic validates public fields and reconstructs the description from
// its allowlisted cause. Even a fabricated JournalStatus cannot forward free
// text through this boundary. The exit-code pointer is copied defensively.
func (s JournalStatus) SafeDiagnostic() JournalDiagnostic {
	detail := JournalCauseDescription(s.Cause)
	if detail == "" || (s.DiagnosticScope != "current" && s.DiagnosticScope != "last_failure") {
		return JournalDiagnostic{}
	}
	diagnostic := JournalDiagnostic{Cause: s.Cause, Detail: detail, Scope: s.DiagnosticScope}
	if !s.DiagnosticAt.IsZero() && s.DiagnosticAt.Year() >= 1970 && s.DiagnosticAt.Year() <= 9999 {
		diagnostic.At = s.DiagnosticAt.UTC()
	}
	if s.ExitCode != nil && *s.ExitCode >= 0 && *s.ExitCode <= 255 {
		code := *s.ExitCode
		diagnostic.ExitCode = &code
	}
	if validJournalSignal(s.Signal) {
		diagnostic.Signal = s.Signal
	}
	return diagnostic
}

func (s *JournalStatus) setDiagnostic(diagnostic JournalDiagnostic) {
	s.Cause, s.Detail = diagnostic.Cause, diagnostic.Detail
	s.DiagnosticScope, s.DiagnosticAt = diagnostic.Scope, diagnostic.At
	s.ExitCode, s.Signal = diagnostic.ExitCode, diagnostic.Signal
}

func journalDiagnostic(cause string, at time.Time) JournalDiagnostic {
	detail := JournalCauseDescription(cause)
	if detail == "" {
		return JournalDiagnostic{}
	}
	return JournalDiagnostic{Cause: cause, Detail: detail, Scope: "current", At: at.UTC()}
}

func journalFailure(reason string, err error, stderr *boundedJournalStderr) journalAttemptError {
	cause := reason
	switch reason {
	case "start_failed":
		cause = "process_start_failed"
	case "read_failed":
		cause = "stream_read_failed"
	}
	diagnostic := journalDiagnostic(cause, time.Now().UTC())
	if reason == "start_failed" || reason == "read_failed" {
		switch {
		case reason == "start_failed" && (errors.Is(err, os.ErrNotExist) || errors.Is(err, exec.ErrNotFound)):
			cause = "executable_not_found"
		case errors.Is(err, os.ErrPermission), errors.Is(err, syscall.EPERM):
			cause = "permission_denied"
		case reason == "start_failed" && errors.Is(err, syscall.ENOEXEC):
			cause = "executable_invalid"
		case errors.Is(err, syscall.EMFILE), errors.Is(err, syscall.ENFILE):
			cause = "file_descriptor_limit"
		case errors.Is(err, syscall.ENOMEM):
			cause = "memory_allocation_failed"
		case errors.Is(err, syscall.EAGAIN):
			cause = "resource_unavailable"
		case errors.Is(err, syscall.ENOSPC):
			cause = "no_space"
		case errors.Is(err, syscall.EIO):
			cause = "io_error"
		}
	} else if reason == "process_exited" || reason == "cursor_unavailable" {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) && exitError.ProcessState != nil {
			code := exitError.ProcessState.ExitCode()
			if code >= 0 && code <= 255 {
				diagnostic.ExitCode = &code
			} else if code < 0 {
				cause = "process_signaled"
				diagnostic.Signal = journalExitSignal(exitError.ProcessState)
			}
		} else if err == nil {
			code := 0
			diagnostic.ExitCode = &code
		}
		// Only inspect stderr after command.Wait has joined its copy worker.
		// An actual terminating signal takes precedence over older warnings.
		if stderr != nil && cause != "process_signaled" && reason != "cursor_unavailable" {
			if classified := stderr.failureCause(); classified != "" {
				cause = classified
			}
		}
	}
	diagnostic.Cause, diagnostic.Detail = cause, JournalCauseDescription(cause)
	return journalAttemptError{reason: reason, diagnostic: diagnostic}
}

func validJournalSignal(signal string) bool {
	switch signal {
	case "SIGHUP", "SIGINT", "SIGQUIT", "SIGILL", "SIGTRAP", "SIGABRT", "SIGBUS", "SIGFPE", "SIGKILL",
		"SIGUSR1", "SIGSEGV", "SIGUSR2", "SIGPIPE", "SIGALRM", "SIGTERM", "SIGXCPU", "SIGXFSZ",
		"SIGVTALRM", "SIGPROF", "SIGSYS":
		return true
	}
	return false
}

// Keep both the initial explanation and the final error, bounded to 8 KiB.
// The mutex also makes diagnostic snapshots safe while a child is draining.
type boundedJournalStderr struct {
	mu           sync.Mutex
	prefix, tail []byte
	truncated    bool
}

func (b *boundedJournalStderr) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(data)
	if left := 4096 - len(b.prefix); left > 0 {
		take := min(left, len(data))
		b.prefix = append(b.prefix, data[:take]...)
		data = data[take:]
	}
	b.truncated = b.truncated || len(b.tail)+len(data) > 4096
	if len(data) >= 4096 {
		b.tail = append(b.tail[:0], data[len(data)-4096:]...)
	} else if len(data) > 0 {
		if keep := 4096 - len(data); len(b.tail) > keep {
			copy(b.tail, b.tail[len(b.tail)-keep:])
			b.tail = b.tail[:keep]
		}
		b.tail = append(b.tail, data...)
	}
	return n, nil
}

func (b *boundedJournalStderr) message() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	separator := ""
	if b.truncated {
		separator = "\n"
	}
	return strings.ToLower(string(b.prefix) + separator + string(b.tail))
}

func (b *boundedJournalStderr) cursorUnavailable() bool {
	message := b.message()
	return strings.Contains(message, "failed to seek to cursor") || strings.Contains(message, "cannot seek to cursor") || strings.Contains(message, "cursor not found")
}

func (b *boundedJournalStderr) failureCause() string {
	message := b.message()
	for _, rule := range []struct {
		cause string
		terms []string
	}{
		{"permission_denied", []string{"permission denied", "operation not permitted", "insufficient permissions"}},
		{"file_descriptor_limit", []string{"too many open files"}},
		{"memory_allocation_failed", []string{"cannot allocate memory", "out of memory"}},
		{"resource_unavailable", []string{"resource temporarily unavailable"}},
		{"no_space", []string{"no space left on device"}},
		{"io_error", []string{"input/output error", "input/output failure"}},
		{"journal_files_unavailable", []string{"no journal files were found", "no journal files were opened"}},
		{"unsupported_option", []string{"unrecognized option", "unknown option", "invalid option"}},
		{"journal_format_unsupported", []string{"unsupported journal", "uses an unsupported feature", "unsupported feature"}},
		{"journal_corrupt", []string{"corrupted", "corruption", "truncated journal", "journal file is truncated"}},
	} {
		for _, term := range rule.terms {
			if strings.Contains(message, term) {
				return rule.cause
			}
		}
	}
	return ""
}
