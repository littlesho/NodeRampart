// SPDX-License-Identifier: MIT

package daemon

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/collector"
	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/enrich"
	"github.com/littlesho/NodeRampart/internal/privacy"
	"github.com/littlesho/NodeRampart/internal/store"
)

func TestJournalMissingRecoveryMarkerPreventsDaemonReadiness(t *testing.T) {
	dir := t.TempDir()
	database := filepath.Join(dir, "state.db")
	db, err := store.Open(database)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", database)
	if err != nil {
		t.Fatal(err)
	}
	_, err = raw.Exec(`DELETE FROM journal_recovery`)
	raw.Close()
	if err != nil {
		t.Fatal(err)
	}
	invoked := filepath.Join(dir, "invoked")
	script := filepath.Join(dir, "journalctl")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntouch '"+invoked+"'\nexec sleep 30\n"), 0700); err != nil {
		t.Fatal(err)
	}
	app := restartTestApp(t, database, script)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := app.Run(ctx); err == nil || !strings.Contains(err.Error(), "journal recovery state unavailable") {
		t.Fatalf("marker read failed silently: %v", err)
	}
	status := restartJournalStatus(app)
	if status.State != "degraded" || status.Reason != "recovery_state_unavailable" {
		t.Fatalf("marker read failure reason lost: %+v", status)
	}
	if _, err := os.Stat(invoked); !os.IsNotExist(err) {
		t.Fatalf("journal started without readiness state: %v", err)
	}
}

func restartTestApp(t *testing.T, database, script string) *App {
	t.Helper()
	db, err := store.Open(database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	geo, err := enrich.Open("", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = geo.Close() })
	transformer, err := privacy.New("prefix", "")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.Paths.Database = database
	cfg.Paths.SensorSocket = filepath.Join(filepath.Dir(database), "sensor.sock")
	cfg.Paths.ControlSocket = filepath.Join(filepath.Dir(database), "control.sock")
	cfg.Sensor.Enabled, cfg.Reports.Enabled = false, false
	cfg.Sensor.Interface = "lo"
	cfg.Auth.Journalctl = script
	// Drive the existing monitor evaluator with a controlled clock, without
	// a competing minute-ticker worker. App.Run and its collector are real.
	cfg.Alerts.Health.Enabled, cfg.Alerts.Budget.Enabled = false, false
	app, err := New(Options{Config: cfg, Store: db, Geo: geo, StorePrivacy: transformer, NotifyPrivacy: transformer, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	return app
}

func restartTestRun(t *testing.T, app *App) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.Run(ctx) }()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Errorf("App.Run: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Error("whole App.Run did not stop")
			}
		})
	}
	t.Cleanup(stop)
	return stop
}

func restartTestWait(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("daemon fixture did not reach expected state")
}

func restartJournalStatus(a *App) collector.JournalStatus {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.journalStatus
}

func restartHealthStep(t *testing.T, a *App, at time.Time) monitorData {
	t.Helper()
	ctx := context.Background()
	records, err := a.options.Store.MonitorStates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	previous := monitorData{}
	revision := int64(0)
	for _, record := range records {
		if record.Key == "health_ssh_journal" {
			previous, err = decodeMonitorData(record.Data, record.Key)
			if err != nil {
				t.Fatal(err)
			}
			revision = record.Revision
		}
	}
	values := make([]monitorObservation, 5)
	a.observeHealth(ctx, at, values, nil)
	in := values[2]
	in.Now = at
	in.Status.Key = "health_ssh_journal"
	in.Status.Enabled = true
	cfg := a.options.Config.Alerts
	cfg.Health.Enabled = true
	cfg.Health.GracePeriod.Duration = time.Minute
	cfg.Health.RecoveryPeriod.Duration = time.Minute
	next, events := evaluateMonitor(previous, in, cfg, a.started)
	raw, err := json.Marshal(next)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.options.Store.CommitMonitorState(ctx, store.MonitorState{Key: in.Status.Key, UpdatedAt: at, Data: raw}, revision, events, make([]*store.OutboxMessage, len(events))); err != nil {
		t.Fatal(err)
	}
	return next
}

