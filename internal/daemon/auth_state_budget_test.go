// SPDX-License-Identifier: MIT

package daemon

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/collector"
	"github.com/littlesho/NodeRampart/internal/detect"
)

func TestAuthCapacityRefusalPersistsDegradationAndRecoversWithoutJournal(t *testing.T) {
	a := eventTestApp(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	limit := a.auth.StatsAt(now).FailureEntryLimit
	for i := range limit + 1 {
		a.auth.Observe(collector.AuthObservation{Kind: collector.AuthFailure, ObservedAt: now,
			SourceIP: netip.AddrFrom4([4]byte{192, 0, 2, byte(i/4096 + 1)}), Method: "password"})
	}
	a.journalStatus = collector.JournalStatus{State: "running"}
	a.refreshAuthCoverage(context.Background(), now)
	stats := a.auth.StatsAt(now)
	if stats.RejectedFailures != 1 || stats.CoverageComplete || stats.FailureCapacity > limit {
		t.Fatalf("capacity loss hidden: %+v", stats)
	}
	values := make([]monitorObservation, 5)
	a.observeHealth(context.Background(), now, values, nil)
	if values[2].Condition != "failed" || values[2].Status.Reason != "auth_state_capacity" {
		t.Fatalf("running journal hid detector loss: %+v", values[2])
	}
	components, err := a.options.Store.ComponentStatuses(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	state := ""
	for _, component := range components {
		if component.Name == "auth_detection" {
			state = component.State
		}
	}
	if state != "degraded" {
		t.Fatal("detector degradation was not persisted", state)
	}
	// The existing coverage ticker recovers an expired detection window;
	// neither a new journal record nor a successful SSH login is required.
	later := now.Add(a.options.Config.Auth.Window.Duration + time.Millisecond)
	a.refreshAuthCoverage(context.Background(), later)
	if a.authCoverageState != "running" || !a.authStats.CoverageComplete || a.authStats.RejectedFailures != 1 {
		t.Fatal("recovery lost historical refusal or required journal traffic", a.authStats)
	}
}

func TestUnknownAuthDetectorDoesNotBecomeHealthy(t *testing.T) {
	a := eventTestApp(t)
	a.auth = nil
	a.authStats = detect.AuthStats{}
	a.journalStatus = collector.JournalStatus{State: "running"}
	values := make([]monitorObservation, 5)
	a.observeHealth(context.Background(), time.Now().UTC(), values, nil)
	if values[2].Condition != "failed" || values[2].Status.Reason != "auth_state_unknown" {
		t.Fatal("missing auth detector became healthy", values[2])
	}
	status := a.Status(context.Background())
	if status.Readiness.AuthDetectionReady {
		t.Fatal("unknown auth stats became ready")
	}
	a.options.Config.Auth.Enabled = false
	if !a.Status(context.Background()).Readiness.AuthDetectionReady {
		t.Fatal("disabled auth was reported as a fault")
	}
}
