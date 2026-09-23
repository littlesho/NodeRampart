// SPDX-License-Identifier: MIT

package collector

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func reliableRecord(t *testing.T, cursor string, at time.Time) string {
	t.Helper()
	data, err := json.Marshal(map[string]string{
		"__CURSOR": cursor, "__REALTIME_TIMESTAMP": fmt.Sprint(at.UnixMicro()),
		"MESSAGE": "Failed password for root from 192.0.2.7 port 54321 ssh2",
		"_UID":    "0", "_EXE": "/usr/sbin/sshd", "_SYSTEMD_UNIT": "ssh.service", "_TRANSPORT": "syslog",
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func journalScript(t *testing.T, script string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("journalctl fixture requires a POSIX shell")
	}
	path := filepath.Join(t.TempDir(), "journalctl")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReliableJournalAcknowledgesOnlyAfterPersistence(t *testing.T) {
	at := time.Now().UTC().Add(-time.Second)
	c0, c1, c2 := reliableRecord(t, "c0", at), reliableRecord(t, "c1", at.Add(time.Millisecond)), reliableRecord(t, "c2", at.Add(2*time.Millisecond))
	trace := filepath.Join(t.TempDir(), "arguments")
	script := fmt.Sprintf(`printf '%%s\n' "$*" >> '%s'
case "$*" in
  *--cursor=c0*)
    cat <<'DATA'
%s
%s
DATA
    exit 9;;
  *--cursor=c1*)
    cat <<'DATA'
%s
%s
DATA
    exit 0;;
esac
exit 12
`, trace, c0, c1, c1, c2)
	path := journalScript(t, script)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var attempts, acknowledged []string
	var statuses []JournalStatus
	err := (Journal{Path: path}).RunReliable(ctx, JournalOptions{InitialCursor: "c0", InitialObservedAt: at, RestartMin: time.Millisecond, RestartMax: time.Millisecond, OnStatus: func(status JournalStatus) { statuses = append(statuses, status) }}, func(_ context.Context, entry JournalEntry) error {
		attempts = append(attempts, entry.Cursor)
		if len(attempts) == 1 {
			return errors.New("synthetic persistence failure")
		}
		acknowledged = append(acknowledged, entry.Cursor)
		if entry.Cursor == "c2" {
			cancel()
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(attempts, []string{"c1", "c1", "c2"}) || !reflect.DeepEqual(acknowledged, []string{"c1", "c2"}) {
		t.Fatalf("acknowledgement boundary lost: attempts=%v committed=%v", attempts, acknowledged)
	}
	arguments, err := os.ReadFile(trace)
	if err != nil {
		t.Fatal(err)
	}
	launches := strings.Split(strings.TrimSpace(string(arguments)), "\n")
	if len(launches) != 3 || !strings.Contains(launches[0], "--cursor=c0") || !strings.Contains(launches[1], "--cursor=c0") || !strings.Contains(launches[2], "--cursor=c1") {
		t.Fatalf("wrong restart cursors: %v", launches)
	}
	for _, arguments := range launches {
		if !strings.Contains(arguments, "--boot=all") || !strings.Contains(arguments, "--no-tail") || strings.Contains(arguments, "--cursor-file") {
			t.Fatalf("unsafe recovery arguments: %s", arguments)
		}
	}
	found := false
	for _, status := range statuses {
		if status.Reason == "persist_failed" {
			found = true
		}
	}
	if !found {
		t.Fatal("persistence failure was not visible")
	}
}

func TestReliableJournalDetectsRotatedCursorAndBoundsFallback(t *testing.T) {
	at := time.Now().UTC().Add(-5 * time.Second)
	record := reliableRecord(t, "available", at.Add(time.Second))
	trace := filepath.Join(t.TempDir(), "arguments")
	path := journalScript(t, fmt.Sprintf(`printf '%%s\n' "$*" >> '%s'
cat <<'DATA'
%s
DATA
`, trace, record))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var gaps []JournalStatus
	consumed := 0
	err := (Journal{Path: path}).RunReliable(ctx, JournalOptions{InitialCursor: "vacuumed", InitialObservedAt: at, RestartMin: time.Millisecond, RestartMax: time.Millisecond, OnStatus: func(status JournalStatus) {
		if status.State == "gap" {
			gaps = append(gaps, status)
		}
	}}, func(_ context.Context, entry JournalEntry) error {
		consumed++
		if entry.Cursor != "available" {
			t.Fatalf("unexpected record: %#v", entry)
		}
		cancel()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if consumed != 1 || len(gaps) != 1 || gaps[0].Reason != "cursor_unavailable" {
		t.Fatalf("missing rotation gap or invalid replay: consumed=%d gaps=%v", consumed, gaps)
	}
	arguments, err := os.ReadFile(trace)
	if err != nil {
		t.Fatal(err)
	}
	launches := strings.Split(strings.TrimSpace(string(arguments)), "\n")
	wantSince := fmt.Sprintf("--since=@%d.%06d", at.Truncate(time.Microsecond).Add(time.Microsecond).Unix(), at.Truncate(time.Microsecond).Add(time.Microsecond).Nanosecond()/1000)
	if len(launches) != 2 || !strings.Contains(launches[1], wantSince) || strings.Contains(launches[1], "--cursor=") {
		t.Fatalf("fallback did not skip the ambiguous acknowledged timestamp: %v", launches)
	}
}

func TestReliableJournalStopsBackfillAtRecordBudget(t *testing.T) {
	at := time.Now().UTC().Add(-5 * time.Second)
	path := journalScript(t, fmt.Sprintf("cat <<'DATA'\n%s\n%s\nDATA\n", reliableRecord(t, "c1", at), reliableRecord(t, "c2", at.Add(time.Millisecond))))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	consumed := 0
	foundLimit := false
	err := (Journal{Path: path}).RunReliable(ctx, JournalOptions{MaxBackfill: 1, RestartMin: time.Millisecond, RestartMax: time.Millisecond, OnStatus: func(status JournalStatus) {
		if status.State == "gap" && status.Reason == "backfill_count_limit" {
			foundLimit = true
			cancel()
		}
	}}, func(_ context.Context, _ JournalEntry) error { consumed++; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if consumed != 1 || !foundLimit {
		t.Fatalf("unbounded backfill: consumed=%d limit=%v", consumed, foundLimit)
	}
}

func TestReliableJournalFallbackDoesNotReplayAcknowledgedTimestamp(t *testing.T) {
	at := time.Now().UTC().Add(-5 * time.Second).Truncate(time.Microsecond)
	path := journalScript(t, fmt.Sprintf("cat <<'DATA'\n%s\n%s\n%s\nDATA\n", reliableRecord(t, "older", at.Add(-time.Microsecond)), reliableRecord(t, "same-time", at), reliableRecord(t, "newer", at.Add(time.Microsecond))))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var consumed []string
	err := (Journal{Path: path}).RunReliable(ctx, JournalOptions{InitialCursor: "vacuumed", InitialObservedAt: at, RestartMin: time.Millisecond, RestartMax: time.Millisecond}, func(_ context.Context, entry JournalEntry) error {
		consumed = append(consumed, entry.Cursor)
		cancel()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(consumed, []string{"newer"}) {
		t.Fatalf("fallback replayed an acknowledged timestamp: %v", consumed)
	}
}

func TestReliableJournalValidCursorPreservesSameTimestampEntries(t *testing.T) {
	at := time.Now().UTC().Add(-time.Second).Truncate(time.Microsecond)
	path := journalScript(t, fmt.Sprintf("cat <<'DATA'\n%s\n%s\nDATA\n", reliableRecord(t, "boundary", at), reliableRecord(t, "next", at)))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var consumed []string
	err := (Journal{Path: path}).RunReliable(ctx, JournalOptions{InitialCursor: "boundary", InitialObservedAt: at, RestartMin: time.Millisecond, RestartMax: time.Millisecond}, func(_ context.Context, entry JournalEntry) error {
		consumed = append(consumed, entry.Cursor)
		cancel()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(consumed, []string{"next"}) {
		t.Fatalf("valid cursor discarded same-timestamp entry: %v", consumed)
	}
}

func TestReliableJournalCancellationInterruptsPipeAndBackoff(t *testing.T) {
	for _, script := range []string{"exec sleep 30\n", "exit 1\n"} {
		ctx, cancel := context.WithCancel(context.Background())
		path := journalScript(t, script)
		done := make(chan error, 1)
		started := make(chan struct{}, 1)
		go func() {
			done <- (Journal{Path: path}).RunReliable(ctx, JournalOptions{OnStatus: func(status JournalStatus) {
				if status.State == "running" {
					select {
					case started <- struct{}{}:
					default:
					}
				}
			}}, func(context.Context, JournalEntry) error { return nil })
		}()
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			cancel()
			t.Fatal("fixture did not start")
		}
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("cancellation left journalctl or backoff running")
		}
	}
}

func TestJournalEntryMetadataValidation(t *testing.T) {
	at := time.Now().UTC()
	valid := reliableRecord(t, "c1", at)
	for _, raw := range []string{
		strings.ReplaceAll(valid, `"__CURSOR":"c1"`, `"__CURSOR":""`),
		strings.ReplaceAll(valid, `"__CURSOR":"c1"`, `"__CURSOR":"bad\ncursor"`),
		strings.ReplaceAll(valid, `"__CURSOR":"c1"`, `"__CURSOR":["c1","c2"]`),
		strings.ReplaceAll(valid, `"__CURSOR":"c1"`, `"__CURSOR":"`+strings.Repeat("x", 4097)+`"`),
		strings.ReplaceAll(valid, fmt.Sprint(at.UnixMicro()), "bad"),
	} {
		if entry, ok := decodeJournalEntry([]byte(raw), at); ok {
			t.Fatalf("accepted invalid checkpoint metadata: %#v", entry)
		}
	}
	for _, test := range []struct{ raw, reason string }{
		{strings.ReplaceAll(valid, `"_UID":"0"`, `"_UID":"1000"`), "untrusted_origin"},
		{strings.ReplaceAll(valid, `"MESSAGE":"Failed password for root from 192.0.2.7 port 54321 ssh2"`, `"MESSAGE":[65,66]`), "malformed_record"},
		{strings.ReplaceAll(valid, "Failed password", "Unrecognized text"), "unrecognized_message"},
	} {
		entry, ok := decodeJournalEntry([]byte(test.raw), at)
		if !ok || entry.Cursor != "c1" || entry.Observation != nil || entry.SkipReason != test.reason {
			t.Fatalf("lost safely skippable journal entry: %#v, ok=%v", entry, ok)
		}
	}
}

func TestJournalDrainsOversizedRecord(t *testing.T) {
	reader := bufio.NewReaderSize(strings.NewReader(strings.Repeat("x", 512<<10)+"\n{}\n"), 4096)
	if line, oversized, err := readJournalLine(reader); len(line) != 0 || !oversized || err != nil {
		t.Fatalf("oversized input: bytes=%d oversized=%v err=%v", len(line), oversized, err)
	}
	if line, oversized, err := readJournalLine(reader); string(line) != "{}\n" || oversized || err != nil {
		t.Fatalf("next record lost after oversized input: %q %v %v", line, oversized, err)
	}
	if _, _, err := readJournalLine(reader); !errors.Is(err, io.EOF) {
		t.Fatalf("expected EOF, got %v", err)
	}
}

func TestReliableJournalRecoversCurrentStateAfterTrustedPersistence(t *testing.T) {
	at := time.Now().UTC().Add(-time.Second)
	for _, rejected := range []string{"malformed JSON", strings.ReplaceAll(reliableRecord(t, "rejected", at), `"_UID":"0"`, `"_UID":"1000"`)} {
		for _, trusted := range []string{reliableRecord(t, "accepted", at.Add(time.Millisecond)), strings.ReplaceAll(reliableRecord(t, "accepted", at.Add(time.Millisecond)), "Failed password", "Connection closed"), sessionCloseRecord(t, "synthetic-close", sessionCloseMessages[0], at.Add(time.Millisecond))} {
			path := journalScript(t, fmt.Sprintf("cat <<'DATA'\n%s\n%s\nDATA\n", rejected, trusted))
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			dirty, restored, persisted := false, false, false
			err := (Journal{Path: path}).RunReliable(ctx, JournalOptions{OnStatus: func(status JournalStatus) {
				if status.Reason == "malformed_record" || status.Reason == "untrusted_origin" {
					dirty = true
				}
				if status.State == "running" && status.Reason == "record_persisted" {
					if !persisted || !dirty {
						t.Fatal("readiness restored before trusted persistence")
					}
					restored = true
					cancel()
				}
			}}, func(_ context.Context, entry JournalEntry) error {
				if entry.SkipReason == "" || entry.SkipReason == "unrecognized_message" {
					persisted = true
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if !restored {
				t.Fatal("bad record left current coverage degraded after trusted persistence")
			}
		}
	}
}

// All addressing, users and endpoints below are synthetic, including cursors.
func sessionCloseRecord(t *testing.T, cursor, message string, at time.Time) string {
	t.Helper()
	data, err := json.Marshal(map[string]string{
		"__CURSOR": cursor, "__REALTIME_TIMESTAMP": fmt.Sprint(at.UnixMicro()),
		"MESSAGE": message, "SYSLOG_IDENTIFIER": "sshd-session", "_UID": "0",
		"_EXE": "/usr/lib/openssh/sshd-session", "_SYSTEMD_UNIT": "session-42.scope", "_TRANSPORT": "syslog",
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

var sessionCloseMessages = []string{
	"Connection closed by 192.0.2.42 port 42424",
	"Disconnected from user lab-user 192.0.2.42 port 42424",
	"pam_unix(sshd:session): session closed for user lab-user",
}

func TestJournalSessionCloseEntry(t *testing.T) {
	at := time.Now().UTC().Truncate(time.Microsecond)
	for _, message := range sessionCloseMessages {
		entry, ok := decodeJournalEntry([]byte(sessionCloseRecord(t, "synthetic-close", message, at)), at)
		if !ok || entry.Cursor != "synthetic-close" || !entry.ReceivedAt.Equal(at) || entry.Observation != nil || entry.SkipReason != "unrecognized_message" {
			t.Errorf("trusted close must checkpoint without auth or origin gap: %+v ok=%v", entry, ok)
		}
	}
	for _, unit := range []string{"ssh.service", "sshd.service", "ssh@lab.service", "sshd@1-192.0.2.1:22-192.0.2.2:23.service", "session-42.scope", "session-c7.scope"} {
		raw := strings.ReplaceAll(sessionCloseRecord(t, "synthetic-failure", "Failed password for lab-user from 192.0.2.42 port 42424 ssh2", at), "session-42.scope", unit)
		entry, ok := decodeJournalEntry([]byte(raw), at)
		if !ok || entry.SkipReason != "" || entry.Observation == nil || entry.Observation.Kind != AuthFailure {
			t.Errorf("lost real auth failure in %s: %+v", unit, entry)
		}
	}
}

func TestJournalSessionScopeRejectsUntrustedFields(t *testing.T) {
	at := time.Now().UTC()
	valid := sessionCloseRecord(t, "synthetic-rejected", sessionCloseMessages[0], at)
	for _, test := range []struct{ field, value, reason string }{
		{"_UID", `"1000"`, "untrusted_origin"},
		{"_UID", `"00"`, "untrusted_origin"},
		{"_UID", `"0\n"`, "untrusted_origin"},
		{"_EXE", `"/usr/bin/logger"`, "untrusted_origin"},
		{"_EXE", `"/tmp/sshd-session"`, "untrusted_origin"},
		{"_EXE", `"/usr/lib/openssh/sshd-session (deleted)"`, "untrusted_origin"},
		{"_EXE", `"/usr/lib/openssh/../openssh/sshd-session"`, "untrusted_origin"},
		{"_EXE", `"/usr/lib/openssh/sshd-session\u0000"`, "untrusted_origin"},
		{"_SYSTEMD_UNIT", `"user@1000.service"`, "untrusted_origin"},
		{"_SYSTEMD_USER_UNIT", `"sshd.service"`, "untrusted_origin"},
		{"_SYSTEMD_USER_UNIT", `"session-42.scope"`, "untrusted_origin"},
		{"_TRANSPORT", `"stdout"`, "untrusted_origin"},
		{"_TRANSPORT", `"kernel"`, "untrusted_origin"},
		{"_TRANSPORT", `"syslog\n"`, "untrusted_origin"},
	} {
		t.Run(test.field+test.value, func(t *testing.T) {
			var fields map[string]json.RawMessage
			if err := json.Unmarshal([]byte(valid), &fields); err != nil {
				t.Fatal(err)
			}
			fields[test.field] = json.RawMessage(test.value)
			raw, err := json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			entry, ok := decodeJournalEntry(raw, at)
			if !ok || entry.Observation != nil || entry.SkipReason != test.reason {
				t.Fatalf("bad source escaped validation: %+v ok=%v", entry, ok)
			}
		})
	}
	for _, field := range []string{"_UID", "_EXE", "_SYSTEMD_UNIT", "_SYSTEMD_USER_UNIT", "_TRANSPORT", "MESSAGE"} {
		for _, value := range []string{`["0","sshd"]`, `123`, `true`, `{}`} {
			var fields map[string]json.RawMessage
			if err := json.Unmarshal([]byte(valid), &fields); err != nil {
				t.Fatal(err)
			}
			fields[field] = json.RawMessage(value)
			raw, err := json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			entry, ok := decodeJournalEntry(raw, at)
			if !ok || entry.Observation != nil || entry.SkipReason != "malformed_record" {
				t.Fatalf("malformed %s=%s escaped validation: %+v ok=%v", field, value, entry, ok)
			}
		}
	}
	for _, field := range []string{"_UID", "_EXE", "_SYSTEMD_UNIT", "_TRANSPORT"} {
		for _, value := range []string{"", `""`, "null"} {
			var fields map[string]json.RawMessage
			if err := json.Unmarshal([]byte(valid), &fields); err != nil {
				t.Fatal(err)
			}
			delete(fields, field)
			if value != "" {
				fields[field] = json.RawMessage(value)
			}
			raw, err := json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			entry, ok := decodeJournalEntry(raw, at)
			if !ok || entry.Observation != nil || entry.SkipReason != "untrusted_origin" {
				t.Fatalf("missing %s escaped origin check: %+v ok=%v", field, entry, ok)
			}
		}
	}
}

func TestReliableJournalSessionCloseThenIdle(t *testing.T) {
	for _, message := range sessionCloseMessages {
		for _, fault := range []string{"", "untrusted_origin", "malformed_record"} {
			t.Run(message+"/"+fault, func(t *testing.T) {
				at := time.Now().UTC().Add(-time.Second)
				raw := sessionCloseRecord(t, "synthetic-close", message, at)
				wantSkip, wantState := "unrecognized_message", "running"
				if fault == "untrusted_origin" {
					raw = strings.ReplaceAll(raw, `"_UID":"0"`, `"_UID":"1000"`)
				} else if fault == "malformed_record" {
					raw = strings.ReplaceAll(raw, `"_UID":"0"`, `"_UID":["0","1000"]`)
				}
				if fault != "" {
					wantSkip, wantState = fault, "degraded"
				}
				path := journalScript(t, "cat <<'DATA'\n"+raw+"\nDATA\nexec sleep 30\n")
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				consumed := make(chan JournalEntry, 1)
				statuses := make(chan JournalStatus, 8)
				done := make(chan error, 1)
				go func() {
					done <- (Journal{Path: path}).RunReliable(ctx, JournalOptions{
						InitialObservedAt: at.Add(-time.Second), MaxBackfill: 100,
						OnStatus: func(status JournalStatus) { statuses <- status },
					}, func(_ context.Context, entry JournalEntry) error {
						consumed <- entry
						return nil
					})
				}()
				var entry JournalEntry
				select {
				case entry = <-consumed:
				case <-ctx.Done():
					t.Fatal("close was not delivered")
				}
				// Keep the child and stream open, with no subsequent records. This
				// exposes the old sticky degraded state instead of a process exit.
				var last JournalStatus
				idle := time.NewTimer(100 * time.Millisecond)
			waitIdle:
				for {
					select {
					case last = <-statuses:
					case <-idle.C:
						break waitIdle
					case err := <-done:
						t.Fatalf("fixture exited before quiet interval: %v", err)
					}
				}
				cancel()
				select {
				case <-done:
				case <-time.After(3 * time.Second):
					t.Fatal("journal child was not reaped")
				}
				if entry.SkipReason != wantSkip || entry.Observation != nil || last.State != wantState || fault != "" && last.Reason != fault {
					t.Fatalf("unexpected idle collector state: skip=%s observation=%v state=%s reason=%s; want %s/%s", entry.SkipReason, entry.Observation, last.State, last.Reason, wantSkip, wantState)
				}
			})
		}
	}
}

func TestReliableJournalSessionClosePersistenceFailureDoesNotRestoreHealth(t *testing.T) {
	at := time.Now().UTC().Add(-time.Second)
	path := journalScript(t, "cat <<'DATA'\nmalformed JSON\n"+sessionCloseRecord(t, "synthetic-close", sessionCloseMessages[0], at)+"\nDATA\nexec sleep 30\n")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	failed, dirty := false, false
	err := (Journal{Path: path}).RunReliable(ctx, JournalOptions{InitialObservedAt: at.Add(-time.Second), OnStatus: func(status JournalStatus) {
		if status.Reason == "malformed_record" {
			dirty = true
		}
		if status.State == "running" && status.Reason == "record_persisted" {
			t.Error("failed close persistence cleared degradation")
		}
		if status.State == "retrying" && status.Reason == "persist_failed" {
			failed = true
			cancel()
		}
	}}, func(_ context.Context, entry JournalEntry) error {
		if entry.Observation != nil || entry.SkipReason != "unrecognized_message" {
			t.Errorf("unexpected close entry: %+v", entry)
		}
		return errors.New("synthetic checkpoint failure")
	})
	if err != nil || !dirty || !failed {
		t.Fatalf("real parse/persistence failure hidden: dirty=%v failed=%v err=%v", dirty, failed, err)
	}
}

func TestReliableJournalQuietRestartDoesNotClaimPersistence(t *testing.T) {
	// Startup is only process liveness: a retry can start and stay silent
	// without acknowledging a single entry. Do not label it record_persisted.
	marker := filepath.Join(t.TempDir(), "started")
	path := journalScript(t, fmt.Sprintf("if [ ! -e '%s' ]; then\n  touch '%s'\n  exit 1\nfi\nexec sleep 30\n", marker, marker))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	statuses := make(chan JournalStatus, 16)
	done := make(chan error, 1)
	go func() {
		done <- (Journal{Path: path}).RunReliable(ctx, JournalOptions{
			InitialObservedAt: time.Now().UTC(), RestartMin: time.Millisecond, RestartMax: time.Millisecond,
			OnStatus: func(status JournalStatus) { statuses <- status },
		}, func(context.Context, JournalEntry) error {
			t.Error("quiet restart delivered an entry")
			return nil
		})
	}()
	starts, retries := 0, 0
	for starts < 2 {
		select {
		case status := <-statuses:
			if status.Reason == "record_persisted" {
				t.Error("startup invented persistence")
			}
			if status.State == "retrying" {
				retries++
			}
			if status.State == "running" && status.Reason == "process_started" {
				starts++
			}
		case <-ctx.Done():
			t.Fatal("fixture did not restart")
		}
	}
	select {
	case status := <-statuses:
		t.Errorf("quiet child changed state without evidence: %+v", status)
	case <-time.After(100 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-done:
		if err != nil || retries != 1 {
			t.Fatalf("unexpected restart result: retries=%d err=%v", retries, err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("quiet restart was not reaped")
	}
}
