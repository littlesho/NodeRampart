// SPDX-License-Identifier: MIT

package daemon

import (
	"time"

	"github.com/littlesho/NodeRampart/internal/config"
)

type ReadinessStatus struct {
	ConfigurationLoaded bool   `json:"configuration_loaded"`
	ConfigFingerprint   string `json:"config_fingerprint"`
	StorageReady        bool   `json:"storage_ready"`
	InterfaceReady      bool   `json:"interface_ready"`
	SensorReady         bool   `json:"sensor_ready"`
	JournalReady        bool   `json:"journal_ready"`
	AuthDetectionReady  bool   `json:"auth_detection_ready"`
	BasicReady          bool   `json:"basic_ready"`
}

func (a *App) readiness(status Status, inspected bool, now time.Time) ReadinessStatus {
	result := ReadinessStatus{ConfigurationLoaded: true, ConfigFingerprint: config.Fingerprint(a.options.Config), StorageReady: inspected && status.Storage.Healthy}
	if status.Budget != nil && status.Budget.Configured && status.Budget.State != "running" {
		result.StorageReady = false
	}
	for _, component := range status.Components {
		if component.Name == "interface_counter" && component.State == "running" && now.Sub(component.UpdatedAt) <= 15*time.Second && !status.LastInterfaceCommit.IsZero() && now.Sub(status.LastInterfaceCommit) <= 15*time.Second {
			result.InterfaceReady = true
		}
	}
	result.SensorReady = !a.options.Config.Sensor.Enabled
	if a.options.Config.Sensor.Enabled {
		deadline := 2*a.options.Config.Sensor.BatchInterval.Duration + 5*time.Second
		result.SensorReady = !status.LastSensorBatch.IsZero() && now.Sub(status.LastSensorBatch) <= deadline && len(status.SensorInterfaces) > 0
		for _, name := range a.options.Config.Sensor.InterfaceNames() {
			at := status.SensorInterfaces[name]
			if at.IsZero() || now.Sub(at) > deadline {
				result.SensorReady = false
			}
		}
	}
	// Quiet SSH is not proof of resumed journal coverage, and optional external
	// delivery never gates the basic interface/sensor readiness contract.
	result.JournalReady = !a.options.Config.Auth.Enabled || status.Journal.State == "running"
	result.AuthDetectionReady = !a.options.Config.Auth.Enabled || status.AuthDetection.FailureEntryLimit > 0 && status.AuthDetection.CoverageComplete
	result.BasicReady = result.StorageReady && result.InterfaceReady && result.SensorReady && result.JournalReady && result.AuthDetectionReady
	return result
}