func TestJournalPendingSurvivesWholeDaemonRestart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires POSIX journal fixture")
	}
	for _, scenario := range []string{"clean", "untrusted_origin", "malformed_record", "malformed_json", "recovered"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			dir := t.TempDir()
			database := filepath.Join(dir, "state.db")
			at := time.Now().UTC().Add(-time.Second).Truncate(time.Microsecond)
			record := func(cursor string) string {
				data, err := json.Marshal(map[string]string{"__CURSOR": cursor, "__REALTIME_TIMESTAMP": fmt.Sprint(at.UnixMicro()), "MESSAGE": "pam_unix(sshd:session): session closed for user lab-user", "_UID": "0", "_EXE": "/usr/sbin/sshd", "_SYSTEMD_UNIT": "ssh.service", "_TRANSPORT": "syslog"})
				if err != nil {
					t.Fatal(err)
				}
				return string(data)
			}
			script := filepath.Join(dir, "journalctl")
			gate := filepath.Join(dir, "emit-fault")
			fault := strings.ReplaceAll(record("rejected"), `"_UID":"0"`, `"_UID":"1000"`)
			if scenario == "malformed_record" {
				fault = strings.ReplaceAll(record("rejected"), `"_UID":"0"`, `"_UID":["0"]`)
			}
			if scenario == "malformed_json" {
				fault = "synthetic malformed JSON"
			}
			first := "#!/bin/sh\ncat <<'TRUSTED'\n" + record("trusted-before") + "\nTRUSTED\n"
			if scenario != "clean" {
				first += fmt.Sprintf("while [ ! -e '%s' ]; do sleep 0.01; done\ncat <<'FAULT'\n%s\nFAULT\n", gate, fault)
			}
			if scenario == "recovered" {
				first += fmt.Sprintf("while [ ! -e '%s' ]; do sleep 0.01; done\ncat <<'TRUSTED'\n%s\nTRUSTED\n", filepath.Join(dir, "emit-recovery"), record("trusted-after"))
			}
			first += "exec sleep 30\n"
			if err := os.WriteFile(script, []byte(first), 0700); err != nil {
				t.Fatal(err)
			}
			app := restartTestApp(t, database, script)
			stop := restartTestRun(t, app)
			restartTestWait(t, func() bool {
				p, e := app.options.Store.JournalCheckpoint(ctx)
				return e == nil && p.Cursor == "trusted-before" && restartJournalStatus(app).State == "running"
			})
			if state := restartHealthStep(t, app, at.Add(2*time.Minute)); state.Status.State != "healthy" {
				t.Fatalf("initial health: %+v", state.Status)
			}
			if scenario != "clean" {
				if err := os.WriteFile(gate, []byte("ready"), 0600); err != nil {
					t.Fatal(err)
				}
				restartTestWait(t, func() bool { s := restartJournalStatus(app); return s.State == "degraded" || s.State == "gap" })
				restartTestWait(t, func() bool {
					g, e := app.options.Store.CoverageGaps(ctx, at.Add(-time.Hour), time.Now().Add(time.Second), 100)
					if e != nil {
						return false
					}
					reason := "untrusted_origin"
					if strings.HasPrefix(scenario, "malformed") {
						reason = "malformed_record"
					}
					for _, gap := range g {
						if gap.Name == "ssh_journal" && gap.Reason == reason {
							return true
						}
					}
					return false
				})
				if scenario != "malformed_json" {
					restartTestWait(t, func() bool {
						p, e := app.options.Store.JournalCheckpoint(ctx)
						return e == nil && p.Cursor == "rejected"
					})
				}
				if state := restartHealthStep(t, app, at.Add(3*time.Minute)); state.Status.State != "alert" {
					t.Fatalf("degradation health: %+v", state.Status)
				}
			}
			if scenario == "recovered" {
				if err := os.WriteFile(filepath.Join(dir, "emit-recovery"), []byte("ready"), 0600); err != nil {
					t.Fatal(err)
				}
				restartTestWait(t, func() bool {
					s := restartJournalStatus(app)
					return s.State == "running" && s.Reason == "record_persisted"
				})
				restartHealthStep(t, app, at.Add(4*time.Minute))
				if state := restartHealthStep(t, app, at.Add(5*time.Minute)); state.Status.State != "healthy" {
					t.Fatal("trusted persistence did not recover before restart")
				}
			}
			stop()
			before, err := app.options.Store.JournalCheckpoint(ctx)
			if err != nil {
				t.Fatal(err)
			}
			oldGaps, err := app.options.Store.CoverageGaps(ctx, at.Add(-time.Hour), at.Add(time.Hour), 100)
			if err != nil {
				t.Fatal(err)
			}
			sshGaps := func(all []store.CoverageGap) []store.CoverageGap {
				var result []store.CoverageGap
				for _, g := range all {
					if g.Name == "ssh_journal" {
						result = append(result, g)
					}
				}
				return result
			}
			oldGaps = sshGaps(oldGaps)
			if err := app.options.Store.Close(); err != nil {
				t.Fatal(err)
			}
			app = nil // End the entire App/RunReliable lifetime, not one attempt.
			if err := os.WriteFile(script, []byte("#!/bin/sh\nexec sleep 30\n"), 0700); err != nil {
				t.Fatal(err)
			}
			restarted := restartTestApp(t, database, script)
			stopAgain := restartTestRun(t, restarted)
			restartTestWait(t, func() bool {
				s := restartJournalStatus(restarted)
				return s.Reason == "process_started" && s.State != "starting"
			})
			restartHealthStep(t, restarted, at.Add(6*time.Minute))
			final := restartHealthStep(t, restarted, at.Add(8*time.Minute))
			stopAgain()
			after, err := restarted.options.Store.JournalCheckpoint(ctx)
			if err != nil {
				t.Fatal(err)
			}
			newGaps, err := restarted.options.Store.CoverageGaps(ctx, at.Add(-time.Hour), at.Add(time.Hour), 100)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(oldGaps, sshGaps(newGaps)) {
				t.Fatal("quiet whole-daemon restart changed checkpoint or historical gaps")
			}
			want := "alert"
			if scenario == "clean" || scenario == "recovered" {
				want = "healthy"
			}
			if final.Status.State != want {
				t.Errorf("whole daemon restart without new records: health=%s want=%s journal=%+v checkpoint_unchanged=true retained_ssh_gaps=%d", final.Status.State, want, restartJournalStatus(restarted), len(oldGaps))
			}
			events, err := restarted.options.Store.Events(ctx, store.EventQuery{Start: at.Add(-time.Minute), End: at.Add(time.Hour), Kind: "health_ssh_journal", Limit: 20})
			if err != nil {
				t.Fatal(err)
			}
			recoveries := 0
			for _, e := range events {
				if e.Phase == "recovery" {
					recoveries++
				}
			}
			wantRecoveries := 0
			if scenario == "recovered" {
				wantRecoveries = 1
			}
			if recoveries != wantRecoveries {
				t.Errorf("whole daemon restart generated recovery without trusted ingest: recoveries=%d want=%d", recoveries, wantRecoveries)
			}
		})
	}
}
