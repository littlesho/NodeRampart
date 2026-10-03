// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/daemon"
	"github.com/littlesho/NodeRampart/internal/store"
)

func TestDoctorStrictUnknownKeepsJSONAndDefaultExitContract(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.json")
	var output bytes.Buffer
	if err := doctorCommand([]string{"--config", path}, &output); err != nil {
		t.Fatal("existing informational contract changed", err)
	}
	output.Reset()
	err := doctorCommand([]string{"--strict", "--config", path}, &output)
	var exit *diagnosticExit
	if !errors.As(err, &exit) || exit.code != 2 {
		t.Fatal("strict config/diagnosis failure did not return 2", err)
	}
	var result doctorResult
	if json.Unmarshal(output.Bytes(), &result) != nil || result.Overall != "unknown" || result.StrictExitCode != 2 {
		t.Fatal("stdout ceased to be machine readable", output.String())
	}
}

func TestDoctorConfirmedDegradationAndNotApplicable(t *testing.T) {
	result := doctorResult{Checks: []doctorCheck{{Name: "sensor", State: "not_applicable"}, {Name: "storage", State: "degraded", ReasonCode: "storage_unready"}}}
	finalizeDoctor(&result)
	if result.StrictExitCode != 1 || result.Overall != "degraded" {
		t.Fatal(result)
	}
	result.Checks = result.Checks[:1]
	finalizeDoctor(&result)
	if result.StrictExitCode != 0 {
		t.Fatal("not applicable failed strict diagnostics", result)
	}
}

func TestDoctorSensorReceiptIdentityAndPrecisionJSON(t *testing.T) {
	cfg := config.Defaults()
	at := time.Now().UTC()
	at = time.UnixMicro(at.UnixMicro()).Add(789 * time.Nanosecond)
	s := daemon.Status{GeneratedAt: at, Diagnosis: &daemon.Diagnosis{Version: 1}, SensorEnabled: true, Batches: 1, Queue: &store.QueueStatus{}, ForeignKeys: &store.ForeignKeyStatus{}, Readiness: daemon.ReadinessStatus{ConfigurationLoaded: true, ConfigFingerprint: config.Fingerprint(cfg), StorageReady: true, InterfaceReady: true, SensorReady: true}}
	s.Detection.ScanCoverageComplete = true
	s.Detection.UDPScanCoverageComplete = true
	s.SensorInterfaces = map[string]time.Time{"lab0": at}
	s.SensorReceipts = map[string]daemon.SensorReceipt{"lab0": {SessionID: strings.Repeat("a", 32), Sequence: 1}}
	s.SensorCommits = []store.SensorWatermark{{Interface: "lab0", SessionID: strings.Repeat("a", 32), Sequence: 1, SentAt: time.UnixMicro(at.UnixMicro()), Complete: true}}
	for _, missing := range []bool{false, true} {
		if missing {
			s.SensorReceipts = nil
		} // schema1 old-daemon JSON cannot invent receipt identity.
		data, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		var result doctorResult
		appendRuntimeDiagnosis(&result, data, cfg)
		finalizeDoctor(&result)
		want := 0
		if missing {
			want = 2
		}
		if result.StrictExitCode != want {
			t.Fatalf("missing=%v: %+v", missing, result)
		}
	}
}
