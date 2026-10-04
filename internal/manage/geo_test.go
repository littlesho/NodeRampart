// SPDX-License-Identifier: MIT

package manage

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/assets"
)

type fixtureTransport func(*http.Request) (*http.Response, error)

func (f fixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func syntheticGeoClient(t *testing.T, failASN *bool) *assets.Client {
	return syntheticGeoClientWithFixture(t, failASN, newManagedGeoFixture())
}

type managedGeoFixture struct {
	cityBuild, asnBuild     uint64
	cityLicense, asnLicense string
	requests                int
}

func newManagedGeoFixture() *managedGeoFixture {
	epoch := uint64(time.Now().Add(-24 * time.Hour).Unix())
	return &managedGeoFixture{cityBuild: epoch, asnBuild: epoch, cityLicense: "Synthetic test fixture license", asnLicense: "Synthetic test fixture license"}
}

func syntheticGeoClientWithFixture(t *testing.T, failASN *bool, fixture *managedGeoFixture) *assets.Client {
	t.Helper()
	return &assets.Client{HTTP: &http.Client{Transport: fixtureTransport(func(r *http.Request) (*http.Response, error) {
		fixture.requests++
		if r.URL.Host != "download.maxmind.com" {
			t.Fatal("unexpected GeoIP endpoint")
		}
		edition := "GeoLite2-City"
		build, license := fixture.cityBuild, fixture.cityLicense
		if strings.Contains(r.URL.Path, "GeoLite2-ASN") {
			edition = "GeoLite2-ASN"
			build, license = fixture.asnBuild, fixture.asnLicense
			if *failASN {
				return nil, errors.New("synthetic-private-key transport failure")
			}
		}
		var out bytes.Buffer
		z := gzip.NewWriter(&out)
		tarfile := tar.NewWriter(z)
		for name, data := range map[string][]byte{edition + ".mmdb": managedSyntheticMMDB(edition, build), "LICENSE.txt": []byte(license), "COPYRIGHT.txt": []byte("Synthetic test fixture authors")} {
			if err := tarfile.WriteHeader(&tar.Header{Name: edition + "_20260101/" + name, Mode: 0o644, Size: int64(len(data))}); err != nil {
				t.Fatal(err)
			}
			if _, err := tarfile.Write(data); err != nil {
				t.Fatal(err)
			}
		}
		if err := tarfile.Close(); err != nil {
			t.Fatal(err)
		}
		if err := z.Close(); err != nil {
			t.Fatal(err)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(out.Bytes())), ContentLength: int64(out.Len()), Request: r}, nil
	})}}
}

func TestGeoActivationIsPairedAndFailedRefreshPreservesIt(t *testing.T) {
	m, _ := fixtureManager(t)
	fail := false
	m.Assets = syntheticGeoClient(t, &fail)
	input := map[string]string{"account_id": "123", "license_key": "synthetic-private-key", "accepted_terms": "yes", "auto_update": "no"}
	for i := 0; i < 2; i++ {
		result, err := m.Action(context.Background(), "geo_download", input)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(result, input["license_key"]) {
			t.Fatal("credential returned")
		}
	}
	snapshot, err := m.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(snapshot.Config.Geo.CityMMDB) != filepath.Dir(snapshot.Config.Geo.ASNMMDB) {
		t.Fatal("unpaired active databases")
	}
	entries, err := os.ReadDir(m.localPath("geoip"))
	if err != nil || len(entries) != 1 {
		t.Fatal("old managed GeoIP generations retained")
	}
	if _, err := readFile(m.localPath("maxmind.credentials.json"), 4096, true, -1); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(m.ConfigPath)
	fail = true
	result, err := m.Action(context.Background(), "geo_refresh", nil)
	if err == nil || strings.Contains(result+err.Error(), input["license_key"]) {
		t.Fatal("missing or secret-bearing download failure")
	}
	after, _ := os.ReadFile(m.ConfigPath)
	if !bytes.Equal(before, after) {
		t.Fatal("failed second edition changed configuration")
	}
	if _, err := os.Stat(snapshot.Config.Geo.CityMMDB); err != nil {
		t.Fatal("failed refresh removed valid old data")
	}
	status, err := m.geoStatus(context.Background())
	if err != nil || !strings.Contains(status, "download_failed") {
		t.Fatal("failed refresh not visible")
	}
}

