// SPDX-License-Identifier: MIT

package manage

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/assets"
	"github.com/littlesho/NodeRampart/internal/billing"
	"github.com/littlesho/NodeRampart/internal/config"
	"golang.org/x/sys/unix"
)

type fakeServices struct {
	active          map[string]bool
	units           map[string]string
	calls           []string
	restartFailures int
}

func fixtureManager(t *testing.T) (*Manager, *fakeServices) {
	t.Helper()
	base := t.TempDir()
	for _, dir := range []string{"etc", "state", "run", "units"} {
		if err := os.Mkdir(filepath.Join(base, dir), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	m := &Manager{ConfigPath: filepath.Join(base, "etc", "config.json"), Assets: &assets.Client{}, daemonUID: os.Geteuid(), daemonGID: os.Getegid(),
		lockPath: filepath.Join(base, "lock"), stateDir: filepath.Join(base, "state"), runtimeDir: filepath.Join(base, "run"), unitDir: filepath.Join(base, "units"), tmpfilesDir: filepath.Join(base, "tmpfiles"), binary: "/usr/bin/noderampart"}
	fake := &fakeServices{active: map[string]bool{}, units: map[string]string{"noderampartd.service": "disabled", "noderampart-sensor.service": "disabled"}}
	m.runner = func(ctx context.Context, program string, args ...string) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if program != "/usr/bin/systemctl" {
			return "", errors.New("unexpected program")
		}
		fake.calls = append(fake.calls, strings.Join(args, " "))
		if args[0] == "show" {
			name := args[len(args)-1]
			if args[1] == "--property=UnitFileState" {
				return fake.units[name], nil
			}
			if fake.active[name] {
				return "active", nil
			}
			return "inactive", nil
		}
		if args[0] == "restart" && fake.restartFailures > 0 {
			fake.restartFailures--
			fake.active[args[1]] = false
			return "", errors.New("synthetic restart failure")
		}
		for _, name := range args[1:] {
			if !strings.HasSuffix(name, ".service") {
				continue
			}
			switch args[0] {
			case "stop":
				fake.active[name] = false
			case "restart", "start", "enable":
				fake.active[name] = true
			}
		}
		return "", nil
	}
	m.Request = func(context.Context, string, any) (json.RawMessage, error) { return json.RawMessage(`{}`), nil }
	cfg := config.Defaults()
	cfg.Paths.Database = filepath.Join(m.stateDir, "data.db")
	cfg.Paths.ControlSocket, cfg.Paths.SensorSocket = filepath.Join(m.runtimeDir, "control.sock"), filepath.Join(m.runtimeDir, "sensor.sock")
	data, _ := json.Marshal(cfg)
	if err := os.WriteFile(m.ConfigPath, data, 0o640); err != nil {
		t.Fatal(err)
	}
	return m, fake
}

func TestSaveConflictAndPreservedServiceChoices(t *testing.T) {
	m, fake := fixtureManager(t)
	ctx := context.Background()
	snapshot, err := m.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Config.Hostname = "synthetic-node"
	fake.units["noderampartd.service"] = "masked"
	if _, err := m.Save(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	for _, call := range fake.calls {
		if !strings.HasPrefix(call, "show ") {
			t.Fatalf("stopped services changed: %s", call)
		}
	}
	if _, err := m.Save(ctx, snapshot); err == nil || !strings.Contains(err.Error(), "changed since") {
		t.Fatalf("stale form accepted: %v", err)
	}
	current, _ := m.Load(ctx)
	if current.Config.Hostname != "synthetic-node" {
		t.Fatal("saved configuration lost")
	}
	info, _ := os.Stat(m.ConfigPath)
	if info.Mode().Perm() != 0o640 {
		t.Fatal("config permissions")
	}
	if _, err := os.Lstat(m.journalPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("completed apply retained journal")
	}
}

func TestFreshStoppedInstallCanSaveBeforeStateDirectoryExists(t *testing.T) {
	m, fake := fixtureManager(t)
	if err := os.Remove(m.stateDir); err != nil {
		t.Fatal(err)
	}
	snapshot, err := m.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Config.Hostname = "fresh-stopped-install"
	if _, err := m.Save(context.Background(), snapshot); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(m.stateDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("save unexpectedly created systemd's StateDirectory")
	}
	for _, call := range fake.calls {
		if !strings.HasPrefix(call, "show ") {
			t.Fatal("save started an initially stopped service")
		}
	}
	snapshot, err = m.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Config.Paths.Database = filepath.Join(m.stateDir, "custom", "data.db")
	if _, err := m.Save(context.Background(), snapshot); err == nil {
		t.Fatal("missing custom database subdirectory accepted")
	}
}

func TestRollbackRetriesServicesWhenConfigAlreadyRestored(t *testing.T) {
	m, fake := fixtureManager(t)
	fake.active["noderampartd.service"], fake.active["noderampart-sensor.service"] = true, true
	fake.restartFailures = 2
	before, _ := os.ReadFile(m.ConfigPath)
	snapshot, _ := m.Load(context.Background())
	snapshot.Config.Hostname = "candidate"
	if _, err := m.Save(context.Background(), snapshot); err == nil {
		t.Fatal("failed activation accepted")
	}
	after, _ := os.ReadFile(m.ConfigPath)
	if string(before) != string(after) {
		t.Fatal("old config was not restored")
	}
	if fake.active["noderampartd.service"] {
		t.Fatal("fixture did not leave service failed")
	}
	if _, err := os.Stat(m.journalPath()); err != nil {
		t.Fatal("recovery journal missing")
	}
	if _, err := m.Action(context.Background(), "recover_config", map[string]string{"confirm": "RESTORE"}); err != nil {
		t.Fatal(err)
	}
	if !fake.active["noderampartd.service"] || !fake.active["noderampart-sensor.service"] {
		t.Fatal("recovery discarded journal without restoring services")
	}
	if _, err := os.Stat(m.journalPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("recovery journal retained")
	}
}

func TestManagerLockAndCancellation(t *testing.T) {
	m, _ := fixtureManager(t)
	release, err := m.lock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Action(context.Background(), "service_stop", nil); err == nil {
		t.Fatal("concurrent mutation acquired lock")
	}
	release()
	snapshot, _ := m.Load(context.Background())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := m.Save(ctx, snapshot); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	if _, err := os.Lstat(m.journalPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("cancelled operation left journal")
	}
}

func TestReadinessAndSandboxAreNotAssumed(t *testing.T) {
	m, fake := fixtureManager(t)
	fake.active["noderampartd.service"] = true
	m.Request = func(context.Context, string, any) (json.RawMessage, error) { return nil, errors.New("not ready") }
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := m.waitReady(ctx, true, false); err == nil {
		t.Fatal("Type=simple running state mistaken for daemon readiness")
	}
	asset := m.localPath("asset")
	if err := os.WriteFile(asset, []byte("synthetic"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := m.daemonReadable(asset, true); err != nil {
		t.Fatal(err)
	}
	m.sandbox = true
	if err := m.daemonReadable(asset, true); err == nil {
		t.Fatal("host temporary asset exposed inside PrivateTmp")
	}
	// Owner permissions take precedence over less restrictive group/other bits.
	if m.dac(unix.Stat_t{Uid: uint32(m.daemonUID), Gid: uint32(m.daemonGID), Mode: 0o004}, 4) {
		t.Fatal("DAC owner precedence ignored")
	}
}

func TestTelegramRotationNeverReturnsSecretAndBoundsHistory(t *testing.T) {
	m, _ := fixtureManager(t)
	first := "123456789:" + strings.Repeat("A", 35)
	second := "123456789:" + strings.Repeat("B", 35)
	for _, token := range []string{first, second} {
		result, err := m.Action(context.Background(), "telegram_setup", map[string]string{"enabled": "yes", "token": token, "chat_id": "-1001234567890"})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(result, token) {
			t.Fatal("token returned to output")
		}
	}
	snapshot, _ := m.Load(context.Background())
	secret, err := readFile(snapshot.Config.Notifications.Telegram.TokenFile, 4096, true, m.daemonUID)
	if err != nil || strings.TrimSpace(string(secret)) != second {
		t.Fatal("new token not installed safely")
	}
	entries, err := os.ReadDir(m.localPath("secrets"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("old tokens retained: %d, %v", len(entries), err)
	}
	configBytes, _ := os.ReadFile(m.ConfigPath)
	if strings.Contains(string(configBytes), first) || strings.Contains(string(configBytes), second) {
		t.Fatal("token embedded in ordinary config")
	}
}

func TestProfileActivationAndOfflineEstimates(t *testing.T) {
	m, _ := fixtureManager(t)
	profile := billing.Profile{SchemaVersion: 1, Name: "fixture", Provider: "aws", SourceRegion: "test", Currency: "USD", EffectiveDate: "2026-09-01", SourceURL: "https://aws.amazon.com/ec2/pricing/on-demand/", UnitBytes: 1 << 30, InternetEgress: []billing.Tier{{PricePerGB: 0.1}}}
	if _, err := m.useProfile(context.Background(), profile); err != nil {
		t.Fatal(err)
	}
	profile.Name = "replacement"
	if _, err := m.useProfile(context.Background(), profile); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(m.localPath("prices"))
	if len(entries) != 1 {
		t.Fatal("old profile generations accumulate")
	}
	m.Request = func(context.Context, string, any) (json.RawMessage, error) { return nil, errors.New("offline") }
	result, err := m.showPrices(context.Background())
	if err != nil || !strings.Contains(result, "replacement") || !strings.Contains(result, "Usage unavailable") || strings.Contains(result, "0.00 USD") {
		t.Fatalf("offline estimate fabricated: %v", err)
	}
	if _, err := m.Action(context.Background(), "billing_profile_save", map[string]string{"profile_json": `{} {}`}); err == nil {
		t.Fatal("trailing profile JSON accepted")
	}
}
