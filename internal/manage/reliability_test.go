// SPDX-License-Identifier: MIT

package manage

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/config"
	"golang.org/x/sys/unix"
)

func awaitFixtureFile(t *testing.T, path string) []byte {
	t.Helper()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
			return data
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatal("fixture did not become ready")
		}
	}
}

func fixtureLockHeld(t *testing.T, path string) bool {
	t.Helper()
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	err = unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if err == nil {
		_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
		return false
	}
	if errors.Is(err, unix.EWOULDBLOCK) {
		return true
	}
	t.Fatal(err)
	return false
}

func liveFixtureProcess(pid int) bool {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return false
	}
	end := strings.LastIndexByte(string(data), ')')
	fields := strings.Fields(string(data)[end+1:])
	return len(fields) > 0 && fields[0] != "Z" && fields[0] != "X"
}

func TestCommandOutputPipesRemainBounded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "output")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 128<<10)), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	m := &Manager{}
	output, err := m.command(ctx, "/bin/sh", "-c", `cat "$1"; cat "$1" >&2`, "fixture", path)
	if err != nil {
		t.Fatal(err)
	}
	if len(output) != 64<<10 || strings.Trim(output, "x") != "" {
		t.Fatalf("command output escaped its bound: %d bytes", len(output))
	}
}

func TestCommandCancellationStopsControlledDescendantsAndPipes(t *testing.T) {
	base := t.TempDir()
	script := filepath.Join(base, "parent.sh")
	code := `#!/bin/sh
exec 9> "$1/lock"
flock -x 9
trap '' TERM
sh -c 'trap "" TERM; echo $$ > "$1/child"; sh -c '\''trap "" TERM; echo $$ > "$1/grandchild"; sleep 0.8; echo late > "$1/late"'\'' fixture "$1" & wait' fixture "$1" &
echo $$ > "$1/parent"
wait
`
	if err := os.WriteFile(script, []byte(code), 0o700); err != nil {
		t.Fatal(err)
	}
	m := &Manager{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := m.command(ctx, "/bin/sh", script, base); done <- err }()
	pids := []int{}
	for _, name := range []string{"parent", "child", "grandchild"} {
		pid, err := strconv.Atoi(strings.TrimSpace(string(awaitFixtureFile(t, filepath.Join(base, name)))))
		if err != nil {
			t.Fatal(err)
		}
		pids = append(pids, pid)
	}
	if !fixtureLockHeld(t, filepath.Join(base, "lock")) {
		t.Fatal("fixture descendants did not retain lock")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled command reported success")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled process group or inherited output pipes did not finish")
	}
	for _, pid := range pids {
		if liveFixtureProcess(pid) {
			t.Fatalf("controlled descendant %d survived cancellation", pid)
		}
	}
	if fixtureLockHeld(t, filepath.Join(base, "lock")) {
		t.Fatal("cancelled descendants retained fixture lock")
	}
	if _, err := os.Stat(filepath.Join(base, "late")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("controlled descendant made a late write")
	}
}

func TestCommandCancellationAfterParentExitKeepsManagementLock(t *testing.T) {
	m, _ := fixtureManager(t)
	m.runner = nil
	release, err := m.lock()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	base := filepath.Dir(m.lockPath)
	script := filepath.Join(base, "exiting-parent.sh")
	code := `#!/bin/sh
sh -c 'test -e /proc/self/fd/3 || exit 2; trap "" TERM; echo $$ > "$1/child"; sleep 0.8; echo late > "$1/late"' fixture "$1" &
exit 0
`
	if err := os.WriteFile(script, []byte(code), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := m.command(ctx, "/bin/sh", script, base); done <- err }()
	awaitFixtureFile(t, filepath.Join(base, "child"))
	if !fixtureLockHeld(t, m.lockPath) {
		t.Fatal("descendant did not inherit active management exclusion")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled child group was reported as successful")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("parent-exit cancellation did not settle inherited pipes")
	}
	release()
	if fixtureLockHeld(t, m.lockPath) {
		t.Fatal("settled descendants retained management lock")
	}
	if _, err := os.Stat(filepath.Join(base, "late")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("descendant made a late write after its parent exited")
	}
}

