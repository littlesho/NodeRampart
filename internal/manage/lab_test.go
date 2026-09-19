// SPDX-License-Identifier: MIT

package manage

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/api"
	"github.com/littlesho/NodeRampart/internal/billing"
	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/console"
	"github.com/littlesho/NodeRampart/internal/ipc"
	"github.com/littlesho/NodeRampart/internal/protocol"
	"github.com/littlesho/NodeRampart/internal/store"
	"golang.org/x/sys/unix"
)

// TestManagementInstalledLab changes the installed configuration and service
// state, and mounts synthetic fixtures. Never run it on the development host.
// The outer lab driver must first verify VMware identity, network containment
// and disposable storage, install the package, and create the explicit sentinel.
// It must coordinate exclusive VM ownership. No VM lifecycle operation occurs
// here. The application stays installed for the driver's removal acceptance.
func TestManagementInstalledLab(t *testing.T) {
	requireManagementLab(t)
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	m := installedLabManager(t)
	original, err := m.Load(ctx)
	if err != nil || original.Fingerprint == "" {
		t.Fatal("fresh installed configuration is unavailable")
	}
	// Refuse a populated installation rather than read or overwrite real secrets.
	cfg := original.Config
	if cfg.Notifications.Telegram.Enabled || cfg.Privacy.HashKeyFile != "" || cfg.Geo.CityMMDB != "" || cfg.Geo.ASNMMDB != "" || cfg.Billing.Enabled || cfg.Billing.ProfilePath != "" {
		t.Fatal("lab acceptance requires a fresh configuration without configured secrets or assets")
	}
	for _, path := range []string{cfg.Notifications.Telegram.TokenFile, m.localPath("maxmind.credentials.json"), m.localPath("geoip-state.json"), m.localPath("geoip-health.json"), m.journalPath(), filepath.Join(m.unitDir, "noderampart-geoip-update.timer"), filepath.Join(m.unitDir, "noderampart-geoip-update.service")} {
		if path != "" {
			if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("lab acceptance refuses existing secrets, update units or recovery state")
			}
		}
	}
	for _, name := range []string{"secrets", "geoip", "prices"} {
		entries, err := os.ReadDir(m.localPath(name))
		if err != nil && !errors.Is(err, os.ErrNotExist) || len(entries) != 0 {
			t.Fatal("lab acceptance requires empty managed asset directories")
		}
	}
	previous, err := m.serviceStates(ctx)
	if err != nil {
		t.Fatal("installed service state is unavailable")
	}
	for _, state := range previous {
		if state.Unit != "enabled" && state.Unit != "disabled" {
			t.Fatal("lab acceptance requires ordinary enabled or disabled installed services")
		}
	}
	t.Cleanup(func() { restoreManagementLab(t, m, original, previous) })

	fixture := original
	fixture.Config.Hostname = "v04-management-lab"
	fixture.Config.Reports.Timezone = "UTC"
	fixture.Config.Reports.Enabled = false // Only explicit local report requests.
	fixture.Config.Auth.Enabled = false
	fixture.Config.Sensor.Enabled = true
	fixture.Config.Notifications.Telegram.Enabled = false
	if _, err := m.Save(ctx, fixture); err != nil {
		t.Fatalf("save installed lab configuration: %v", err)
	}
	assertLabFile(t, m.ConfigPath, 0, m.daemonGID, 0o640)
	labAction(t, ctx, m, "service_start", nil)
	labAction(t, ctx, m, "service_restart", nil)
	assertLabStatus(t, ctx, m, false)
	assertLabNoNotifications(t, ctx, m)
	t.Log("installed configuration save, authenticated readiness and service restart passed")

	token := "123456789:" + strings.Repeat("x", 35)
	result := labAction(t, ctx, m, "telegram_setup", map[string]string{"enabled": "no", "token": token, "chat_id": "-1001234567890"})
	if strings.Contains(result, token) {
		t.Fatal("management result exposed the synthetic Telegram token")
	}
	labAction(t, ctx, m, "privacy_key_generate", nil)
	current := labLoad(t, ctx, m)
	if current.Config.Notifications.Telegram.Enabled {
		t.Fatal("synthetic Telegram configuration unexpectedly enabled notifications")
	}
	assertLabFile(t, current.Config.Notifications.Telegram.TokenFile, m.daemonUID, m.daemonGID, 0o600)
	assertLabFile(t, current.Config.Privacy.HashKeyFile, m.daemonUID, m.daemonGID, 0o600)
	stored, err := readFile(current.Config.Notifications.Telegram.TokenFile, 4096, true, m.daemonUID)
	if err != nil || string(stored) != token+"\n" {
		t.Fatal("synthetic token was not stored intact")
	}
	t.Log("disabled Telegram configuration and daemon-owned 0600 secrets passed")

	failASN := false
	geoFixture := newManagedGeoFixture()
	m.Assets = syntheticGeoClientWithFixture(t, &failASN, geoFixture) // No external HTTP transport.
	geoInput := map[string]string{"account_id": "123", "license_key": "synthetic-private-key", "accepted_terms": "yes", "auto_update": "no"}
	oldGeneration := ""
	serviceInvocations := func() string {
		t.Helper()
		var identities strings.Builder
		for _, unit := range []string{"noderampartd.service", "noderampart-sensor.service"} {
			identity, err := m.command(ctx, "/usr/bin/systemctl", "show", "--property=InvocationID", "--value", unit)
			if err != nil || len(strings.TrimSpace(identity)) != 32 {
				t.Fatal("native service invocation identity is unavailable")
			}
			identities.WriteString(identity)
		}
		return identities.String()
	}
	for i := 0; i < 3; i++ {
		var beforeState geoState
		var beforeInvocations, beforeFingerprint string
		var beforeFile os.FileInfo
		if i == 1 {
			beforeState = readGeoState(t, m)
			beforeInvocations = serviceInvocations()
			beforeFingerprint = labLoad(t, ctx, m).Fingerprint
			beforeFile, err = os.Stat(m.ConfigPath)
			if err != nil {
				t.Fatal("active configuration identity unavailable before unchanged refresh")
			}
		}
		if i == 2 {
			geoFixture.cityBuild-- // Only City changes; ASN and all notices stay identical.
		}
		var result string
		if i == 1 {
			result = labAction(t, ctx, m, "geo_refresh", nil)
		} else {
			result = labAction(t, ctx, m, "geo_download", geoInput)
		}
		if strings.Contains(result, geoInput["license_key"]) {
			t.Fatal("GeoIP management result exposed the synthetic license key")
		}
		current = labLoad(t, ctx, m)
		generation := filepath.Dir(current.Config.Geo.CityMMDB)
		if !within(generation, m.localPath("geoip")) || generation != filepath.Dir(current.Config.Geo.ASNMMDB) {
			t.Fatal("GeoIP activation did not retain a paired managed generation")
		}
		if i == 1 {
			state := readGeoState(t, m)
			afterFile, err := os.Stat(m.ConfigPath)
			if err != nil || !os.SameFile(beforeFile, afterFile) || current.Fingerprint != beforeFingerprint || generation != oldGeneration ||
				state.Result != "unchanged" || !state.Checked.After(beforeState.Checked) || state.Updated != beforeState.Updated ||
				state.Generation != beforeState.Generation || serviceInvocations() != beforeInvocations {
				t.Fatal("identical GeoIP refresh changed configuration, generation, activation time or native service invocation")
			}
		} else if generation == oldGeneration {
			t.Fatal("changed GeoIP data did not activate a new paired generation")
		}
		entries, err := os.ReadDir(generation)
		if err != nil || len(entries) != 6 {
			t.Fatal("paired GeoIP databases and four license notices were not retained")
		}
		for _, entry := range entries {
			assertLabFile(t, filepath.Join(generation, entry.Name()), 0, m.daemonGID, 0o640)
		}
		if oldGeneration != "" && i != 1 {
			if _, err := os.Lstat(oldGeneration); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("superseded GeoIP generation was retained")
			}
		}
		oldGeneration = generation
		assertLabStatus(t, ctx, m, true)
	}
	if geoFixture.requests != 6 {
		t.Fatal("GeoIP checks did not validate both editions on every download")
	}
	assertLabFile(t, m.localPath("maxmind.credentials.json"), 0, 0, 0o600)
	assertLabFile(t, m.localPath("geoip-health.json"), 0, m.daemonGID, 0o640)
	before, err := readFile(m.ConfigPath, maxManagedJSON, false, -1)
	if err != nil {
		t.Fatal("active configuration could not be checked before failed refresh")
	}
	failASN = true
	result, err = m.Action(ctx, "geo_refresh", nil)
	if err == nil || strings.Contains(result+err.Error(), geoInput["license_key"]) {
		t.Fatal("failed ASN refresh did not return a sanitized failure")
	}
	after, err := readFile(m.ConfigPath, maxManagedJSON, false, -1)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("failed ASN refresh changed active configuration")
	}
	for _, path := range []string{current.Config.Geo.CityMMDB, current.Config.Geo.ASNMMDB} {
		assertLabFile(t, path, 0, m.daemonGID, 0o640)
	}
	if !strings.Contains(labAction(t, ctx, m, "geo_status", nil), "download_failed") {
		t.Fatal("failed refresh is absent from GeoIP status")
	}
	assertLabStatus(t, ctx, m, true)
	t.Log("paired synthetic GeoIP activation, unchanged refresh without native restart, one-edition replacement and failed-refresh preservation passed")
	verifyLabGeoTimer(t, ctx, m)

	profile := billing.Profile{SchemaVersion: 1, Name: "Synthetic lab egress", Provider: "custom", SourceRegion: "lab", Currency: "USD", EffectiveDate: time.Now().UTC().Format("2006-01-02"), SourceURL: "https://example.com/synthetic-pricing", FreeGB: 2, UnitBytes: 1 << 30, InternetEgress: []billing.Tier{{UpToGB: 10, PricePerGB: 0.1}, {PricePerGB: 0.05}}}
	encoded, err := json.Marshal(profile)
	if err != nil {
		t.Fatal("synthetic profile encoding failed")
	}
	labAction(t, ctx, m, "billing_profile_save", map[string]string{"profile_json": string(encoded)})
	current = labLoad(t, ctx, m)
	active, err := billing.Load(current.Config.Billing.ProfilePath)
	if err != nil || !current.Config.Billing.Enabled || active.UnitBytes != profile.UnitBytes || active.Estimate(14<<30).Cost != 1.1 {
		t.Fatal("installed billing profile or binary-unit tier calculation is incorrect")
	}
	assertLabFile(t, current.Config.Billing.ProfilePath, 0, m.daemonGID, 0o640)
	prices := labAction(t, ctx, m, "prices_show", nil)
	if !strings.Contains(prices, profile.Name) || !strings.Contains(prices, "1073741824") || !strings.Contains(prices, "Coverage") {
		t.Fatal("price view lacks the active profile, unit assumption or real IPC coverage")
	}
	var recent struct {
		Report string `json:"report"`
	}
	if json.Unmarshal(labRequest(t, ctx, m, "report_now", struct{}{}), &recent) != nil || recent.Report == "" || strings.Contains(recent.Report, token) {
		t.Fatal("local report result is invalid or contains a secret")
	}
	day := time.Now().UTC().AddDate(0, 0, -14).Format("2006-01-02")
	labAction(t, ctx, m, "report_backfill", map[string]string{"from": day, "through": day})
	labRequest(t, ctx, m, "report_show", api.DateArgs{Date: day})
	now := time.Now().UTC()
	var health map[string]json.RawMessage
	if json.Unmarshal(labRequest(t, ctx, m, "health", api.HealthArgs{Start: now.Add(-time.Hour), End: now, Limit: 10}), &health) != nil || health["history"] == nil || health["interface_traffic"] == nil || health["current"] == nil {
		t.Fatal("health result lacks coverage or interface traffic evidence")
	}
	assertLabNoNotifications(t, ctx, m)
	t.Log("custom price profile, report/backfill/show, health and notification-free IPC passed")
	t.Run("same_filesystem_bind_mounts", testLabMountedGeneration)
}