func TestGeoTimerIsDataOnlyAndExplicit(t *testing.T) {
	m, fake := fixtureManager(t)
	if _, err := m.scheduleGeo(context.Background(), true); err == nil {
		t.Fatal("timer enabled without credentials")
	}
	if err := m.writeJSON(m.localPath("maxmind.credentials.json"), assets.Credentials{AccountID: "123", LicenseKey: "synthetic-only"}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := m.scheduleGeo(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(m.unitDir, "noderampart-geoip-update.service"))
	if err != nil {
		t.Fatal(err)
	}
	unit := string(data)
	if !strings.Contains(unit, "ExecStart=/usr/bin/noderampart assets update") || strings.Contains(unit, "CAP_SYS_ADMIN") || strings.Contains(unit, "synthetic-only") {
		t.Fatal("unsafe data updater unit")
	}
	if !strings.Contains(unit, "\nCapabilityBoundingSet=CAP_CHOWN CAP_DAC_READ_SEARCH CAP_FOWNER CAP_KILL CAP_SETGID CAP_SETUID CAP_DAC_OVERRIDE\n") ||
		!strings.Contains(unit, "\nAmbientCapabilities=CAP_SETUID\n") || strings.Count(unit, "\nAmbientCapabilities=") != 1 ||
		!strings.Contains(unit, "\nNoNewPrivileges=yes\n") {
		t.Fatal("updater must retain only its required capabilities across systemd seccomp setup and exec")
	}
	if len(fake.calls) == 0 || fake.calls[len(fake.calls)-1] != "enable --now noderampart-geoip-update.timer" {
		t.Fatal("timer not activated explicitly")
	}
	rule, err := readFile(filepath.Join(m.tmpfilesDir, "noderampart-management.conf"), 4096, false, -1)
	if err != nil || !strings.Contains(string(rule), "f /run/noderampart-management.lock 0600 root root - -") || !strings.Contains(unit, "systemd-tmpfiles-setup.service") {
		t.Fatal("updater lacks a safe boot-time lock creator")
	}
}

type geoScheduleSystemdFixture struct {
	loadState, unitFileState, activeState string
	calls                                 []string
}

func geoScheduleManagerFixture(t *testing.T) (*Manager, *geoScheduleSystemdFixture) {
	t.Helper()
	m, _ := fixtureManager(t)
	timer := &geoScheduleSystemdFixture{loadState: "not-found", activeState: "inactive"}
	m.runner = func(ctx context.Context, program string, args ...string) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if program != "/usr/bin/systemctl" {
			t.Fatal("unexpected program in GeoIP schedule fixture")
		}
		call := strings.Join(args, " ")
		timer.calls = append(timer.calls, call)
		switch call {
		case "show --property=LoadState,UnitFileState,ActiveState --all noderampart-geoip-update.timer":
			deadline, ok := ctx.Deadline()
			if !ok || time.Until(deadline) > 5*time.Second {
				t.Fatal("schedule read has no short timeout")
			}
			// systemctl property ordering need not match the request ordering.
			return "ActiveState=" + timer.activeState + "\nLoadState=" + timer.loadState + "\nUnitFileState=" + timer.unitFileState + "\n", nil
		case "daemon-reload":
			if _, err := os.Stat(filepath.Join(m.unitDir, "noderampart-geoip-update.timer")); err != nil {
				t.Fatal("timer definition was not installed before reload")
			}
			timer.loadState = "loaded"
		case "enable --now noderampart-geoip-update.timer":
			timer.unitFileState, timer.activeState = "enabled", "active"
		case "disable --now noderampart-geoip-update.timer":
			timer.unitFileState, timer.activeState = "disabled", "inactive"
		default:
			t.Fatalf("unexpected GeoIP schedule command: %s", call)
		}
		return "", nil
	}
	return m, timer
}

func requireGeoScheduleState(t *testing.T, m *Manager, want bool) {
	t.Helper()
	result, err := m.Action(context.Background(), "geo_schedule_state", nil)
	if err != nil {
		t.Fatal(err)
	}
	var state map[string]bool
	if json.Unmarshal([]byte(result), &state) != nil || !reflect.DeepEqual(state, map[string]bool{"enabled": want}) {
		t.Fatalf("unexpected saved schedule selection: %s", result)
	}
}

