// SPDX-License-Identifier: MIT

package daemon

import (
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/store"
)

func TestReadinessRequiresCollectionAndPreservesOptionalDegradation(t *testing.T) {
	cfg := config.Defaults()
	cfg.Auth.Enabled = false
	cfg.Sensor.Interface = "test0"
	cfg.Sensor.BatchInterval.Duration = time.Minute
	a := &App{options: Options{Config: cfg}}
	now := time.Now().UTC()
	status := Status{Storage: StorageHealth{Healthy: true}, LastInterfaceCommit: now, Components: []store.ComponentStatus{{Name: "interface_counter", State: "running", UpdatedAt: now}}, SensorInterfaces: map[string]time.Time{}}
	if a.readiness(status, true, now).BasicReady {
		t.Fatal("successful status alone became ready")
	}
	status.LastSensorBatch = now.Add(-61 * time.Second)
	status.SensorInterfaces["test0"] = status.LastSensorBatch
	status.OptionalFailures = map[string]string{"telegram": "telegram_credentials_unavailable"}
	ready := a.readiness(status, true, now)
	if !ready.BasicReady || ready.ConfigFingerprint != config.Fingerprint(cfg) {
		t.Fatalf("legal period or optional outage blocked base readiness: %+v", ready)
	}
	status.Storage.Healthy = false
	if a.readiness(status, true, now).BasicReady {
		t.Fatal("failed storage became ready")
	}
	status.Storage.Healthy = true
	status.SensorInterfaces = map[string]time.Time{"other": now}
	if a.readiness(status, true, now).BasicReady {
		t.Fatal("missing requested interface became ready")
	}
	if a.readiness(status, false, now).StorageReady {
		t.Fatal("unknown inspection became healthy")
	}
}