func requireManagementLab(t *testing.T) {
	t.Helper()
	if os.Getenv("NR_MANAGEMENT_LAB") != "1" {
		t.Skip("requires explicit disposable Debian/Fedora VMware management lab authorization")
	}
	if os.Geteuid() != 0 {
		t.Fatal("management lab requires root")
	}
	dmi, err := os.ReadFile("/sys/class/dmi/id/sys_vendor")
	if err != nil || len(dmi) > 4096 || !strings.Contains(string(dmi), "VMware") {
		t.Fatal("management lab requires verified VMware DMI")
	}
	distro, err := os.ReadFile("/etc/os-release")
	if err != nil || len(distro) > 16<<10 {
		t.Fatal("management lab distribution is unavailable")
	}
	fields := make(map[string]string)
	for _, line := range strings.Split(string(distro), "\n") {
		if key, value, ok := strings.Cut(line, "="); ok {
			fields[key] = strings.Trim(value, "\"")
		}
	}
	if fields["ID"] != "fedora" && !(fields["ID"] == "debian" && fields["VERSION_ID"] == "13") {
		t.Fatal("management lab is restricted to Debian 13 or Fedora")
	}
	const sentinel = "disposable-v04-management-lab\n"
	fd, err := unix.Open("/run/noderampart-v04-authorized-lab", unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		t.Fatal("explicit management lab sentinel is unavailable")
	}
	file := os.NewFile(uintptr(fd), "management-lab-sentinel")
	defer file.Close()
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Uid != 0 || st.Nlink != 1 || st.Mode&0o7777 != 0o600 || st.Size != int64(len(sentinel)) {
		t.Fatal("management lab sentinel must be a root-owned regular 0600 file")
	}
	data, err := io.ReadAll(io.LimitReader(file, int64(len(sentinel)+1)))
	if err != nil || string(data) != sentinel {
		t.Fatal("management lab sentinel does not authorize this test")
	}
}

