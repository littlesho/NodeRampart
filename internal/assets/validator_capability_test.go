// SPDX-License-Identifier: MIT

package assets

import (
	"context"
	"errors"
	"os"
	"os/exec"
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
	for _, capability := range []uint{unix.CAP_CHOWN, unix.CAP_DAC_READ_SEARCH, unix.CAP_FOWNER, unix.CAP_KILL, unix.CAP_SETGID, unix.CAP_SETUID, unix.CAP_SETPCAP} {
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
	for _, mode := range []string{"legacy", "fixed"} {
		t.Run(mode, func(t *testing.T) {
			capabilities := "-all,+chown,+dac_read_search,+fowner"
			inheritable, ambient := "-all", "-all"
			if mode == "fixed" {
				capabilities += ",+kill,+setgid,+setuid"
				inheritable, ambient = "-all,+setuid", "-all,+setuid"
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
	if mode != "legacy" && mode != "fixed" {
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
