// SPDX-License-Identifier: MIT

package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/collector"
	"github.com/littlesho/NodeRampart/internal/store"
)

func TestJournalQualityRecoveryAcrossChildRestarts(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("journal fixture requires a POSIX shell")
	}
	for _, fault := range []string{"untrusted_origin", "malformed_record"} {
		t.Run(fault, func(t *testing.T) {
			a := eventTestApp(t)
			a.options.Notifier = nil
			a.options.Config.Alerts.Health.Enabled = true
			a.options.Config.Alerts.Health.GracePeriod.Duration = time.Minute
			a.options.Config.Alerts.Health.RecoveryPeriod.Duration = time.Minute
			ctx := context.Background()
			at := time.Now().UTC().Add(-time.Second).Truncate(time.Microsecond)
			a.started = at.Add(-time.Hour)
			historical := store.CoverageGap{Name: "ssh_journal", Reason: "untrusted_origin", Start: at.Add(-time.Hour).Truncate(time.Millisecond), End: at.Add(-time.Minute).Truncate(time.Millisecond), Count: 1}
			if err := a.options.Store.RecordCoverageGap(ctx, historical); err != nil {
				t.Fatal(err)
			}
			record := func(cursor, message string) string {
				t.Helper()
				data, err := json.Marshal(map[string]string{
					"__CURSOR": cursor, "__REALTIME_TIMESTAMP": fmt.Sprint(at.UnixMicro()),
					"MESSAGE": message, "_UID": "0", "_EXE": "/usr/sbin/sshd",
					"_SYSTEMD_UNIT": "ssh.service", "_TRANSPORT": "syslog",
				})
				if err != nil {
					t.Fatal(err)
				}
				return string(data)
			}
			before := record("before", "Failed password for lab-user from 192.0.2.7 port 54321 ssh2")
			rejected := record("rejected", "Connection closed by 192.0.2.7 port 54321")
			if fault == "untrusted_origin" {
				rejected = strings.ReplaceAll(rejected, `"_UID":"0"`, `"_UID":"1000"`)
			} else {
				rejected = strings.ReplaceAll(rejected, `"MESSAGE":"Connection closed by 192.0.2.7 port 54321"`, `"MESSAGE":[65,66]`)
			}
			closeRecord := record("close", "pam_unix(sshd:session): session closed for user lab-user")
			after := record("after", "Failed password for lab-user from 192.0.2.7 port 54322 ssh2")
			dir := t.TempDir()
			path, count := filepath.Join(dir, "journalctl"), filepath.Join(dir, "launches")
			script := fmt.Sprintf(`#!/bin/sh
n=0
if [ -f '%s' ]; then read -r n < '%s'; fi
n=$((n + 1))
printf '%%s\n' "$n" > '%s'
case "$n" in
1) cat <<'FIRST'
%s
%s
FIRST
exit 1;;
2) exit 1;;
esac
case "$*" in *--cursor=rejected*) cat <<'BOUNDARY'
%s
BOUNDARY
;; *) exit 9;; esac
cat <<'RECOVERY'
%s
%s
RECOVERY
exec sleep 30
`, count, count, count, before, rejected, rejected, closeRecord, after)
			if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			monitor := newMonitorRuntime(a)
			clock := at.Add(time.Second)
			observe := func(want string) {
				t.Helper()
				clock = clock.Add(time.Minute)
				values := make([]monitorObservation, 5)
				a.observeHealth(ctx, clock, values, nil)
				value := values[2]
				value.Now = clock
				value.Status.Key, value.Status.Enabled = "health_ssh_journal", true
				monitor.pass(ctx, clock, []monitorObservation{value})
				if got := monitor.states["health_ssh_journal"].Status.State; got != want {
					t.Errorf("journal %+v mapped to health %s, want %s", a.journalStatus, got, want)
				}
			}
			checkpoint := func(want string) {
				t.Helper()
				value, err := a.options.Store.JournalCheckpoint(ctx)
				if err != nil || value.Cursor != want {
					t.Fatalf("checkpoint=%+v want=%s err=%v", value, want, err)
				}
			}
			pending, recovered, persistFailed := false, false, false
			starts, closeAttempts := 0, 0
			runCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			err := (collector.Journal{Path: path}).RunReliable(runCtx, collector.JournalOptions{
				InitialObservedAt: at.Add(-time.Second), RestartMin: time.Millisecond, RestartMax: time.Millisecond,
				OnStatus: func(status collector.JournalStatus) {
					a.journalStatus = status
					if status.State == "gap" || status.State == "degraded" {
						if err := a.options.Store.RecordCoverageGap(ctx, store.CoverageGap{Name: "ssh_journal", Reason: status.Reason, Start: status.Since, End: status.At, Count: status.Count}); err != nil {
							t.Fatal(err)
						}
						pending = true
						observe("alert")
					}
					if status.Reason == "persist_failed" {
						persistFailed = true
						checkpoint("rejected")
						if !a.monitorStorageFailure() {
							t.Error("persistence failure hidden from storage health")
						}
						eventTestBudget(t, a, false)
					}
					if status.Reason == "process_started" && status.State != "starting" {
						starts++
						if pending {
							// Observe a quiet interval beyond both configured periods,
							// while the restarted child has supplied no new records.
							observe("alert")
							observe("alert")
							for _, event := range monitorEvents(t, a, clock) {
								if event.Kind == "health_ssh_journal" && event.Phase == "recovery" {
									t.Error("quiet child restart generated a recovery event")
								}
							}
						} else {
							observe("healthy")
						}
					}
					if status.State == "running" && status.Reason == "record_persisted" {
						checkpoint("close")
						if closeAttempts != 2 || a.monitorStorageFailure() {
							t.Error("recovery preceded successful checkpoint retry")
						}
						recovered, pending = true, false
						observe("recovering")
						observe("healthy")
					}
				},
			}, func(ctx context.Context, entry collector.JournalEntry) error {
				if entry.Cursor == "close" {
					closeAttempts++
					if entry.Observation != nil || entry.SkipReason != "unrecognized_message" {
						t.Error("close became an authentication event")
					}
					if closeAttempts == 1 {
						eventTestBudget(t, a, true)
					}
				}
				err := a.handleJournal(ctx, entry)
				if entry.Cursor == "close" && closeAttempts == 1 {
					if err == nil {
						t.Fatal("failed persistence acknowledged")
					}
					checkpoint("rejected")
				}
				if entry.Cursor == "after" && err == nil {
					cancel()
				}
				return err
			})
			if err != nil || starts != 4 || !persistFailed || !recovered {
				t.Fatalf("incomplete recovery: starts=%d persist_failed=%v recovered=%v err=%v", starts, persistFailed, recovered, err)
			}
			checkpoint("after")
			summary, err := a.options.Store.Summary(ctx, at.Add(-2*time.Hour), clock.Add(time.Hour), 10)
			if err != nil {
				t.Fatal(err)
			}
			if len(summary.Auth) != 1 || summary.Auth[0].Count != 2 || a.auth.Stats().CanonicalFailures != 2 || a.auth.Stats().AuxiliaryObservations != 0 {
				t.Fatalf("auth counts changed across rejection/close/retry: %+v %+v", summary.Auth, a.auth.Stats())
			}
			if len(summary.Gaps) != 2 || summary.Gaps[0].Reason != fault || !reflect.DeepEqual(summary.Gaps[1], historical) {
				t.Fatalf("historical coverage changed or duplicate gaps created: %+v", summary.Gaps)
			}
			events := monitorEvents(t, a, clock)
			if len(events) != 2 || events[0].Phase != "recovery" || events[1].Phase != "start" || events[0].IncidentID != events[1].IncidentID {
				t.Fatalf("unexpected health incident transitions: %+v", events)
			}
		})
	}
}
