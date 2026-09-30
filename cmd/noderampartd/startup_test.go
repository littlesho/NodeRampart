// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/api"
	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/daemon"
	"github.com/littlesho/NodeRampart/internal/ipc"
	"github.com/littlesho/NodeRampart/internal/protocol"
)

// Exercise the production startup entry and real authenticated Unix control
// socket. Everything, including deliberately absent optional resources, lives
// in one private temporary directory. No capture capability or HTTP is used.
func TestDaemonStartupWithAbsentOptionalResources(t *testing.T) {
	if path := os.Getenv("NR_STARTUP_CONFIG"); path != "" {
		os.Args = []string{"noderampartd", "--config", path}
		if err := run(); err != nil {
			t.Fatal(err)
		}
		return
	}
	root, err := os.MkdirTemp("", "nr-start-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	cfg := config.Defaults()
	cfg.Sensor.Enabled = false
	cfg.Sensor.Required = false
	cfg.Sensor.Interface = "lo"
	cfg.Auth.Enabled = false
	cfg.Reports.Enabled = false
	cfg.Paths.Database = filepath.Join(root, "db.sqlite")
	cfg.Paths.SensorSocket = filepath.Join(root, "sensor.sock")
	cfg.Paths.ControlSocket = filepath.Join(root, "control.sock")
	cfg.Storage.MinFreeBytes = 0
	cfg.Geo.CityMMDB = filepath.Join(root, "missing.mmdb")
	cfg.Billing.Enabled = true
	cfg.Billing.ProfilePath = filepath.Join(root, "missing-profile.json")
	cfg.Notifications.Telegram.Enabled = true
	cfg.Notifications.Telegram.TokenFile = filepath.Join(root, "missing-token")
	cfg.Notifications.Telegram.ChatID = "123"
	cfg.Notifications.Webhook.Enabled = true
	cfg.Notifications.Webhook.Endpoint = "https://webhook.invalid/synthetic"
	cfg.Notifications.Webhook.ReceiverID = "synthetic-receiver"
	cfg.Notifications.Webhook.CredentialFile = filepath.Join(root, "missing-webhook")
	cfg.Heartbeat.Enabled = true
	cfg.Heartbeat.Endpoint = "https://heartbeat.invalid/synthetic"
	cfg.Heartbeat.InstanceID = "synthetic-instance"
	cfg.Heartbeat.CredentialFile = filepath.Join(root, "missing-heartbeat")
	path := filepath.Join(root, "config.json")
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(executable, "-test.run=^TestDaemonStartupWithAbsentOptionalResources$")
	cmd.Env = []string{"PATH=/usr/bin:/bin", "NR_STARTUP_CONFIG=" + path}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	defer func() {
		if !waited {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	deadline := time.Now().Add(8 * time.Second)
	var status daemon.Status
	for time.Now().Before(deadline) {
		conn, err := ipc.DialUnixPeer(cfg.Paths.ControlSocket, 100*time.Millisecond, uint32(os.Geteuid()))
		if err == nil {
			_ = conn.SetDeadline(time.Now().Add(time.Second))
			err = protocol.WriteFrame(conn, api.Request{Version: api.Version, Command: "status"})
			var wire struct {
				Version int             `json:"version"`
				OK      bool            `json:"ok"`
				Data    json.RawMessage `json:"data"`
			}
			if err == nil {
				err = protocol.ReadFrame(bufio.NewReader(conn), &wire)
			}
			conn.Close()
			if err == nil && wire.OK && json.Unmarshal(wire.Data, &status) == nil && status.Readiness.BasicReady {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !status.Readiness.BasicReady || len(status.OptionalFailures) != 5 || !status.TelegramEnabled || !status.WebhookEnabled || !status.HeartbeatEnabled {
		t.Fatalf("startup lost base readiness or notification intent: readiness=%+v optional=%v enabled=%v", status.Readiness, status.OptionalFailures, status.TelegramEnabled)
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	waited = true
}