func installedLabManager(t *testing.T) *Manager {
	t.Helper()
	uid, err := ipc.ServiceUID(ipc.DaemonUser)
	if err != nil {
		t.Fatal("installed daemon identity is unavailable")
	}
	daemonUID, err := checkedServiceID(uid)
	if err != nil {
		t.Fatal("installed daemon identity is invalid")
	}
	group, err := user.LookupGroup("noderampart")
	if err != nil {
		t.Fatal("installed service group is unavailable")
	}
	gid, err := serviceGroupID(group.Gid)
	if err != nil {
		t.Fatal("installed service group is invalid")
	}
	m := &Manager{ConfigPath: "/etc/noderampart/config.json", daemonUID: daemonUID, daemonGID: gid, lockPath: "/run/noderampart-management.lock", unitDir: "/etc/systemd/system", tmpfilesDir: "/etc/tmpfiles.d", stateDir: "/var/lib/noderampart", runtimeDir: "/run/noderampart", binary: "/usr/bin/noderampart", sandbox: true}
	if _, err := readFile(m.binary, 128<<20, false, -1); err != nil {
		t.Fatal("trusted native package executable is unavailable")
	}
	m.Request = func(ctx context.Context, command string, args any) (json.RawMessage, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		cfg, err := config.Load(m.ConfigPath)
		if err != nil || cfg.Paths.ControlSocket != "/run/noderampart/control.sock" {
			return nil, errors.New("installed lab control socket configuration is invalid")
		}
		deadline := time.Now().Add(15 * time.Second)
		if outer, ok := ctx.Deadline(); ok && outer.Before(deadline) {
			deadline = outer
		}
		conn, err := ipc.DialUnixPeer(cfg.Paths.ControlSocket, min(3*time.Second, time.Until(deadline)), uid)
		if err != nil {
			return nil, errors.New("authenticated lab daemon connection failed")
		}
		defer conn.Close()
		stopClose := context.AfterFunc(ctx, func() { _ = conn.Close() })
		defer stopClose()
		if err := conn.SetDeadline(deadline); err != nil {
			return nil, errors.New("lab daemon connection deadline failed")
		}
		encoded, err := json.Marshal(args)
		if err != nil || protocol.WriteFrame(conn, api.Request{Version: api.Version, Command: command, Args: encoded}) != nil {
			return nil, errors.New("lab daemon request failed")
		}
		var response struct {
			Version int             `json:"version"`
			OK      bool            `json:"ok"`
			Error   string          `json:"error,omitempty"`
			Data    json.RawMessage `json:"data,omitempty"`
		}
		if protocol.ReadFrame(bufio.NewReader(conn), &response) != nil || response.Version != api.Version || !response.OK || !json.Valid(response.Data) {
			return nil, errors.New("lab daemon response failed validation")
		}
		return response.Data, nil // Never log response bodies or daemon error text.
	}
	return m
}

