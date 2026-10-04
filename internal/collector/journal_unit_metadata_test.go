// SPDX-License-Identifier: MIT

package collector

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// This reproduces the observed metadata shape using a synthetic cursor,
// account and address. A unitless record can still carry trusted root UID,
// an approved OpenSSH executable, and direct syslog/journal credentials.
func unitlessSSHRecord(t *testing.T, at time.Time, message string) string {
	t.Helper()
	var fields map[string]string
	if err := json.Unmarshal([]byte(sessionCloseRecord(t, "synthetic-unitless", message, at)), &fields); err != nil {
		t.Fatal(err)
	}
	delete(fields, "_SYSTEMD_UNIT")
	delete(fields, "_SYSTEMD_USER_UNIT")
	raw, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestJournalMissingUnitRetainsTrustedOpenSSH(t *testing.T) {
	at := time.Now().UTC()
	for _, message := range []string{
		"Failed password for lab-user from 192.0.2.42 port 42424 ssh2",
		"pam_unix(sshd:session): session closed for user lab-user",
	} {
		entry, ok := decodeJournalEntry([]byte(unitlessSSHRecord(t, at, message)), at)
		want := ""
		if message[0] == 'p' {
			want = "unrecognized_message"
		}
		if !ok || entry.Cursor != "synthetic-unitless" || entry.SkipReason != want || (entry.Observation == nil) != (want != "") {
			t.Fatalf("optional unit metadata rejected a trusted OpenSSH record: entry=%+v ok=%v", entry, ok)
		}
	}
}

func TestJournalMissingUnitStillRequiresStrongOrigin(t *testing.T) {
	at := time.Now().UTC()
	base := unitlessSSHRecord(t, at, "Failed password for lab-user from 192.0.2.42 port 42424 ssh2")
	for _, test := range []struct{ field, value string }{
		{"_UID", `"1000"`}, {"_UID", ""}, {"_UID", `null`},
		{"_EXE", `"/usr/bin/logger"`}, {"_EXE", ""}, {"_EXE", `null`},
		{"_TRANSPORT", `"stdout"`}, {"_TRANSPORT", `"kernel"`}, {"_TRANSPORT", ""},
		{"_SYSTEMD_USER_UNIT", `"sshd.service"`},
		{"_SYSTEMD_UNIT", `"other.service"`}, {"_SYSTEMD_UNIT", `""`}, {"_SYSTEMD_UNIT", `null`},
		{"_SYSTEMD_UNIT", `"session-4294967295.scope"`},
	} {
		t.Run(test.field+"/"+test.value, func(t *testing.T) {
			var fields map[string]json.RawMessage
			if err := json.Unmarshal([]byte(base), &fields); err != nil {
				t.Fatal(err)
			}
			delete(fields, test.field)
			if test.value != "" {
				fields[test.field] = json.RawMessage(test.value)
			}
			// Display tags and a forged implementation flag are never proof.
			fields["SYSLOG_IDENTIFIER"] = json.RawMessage(`"sshd"`)
			fields["_COMM"] = json.RawMessage(`"sshd"`)
			fields["unitMissing"] = json.RawMessage(`true`)
			raw, err := json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			entry, ok := decodeJournalEntry(raw, at)
			if !ok || entry.Observation != nil || entry.SkipReason != "untrusted_origin" {
				t.Fatalf("missing-unit compatibility bypassed origin validation: entry=%+v ok=%v", entry, ok)
			}
		})
	}
}

func TestReliableJournalMissingUnitRemainsAvailableWhenIdle(t *testing.T) {
	at := time.Now().UTC().Add(-time.Second)
	for _, scenario := range []string{"clean", "recovery", "duplicate"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			raw := unitlessSSHRecord(t, at, "pam_unix(sshd:session): session closed for user lab-user")
			path := journalScript(t, "cat <<'RECORD'\n"+raw+"\nRECORD\nexec sleep 30\n")
			consumed, degraded, recovered := 0, 0, 0
			var last JournalStatus
			err := (Journal{Path: path}).RunReliable(ctx, JournalOptions{
				InitialObservedAt: at.Add(-time.Second), InitialRecoveryPending: scenario != "clean",
				OnDegradation: func(context.Context, JournalStatus) error { degraded++; return nil },
				OnStatus: func(status JournalStatus) {
					last = status
					if status.Reason == "record_persisted" {
						recovered++
					}
				},
			}, func(_ context.Context, entry JournalEntry) error {
				consumed++
				if entry.Observation != nil || entry.SkipReason != "unrecognized_message" {
					t.Errorf("trusted unitless close should only checkpoint: %+v", entry)
				}
				time.AfterFunc(20*time.Millisecond, cancel)
				if scenario == "duplicate" {
					return ErrJournalAlreadyAcknowledged
				}
				return nil
			})
			wantRecovered, wantState := 0, "running"
			if scenario == "recovery" {
				wantRecovered = 1
			} else if scenario == "duplicate" {
				wantState = "retrying"
			}
			if err != nil || consumed != 1 || degraded != 0 || recovered != wantRecovered || last.State != wantState {
				t.Fatalf("unitless record damaged readiness or ACK semantics: consumed=%d degraded=%d recovered=%d last=%+v err=%v", consumed, degraded, recovered, last, err)
			}
		})
	}
}

func TestJournalRunMissingUnitMatchesReliableCollector(t *testing.T) {
	at := time.Now().UTC().Add(-time.Second)
	raw := unitlessSSHRecord(t, at, "Failed password for lab-user from 192.0.2.42 port 42424 ssh2")
	path := journalScript(t, "cat <<'RECORD'\n"+raw+"\nRECORD\n")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	observations := make(chan AuthObservation, 1)
	if err := (Journal{Path: path}).Run(ctx, observations); err != nil || len(observations) != 1 {
		t.Fatalf("legacy collector rejected optional unit metadata: observations=%d err=%v", len(observations), err)
	}
}
