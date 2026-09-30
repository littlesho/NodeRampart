// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/api"
	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/daemon"
	"github.com/littlesho/NodeRampart/internal/protocol"
	"github.com/littlesho/NodeRampart/internal/store"
	"github.com/littlesho/NodeRampart/internal/version"
)

func diagnosticSocketFixture(t *testing.T, command string, call func(string, string)) {
	t.Helper()
	dir, err := os.MkdirTemp("", "nr-diagnostic-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	cfg := config.Defaults()
	cfg.Hostname = "synthetic"
	cfg.Sensor.Enabled, cfg.Auth.Enabled, cfg.Reports.Enabled = false, false, false
	cfg.Paths.Database, cfg.Paths.SensorSocket, cfg.Paths.ControlSocket = filepath.Join(dir, "database"), filepath.Join(dir, "sensor"), filepath.Join(dir, "control")
	path := filepath.Join(dir, "config.json")
	data, _ := json.Marshal(cfg)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.Paths.Database, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: cfg.Paths.ControlSocket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	_ = listener.SetDeadline(time.Now().Add(5 * time.Second))
	finished := make(chan error, 1)
	go func() {
		connection, err := listener.AcceptUnix()
		if err != nil {
			finished <- err
			return
		}
		defer connection.Close()
		_ = connection.SetDeadline(time.Now().Add(5 * time.Second))
		var request api.Request
		if err := protocol.ReadFrame(bufio.NewReader(connection), &request); err != nil {
			finished <- err
			return
		}
		if request.Command != command {
			finished <- fmt.Errorf("unexpected command %s", request.Command)
			return
		}
		status := daemon.Status{GeneratedAt: time.Now().UTC(), Version: version.Current(), Queue: &store.QueueStatus{}, ForeignKeys: &store.ForeignKeyStatus{}, Readiness: daemon.ReadinessStatus{ConfigurationLoaded: true, ConfigFingerprint: config.Fingerprint(cfg), StorageReady: true, InterfaceReady: true, SensorReady: true, JournalReady: true, AuthDetectionReady: true, BasicReady: true}}
		diagnosis := daemon.Diagnose(status, true)
		status.Diagnosis = &diagnosis
		finished <- protocol.WriteFrame(connection, api.Response{Version: api.Version, OK: true, Data: status})
	}()
	call(path, dir)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
}

func TestDoctorStrictHealthyOverAuthenticatedUnixSocket(t *testing.T) {
	diagnosticSocketFixture(t, "doctor", func(path, _ string) {
		var output bytes.Buffer
		if err := doctorCommand([]string{"--strict", "--manual-current-uid", "--config", path}, &output); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(output.String(), `"overall": "healthy"`) {
			t.Fatal(output.String())
		}
	})
}

func TestMetricsSuccessfulAuthenticatedUnixExport(t *testing.T) {
	diagnosticSocketFixture(t, "status", func(path, dir string) {
		var output bytes.Buffer
		target := filepath.Join(dir, "status.prom")
		if err := metricsCommand([]string{"export", "--manual-current-uid", "--config", path, "--output", target}, &output); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(target)
		if err != nil || !bytes.Contains(data, []byte("noderampart_collection_success 1\n")) || !bytes.Contains(data, []byte("noderampart_health_state 0\n")) {
			t.Fatal(err, string(data))
		}
	})
}
