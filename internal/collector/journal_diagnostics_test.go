// SPDX-License-Identifier: MIT

package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestJournalSubprocessDiagnosticsAreSafeAndSpecific(t *testing.T) {
	const secret = "synthetic-secret:/private/username?token=synthetic-token"
	for _, test := range []struct {
		name, script, cause string
		code                int
	}{
		{"permission", "printf '%s\\n' 'Failed to open " + secret + ": Permission denied' >&2\nexit 1\n", "permission_denied", 1},
		{"missing-journal", "printf '%s\\n' 'No journal files were found.' >&2\nexit 1\n", "journal_files_unavailable", 1},
		{"unsupported-option", "printf '%s\\n' 'journalctl: unrecognized option --synthetic' >&2\nexit 2\n", "unsupported_option", 2},
		{"unknown", "printf '%s\\n' '" + secret + "' >&2\nexit 17\n", "process_exited", 17},
		{"clean-eof", "exit 0\n", "process_exited", 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			status := firstJournalFailure(t, journalScript(t, test.script))
			if status.State != "retrying" || status.Reason != "process_exited" || status.Cause != test.cause || status.DiagnosticScope != "current" || status.ExitCode == nil || *status.ExitCode != test.code || status.DiagnosticAt.IsZero() {
				t.Fatalf("lost subprocess diagnosis: %+v", status)
			}
			data, err := json.Marshal(status)
			if err != nil || strings.Contains(string(data), "synthetic-secret") || strings.Contains(string(data), "synthetic-token") || strings.Contains(string(data), "/private/") {
				t.Fatalf("raw stderr escaped into diagnostics: %s err=%v", data, err)
			}
		})
	}
}

func firstJournalFailure(t *testing.T, path string) JournalStatus {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var failure JournalStatus
	err := (Journal{Path: path}).RunReliable(ctx, JournalOptions{
		InitialObservedAt: time.Now().UTC().Add(-time.Second),
		OnStatus: func(status JournalStatus) {
			if status.State == "retrying" && status.Reason != "process_started" {
				failure = status
				cancel()
			}
		},
	}, func(context.Context, JournalEntry) error { return nil })
	if err != nil || failure.Cause == "" {
		t.Fatalf("failure was not classified: status=%+v err=%v", failure, err)
	}
	return failure
}

func TestJournalExecutableFailuresAndTermination(t *testing.T) {
	missing := firstJournalFailure(t, filepath.Join(t.TempDir(), "synthetic-missing-journalctl"))
	if missing.Reason != "start_failed" || missing.Cause != "executable_not_found" || missing.ExitCode != nil {
		t.Fatalf("lost missing executable cause: %+v", missing)
	}
	if runtime.GOOS != "linux" {
		return
	}
	path := journalScript(t, "exit 0\n")
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	denied := firstJournalFailure(t, path)
	if denied.Reason != "start_failed" || denied.Cause != "permission_denied" {
		t.Fatalf("lost execute permission cause: %+v", denied)
	}
	killed := firstJournalFailure(t, journalScript(t, "kill -KILL $$\n"))
	if killed.Cause != "process_signaled" || killed.Signal != "SIGKILL" || killed.ExitCode != nil || strings.Contains(killed.Detail, "was killed by the OOM") {
		t.Fatalf("signal was lost or overinterpreted: %+v", killed)
	}
}

func TestJournalOriginDiagnosticsIdentifyTheRejectedBoundary(t *testing.T) {
	at := time.Now().UTC()
	for _, test := range []struct{ field, value, cause string }{
		{"_UID", "1000", "untrusted_uid"},
		{"_EXE", "/private/synthetic-token/sshd", "untrusted_executable"},
		{"_SYSTEMD_UNIT", "synthetic-custom.service", "untrusted_unit"},
		{"_SYSTEMD_USER_UNIT", "sshd.service", "untrusted_unit"},
		{"_TRANSPORT", "stdout", "untrusted_transport"},
		{"_UID", "", "missing_origin_metadata"},
		{"_EXE", "", "missing_origin_metadata"},
		{"_SYSTEMD_UNIT", "", "untrusted_unit"},
		{"_TRANSPORT", "", "missing_origin_metadata"},
	} {
		t.Run(test.field+"/"+test.cause, func(t *testing.T) {
			var fields map[string]string
			if err := json.Unmarshal([]byte(reliableRecord(t, "origin-diagnostic", at)), &fields); err != nil {
				t.Fatal(err)
			}
			fields[test.field] = test.value
			raw, err := json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			entry, ok := decodeJournalEntry(raw, at)
			if !ok || entry.Observation != nil || entry.SkipReason != "untrusted_origin" || entry.SkipCause != test.cause {
				t.Fatalf("origin rejection was weakened or misclassified: %+v ok=%v", entry, ok)
			}
		})
	}
}

func TestJournalQualityDiagnosisSurvivesRestarts(t *testing.T) {
	at := time.Now().UTC().Add(-time.Second)
	fault := strings.ReplaceAll(reliableRecord(t, "rejected", at), "ssh.service", "synthetic-custom.service")
	path := recoveryJournal(t, fault, "")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	starts := 0
	var initial, last JournalStatus
	err := (Journal{Path: path}).RunReliable(ctx, JournalOptions{
		InitialObservedAt: at.Add(-time.Second), RestartMin: time.Millisecond, RestartMax: time.Millisecond,
		OnStatus: func(status JournalStatus) {
			last = status
			if status.QualityDegraded() {
				initial = status
			}
			if status.Reason == "process_started" && status.State != "starting" {
				starts++
				if starts > 1 && (status.State != "retrying" || status.Cause != "untrusted_unit" || status.DiagnosticScope != "current" || !status.DiagnosticAt.Equal(initial.DiagnosticAt)) {
					t.Errorf("restart erased or replaced current quality diagnosis: initial=%+v current=%+v", initial, status)
				}
				if starts == 3 {
					time.AfterFunc(20*time.Millisecond, cancel)
				}
			}
		},
	}, func(context.Context, JournalEntry) error { return nil })
	if err != nil || starts != 3 || initial.Cause != "untrusted_unit" || last.Cause != "untrusted_unit" || last.State != "retrying" {
		t.Fatalf("quality diagnosis was not retained: initial=%+v last=%+v starts=%d err=%v", initial, last, starts, err)
	}
}