func labLoad(t *testing.T, ctx context.Context, m *Manager) console.Snapshot {
	t.Helper()
	snapshot, err := m.Load(ctx)
	if err != nil {
		t.Fatal("installed lab configuration could not be loaded")
	}
	return snapshot
}

func labAction(t *testing.T, ctx context.Context, m *Manager, action string, input map[string]string) string {
	t.Helper()
	result, err := m.Action(ctx, action, input)
	if err != nil {
		t.Fatalf("lab management action %s failed (inspect local state without publishing secrets)", action)
	}
	return result
}

func labRequest(t *testing.T, ctx context.Context, m *Manager, command string, args any) json.RawMessage {
	t.Helper()
	data, err := m.Request(ctx, command, args)
	if err != nil || !json.Valid(data) {
		t.Fatalf("authenticated lab IPC %s failed", command)
	}
	return data
}

func assertLabFile(t *testing.T, path string, uid, gid int, mode uint32) {
	t.Helper()
	var st unix.Stat_t
	if unix.Lstat(path, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Nlink != 1 || st.Uid != uint32(uid) || st.Gid != uint32(gid) || st.Mode&0o7777 != mode {
		t.Fatal("installed fixture file has unsafe type, ownership, links or permissions")
	}
}

func assertLabStatus(t *testing.T, ctx context.Context, m *Manager, geo bool) {
	t.Helper()
	var status struct {
		TelegramEnabled bool                   `json:"telegram_enabled"`
		GeoEnabled      bool                   `json:"geo_enabled"`
		Storage         struct{ Healthy bool } `json:"storage"`
	}
	if json.Unmarshal(labRequest(t, ctx, m, "status", struct{}{}), &status) != nil || status.TelegramEnabled || status.GeoEnabled != geo || !status.Storage.Healthy {
		t.Fatal("installed daemon status does not reflect healthy storage, disabled Telegram and expected GeoIP")
	}
}

func assertLabNoNotifications(t *testing.T, ctx context.Context, m *Manager) {
	t.Helper()
	var queue store.QueueStatus
	if json.Unmarshal(labRequest(t, ctx, m, "notify_status", struct{}{}), &queue) != nil || queue.Pending != 0 || !queue.LastSent.IsZero() {
		t.Fatal("lab must not contain queued or sent notifications")
	}
}

func verifyLabGeoTimer(t *testing.T, ctx context.Context, m *Manager) {
	t.Helper()
	dir, err := os.MkdirTemp("/run", "noderampart-v04-timer-lab-")
	if err != nil {
		t.Fatal("private timer fixture directory unavailable")
	}
	defer os.RemoveAll(dir)
	staged := *m
	staged.unitDir = dir
	staged.tmpfilesDir = filepath.Join(dir, "tmpfiles")
	var calls []string
	staged.runner = func(_ context.Context, program string, args ...string) (string, error) {
		if program != "/usr/bin/systemctl" {
			return "", errors.New("unexpected timer fixture command")
		}
		calls = append(calls, strings.Join(args, " "))
		return "", nil // Only the actual systemd-analyze below executes a process.
	}
	if _, err := staged.scheduleGeo(ctx, true); err != nil || len(calls) != 2 || calls[0] != "daemon-reload" || calls[1] != "enable --now noderampart-geoip-update.timer" {
		t.Fatal("staged GeoIP timer generation failed")
	}
	service := filepath.Join(dir, "noderampart-geoip-update.service")
	timer := filepath.Join(dir, "noderampart-geoip-update.timer")
	if _, err := m.command(ctx, "/usr/bin/systemd-analyze", "verify", service, timer); err != nil {
		t.Fatal("installed systemd rejected the staged GeoIP units")
	}
	state, err := m.command(ctx, "/usr/bin/systemctl", "show", "--property=ActiveState", "--value", "noderampart-geoip-update.timer")
	if err != nil || strings.TrimSpace(state) == "active" || strings.TrimSpace(state) == "activating" {
		t.Fatal("lab GeoIP timer unexpectedly active or unverified")
	}
	t.Log("generated GeoIP units passed native systemd verification without timer activation")
	verifyLabColdGeoNamespace(t, ctx, m, service, filepath.Join(staged.tmpfilesDir, "noderampart-management.conf"))
}

func verifyLabColdGeoNamespace(t *testing.T, ctx context.Context, m *Manager, service, rule string) {
	t.Helper()
	work, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	for _, unit := range []string{"noderampart-geoip-update.timer", "noderampart-geoip-update.service"} {
		state, err := m.command(work, "/usr/bin/systemctl", "show", "--property=ActiveState", "--value", unit)
		if err != nil || strings.TrimSpace(state) != "inactive" && strings.TrimSpace(state) != "failed" {
			t.Fatal("cold namespace test requires an inactive GeoIP timer and updater")
		}
	}
	rules, err := readFile(rule, 4096, false, -1)
	if err != nil || !strings.Contains(string(rules), "\nf /run/noderampart-management.lock 0600 root root - -\n") {
		t.Fatal("staged tmpfiles rule does not prepare the fixed private management lock")
	}
	// All preceding synchronous management actions have returned; this gated
	// test owns the VM exclusively and has verified that both update units are
	// inactive. No product lock replacement is added: removing the idle lock
	// here simulates /run being empty after boot solely for this lab fixture.
	release, err := m.lock()
	if err != nil {
		t.Fatal("cold namespace test found a concurrent management operation")
	}
	release()
	assertLabFile(t, m.lockPath, 0, 0, 0o600)
	if removeFile(m.lockPath) != nil {
		t.Fatal("could not prepare the absent cold-start lock fixture")
	}
	defer func() {
		if _, err := os.Lstat(m.lockPath); errors.Is(err, os.ErrNotExist) {
			if release, err := m.lock(); err == nil {
				release()
			} else {
				t.Error("cold namespace fixture could not restore the management lock")
			}
		}
	}()
	if _, err := m.command(work, "/usr/bin/systemd-tmpfiles", "--create", rule); err != nil {
		t.Fatal("native tmpfiles could not create the absent management lock")
	}
	assertLabFile(t, m.lockPath, 0, 0, 0o600)
	unitData, err := readFile(service, 8192, false, -1)
	needle := "ExecStart=" + m.binary + " assets update\n"
	if err != nil || strings.Count(string(unitData), needle) != 1 || strings.Contains(string(unitData), "CAP_SYS_ADMIN") {
		t.Fatal("staged GeoIP service is not the expected bounded update unit")
	}
	// Keep every production sandbox directive. The only executable operation is
	// local version output, so this namespace test cannot contact MaxMind.
	unitData = []byte(strings.Replace(string(unitData), needle, "ExecStart="+m.binary+" version\n", 1))
	unit := "noderampart-v04-geo-namespace-" + filepath.Base(filepath.Dir(service)) + ".service"
	unitPath := filepath.Join("/run/systemd/system", unit)
	if err := writeFile(unitPath, bytes.NewReader(unitData), 8192, 0o644, 0, 0, false); err != nil {
		t.Fatal("private cold namespace service fixture could not be created")
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := m.command(cleanup, "/usr/bin/systemctl", "stop", unit); err != nil {
			t.Error("cold namespace fixture service could not be stopped")
			return
		}
		if removeFile(unitPath) != nil {
			t.Error("cold namespace fixture unit could not be removed")
		}
		if _, err := m.command(cleanup, "/usr/bin/systemctl", "daemon-reload"); err != nil {
			t.Error("cold namespace fixture unit cleanup could not be reloaded")
		}
	}()
	if _, err := m.command(work, "/usr/bin/systemctl", "daemon-reload"); err != nil {
		t.Fatal("cold namespace fixture service could not be loaded")
	}
	if _, err := m.command(work, "/usr/bin/systemctl", "start", unit); err != nil {
		t.Fatal("production GeoIP sandbox failed the native cold-start namespace check")
	}
	result, resultErr := m.command(work, "/usr/bin/systemctl", "show", "--property=Result", "--value", unit)
	status, statusErr := m.command(work, "/usr/bin/systemctl", "show", "--property=ExecMainStatus", "--value", unit)
	if resultErr != nil || statusErr != nil || strings.TrimSpace(result) != "success" || strings.TrimSpace(status) != "0" {
		t.Fatal("cold namespace version fixture did not complete successfully")
	}
	t.Log("native tmpfiles restored the absent 0600 lock and the production GeoIP sandbox executed local version output")
}

func testLabMountedGeneration(t *testing.T) {
	// This helper is called only below the complete privileged lab gate.
	dir, err := os.MkdirTemp("/run", "noderampart-v04-mount-lab-")
	if err != nil {
		t.Fatal("private mount fixture directory unavailable")
	}
	var mounts []string
	t.Cleanup(func() {
		unmounted := true
		for i := len(mounts) - 1; i >= 0; i-- {
			if unix.Unmount(mounts[i], 0) != nil {
				unmounted = false
				t.Error("lab bind mount cleanup failed; fixture tree retained for local inspection")
			}
		}
		if unmounted {
			if err := os.RemoveAll(dir); err != nil {
				t.Error("private unmounted fixture cleanup failed")
			}
		}
	})
	for _, name := range []string{"source", "gen-directory", "gen-member"} {
		if os.Mkdir(filepath.Join(dir, name), 0o700) != nil {
			t.Fatal("mount fixture directory creation failed")
		}
	}
	data := []byte("synthetic mount fixture\n")
	source := filepath.Join(dir, "source", "keep.txt")
	member := filepath.Join(dir, "gen-member", "mounted.txt")
	other := filepath.Join(dir, "gen-member", "untouched.txt")
	for _, path := range []string{source, member, other} {
		if os.WriteFile(path, data, 0o600) != nil {
			t.Fatal("mount fixture file creation failed")
		}
	}
	bind := func(source, target string) {
		t.Helper()
		if unix.Mount(source, target, "", unix.MS_BIND, "") != nil {
			t.Fatal("authorized same-filesystem bind mount failed")
		}
		mounts = append(mounts, target)
		var sourceStat, parentStat unix.Stat_t
		if unix.Stat(target, &sourceStat) != nil || unix.Stat(filepath.Dir(target), &parentStat) != nil || sourceStat.Dev != parentStat.Dev {
			t.Fatal("bind fixture does not exercise a same-filesystem mount")
		}
	}
	bind(filepath.Join(dir, "source"), filepath.Join(dir, "gen-directory"))
	if removeGeneration(filepath.Join(dir, "gen-directory")) == nil {
		t.Fatal("mounted managed generation was accepted for deletion")
	}
	bind(source, member)
	if removeGeneration(filepath.Join(dir, "gen-member")) == nil {
		t.Fatal("mounted generation member was accepted for deletion")
	}
	for _, path := range []string{source, member, other} {
		actual, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(actual, data) {
			t.Fatal("refused generation cleanup modified synthetic source or member data")
		}
	}
}

func restoreManagementLab(t *testing.T, m *Manager, original console.Snapshot, previous map[string]serviceState) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	// Keep assets pinned if recovery is required; never delete evidence after a
	// failed restoration. All input credentials in this gated test are synthetic.
	current, err := m.Load(ctx)
	if err != nil {
		t.Error("lab cleanup could not load active configuration; retained installed fixtures")
		return
	}
	current.Config = original.Config
	if _, err := m.Save(ctx, current); err != nil {
		t.Error("lab cleanup could not restore original configuration; retained installed fixtures")
		return
	}
	for _, unit := range []string{"noderampart-sensor.service", "noderampartd.service"} {
		state := previous[unit]
		enablement := "disable"
		if state.Unit == "enabled" {
			enablement = "enable"
		}
		if _, err := m.command(ctx, "/usr/bin/systemctl", enablement, unit); err != nil {
			t.Error("lab cleanup could not restore original service enablement")
		}
		if !state.Active {
			if _, err := m.command(ctx, "/usr/bin/systemctl", "stop", unit); err != nil {
				t.Error("lab cleanup could not restore original stopped service state")
			}
		}
	}
	for _, unit := range []string{"noderampartd.service", "noderampart-sensor.service"} {
		if previous[unit].Active {
			if _, err := m.command(ctx, "/usr/bin/systemctl", "start", unit); err != nil {
				t.Error("lab cleanup could not restore original active service state")
			}
		}
	}
	for _, dir := range []string{"geoip", "prices", "secrets"} {
		if _, err := os.Lstat(m.localPath(dir)); errors.Is(err, os.ErrNotExist) {
			continue
		}
		var err error
		switch dir {
		case "geoip":
			err = m.cleanGeo("", "")
		case "prices":
			err = m.cleanManagedFiles(m.localPath(dir), "profile-", ".json")
		case "secrets":
			err = m.cleanManagedFiles(m.localPath(dir), "telegram-", ".secret")
			if err == nil {
				err = m.cleanManagedFiles(m.localPath(dir), "privacy-", ".secret")
			}
		}
		if err != nil {
			t.Error("lab cleanup retained an unsafe or pinned managed fixture")
		}
	}
	for _, name := range []string{"maxmind.credentials.json", "geoip-state.json", "geoip-health.json"} {
		path := m.localPath(name)
		if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
			continue
		}
		if removeFile(path) != nil {
			t.Error("lab cleanup could not remove synthetic update metadata")
		}
	}
}
