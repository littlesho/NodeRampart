// SPDX-License-Identifier: MIT

package manage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func geoReconcileFixture(t *testing.T) (*Manager, *fakeServices, string) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("installed-unit reconciliation integration requires root-owned fixtures")
	}
	m, fake := fixtureManager(t)
	m.runner = func(ctx context.Context, program string, args ...string) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if program != "/usr/bin/systemctl" {
			t.Fatalf("unexpected program: %s", program)
		}
		call := strings.Join(args, " ")
		fake.calls = append(fake.calls, call)
		if call != "show --property=DropInPaths --value "+geoUpdateUnit && call != "daemon-reload" {
			t.Fatalf("reconciliation performed an unexpected service operation: %s", call)
		}
		return "", nil
	}
	path := filepath.Join(m.unitDir, geoUpdateUnit)
	return m, fake, path
}

func writeLegacyGeoUnit(t *testing.T, m *Manager, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(fmt.Sprintf(legacyGeoUpdateService, m.binary)), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCanonicalGeoUnitOwnerRejectsCustomMetadata(t *testing.T) {
	valid := unix.Stat_t{Mode: unix.S_IFREG | 0o644, Uid: 0, Gid: 0, Nlink: 1, Size: 1024}
	if !canonicalGeoUnitOwner(valid) {
		t.Fatal("ordinary generated root unit was rejected")
	}
	for _, mutate := range []func(*unix.Stat_t){
		func(s *unix.Stat_t) { s.Uid = 1 },
		func(s *unix.Stat_t) { s.Gid = 1 },
		func(s *unix.Stat_t) { s.Mode = unix.S_IFLNK | 0o777 },
		func(s *unix.Stat_t) { s.Mode = unix.S_IFREG | 0o640 },
		func(s *unix.Stat_t) { s.Mode = unix.S_IFREG | 0o664 },
		func(s *unix.Stat_t) { s.Mode |= unix.S_ISUID },
		func(s *unix.Stat_t) { s.Nlink = 2 },
		func(s *unix.Stat_t) { s.Size = 0 },
		func(s *unix.Stat_t) { s.Size = 8193 },
	} {
		changed := valid
		mutate(&changed)
		if canonicalGeoUnitOwner(changed) {
			t.Fatal("custom or unsafe unit metadata was accepted")
		}
	}
}

func TestReconcileGeoSchedulePreservesStateAndIsIdempotent(t *testing.T) {
	for _, binary := range []string{"/usr/bin/noderampart", "/usr/local/bin/noderampart"} {
		t.Run(binary, func(t *testing.T) {
			m, fake, path := geoReconcileFixture(t)
			m.binary = binary
			writeLegacyGeoUnit(t, m, path)
			timer := filepath.Join(m.unitDir, "noderampart-geoip-update.timer")
			health := m.localPath("geoip-health.json")
			for name, data := range map[string]string{timer: "[Timer]\nOnCalendar=weekly\n", health: "retained failure history\n"} {
				if err := os.WriteFile(name, []byte(data), 0o640); err != nil {
					t.Fatal(err)
				}
			}
			before := make(map[string][]byte)
			identities := make(map[string]os.FileInfo)
			for _, name := range []string{timer, health, m.ConfigPath} {
				before[name], _ = os.ReadFile(name)
				identities[name], _ = os.Stat(name)
			}
			result, err := m.reconcileGeoSchedule(context.Background(), true, []string{m.unitDir})
			if err != nil || !strings.Contains(result, "migrated and reloaded") {
				t.Fatalf("reconcile: %q, %v", result, err)
			}
			data, _ := os.ReadFile(path)
			if string(data) != m.geoUpdateService() || strings.Join(fake.calls, "\n") != "show --property=DropInPaths --value "+geoUpdateUnit+"\ndaemon-reload" {
				t.Fatal("generated unit was not migrated/reloaded exactly once")
			}
			if !bytes.Contains(data, []byte("\nAmbientCapabilities=CAP_SETUID\n")) {
				t.Fatal("migration omitted the ambient SETUID capability required by systemd sandbox setup")
			}
			for _, name := range []string{timer, health, m.ConfigPath} {
				after, _ := os.ReadFile(name)
				info, _ := os.Stat(name)
				if !bytes.Equal(before[name], after) || !os.SameFile(identities[name], info) || identities[name].ModTime() != info.ModTime() {
					t.Fatal("reconciliation changed timer, configuration or health metadata")
				}
			}
			current, _ := os.Stat(path)
			fake.calls = nil
			result, err = m.reconcileGeoSchedule(context.Background(), true, []string{m.unitDir})
			after, _ := os.Stat(path)
			if err != nil || !strings.Contains(result, "template is current") || !os.SameFile(current, after) || strings.Join(fake.calls, "\n") != "show --property=DropInPaths --value "+geoUpdateUnit {
				t.Fatal("current service was unnecessarily replaced or reloaded")
			}
		})
	}
}

func TestReconcileGeoScheduleLeavesUnownedOrCustomizedUnits(t *testing.T) {
	for _, kind := range []string{"absent", "masked", "symlink", "custom", "unpublished_six_capabilities", "custom_ambient", "other_install", "hardlink", "permissions"} {
		t.Run(kind, func(t *testing.T) {
			m, fake, path := geoReconcileFixture(t)
			if kind != "absent" && kind != "masked" && kind != "symlink" {
				writeLegacyGeoUnit(t, m, path)
			}
			canary := filepath.Join(t.TempDir(), "canary")
			if err := os.WriteFile(canary, []byte("private canary"), 0o600); err != nil {
				t.Fatal(err)
			}
			var err error
			switch kind {
			case "masked":
				err = os.Symlink("/dev/null", path)
			case "symlink":
				err = os.Symlink(canary, path)
			case "custom":
				err = os.WriteFile(path, []byte(fmt.Sprintf(legacyGeoUpdateService, m.binary)+"# administrator change\n"), 0o644)
			case "unpublished_six_capabilities":
				data := strings.Replace(fmt.Sprintf(legacyGeoUpdateService, m.binary),
					"CapabilityBoundingSet=CAP_CHOWN CAP_DAC_READ_SEARCH CAP_FOWNER\n",
					"CapabilityBoundingSet=CAP_CHOWN CAP_DAC_READ_SEARCH CAP_FOWNER CAP_KILL CAP_SETGID CAP_SETUID\n", 1)
				err = os.WriteFile(path, []byte(data), 0o644)
			case "custom_ambient":
				err = os.WriteFile(path, []byte(fmt.Sprintf(legacyGeoUpdateService, m.binary)+"AmbientCapabilities=CAP_NET_ADMIN\n"), 0o644)
			case "other_install":
				err = os.WriteFile(path, []byte(fmt.Sprintf(legacyGeoUpdateService, "/usr/local/bin/noderampart")), 0o644)
			case "hardlink":
				err = os.Link(path, filepath.Join(m.unitDir, "retained-hardlink"))
			case "permissions":
				err = os.Chmod(path, 0o664)
			}
			if err != nil {
				t.Fatal(err)
			}
			before, _ := os.Lstat(path)
			dataBefore, _ := os.ReadFile(path)
			result, err := m.reconcileGeoSchedule(context.Background(), true, []string{m.unitDir})
			if err != nil || result == "" || len(fake.calls) != 0 || strings.Contains(result, "migrated") {
				t.Fatalf("custom/absent service not preserved: %q, %v, %v", result, err, fake.calls)
			}
			after, _ := os.Lstat(path)
			dataAfter, _ := os.ReadFile(path)
			if !bytes.Equal(dataBefore, dataAfter) || before != nil && !os.SameFile(before, after) {
				t.Fatal("custom unit or symlink target changed")
			}
			canaryData, _ := os.ReadFile(canary)
			if string(canaryData) != "private canary" {
				t.Fatal("unrelated target changed")
			}
		})
	}
}

func TestReconcileGeoSchedulePreservesDropIns(t *testing.T) {
	for _, dir := range []string{geoUpdateUnit + ".d", "noderampart-geoip-.service.d", "noderampart-.service.d", "service.d", "ambient-reset", "loaded-only"} {
		t.Run(dir, func(t *testing.T) {
			m, fake, path := geoReconcileFixture(t)
			writeLegacyGeoUnit(t, m, path)
			var dropIn string
			var dropInBefore []byte
			var dropInIdentity os.FileInfo
			if dir == "loaded-only" {
				m.runner = func(_ context.Context, _ string, args ...string) (string, error) {
					fake.calls = append(fake.calls, strings.Join(args, " "))
					return "/run/custom-unit-location/override.conf", nil
				}
			} else {
				p := filepath.Join(m.unitDir, dir)
				if dir == "ambient-reset" {
					p = filepath.Join(m.unitDir, geoUpdateUnit+".d")
				}
				if err := os.Mkdir(p, 0o755); err != nil {
					t.Fatal(err)
				}
				dropIn = filepath.Join(p, "override.conf")
				dropInBefore = []byte("[Service]\nCapabilityBoundingSet=\n")
				if dir == "service.d" {
					dropInBefore = []byte("[Service]\nTimeoutStopFailureMode=abort\n")
				} else if dir == "ambient-reset" {
					dropInBefore = []byte("[Service]\nAmbientCapabilities=\n")
				}
				if err := os.WriteFile(dropIn, dropInBefore, 0o644); err != nil {
					t.Fatal(err)
				}
				dropInIdentity, _ = os.Stat(dropIn)
			}
			before, _ := os.Stat(path)
			result, err := m.reconcileGeoSchedule(context.Background(), true, []string{m.unitDir})
			after, _ := os.Stat(path)
			data, _ := os.ReadFile(path)
			if err != nil || !strings.Contains(result, "migrated and reloaded") || !strings.Contains(result, "effective sandbox is not verified") || os.SameFile(before, after) ||
				!strings.Contains(strings.Join(fake.calls, "\n"), "daemon-reload") || string(data) != m.geoUpdateService() ||
				!bytes.Contains(data, []byte("\nAmbientCapabilities=CAP_SETUID\n")) {
				t.Fatalf("managed template not migrated with explicit override uncertainty: %q, %v", result, err)
			}
			if dropIn != "" {
				dropInAfter, _ := os.ReadFile(dropIn)
				identity, _ := os.Stat(dropIn)
				if !bytes.Equal(dropInBefore, dropInAfter) || !os.SameFile(dropInIdentity, identity) || dropInIdentity.ModTime() != identity.ModTime() {
					t.Fatal("reconciliation changed an unrelated or capability-overriding drop-in")
				}
			}
			fake.calls = nil
			result, err = m.reconcileGeoSchedule(context.Background(), true, []string{m.unitDir})
			if err != nil || !strings.Contains(result, "template is current") || !strings.Contains(result, "effective sandbox is not verified") || strings.Contains(strings.Join(fake.calls, "\n"), "daemon-reload") {
				t.Fatal("current template lost the preserved drop-in uncertainty or reloaded again")
			}
		})
	}
}

func TestReconcileGeoScheduleOfflineAndReloadFailure(t *testing.T) {
	for _, scenario := range []string{"offline", "reload_failed", "inspection_failed"} {
		t.Run(scenario, func(t *testing.T) {
			m, fake, path := geoReconcileFixture(t)
			writeLegacyGeoUnit(t, m, path)
			m.runner = func(_ context.Context, _ string, args ...string) (string, error) {
				fake.calls = append(fake.calls, strings.Join(args, " "))
				if scenario == "inspection_failed" || args[0] == "daemon-reload" {
					return "", errors.New("synthetic command failure")
				}
				return "", nil
			}
			result, err := m.reconcileGeoSchedule(context.Background(), scenario != "offline", []string{m.unitDir})
			data, _ := os.ReadFile(path)
			if scenario == "offline" {
				if err != nil || len(fake.calls) != 0 || string(data) != m.geoUpdateService() || !strings.Contains(result, "when started") {
					t.Fatalf("offline reconcile: %q, %v", result, err)
				}
			} else if scenario == "reload_failed" {
				if err == nil || !strings.Contains(err.Error(), "daemon-reload failed") || string(data) != m.geoUpdateService() || !strings.Contains(result, "was migrated") {
					t.Fatalf("failed reload lost repair or recovery instruction: %q, %v", result, err)
				}
			} else if err == nil || string(data) != fmt.Sprintf(legacyGeoUpdateService, m.binary) {
				t.Fatal("failed drop-in inspection changed unit or hid failure")
			}
		})
	}
}

func TestReconcileGeoScheduleHonorsLockAndPathSafety(t *testing.T) {
	m, fake, path := geoReconcileFixture(t)
	writeLegacyGeoUnit(t, m, path)
	release, err := m.lock()
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.reconcileGeoSchedule(context.Background(), true, []string{m.unitDir})
	release()
	if err == nil || len(fake.calls) != 0 {
		t.Fatal("reconciliation ignored a held management lock")
	}
	actual := m.unitDir
	m.unitDir = filepath.Join(filepath.Dir(actual), "unit-alias")
	if err := os.Symlink(actual, m.unitDir); err != nil {
		t.Fatal(err)
	}
	if _, err := m.reconcileGeoSchedule(context.Background(), true, []string{m.unitDir}); err == nil || len(fake.calls) != 0 {
		t.Fatal("reconciliation traversed a symlinked output directory")
	}
	data, _ := os.ReadFile(path)
	if string(data) != fmt.Sprintf(legacyGeoUpdateService, m.binary) {
		t.Fatal("rejected reconciliation changed unit")
	}
}

type geoGuardMutationReader struct {
	io.Reader
	mutate func()
}

func (r *geoGuardMutationReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if r.mutate != nil {
		r.mutate()
		r.mutate = nil
	}
	return n, err
}

func TestExpectedFileGuardRejectsObservedEditsAndReplacement(t *testing.T) {
	for _, replacement := range []bool{false, true} {
		t.Run(fmt.Sprint(replacement), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "managed.service")
			if err := os.WriteFile(path, []byte("recognized old service\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			var before unix.Stat_t
			if err := unix.Lstat(path, &before); err != nil {
				t.Fatal(err)
			}
			content := &geoGuardMutationReader{Reader: strings.NewReader("new service\n"), mutate: func() {
				if replacement {
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.WriteFile(path, []byte("concurrent administrator change\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}}
			err := writeFileExpected(path, content, 8192, 0o644, os.Geteuid(), os.Getegid(), true, &before)
			data, _ := os.ReadFile(path)
			entries, _ := os.ReadDir(filepath.Dir(path))
			if err == nil || !strings.Contains(err.Error(), "changed before publication") || string(data) != "concurrent administrator change\n" || len(entries) != 1 {
				t.Fatalf("observed change overwritten or staging file leaked: %v", err)
			}
		})
	}
}