func TestJournalProcessDiagnosisIsRetainedUntilNewTrustedAcknowledgement(t *testing.T) {
	at := time.Now().UTC().Add(-time.Second)
	for _, scenario := range []string{"quiet", "duplicate", "trusted"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			body := ""
			if scenario != "quiet" {
				body = fmt.Sprintf("cat <<'RECORD'\n%s\nRECORD", reliableRecord(t, "replayed", at))
			}
			path := recoveryJournal(t, "", body)
			starts, recovered := 0, 0
			var last JournalStatus
			err := (Journal{Path: path}).RunReliable(ctx, JournalOptions{
				InitialObservedAt: at.Add(-time.Second), RestartMin: time.Millisecond, RestartMax: time.Millisecond,
				OnStatus: func(status JournalStatus) {
					last = status
					if status.Reason == "process_started" && status.State == "running" {
						starts++
						if starts > 1 && (status.Cause != "process_exited" || status.DiagnosticScope != "last_failure") {
							t.Errorf("startup falsely erased failure evidence: %+v", status)
						}
						if starts == 3 {
							time.AfterFunc(30*time.Millisecond, cancel)
						}
					}
					if status.Reason == "record_persisted" {
						recovered++
						if status.Cause != "" || status.DiagnosticScope != "" || status.ExitCode != nil || status.Signal != "" {
							t.Errorf("new durable acknowledgement retained stale failure: %+v", status)
						}
					}
				},
			}, func(context.Context, JournalEntry) error {
				if scenario == "duplicate" {
					return ErrJournalAlreadyAcknowledged
				}
				return nil
			})
			want := 0
			if scenario == "trusted" {
				want = 1
			}
			if err != nil || starts != 3 || recovered != want || last.State != "running" || scenario != "trusted" && last.DiagnosticScope != "last_failure" {
				t.Fatalf("last failure/acknowledgement semantics changed: starts=%d recovered=%d last=%+v err=%v", starts, recovered, last, err)
			}
		})
	}
}

func TestJournalDiagnosticBoundaryRejectsUnknownAndFreeText(t *testing.T) {
	code := 1
	status := JournalStatus{Cause: "permission_denied", Detail: "synthetic-secret", DiagnosticScope: "current", DiagnosticAt: time.Now().UTC(), ExitCode: &code, Signal: "SIGKILL"}
	safe := status.SafeDiagnostic()
	code = 257
	if safe.Detail != JournalCauseDescription("permission_denied") || safe.ExitCode == nil || *safe.ExitCode != 1 || safe.Signal != "SIGKILL" {
		t.Fatalf("diagnostic boundary did not rebuild/copy safe data: %+v", safe)
	}
	status.Signal = "synthetic-secret"
	safe = status.SafeDiagnostic()
	if safe.ExitCode != nil || safe.Signal != "" {
		t.Fatalf("invalid numeric or signal metadata escaped: %+v", safe)
	}
	for _, change := range []func(*JournalStatus){
		func(s *JournalStatus) { s.Cause = "synthetic-secret" },
		func(s *JournalStatus) { s.DiagnosticScope = "synthetic-secret" },
	} {
		candidate := status
		change(&candidate)
		if got := candidate.SafeDiagnostic(); got.Cause != "" || got.Detail != "" || got.ExitCode != nil {
			t.Fatalf("unknown public field was accepted: %+v", got)
		}
	}
	if SafeJournalReason("synthetic-secret") != "" || SafeJournalState("synthetic-secret") != "" {
		t.Fatal("unknown reason or state was accepted")
	}
}

func TestJournalDiagnosticSystemErrorsAndBoundedStderr(t *testing.T) {
	for _, test := range []struct {
		err   syscall.Errno
		cause string
	}{
		{syscall.EMFILE, "file_descriptor_limit"}, {syscall.ENFILE, "file_descriptor_limit"},
		{syscall.ENOMEM, "memory_allocation_failed"}, {syscall.EAGAIN, "resource_unavailable"},
		{syscall.ENOSPC, "no_space"}, {syscall.EIO, "io_error"}, {syscall.ENOEXEC, "executable_invalid"},
	} {
		failure := journalFailure("start_failed", &os.PathError{Op: "fork/exec", Path: "/private/synthetic-secret", Err: test.err}, nil)
		if failure.diagnostic.Cause != test.cause || strings.Contains(failure.Error(), "synthetic-secret") || strings.Contains(failure.diagnostic.Detail, "synthetic-secret") {
			t.Fatalf("system error was lost or disclosed: %+v", failure)
		}
	}
	for _, filler := range []int{4085, 32 << 10} {
		var stderr boundedJournalStderr
		_, _ = stderr.Write([]byte(strings.Repeat("x", filler)))
		_, _ = stderr.Write([]byte("\nFailed to open journal: Permission denied\n"))
		if stderr.failureCause() != "permission_denied" || len(stderr.message()) > 8193 {
			t.Fatalf("diagnostic across buffer boundary or after large stderr was lost: filler=%d bytes=%d cause=%s", filler, len(stderr.message()), stderr.failureCause())
		}
	}
}