func TestGeoScheduleStateSurvivesReopenAndReflectsManualChanges(t *testing.T) {
	m, timer := geoScheduleManagerFixture(t)
	requireGeoScheduleState(t, copyManagerFixture(m), false)
	if err := m.writeJSON(m.localPath("maxmind.credentials.json"), assets.Credentials{AccountID: "123", LicenseKey: "synthetic-only"}, true); err != nil {
		t.Fatal(err)
	}
	for _, enabled := range []string{"yes", "no"} {
		if _, err := m.Action(context.Background(), "geo_schedule", map[string]string{"enabled": enabled}); err != nil {
			t.Fatal(err)
		}
		want := enabled == "yes"
		m = copyManagerFixture(m)
		requireGeoScheduleState(t, m, want)
		before, err := os.ReadFile(m.localPath("geoip-health.json"))
		if err != nil || m.previousGeoHealth().Scheduled != want {
			t.Fatal("schedule action did not retain its outcome")
		}
		// A later manual change has no management metadata update. A newly
		// opened form must follow systemd, not the last saved health outcome.
		if want {
			timer.unitFileState, timer.activeState = "disabled", "inactive"
		} else {
			timer.unitFileState, timer.activeState = "enabled-runtime", "active"
		}
		requireGeoScheduleState(t, copyManagerFixture(m), !want)
		after, err := os.ReadFile(m.localPath("geoip-health.json"))
		if err != nil || !bytes.Equal(before, after) {
			t.Fatal("reading the current schedule changed historical health")
		}
	}
}

type geoScheduleFileSnapshot struct {
	mode    os.FileMode
	modTime time.Time
	data    string
}

func snapshotGeoScheduleTree(t *testing.T, m *Manager) map[string]geoScheduleFileSnapshot {
	t.Helper()
	files := map[string]geoScheduleFileSnapshot{}
	err := filepath.WalkDir(filepath.Dir(filepath.Dir(m.ConfigPath)), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		state := geoScheduleFileSnapshot{mode: info.Mode(), modTime: info.ModTime()}
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			state.data = string(data)
		}
		files[path] = state
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestGeoScheduleStateReadDoesNotWriteOrTakeManagementLock(t *testing.T) {
	m, timer := geoScheduleManagerFixture(t)
	// An absent timer is the normal fresh-install state. Inspecting it must
	// not create units, metadata, directories or even a management lock file.
	before := snapshotGeoScheduleTree(t, m)
	requireGeoScheduleState(t, copyManagerFixture(m), false)
	if !reflect.DeepEqual(before, snapshotGeoScheduleTree(t, m)) {
		t.Fatal("fresh schedule read changed managed files")
	}
	if err := m.writeJSON(m.localPath("maxmind.credentials.json"), assets.Credentials{AccountID: "123", LicenseKey: "synthetic-only"}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Action(context.Background(), "geo_schedule", map[string]string{"enabled": "yes"}); err != nil {
		t.Fatal(err)
	}
	release, err := m.lock()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	before = snapshotGeoScheduleTree(t, m)
	timer.calls = nil
	requireGeoScheduleState(t, copyManagerFixture(m), true)
	if !reflect.DeepEqual(before, snapshotGeoScheduleTree(t, m)) {
		t.Fatal("enabled schedule read changed managed files")
	}
	if len(timer.calls) != 1 || !strings.HasPrefix(timer.calls[0], "show ") {
		t.Fatalf("schedule read issued operations beyond a single query: %v", timer.calls)
	}
}

func TestGeoScheduleStateReadsEnablementIndependentlyOfActivity(t *testing.T) {
	for _, tc := range []struct {
		name, load, unit, active string
		want                     bool
	}{
		{"enabled_inactive", "loaded", "enabled", "inactive", true},
		{"enabled_failed", "loaded", "enabled", "failed", true},
		{"runtime_enabled", "loaded", "enabled-runtime", "active", true},
		{"disabled_active", "loaded", "disabled", "active", false},
		{"masked", "masked", "masked", "inactive", false},
		{"runtime_masked", "masked", "masked-runtime", "inactive", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, timer := geoScheduleManagerFixture(t)
			timer.loadState, timer.unitFileState, timer.activeState = tc.load, tc.unit, tc.active
			requireGeoScheduleState(t, m, tc.want)
		})
	}
}

func TestGeoScheduleStateRejectsUnreadableOrUnsupportedState(t *testing.T) {
	valid := "LoadState=loaded\nUnitFileState=enabled\nActiveState=active\n"
	for _, tc := range []struct {
		name, output string
		commandErr   error
	}{
		{"systemd_unavailable", "SYNTHETIC_PRIVATE_DETAIL", errors.New("SYNTHETIC_PRIVATE_DETAIL")},
		{"failed_command_with_valid_output", valid, errors.New("SYNTHETIC_PRIVATE_DETAIL")},
		{"empty", "", nil},
		{"oversized", valid + strings.Repeat("SYNTHETIC_PRIVATE_DETAIL", 4096), nil},
		{"missing_property", "LoadState=loaded\nUnitFileState=enabled\n", nil},
		{"duplicate_property", valid + "UnitFileState=disabled\n", nil},
		{"unexpected_property", valid + "SYNTHETIC_PRIVATE_DETAIL=value\n", nil},
		{"unnamed_values", "loaded\nenabled\nactive\n", nil},
		{"unknown_load", strings.ReplaceAll(valid, "LoadState=loaded", "LoadState=SYNTHETIC_PRIVATE_DETAIL"), nil},
		{"bad_setting", strings.ReplaceAll(valid, "LoadState=loaded", "LoadState=bad-setting"), nil},
		{"unknown_activity", strings.ReplaceAll(valid, "ActiveState=active", "ActiveState=SYNTHETIC_PRIVATE_DETAIL"), nil},
		{"unknown_enablement", strings.ReplaceAll(valid, "UnitFileState=enabled", "UnitFileState=SYNTHETIC_PRIVATE_DETAIL"), nil},
		{"empty_enablement", strings.ReplaceAll(valid, "UnitFileState=enabled", "UnitFileState="), nil},
		{"static", strings.ReplaceAll(valid, "UnitFileState=enabled", "UnitFileState=static"), nil},
		{"indirect", strings.ReplaceAll(valid, "UnitFileState=enabled", "UnitFileState=indirect"), nil},
		{"generated", strings.ReplaceAll(valid, "UnitFileState=enabled", "UnitFileState=generated"), nil},
		{"linked", strings.ReplaceAll(valid, "UnitFileState=enabled", "UnitFileState=linked"), nil},
		{"not_found_active", "LoadState=not-found\nUnitFileState=\nActiveState=active\n", nil},
		{"not_found_enabled", "LoadState=not-found\nUnitFileState=enabled\nActiveState=inactive\n", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := geoScheduleManagerFixture(t)
			m.runner = func(context.Context, string, ...string) (string, error) { return tc.output, tc.commandErr }
			before := snapshotGeoScheduleTree(t, m)
			result, err := m.Action(context.Background(), "geo_schedule_state", nil)
			if err == nil || result != "" || strings.Contains(err.Error(), "SYNTHETIC_PRIVATE_DETAIL") {
				t.Fatalf("unsupported/unreadable state became a selection or exposed details: %q, %v", result, err)
			}
			if !reflect.DeepEqual(before, snapshotGeoScheduleTree(t, m)) {
				t.Fatal("failed schedule read changed managed files")
			}
		})
	}
}

