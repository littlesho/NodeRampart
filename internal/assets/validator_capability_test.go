// SPDX-License-Identifier: MIT

package assets

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// This test changes capabilities only in disposable child processes. Ordinary
// unprivileged runs explicitly skip it; native root runs exercise the same
// capability sets as the historical and corrected updater service.
func TestMMDBValidatorServiceCapabilities(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires native root capabilities to exercise the updater sandbox")
	}
	header := unix.CapUserHeader{Version: unix.LINUX_CAPABILITY_VERSION_3}
	var capabilities [2]unix.CapUserData
	if err := unix.Capget(&header, &capabilities[0]); err != nil {
		t.Fatal("cannot inspect native capability prerequisites")
	}
	for _, capability := range []uint{unix.CAP_CHOWN, unix.CAP_DAC_READ_SEARCH, unix.CAP_FOWNER, unix.CAP_KILL, unix.CAP_SETGID, unix.CAP_SETUID, unix.CAP_DAC_OVERRIDE, unix.CAP_SETPCAP} {
		if capabilities[capability/32].Effective&(1<<(capability%32)) == 0 {
			t.Skip("requires root with the updater capabilities and CAP_SETPCAP; container root is insufficient")
		}
	}
	setpriv, err := exec.LookPath("setpriv")
	if err != nil {
		t.Skip("requires util-linux setpriv for the isolated capability regression")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"legacy", "without_socket_write", "fixed"} {
		t.Run(mode, func(t *testing.T) {
			capabilities := "-all,+chown,+dac_read_search,+fowner"
			inheritable, ambient := "-all", "-all"
			if mode != "legacy" {
				capabilities += ",+kill,+setgid,+setuid"
				inheritable, ambient = "-all,+setuid", "-all,+setuid"
			}
			if mode == "fixed" {
				capabilities += ",+dac_override"
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, setpriv, "--bounding-set="+capabilities, "--inh-caps="+inheritable, "--ambient-caps="+ambient, "--no-new-privs", executable, "-test.run=^TestMMDBValidatorServiceCapabilitiesFixture$", "-test.v")
			cmd.Env = append(os.Environ(), "NODERAMPART_GEO_CAPABILITY_FIXTURE="+mode)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("isolated %s capability fixture failed: %v\n%s", mode, err, output)
			}
			t.Log(strings.TrimSpace(string(output)))
		})
	}
}

func TestMMDBValidatorServiceCapabilitiesFixture(t *testing.T) {
	mode := os.Getenv("NODERAMPART_GEO_CAPABILITY_FIXTURE")
	if mode != "legacy" && mode != "without_socket_write" && mode != "fixed" {
		t.Skip("invoked only by the isolated capability regression")
	}
	path := filepath.Join(t.TempDir(), "fixture.mmdb")
	if err := os.WriteFile(path, syntheticMMDB("GeoLite2-City", 1767225600), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	result, err := verifyMMDBFile(context.Background(), file, "GeoLite2-City")
	if mode == "legacy" {
		diagnostic, ok := GeoUpdateDiagnostic(err)
		if err == nil || !ok || diagnostic.Reason != "validator_privilege_drop_failed" || diagnostic.Stage != "validation" || result.BuildEpoch != 0 {
			t.Fatalf("historical capability set did not expose the expected privilege-drop failure: %+v %v", diagnostic, err)
		}
		return
	}
	if err != nil || result.BuildEpoch != 1767225600 {
		t.Fatalf("corrected capability set did not validate the synthetic MMDB: %v", err)
	}
	assertGeoUpdaterControlSocketAccess(t, mode)

	base := t.TempDir()
	blocking := filepath.Join(base, "blocking.mmdb")
	if err := os.WriteFile(blocking, []byte("noderampart-validator-drop-block\n"+base), 0o600); err != nil {
		t.Fatal(err)
	}
	input, err := os.Open(blocking)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	finished := make(chan struct{})
	defer func() {
		cancel()
		<-finished // A Fatal path must join before deferred input.Close.
	}()
	go func() {
		defer close(finished)
		_, err := verifyMMDBFile(ctx, input, "GeoLite2-City")
		done <- err
	}()
	pid, err := strconv.Atoi(strings.TrimSpace(string(waitValidatorFixture(t, filepath.Join(base, "ready")))))
	if err != nil {
		t.Fatal("invalid privilege-drop readiness")
	}
	descendant, err := strconv.Atoi(strings.TrimSpace(string(waitValidatorFixture(t, filepath.Join(base, "descendant")))))
	if err != nil {
		t.Fatal("invalid privilege-drop descendant identity")
	}
	parentState, parentErr := readValidatorFixtureProcess(pid)
	childState, childErr := readValidatorFixtureProcess(descendant)
	if parentErr != nil || childErr != nil || parentState.ppid != os.Getpid() || parentState.pgid != pid || childState.ppid != pid || childState.pgid != pid {
		t.Fatal("privilege-drop cancellation fixture was not an owned process group")
	}
	started := time.Now()
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) || time.Since(started) > time.Second {
		t.Fatalf("corrected capability set could not promptly cancel the nobody process group: %v", err)
	}
	waitValidatorProcessesStopped(t, base, []validatorFixtureProcess{parentState, childState})
}

