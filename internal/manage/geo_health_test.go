// SPDX-License-Identifier: MIT

package manage

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/assets"
)

func TestGeoPublicHealthTracksFailuresAndUnchangedRecovery(t *testing.T) {
	m, _ := fixtureManager(t)
	fail := false
	fixture := newManagedGeoFixture()
	m.Assets = syntheticGeoClientWithFixture(t, &fail, fixture)
	input := map[string]string{"account_id": "123", "license_key": "synthetic-private-key", "accepted_terms": "yes", "auto_update": "yes"}
	read := func() assets.Health {
		t.Helper()
		data, err := os.ReadFile(m.localPath("geoip-health.json"))
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{"synthetic-private-key", "account_id", "license_key", "generation", m.ConfigPath} {
			if strings.Contains(string(data), secret) {
				t.Fatal("public metadata contains excluded data")
			}
		}
		var value assets.Health
		if json.Unmarshal(data, &value) != nil || value.Validate() != nil {
			t.Fatal("invalid public metadata")
		}
		info, err := os.Stat(m.localPath("geoip-health.json"))
		if err != nil || info.Mode().Perm() != 0o640 {
			t.Fatal("public metadata permissions changed")
		}
		return value
	}
	if _, err := m.Action(context.Background(), "geo_download", input); err != nil {
		t.Fatal(err)
	}
	first := read()
	if first.Result != "ok" || !first.Scheduled || first.LastSuccessAt.IsZero() {
		t.Fatal("initial success missing")
	}
	fail = true
	for want := uint32(1); want <= 2; want++ {
		if _, err := m.Action(context.Background(), "geo_refresh", nil); err == nil {
			t.Fatal("failure fixture unexpectedly succeeded")
		}
		value := read()
		if value.Result != "download_failed" || value.ConsecutiveFailures != want || !value.LastSuccessAt.Equal(first.LastSuccessAt) {
			t.Fatalf("failure streak or successful timestamp lost: %+v", value)
		}
	}
	fail = false
	if _, err := m.Action(context.Background(), "geo_refresh", nil); err != nil {
		t.Fatal(err)
	}
	recovered := read()
	if recovered.Result != "unchanged" || recovered.ConsecutiveFailures != 0 || !recovered.LastSuccessAt.After(first.LastSuccessAt) || !recovered.Scheduled {
		t.Fatal("unchanged check did not recover health")
	}
	if _, err := m.Action(context.Background(), "geo_schedule", map[string]string{"enabled": "no"}); err != nil {
		t.Fatal(err)
	}
	disabled := read()
	if disabled.Scheduled || !disabled.LastSuccessAt.Equal(recovered.LastSuccessAt) {
		t.Fatal("schedule action fabricated a successful download")
	}
}

func TestGeoHealthBootstrapsLegacyTimerAndCountsNestedFailureOnce(t *testing.T) {
	m, fake, _, _ := installedGeoFixture(t)
	if err := os.Remove(m.localPath("geoip-health.json")); err != nil {
		t.Fatal(err)
	}
	fake.units["noderampart-geoip-update.timer"] = "enabled"
	if _, err := m.Action(context.Background(), "geo_refresh", nil); err != nil {
		t.Fatal(err)
	}
	got := m.previousGeoHealth()
	if !got.Scheduled || got.Result != "unchanged" {
		t.Fatal("legacy schedule not discovered")
	}
	runner := m.runner
	m.runner = func(ctx context.Context, program string, args ...string) (string, error) {
		if len(args) > 0 && args[0] == "enable" {
			return "", errors.New("synthetic scheduling failure")
		}
		return runner(ctx, program, args...)
	}
	if _, err := m.Action(context.Background(), "geo_download", map[string]string{"account_id": "123", "license_key": "synthetic-key", "accepted_terms": "yes", "auto_update": "yes"}); err == nil {
		t.Fatal("schedule failure accepted")
	}
	got = m.previousGeoHealth()
	if got.Result != "unchanged" || got.ConsecutiveFailures != 0 || got.Schedule == nil || got.Schedule.Result != "failed" || got.Schedule.ConsecutiveFailures != 1 {
		t.Fatal("nested schedule failure replaced successful verification or was counted twice")
	}
}

func failGeoSchedule(m *Manager, fail *bool) {
	runner := m.runner
	m.runner = func(ctx context.Context, program string, args ...string) (string, error) {
		if *fail && len(args) > 0 && (args[0] == "enable" || args[0] == "disable") {
			return "", errors.New("synthetic scheduling failure")
		}
		return runner(ctx, program, args...)
	}
}