func TestGeoScheduleStateHonorsCancellation(t *testing.T) {
	m, timer := geoScheduleManagerFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if result, err := m.Action(ctx, "geo_schedule_state", nil); result != "" || !errors.Is(err, context.Canceled) || len(timer.calls) != 0 {
		t.Fatalf("pre-canceled read reached systemd or returned a selection: %q, %v", result, err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	m.runner = func(context.Context, string, ...string) (string, error) {
		cancel()
		return "LoadState=loaded\nUnitFileState=enabled\nActiveState=active\n", nil
	}
	if result, err := m.Action(ctx, "geo_schedule_state", nil); result != "" || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled query accepted a late successful response: %q, %v", result, err)
	}
}

// Empty test MMDB generated from metadata; no licensed database records.
func managedSyntheticMMDB(edition string, epoch uint64) []byte {
	text := func(s string) []byte {
		if len(s) < 29 {
			return append([]byte{0x40 | byte(len(s))}, []byte(s)...)
		}
		return append([]byte{0x5d, byte(len(s) - 29)}, []byte(s)...)
	}
	integer := func(value uint64, kind byte) []byte {
		data := []byte{}
		for value > 0 {
			data = append([]byte{byte(value)}, data...)
			value >>= 8
		}
		if kind <= 7 {
			return append([]byte{kind<<5 | byte(len(data))}, data...)
		}
		return append([]byte{byte(len(data)), kind - 7}, data...)
	}
	metadata := []byte{0xe9}
	add := func(key string, value []byte) {
		metadata = append(metadata, text(key)...)
		metadata = append(metadata, value...)
	}
	add("binary_format_major_version", integer(2, 5))
	add("binary_format_minor_version", integer(0, 5))
	add("build_epoch", integer(epoch, 9))
	add("database_type", text(edition))
	description := append([]byte{0xe1}, text("en")...)
	description = append(description, text("Synthetic empty fixture")...)
	add("description", description)
	add("ip_version", integer(4, 5))
	add("node_count", integer(1, 6))
	add("record_size", integer(24, 5))
	add("languages", append([]byte{1, 4}, text("en")...))
	data := append([]byte{0, 0, 1, 0, 0, 1}, make([]byte, 16)...)
	data = append(data, []byte{0xab, 0xcd, 0xef, 'M', 'a', 'x', 'M', 'i', 'n', 'd', '.', 'c', 'o', 'm'}...)
	return append(data, metadata...)
}
