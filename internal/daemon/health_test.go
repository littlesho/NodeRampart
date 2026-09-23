// SPDX-License-Identifier: MIT

package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/collector"
	"github.com/littlesho/NodeRampart/internal/store"
)

func TestStorageHeartbeatDoesNotHideRejectedIngest(t *testing.T) {
	app := &App{options: Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}}
	app.recordWrite(errors.New("synthetic storage failure"), "traffic", true)
	app.recordWrite(nil, "coverage", false)
	app.recordWrite(nil, "interface", true)
	if app.storageHealth.Healthy || len(app.storageHealth.FailedOperations) != 1 || app.storageHealth.FailedOperations[0] != "traffic" {
		t.Fatalf("unrelated write hid ingest failure: %+v", app.storageHealth)
	}
	app.recordWrite(nil, "traffic", true)
	if !app.storageHealth.Healthy || app.storageHealth.Failures != 1 || app.storageHealth.LastFailure.IsZero() || app.storageHealth.LastDurableIngest.IsZero() {
		t.Fatalf("recovery lost historical failure evidence: %+v", app.storageHealth)
	}
}

func TestJournalSessionCloseCheckpointAndCoverage(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("journal fixture requires POSIX shell")
	}
	for _, fault := range []string{"none", "untrusted_origin", "malformed_record"} {
		t.Run(fault, func(t *testing.T) {
			a := eventTestApp(t)
			ctx := context.Background()
			at := time.Now().UTC().Add(-time.Second).Truncate(time.Microsecond)
			// A pre-existing historical gap must survive both compatibility
			// handling and recovery. Never claim it has been backfilled.
			historical := store.CoverageGap{Name: "ssh_journal", Reason: "untrusted_origin", Start: at.Add(-time.Hour).Truncate(time.Millisecond), End: at.Add(-time.Minute).Truncate(time.Millisecond), Count: 1}
			if err := a.options.Store.RecordCoverageGap(ctx, historical); err != nil {
				t.Fatal(err)
			}
			record := func(cursor, message, unit, uid string) string {
				t.Helper()
				data, err := json.Marshal(map[string]string{
					"__CURSOR": cursor, "__REALTIME_TIMESTAMP": fmt.Sprint(at.UnixMicro()),
					"MESSAGE": message, "SYSLOG_IDENTIFIER": "sshd-session", "_UID": uid,
					"_EXE": "/usr/lib/openssh/sshd-session", "_SYSTEMD_UNIT": unit, "_TRANSPORT": "syslog",
				})
				if err != nil {
					t.Fatal(err)
				}
				return string(data)
			}
			stream := ""
			if fault == "untrusted_origin" {
				stream = record("synthetic-rejected", "Failed password for lab-user from 192.0.2.42 port 42424 ssh2", "session-42.scope", "1000") + "\n"
			} else if fault == "malformed_record" {
				stream = "malformed JSON\n"
			}
			stream += record("synthetic-connection-close", "Connection closed by 192.0.2.42 port 42424", "session-42.scope", "0") + "\n"
			stream += record("synthetic-pam-close", "pam_unix(sshd:session): session closed for user lab-user", "session-43.scope", "0") + "\n"
			path := filepath.Join(t.TempDir(), "journalctl")
			if err := os.WriteFile(path, []byte("#!/bin/sh\ncat <<'DATA'\n"+stream+"DATA\nexec sleep 30\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			runCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			var checkpointed []string
			err := (collector.Journal{Path: path}).RunReliable(runCtx, collector.JournalOptions{
				InitialObservedAt: at.Add(-time.Second),
				OnStatus: func(status collector.JournalStatus) {
					a.journalStatus = status
					if status.State == "gap" || status.State == "degraded" {
						if err := a.options.Store.RecordCoverageGap(ctx, store.CoverageGap{Name: "ssh_journal", Reason: status.Reason, Start: status.Since, End: status.At, Count: status.Count}); err != nil {
							t.Fatal(err)
						}
						values := make([]monitorObservation, 5)
						a.observeHealth(ctx, time.Now().UTC(), values, nil)
						if values[2].Condition != "failed" || values[2].Status.Reason != "journal_unavailable" {
							t.Errorf("real %s was hidden from health: %+v", fault, values[2])
						}
					}
				},
			}, func(ctx context.Context, entry collector.JournalEntry) error {
				if entry.Observation != nil {
					t.Error("close or rejected source became an auth observation")
				}
				if err := a.handleJournal(ctx, entry); err != nil {
					return err
				}
				checkpoint, err := a.options.Store.JournalCheckpoint(ctx)
				if err != nil || checkpoint.Cursor != entry.Cursor || !checkpoint.ObservedAt.Equal(at) {
					t.Fatalf("checkpoint did not advance: %+v %v", checkpoint, err)
				}
				checkpointed = append(checkpointed, entry.Cursor)
				if entry.Cursor == "synthetic-pam-close" {
					cancel()
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			want := []string{"synthetic-connection-close", "synthetic-pam-close"}
			if fault == "untrusted_origin" {
				want = append([]string{"synthetic-rejected"}, want...)
			}
			if !reflect.DeepEqual(checkpointed, want) {
				t.Fatalf("lost entries: %v", checkpointed)
			}
			if a.journalStatus.State != "running" || fault != "none" && a.journalStatus.Reason != "record_persisted" {
				t.Fatalf("trusted close did not restore current readiness: %+v", a.journalStatus)
			}
			summary, err := a.options.Store.Summary(ctx, at.Add(-2*time.Hour), at.Add(time.Hour), 10)
			if err != nil {
				t.Fatal(err)
			}
			if len(summary.Auth) != 0 || len(summary.Events) != 0 || a.auth.Stats().CanonicalFailures != 0 || a.auth.Stats().AuxiliaryObservations != 0 {
				t.Fatal("session close changed authentication totals or detector state")
			}
			wantGaps := 1
			if fault != "none" {
				wantGaps++
			}
			if len(summary.Gaps) != wantGaps || !reflect.DeepEqual(summary.Gaps[len(summary.Gaps)-1], historical) {
				t.Fatalf("historical gaps altered or false gaps created: %+v", summary.Gaps)
			}
			if fault != "none" && summary.Gaps[0].Reason != fault {
				t.Fatalf("real rejection/parse gap was hidden: %+v", summary.Gaps)
			}
		})
	}
}

func TestJournalClosePersistenceFailureRetainsCheckpoint(t *testing.T) {
	a := eventTestApp(t)
	ctx := context.Background()
	at := time.Now().UTC().Truncate(time.Microsecond)
	before := collector.JournalEntry{Cursor: "synthetic-before", ReceivedAt: at, ObservedAt: at, SkipReason: "unrecognized_message"}
	if err := a.handleJournal(ctx, before); err != nil {
		t.Fatal(err)
	}
	eventTestBudget(t, a, true)
	closeEntry := collector.JournalEntry{Cursor: "synthetic-close", ReceivedAt: at.Add(time.Second), ObservedAt: at.Add(time.Second), SkipReason: "unrecognized_message"}
	if err := a.handleJournal(ctx, closeEntry); err == nil {
		t.Fatal("failed close checkpoint was acknowledged")
	}
	checkpoint, err := a.options.Store.JournalCheckpoint(ctx)
	if err != nil || checkpoint.Cursor != before.Cursor || !a.monitorStorageFailure() || a.pendingJournal == nil {
		t.Fatalf("failed checkpoint or storage health hidden: %+v %v", checkpoint, err)
	}
	eventTestBudget(t, a, false)
	if err := a.handleJournal(ctx, closeEntry); err != nil {
		t.Fatal(err)
	}
	checkpoint, err = a.options.Store.JournalCheckpoint(ctx)
	if err != nil || checkpoint.Cursor != closeEntry.Cursor || a.monitorStorageFailure() || a.pendingJournal != nil {
		t.Fatalf("close checkpoint retry failed: %+v %v", checkpoint, err)
	}
}

func TestMonitorSSHDegradationDoesNotExpire(t *testing.T) {
	a := eventTestApp(t)
	at := time.Now().UTC()
	for _, reason := range []string{"untrusted_origin", "malformed_record", "persist_failed"} {
		a.journalStatus = collector.JournalStatus{State: "degraded", Reason: reason, Since: at, At: at}
		for _, now := range []time.Time{at, at.Add(48 * time.Hour)} {
			values := make([]monitorObservation, 5)
			a.observeHealth(context.Background(), now, values, nil)
			if values[2].Condition != "failed" || values[2].Status.Reason != "journal_unavailable" || !values[2].Since.Equal(at) || a.journalStatus.Reason != reason {
				t.Fatalf("quiet period hid %s: %+v", reason, values[2])
			}
		}
	}
}