func TestRemovalCancellationSupervisesTransactionAndRetainsLock(t *testing.T) {
	base := t.TempDir()
	helper, err := os.ReadFile("../../scripts/manage-remove.sh")
	if err != nil {
		t.Fatal(err)
	}
	// Execute the real transaction/checkpoint functions with synthetic commands;
	// never enter remove_main's installed paths or package operations.
	functions := strings.TrimSuffix(string(helper), "remove_main \"$@\"\n")
	script := filepath.Join(base, "supervisor.sh")
	code := functions + `
remove_cancel_requested=false
fixture_base=$1
trap 'remove_cancel_requested=true; echo requested > "$fixture_base/cancelled"' USR1
exec 9> "$1/lock"
flock -x 9
remove_transaction sh -c 'echo $$ > "$1/transaction"; sh -c '\''echo $$ > "$1/grandchild"; while [ ! -f "$1/release" ]; do sleep 0.01; done; echo committed > "$1/committed"'\'' fixture "$1" & wait' fixture "$1"
echo dispatched > "$1/after"
`
	if err := os.WriteFile(script, []byte(code), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := &Manager{}
	done := make(chan error, 1)
	go func() { _, err := m.removalCommand(ctx, "/bin/sh", script, base); done <- err }()
	t.Cleanup(func() {
		_ = os.WriteFile(filepath.Join(base, "release"), []byte("finish"), 0o600)
		cancel()
	})
	awaitFixtureFile(t, filepath.Join(base, "grandchild"))
	cancel()
	awaitFixtureFile(t, filepath.Join(base, "cancelled"))
	if !fixtureLockHeld(t, filepath.Join(base, "lock")) {
		t.Fatal("cancellation released lock before transaction completion")
	}
	select {
	case err := <-done:
		t.Fatalf("supervisor returned during package transaction: %v", err)
	default:
	}
	if err := os.WriteFile(filepath.Join(base, "release"), []byte("finish"), 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "installation state needs inspection") {
			t.Fatalf("uncertain cancellation was reported as complete: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("supervisor did not settle completed fixture transaction")
	}
	if _, err := os.Stat(filepath.Join(base, "committed")); err != nil {
		t.Fatal("cooperative cancellation killed the in-progress transaction")
	}
	if _, err := os.Stat(filepath.Join(base, "after")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("cancellation dispatched another action after transaction")
	}
	if fixtureLockHeld(t, filepath.Join(base, "lock")) {
		t.Fatal("finished transaction retained lock")
	}
}

func TestBadConfigurationCanStopFixedServices(t *testing.T) {
	m, fake := fixtureManager(t)
	fake.active["noderampartd.service"], fake.active["noderampart-sensor.service"] = true, true
	if err := os.WriteFile(m.ConfigPath, []byte("{broken"), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Action(context.Background(), "service_stop", nil); err != nil {
		t.Fatal(err)
	}
	if fake.active["noderampartd.service"] || fake.active["noderampart-sensor.service"] {
		t.Fatal("bad configuration prevented fixed service stop")
	}
	if _, err := m.Action(context.Background(), "service_restart", nil); err == nil {
		t.Fatal("restart bypassed invalid configuration")
	}
}

func TestServiceTransitionIsPreservedAndRejectsApply(t *testing.T) {
	for _, state := range []string{"activating", "deactivating", "reloading", "refreshing", "maintenance"} {
		t.Run(state, func(t *testing.T) {
			m, _ := fixtureManager(t)
			runner := m.runner
			m.runner = func(ctx context.Context, program string, args ...string) (string, error) {
				if args[0] == "show" && args[1] == "--property=ActiveState" {
					return state, nil
				}
				return runner(ctx, program, args...)
			}
			states, err := m.serviceStates(context.Background())
			if err != nil || states["noderampartd.service"].State != state {
				t.Fatalf("transition flattened: %v %#v", err, states)
			}
			before, _ := os.ReadFile(m.ConfigPath)
			snapshot, _ := m.Load(context.Background())
			snapshot.Config.Hostname = "candidate"
			if _, err := m.Save(context.Background(), snapshot); err == nil || !strings.Contains(err.Error(), state) {
				t.Fatalf("transition accepted as stopped: %v", err)
			}
			after, _ := os.ReadFile(m.ConfigPath)
			if string(before) != string(after) {
				t.Fatal("transition refusal modified configuration")
			}
			if _, err := os.Stat(m.journalPath()); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("transition refusal created recovery record")
			}
		})
	}
}

func TestFailedServiceRemainsExplicitAndDisabled(t *testing.T) {
	m, fake := fixtureManager(t)
	runner := m.runner
	m.runner = func(ctx context.Context, program string, args ...string) (string, error) {
		if args[0] == "show" && args[1] == "--property=ActiveState" && args[len(args)-1] == "noderampartd.service" {
			return "failed", nil
		}
		return runner(ctx, program, args...)
	}
	snapshot, _ := m.Load(context.Background())
	snapshot.Config.Hostname = "failed-but-disabled"
	text, err := m.Save(context.Background(), snapshot)
	if err != nil || !strings.Contains(text, "remains failed") {
		t.Fatalf("failed state shown as intentionally stopped: %q %v", text, err)
	}
	for _, call := range fake.calls {
		if !strings.HasPrefix(call, "show ") {
			t.Fatalf("failed/disabled service intent changed: %s", call)
		}
	}
}

func TestLegacyRecoveryRecordRestoresServiceState(t *testing.T) {
	m, fake := fixtureManager(t)
	before, _ := os.ReadFile(m.ConfigPath)
	record := applyRecord{Version: 1, Before: before, AfterSHA: digest([]byte("candidate")), Services: map[string]serviceState{
		"noderampartd.service": {Active: true, Unit: "disabled"}, "noderampart-sensor.service": {Unit: "masked"}}}
	data, _ := json.Marshal(record)
	if err := os.WriteFile(m.journalPath(), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Action(context.Background(), "recover_config", map[string]string{"confirm": "RESTORE"}); err != nil {
		t.Fatal(err)
	}
	if !fake.active["noderampartd.service"] || fake.active["noderampart-sensor.service"] {
		t.Fatal("legacy record's service intent was not restored")
	}
}

func TestReadinessChecksDataPlaneAndEffectiveConfiguration(t *testing.T) {
	for _, missing := range []string{"configuration_loaded", "config_fingerprint", "storage_ready", "interface_ready", "sensor_ready", "basic_ready"} {
		t.Run(missing, func(t *testing.T) {
			m, fake := fixtureManager(t)
			fake.active["noderampartd.service"], fake.active["noderampart-sensor.service"] = true, true
			snapshot, _ := m.Load(context.Background())
			ready := map[string]any{"configuration_loaded": true, "config_fingerprint": config.Fingerprint(snapshot.Config), "storage_ready": true, "interface_ready": true, "sensor_ready": true, "basic_ready": true}
			delete(ready, missing)
			data, _ := json.Marshal(map[string]any{"readiness": ready})
			m.Request = func(context.Context, string, any) (json.RawMessage, error) { return data, nil }
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
			defer cancel()
			if err := m.waitReadyConfig(ctx, true, true, snapshot.Config); err == nil {
				t.Fatal("status success substituted for confirmed readiness")
			}
		})
	}
	m, fake := fixtureManager(t)
	fake.active["noderampartd.service"], fake.active["noderampart-sensor.service"] = true, true
	snapshot, _ := m.Load(context.Background())
	snapshot.Config.Sensor.BatchInterval.Duration = time.Minute
	snapshot.Config.Hostname = "legally-slow-sampling"
	encoded, _ := json.Marshal(snapshot.Config)
	if err := os.WriteFile(m.ConfigPath, encoded, 0o640); err != nil {
		t.Fatal(err)
	}
	request := m.Request
	m.Request = func(ctx context.Context, command string, args any) (json.RawMessage, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) < 120*time.Second {
			t.Fatal("readiness deadline does not allow two legal minute samples")
		}
		return request(ctx, command, args)
	}
	if err := m.waitReadyConfig(context.Background(), true, true, snapshot.Config); err != nil {
		t.Fatal(err)
	}
}

func TestRecoveryRecordsEveryInterruptedStageAndKeepsExternalChanges(t *testing.T) {
	for _, stage := range []string{"prepared", "candidate_written", "activating", "recovering", "config_restored", "services_restored", "failed"} {
		t.Run(stage, func(t *testing.T) {
			m, fake := fixtureManager(t)
			before, _ := os.ReadFile(m.ConfigPath)
			snapshot, _ := m.Load(context.Background())
			snapshot.Config.Hostname = "interrupted-candidate"
			after, _ := json.Marshal(snapshot.Config)
			now := time.Now().UTC()
			record := applyRecord{Version: 2, Before: before, BeforeSHA: digest(before), AfterSHA: digest(after), Stage: stage, StartedAt: now, UpdatedAt: now,
				Services: map[string]serviceState{"noderampartd.service": {Active: true, State: "active", Unit: "disabled"}, "noderampart-sensor.service": {Unit: "masked", State: "inactive"}}}
			data, _ := json.Marshal(record)
			if err := os.WriteFile(m.journalPath(), data, 0o600); err != nil {
				t.Fatal(err)
			}
			if stage == "candidate_written" || stage == "activating" || stage == "recovering" {
				if err := os.WriteFile(m.ConfigPath, after, 0o640); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := m.Action(context.Background(), "recover_config", map[string]string{"confirm": "RESTORE"}); err != nil {
				t.Fatal(err)
			}
			current, _ := os.ReadFile(m.ConfigPath)
			if string(current) != string(before) || !fake.active["noderampartd.service"] || fake.active["noderampart-sensor.service"] {
				t.Fatal("recovery did not restore configuration and service intent")
			}
			for _, call := range fake.calls {
				if strings.HasPrefix(call, "enable ") || strings.HasPrefix(call, "unmask ") {
					t.Fatalf("recovery changed enable/mask intent: %s", call)
				}
			}
		})
	}
	m, _ := fixtureManager(t)
	before, _ := os.ReadFile(m.ConfigPath)
	record := applyRecord{Version: 1, Before: before, AfterSHA: digest([]byte("candidate")), Services: map[string]serviceState{"noderampartd.service": {Unit: "disabled"}, "noderampart-sensor.service": {Unit: "disabled"}}}
	data, _ := json.Marshal(record)
	_ = os.WriteFile(m.journalPath(), data, 0o600)
	_ = os.WriteFile(m.ConfigPath, []byte("external edit"), 0o640)
	if _, err := m.Action(context.Background(), "recover_config", map[string]string{"confirm": "RESTORE"}); err == nil {
		t.Fatal("recovery overwrote an external change")
	}
	if _, err := os.Stat(m.journalPath()); err != nil {
		t.Fatal("conflicting recovery discarded record")
	}
}