func TestGeoSchedulingRecoveryPreservesUpdateOutcomeAcrossManagerRestart(t *testing.T) {
	m, _, _, downloadFailure := installedGeoFixture(t)
	ctx := context.Background()
	first := m.previousGeoHealth()
	failed := true
	failGeoSchedule(m, &failed)
	for want := uint32(1); want <= 2; want++ {
		if _, err := m.Action(ctx, "geo_schedule", map[string]string{"enabled": "yes"}); err == nil {
			t.Fatal("failure fixture succeeded")
		}
		h := m.previousGeoHealth()
		if h.Result != first.Result || h.CheckedAt != first.CheckedAt || h.LastSuccessAt != first.LastSuccessAt || h.ConsecutiveFailures != 0 || h.Scheduled || h.Schedule == nil || h.Schedule.Result != "failed" || h.Schedule.ConsecutiveFailures != want {
			t.Fatalf("channels merged: %+v", h)
		}
	}
	// A new Manager has no health cache; only the sanitized persisted file
	// carries both outcomes across invocations/restarts.
	copy := *m
	m = &copy
	failed = false
	if _, err := m.Action(ctx, "geo_schedule", map[string]string{"enabled": "yes"}); err != nil {
		t.Fatal(err)
	}
	h := m.previousGeoHealth()
	if !h.Scheduled || h.Schedule.Result != "ok" || h.Schedule.ConsecutiveFailures != 0 || h.Result != first.Result || h.LastSuccessAt != first.LastSuccessAt || h.CheckedAt != first.CheckedAt {
		t.Fatal("schedule recovery erased or fabricated update verification")
	}
	*downloadFailure = true
	if _, err := m.Action(ctx, "geo_refresh", nil); err == nil {
		t.Fatal("download failure fixture succeeded")
	}
	updateFailed := m.previousGeoHealth()
	failed = true
	if _, err := m.Action(ctx, "geo_schedule", map[string]string{"enabled": "no"}); err == nil {
		t.Fatal("schedule failure fixture succeeded")
	}
	h = m.previousGeoHealth()
	if h.ConsecutiveFailures != 1 || h.Result != "download_failed" || h.Schedule.ConsecutiveFailures != 1 || !h.Scheduled {
		t.Fatal("mixed failures were not independent")
	}
	failed = false
	if _, err := m.Action(ctx, "geo_schedule", map[string]string{"enabled": "no"}); err != nil {
		t.Fatal(err)
	}
	h = m.previousGeoHealth()
	if h.ConsecutiveFailures != 1 || h.Result != "download_failed" || h.CheckedAt != updateFailed.CheckedAt || h.LastSuccessAt != first.LastSuccessAt || h.Schedule.ConsecutiveFailures != 0 || h.Scheduled {
		t.Fatal("schedule success erased a download failure")
	}
	failed = true
	if _, err := m.Action(ctx, "geo_schedule", map[string]string{"enabled": "yes"}); err == nil {
		t.Fatal("schedule failure fixture succeeded")
	}
	*downloadFailure = false
	if _, err := m.Action(ctx, "geo_refresh", nil); err != nil {
		t.Fatal(err)
	}
	h = m.previousGeoHealth()
	if h.Result != "unchanged" || h.ConsecutiveFailures != 0 || h.Schedule.Result != "failed" || h.Schedule.ConsecutiveFailures != 1 {
		t.Fatal("verified refresh erased outstanding scheduling failure")
	}
}

