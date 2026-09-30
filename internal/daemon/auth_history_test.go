// SPDX-License-Identifier: MIT

package daemon

import (
	"context"
	"database/sql"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/collector"
	"github.com/littlesho/NodeRampart/internal/model"
	"github.com/littlesho/NodeRampart/internal/privacy"
	"github.com/littlesho/NodeRampart/internal/store"
)

func authHintHistory(t *testing.T, mode string) (*App, collector.AuthObservation) {
	t.Helper()
	a := eventTestApp(t)
	a.options.Config.Auth.HistoryHintsEnabled = true
	key := ""
	if mode == "hash" {
		key = filepath.Join(t.TempDir(), "key")
		if err := os.WriteFile(key, []byte(strings.Repeat("synthetic-", 8)), 0600); err != nil {
			t.Fatal(err)
		}
	}
	transformer, err := privacy.New(mode, key)
	if err != nil {
		t.Fatal(err)
	}
	a.options.StorePrivacy = transformer
	now := time.Now().UTC().Truncate(time.Millisecond)
	from := now.Add(-authHistoryObservation)
	a.started = from.Add(-2 * 24 * time.Hour)
	a.report.Location = time.FixedZone("synthetic+0545", 5*3600+45*60)
	// Simulate an existing seven-day deployment in this temporary fixture,
	// including the actual retention tracking start rather than ignoring it.
	db, err := sql.Open("sqlite", a.options.Config.Paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE retention_meta SET tracking_started=? WHERE id=1`, a.started.UnixMilli()); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()
	if err := a.options.Store.SetComponentStatus(context.Background(), "ssh_journal", "running", a.started); err != nil {
		t.Fatal(err)
	}
	if err := a.options.Store.SetComponentStatus(context.Background(), "ssh_journal", "running", now); err != nil {
		t.Fatal(err)
	}
	ip, sourceRange := transformer.IP("192.0.2.7")
	for i := 0; i < 28; i++ {
		local := now.In(a.report.Location).AddDate(0, 0, -(i%7 + 1))
		at := time.Date(local.Year(), local.Month(), local.Day(), 12, 0, 0, 0, a.report.Location)
		if at.Before(from) {
			at = from.Add(time.Hour)
		}
		if err := a.options.Store.InsertEvent(context.Background(), model.Event{ID: fmt.Sprintf("historical-%d", i), ObservedAt: at, Kind: "ssh_login_success", Severity: model.SeverityInfo, SourceIP: ip, SourceRange: sourceRange}); err != nil {
			t.Fatal(err)
		}
	}
	observation := collector.AuthObservation{Kind: collector.AuthSuccess, ObservedAt: now, SourceIP: netip.MustParseAddr("198.51.100.8"), Method: "publickey", User: "synthetic"}
	return a, observation
}

func TestSSHHistoryHintsRespectPrivacyAndObservedHour(t *testing.T) {
	for _, mode := range []string{"prefix", "hash", "full"} {
		t.Run(mode, func(t *testing.T) {
			a, observation := authHintHistory(t, mode)
			event := a.auth.Observe(observation)
			a.addAuthHistoryHints(context.Background(), observation, event)
			want := "first_observed_source"
			if mode == "prefix" {
				want = "first_observed_prefix"
			}
			if event.Evidence["history_hint_state"] != "available" || event.Evidence["history_source_hint"] != want {
				t.Fatal(event.Evidence)
			}
			if observation.ObservedAt.In(a.report.Location).Hour() != 12 && event.Evidence["history_time_hint"] != "unseen_local_hour" {
				t.Fatal("unseen local hour hidden", event.Evidence)
			}
			observation.SourceIP = netip.MustParseAddr("192.0.2.9")
			local := observation.ObservedAt.In(a.report.Location)
			observation.ObservedAt = time.Date(local.Year(), local.Month(), local.Day(), 12, 0, 0, 0, a.report.Location)
			// Keep the evaluated endpoint at or before the recorded coverage tail.
			if observation.ObservedAt.After(time.Now()) {
				observation.ObservedAt = observation.ObservedAt.AddDate(0, 0, -1)
			}
			event = a.auth.Observe(observation)
			a.addAuthHistoryHints(context.Background(), observation, event)
			if event.Evidence["history_hint_state"] != "available" {
				t.Fatal("known prefix/hour case had no usable history", event.Evidence)
			}
			if mode == "prefix" && event.Evidence["history_source_hint"] != "" {
				t.Fatal("shared prefix was treated as exact identity", event.Evidence)
			}
			if event.Evidence["history_time_hint"] != "" {
				t.Fatal("observed local hour mislabeled", event.Evidence)
			}
		})
	}
}

func TestSSHHistoryDisabledColdStartAndCoverageGapsNeverHint(t *testing.T) {
	a, observation := authHintHistory(t, "prefix")
	a.options.Config.Auth.HistoryHintsEnabled = false
	event := a.auth.Observe(observation)
	a.addAuthHistoryHints(context.Background(), observation, event)
	if event.Evidence["history_hint_state"] != "" {
		t.Fatal("disabled hint modified event", event.Evidence)
	}
	a.options.Config.Auth.HistoryHintsEnabled = true
	a.started = observation.ObservedAt.Add(-time.Hour)
	event = a.auth.Observe(observation)
	a.addAuthHistoryHints(context.Background(), observation, event)
	if event.Evidence["history_hint_state"] != "observing" || event.Evidence["history_source_hint"] != "" {
		t.Fatal("cold start invented rare source", event.Evidence)
	}
	a.started = observation.ObservedAt.Add(-8 * 24 * time.Hour)
	if err := a.options.Store.RecordCoverageGap(context.Background(), store.CoverageGap{Name: "ssh_journal", Reason: "synthetic_gap", Start: observation.ObservedAt.Add(-time.Hour), End: observation.ObservedAt, Count: 0}); err != nil {
		t.Fatal(err)
	}
	event = a.auth.Observe(observation)
	a.addAuthHistoryHints(context.Background(), observation, event)
	if event.Evidence["history_hint_state"] != "history_incomplete" || event.Evidence["history_source_hint"] != "" || event.Evidence["history_time_hint"] != "" {
		t.Fatal("incomplete history became anomalous", event.Evidence)
	}
}

func TestSSHHistoryCoverageRejectsPrunedAndUnknownBaseline(t *testing.T) {
	now := time.Now().UTC()
	from := now.Add(-authHistoryObservation)
	view := store.IntegrityView{Components: []store.IntegrityComponent{{Name: "ssh_journal", RunningMS: authHistoryObservation.Milliseconds()}}, Retention: &store.RetentionView{TrackingStarted: from.Add(-time.Hour)}}
	if !authHistoryCoverage(view, from, now) {
		t.Fatal("complete baseline rejected")
	}
	view.Retention.Entries = []store.RetentionEntry{{Dataset: "events"}}
	if authHistoryCoverage(view, from, now) {
		t.Fatal("pruned events treated as complete")
	}
	view.Retention.Entries = nil
	view.Retention.TrackingStarted = from.Add(time.Hour)
	if authHistoryCoverage(view, from, now) {
		t.Fatal("unknown historical pruning treated as zero")
	}
}

func TestSSHHistoryKeyAndConfigurationRestartBeginsNewObservation(t *testing.T) {
	a, observation := authHintHistory(t, "hash")
	key := filepath.Join(t.TempDir(), "replacement-key")
	if err := os.WriteFile(key, []byte(strings.Repeat("different-synthetic-", 4)), 0600); err != nil {
		t.Fatal(err)
	}
	transformer, err := privacy.New("hash", key)
	if err != nil {
		t.Fatal(err)
	}
	options := a.options
	options.StorePrivacy = transformer
	options.Config.Reports.Timezone = "Asia/Kathmandu"
	restarted, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	observation.ObservedAt = time.Now().UTC()
	event := restarted.auth.Observe(observation)
	restarted.addAuthHistoryHints(context.Background(), observation, event)
	if event.Evidence["history_hint_state"] != "observing" || event.Evidence["history_source_hint"] != "" || event.Evidence["history_time_hint"] != "" {
		t.Fatal("new key/config inherited precise old baseline", event.Evidence)
	}
}