func assertGeoUpdaterControlSocketAccess(t *testing.T, mode string) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Fatal("control socket fixture must exercise the root updater identity")
	}
	// Both cases retain the MMDB privilege-drop/cancellation capabilities and
	// ambient SETUID. Only the fixed case can bypass the daemon socket's DAC.
	expected := uint32(1<<unix.CAP_CHOWN | 1<<unix.CAP_DAC_READ_SEARCH | 1<<unix.CAP_FOWNER |
		1<<unix.CAP_KILL | 1<<unix.CAP_SETGID | 1<<unix.CAP_SETUID)
	if mode == "fixed" {
		expected |= 1 << unix.CAP_DAC_OVERRIDE
	}
	header := unix.CapUserHeader{Version: unix.LINUX_CAPABILITY_VERSION_3}
	var capabilities [2]unix.CapUserData
	if err := unix.Capget(&header, &capabilities[0]); err != nil ||
		capabilities[0].Permitted != expected || capabilities[0].Effective != expected ||
		capabilities[0].Inheritable != 1<<unix.CAP_SETUID || capabilities[1] != (unix.CapUserData{}) {
		t.Fatal("control socket fixture did not retain exactly the requested capability set")
	}
	ambient, ambientErr := unix.PrctlRetInt(unix.PR_CAP_AMBIENT, unix.PR_CAP_AMBIENT_IS_SET, unix.CAP_SETUID, 0, 0)
	noNewPrivileges, noNewPrivilegesErr := unix.PrctlRetInt(unix.PR_GET_NO_NEW_PRIVS, 0, 0, 0, 0)
	if ambientErr != nil || ambient != 1 || noNewPrivilegesErr != nil || noNewPrivileges != 1 {
		t.Fatal("control socket fixture lost ambient SETUID or NoNewPrivileges")
	}
	account, err := user.Lookup("nobody")
	if err != nil {
		t.Fatal("control socket fixture requires an unprivileged owner:", err)
	}
	uid, uidErr := strconv.Atoi(account.Uid)
	gid, gidErr := strconv.Atoi(account.Gid)
	if uidErr != nil || gidErr != nil || uid <= 0 || gid <= 0 {
		t.Fatal("control socket fixture requires non-root ownership")
	}
	path := filepath.Join(t.TempDir(), "control.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal("create real control socket fixture:", err)
	}
	defer listener.Close()
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal("restrict control socket fixture permissions:", err)
	}
	if err := os.Chown(path, uid, gid); err != nil {
		t.Fatal("assign control socket fixture ownership:", err)
	}
	checkSocket := func() {
		t.Helper()
		var state unix.Stat_t
		if unix.Lstat(path, &state) != nil || state.Mode&unix.S_IFMT != unix.S_IFSOCK ||
			state.Mode&0o7777 != 0o600 || state.Uid != uint32(uid) || state.Gid != uint32(gid) {
			t.Fatal("control socket fixture ownership or owner-only permissions changed")
		}
	}
	checkSocket()
	connection, err := net.DialTimeout("unix", path, time.Second)
	if connection != nil {
		_ = connection.Close()
	}
	if mode == "without_socket_write" {
		if !errors.Is(err, unix.EACCES) {
			t.Fatalf("six-capability updater must fail the daemon-owned 0600 socket's write check: %v", err)
		}
	} else if err != nil || connection == nil {
		t.Fatal("seven-capability updater could not connect to the daemon-owned 0600 socket:", err)
	}
	checkSocket()
}