func TestGeoLegacyScheduleMigrationDoesNotInventUpdateRecovery(t *testing.T) {
	m, _, _, _ := installedGeoFixture(t)
	last := time.Now().UTC().Add(-time.Hour).Truncate(time.Millisecond)
	legacy := assets.Health{SchemaVersion: 1, CheckedAt: last.Add(time.Minute), LastSuccessAt: last, ConsecutiveFailures: 7, Result: "schedule_failed", Scheduled: true}
	if err := m.writeJSON(m.localPath("geoip-health.json"), legacy, false); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(m.localPath("geoip-health.json"))
	if err != nil {
		t.Fatal(err)
	}
	h := m.previousGeoHealth()
	if h.Result != "unknown" || h.ConsecutiveFailures != 0 || h.CheckedAt != last || h.LastSuccessAt != last || h.Schedule.Result != "failed" || h.Schedule.ConsecutiveFailures != 1 || h.Schedule.CheckedAt != legacy.CheckedAt {
		t.Fatal("legacy combined count was presented as known separate history")
	}
	after, _ := os.ReadFile(m.localPath("geoip-health.json"))
	if string(before) != string(after) {
		t.Fatal("reading legacy metadata wrote to disk")
	}
	if _, err := m.Action(context.Background(), "geo_schedule", map[string]string{"enabled": "no"}); err != nil {
		t.Fatal(err)
	}
	h = m.previousGeoHealth()
	if h.Result != "unknown" || h.LastSuccessAt != last || h.Schedule.Result != "ok" || h.Schedule.ConsecutiveFailures != 0 {
		t.Fatal("schedule-only recovery fabricated update recovery")
	}
	if _, err := m.Action(context.Background(), "geo_refresh", nil); err != nil {
		t.Fatal(err)
	}
	h = m.previousGeoHealth()
	if h.Result != "unchanged" || !h.LastSuccessAt.After(last) {
		t.Fatal("verified refresh did not establish known recovery")
	}
}

func TestGeoScheduleCountersSaturateAndClocksDoNotRegress(t *testing.T) {
	m, _, _, downloadFailure := installedGeoFixture(t)
	future := time.Now().UTC().Add(time.Hour).Truncate(time.Millisecond)
	h := assets.Health{SchemaVersion: 1, CheckedAt: future, LastSuccessAt: future, Result: "ok", Schedule: &assets.ScheduleHealth{CheckedAt: future, ConsecutiveFailures: math.MaxUint32, Result: "failed"}}
	if err := m.writeJSON(m.localPath("geoip-health.json"), h, false); err != nil {
		t.Fatal(err)
	}
	failed := true
	failGeoSchedule(m, &failed)
	if _, err := m.Action(context.Background(), "geo_schedule", map[string]string{"enabled": "yes"}); err == nil {
		t.Fatal("schedule failure fixture succeeded")
	}
	got := m.previousGeoHealth()
	if got.Schedule.ConsecutiveFailures != math.MaxUint32 || got.Schedule.CheckedAt.Before(future) || got.CheckedAt != future {
		t.Fatal("counter overflow or backwards schedule time")
	}
	*downloadFailure = true
	if _, err := m.Action(context.Background(), "geo_refresh", nil); err == nil {
		t.Fatal("download failure fixture succeeded")
	}
	got = m.previousGeoHealth()
	if got.Result != "download_failed" || got.CheckedAt.Before(future) || got.LastSuccessAt != future {
		t.Fatal("clock regression lost update classification or last success")
	}
}

func TestGeoFirstSetupScheduleFailureStillRecordsVerifiedPair(t *testing.T) {
	m, _ := fixtureManager(t)
	downloadFailure := false
	m.Assets = syntheticGeoClientWithFixture(t, &downloadFailure, newManagedGeoFixture())
	failed := true
	failGeoSchedule(m, &failed)
	if _, err := m.Action(context.Background(), "geo_download", map[string]string{"account_id": "123", "license_key": "synthetic-private-key", "accepted_terms": "yes", "auto_update": "yes"}); err == nil {
		t.Fatal("setup concealed schedule failure")
	}
	h := m.previousGeoHealth()
	state := readGeoState(t, m)
	if h.Result != "ok" || h.ConsecutiveFailures != 0 || h.LastSuccessAt.IsZero() || h.Schedule == nil || h.Schedule.Result != "failed" || h.Schedule.ConsecutiveFailures != 1 || h.Scheduled || state.Result != "ok" || state.Generation == "" {
		t.Fatal("first verified activation was counted as update failure")
	}
	snapshot, err := m.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Config.Geo.CityMMDB == "" || snapshot.Config.Geo.ASNMMDB == "" {
		t.Fatal("verified pair not installed")
	}
	failed = false
	if _, err := m.Action(context.Background(), "geo_schedule", map[string]string{"enabled": "yes"}); err != nil {
		t.Fatal(err)
	}
	got := m.previousGeoHealth()
	if got.Result != "ok" || got.LastSuccessAt != h.LastSuccessAt || !got.Scheduled || got.Schedule.Result != "ok" || got.Schedule.ConsecutiveFailures != 0 {
		t.Fatal("scheduling retry did not recover only the scheduling channel")
	}
}
