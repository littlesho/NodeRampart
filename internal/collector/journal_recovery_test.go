// SPDX-License-Identifier: MIT

package collector

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func recoveryFaults(t *testing.T, at time.Time) map[string]string {
	t.Helper()
	valid := reliableRecord(t, "rejected", at)
	return map[string]string{
		"untrusted_origin":  strings.ReplaceAll(valid, `"_UID":"0"`, `"_UID":"1000"`),
		"malformed_message": strings.ReplaceAll(valid, `"MESSAGE":"Failed password for root from 192.0.2.7 port 54321 ssh2"`, `"MESSAGE":[65,66]`),
		"malformed_json":    "malformed JSON",
		"oversized":         strings.Repeat("x", 257<<10),
	}
}

// The first child emits a fault and exits. The second exits without output.
// Later children either remain quiet or replay the inclusive checkpoint boundary
// followed by a new entry. No restart itself supplies recovery evidence.
func recoveryJournal(t *testing.T, fault, afterRestart string) string {
	t.Helper()
	count := filepath.Join(t.TempDir(), "launches")
	first := ""
	if fault != "" {
		first = "cat <<'FAULT'\n" + fault + "\nFAULT\n"
	}
	boundary := ""
	if entry, ok := decodeJournalEntry([]byte(fault), time.Now()); ok {
		boundary = fmt.Sprintf("case \"$*\" in *--cursor=%s*) cat <<'BOUNDARY'\n%s\nBOUNDARY\n;; esac\n", entry.Cursor, fault)
	}
	return journalScript(t, fmt.Sprintf(`n=0
if [ -f '%s' ]; then read -r n < '%s'; fi
n=$((n + 1))
printf '%%s\n' "$n" > '%s'
case "$n" in
1) %s
exit 1;;
2) exit 1;;
esac
%s
%s
exec sleep 30
`, count, count, count, first, boundary, afterRestart))
}

func TestReliableJournalQualityDegradationSurvivesQuietRestarts(t *testing.T) {
	at := time.Now().UTC().Add(-time.Second)
	for name, fault := range recoveryFaults(t, at) {
		t.Run(name, func(t *testing.T) {
			path := recoveryJournal(t, fault, "")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			starts, gaps, consumed := 0, 0, 0
			dirty := false
			var last JournalStatus
			err := (Journal{Path: path}).RunReliable(ctx, JournalOptions{
				InitialObservedAt: at.Add(-time.Second), RestartMin: time.Millisecond, RestartMax: time.Millisecond,
				OnStatus: func(status JournalStatus) {
					last = status
					if status.State == "gap" || status.State == "degraded" {
						dirty = true
						gaps++
					}
					if dirty && status.State == "running" {
						t.Errorf("restart restored readiness without trusted persistence: %+v", status)
					}
					if status.Reason == "process_started" && status.State != "starting" {
						starts++
						if starts == 3 {
							time.AfterFunc(50*time.Millisecond, cancel)
						}
					}
				},
			}, func(_ context.Context, entry JournalEntry) error {
				consumed++
				if entry.Observation != nil || entry.SkipReason == "unrecognized_message" {
					t.Errorf("fixture supplied a trusted record: %+v", entry)
				}
				return nil
			})
			wantConsumed := 0
			if _, ok := decodeJournalEntry([]byte(fault), at); ok {
				wantConsumed = 1
			}
			if err != nil || starts != 3 || gaps != 1 || consumed != wantConsumed || last.State == "running" {
				t.Fatalf("quiet restart result: starts=%d gaps=%d consumed=%d last=%+v err=%v", starts, gaps, consumed, last, err)
			}
		})
	}
}

func TestReliableJournalRestartRecoveryRequiresConsumerAcknowledgement(t *testing.T) {
	at := time.Now().UTC().Add(-time.Second)
	for name, fault := range recoveryFaults(t, at) {
		t.Run(name, func(t *testing.T) {
			closeRecord := strings.ReplaceAll(reliableRecord(t, "close", at.Add(time.Millisecond)), "Failed password for root from 192.0.2.7 port 54321 ssh2", "pam_unix(sshd:session): session closed for user lab-user")
			path := recoveryJournal(t, fault, "cat <<'CLOSE'\n"+closeRecord+"\nCLOSE")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			attempts, recovered, starts := 0, 0, 0
			pending, committed, persistFailed := false, false, false
			err := (Journal{Path: path}).RunReliable(ctx, JournalOptions{
				InitialObservedAt: at.Add(-time.Second), RestartMin: time.Millisecond, RestartMax: time.Millisecond,
				OnStatus: func(status JournalStatus) {
					if status.State == "gap" || status.State == "degraded" {
						pending = true
					}
					if status.Reason == "process_started" && status.State != "starting" {
						starts++
					}
					if status.Reason == "persist_failed" {
						persistFailed = true
					}
					if pending && status.State == "running" {
						if !committed || status.Reason != "record_persisted" {
							t.Errorf("premature recovery: %+v", status)
							return
						}
						recovered++
						cancel()
					}
				},
			}, func(_ context.Context, entry JournalEntry) error {
				if entry.Cursor != "close" {
					return nil
				}
				if entry.SkipReason != "unrecognized_message" || entry.Observation != nil {
					t.Fatalf("trusted close was not a checkpoint-only entry: %+v", entry)
				}
				attempts++
				if attempts == 1 {
					return errors.New("synthetic persistence failure")
				}
				committed = true
				return nil
			})
			if err != nil || starts != 4 || attempts != 2 || recovered != 1 || !persistFailed {
				t.Fatalf("recovery/acknowledgement lost: starts=%d attempts=%d recovered=%d persist_failed=%v err=%v", starts, attempts, recovered, persistFailed, err)
			}
		})
	}
}

func TestReliableJournalQuietStartupAndProcessRetriesRemainAvailable(t *testing.T) {
	path := recoveryJournal(t, "", "")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	starts, backfillGaps := 0, 0
	var last JournalStatus
	err := (Journal{Path: path}).RunReliable(ctx, JournalOptions{
		RestartMin: time.Millisecond, RestartMax: time.Millisecond,
		OnStatus: func(status JournalStatus) {
			last = status
			if status.State == "gap" && status.Reason == "backfill_time_limit" {
				backfillGaps++
			}
			if status.Reason == "record_persisted" {
				t.Error("quiet child falsely claimed persistence")
			}
			if status.Reason == "process_started" && status.State != "starting" {
				starts++
				if status.State != "running" {
					t.Errorf("clean quiet child was unavailable: %+v", status)
				}
				if starts == 3 {
					time.AfterFunc(50*time.Millisecond, cancel)
				}
			}
		},
	}, func(context.Context, JournalEntry) error {
		t.Error("quiet child delivered a record")
		return nil
	})
	if err != nil || starts != 3 || backfillGaps == 0 || last.State != "running" {
		t.Fatalf("quiet startup semantics changed: starts=%d backfill_gaps=%d status=%+v err=%v", starts, backfillGaps, last, err)
	}
}
