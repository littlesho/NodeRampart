// SPDX-License-Identifier: MIT

package daemon

import (
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/collector"
	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/detect"
	"github.com/littlesho/NodeRampart/internal/store"
)

func TestJournalRecoveryPendingBlocksBasicReadiness(t *testing.T) {
	cfg := config.Defaults()
	cfg.Sensor.Enabled = false
	a := &App{options: Options{Config: cfg}}
	now := time.Now().UTC()
	status := Status{Storage: StorageHealth{Healthy: true}, LastInterfaceCommit: now,
		Components:    []store.ComponentStatus{{Name: "interface_counter", State: "running", UpdatedAt: now}},
		AuthDetection: detect.NewAuth(cfg.Auth).Stats(), OptionalFailures: map[string]string{"telegram": "telegram_credentials_unavailable"}}
	for _, state := range []string{"", "starting", "retrying", "degraded", "gap", "running"} {
		status.Journal = collector.JournalStatus{State: state, Reason: "process_started"}
		ready := a.readiness(status, true, now)
		want := state == "running"
		if ready.JournalReady != want || ready.BasicReady != want {
			t.Errorf("journal=%q readiness=%+v want=%v", state, ready, want)
		}
	}
	status.Journal.State = "running"
	status.AuthDetection.CoverageComplete = false
	if ready := a.readiness(status, true, now); ready.AuthDetectionReady || ready.BasicReady {
		t.Fatal("partial authentication history became ready", ready)
	}
	// Auth disabled is inapplicable, while an enabled unknown feed is not ready.
	a.options.Config.Auth.Enabled = false
	status.Journal = collector.JournalStatus{}
	if ready := a.readiness(status, true, now); !ready.JournalReady || !ready.BasicReady {
		t.Fatal("disabled auth or unavailable optional sender blocked base readiness", ready)
	}
}
