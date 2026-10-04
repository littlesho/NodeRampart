// SPDX-License-Identifier: MIT

package assets

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/oschwald/maxminddb-golang"
	"golang.org/x/sys/unix"
)

func TestMain(m *testing.M) {
	if len(os.Args) >= 3 && os.Args[1] == "assets" && os.Args[2] == "validate-mmdb" {
		input := os.NewFile(3, "synthetic-validator-input")
		prefix := make([]byte, 256)
		n, _ := input.ReadAt(prefix, 0)
		if bytes.HasPrefix(prefix[:n], []byte("noderampart-validator-drop-block\n")) {
			base := strings.TrimSpace(string(prefix[len("noderampart-validator-drop-block\n"):n]))
			ready, readyErr := os.OpenFile(filepath.Join(base, "ready"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
			descendant, descendantErr := os.OpenFile(filepath.Join(base, "descendant"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
			if readyErr != nil || descendantErr != nil || constrainValidator() != nil || os.Geteuid() == 0 || !validatorFixtureCapabilitiesCleared() {
				os.Exit(1)
			}
			// Retained fixture descriptors permit readiness reporting after the
			// real privilege drop, without writable paths for nobody. Both this
			// process and its descendant now require the parent's CAP_KILL.
			child := exec.Command("/bin/sleep", "2")
			child.Stdout, child.Stderr = os.Stdout, os.Stderr
			if child.Start() != nil {
				os.Exit(1)
			}
			_, _ = descendant.WriteString(strconv.Itoa(child.Process.Pid))
			_ = descendant.Close()
			_, _ = ready.WriteString(strconv.Itoa(os.Getpid()))
			_ = ready.Close()
			// A broken cancellation still self-terminates this synthetic fixture.
			time.Sleep(2 * time.Second)
			_ = child.Wait()
			os.Exit(1)
		}
		if bytes.HasPrefix(prefix[:n], []byte("noderampart-validator-block\n")) {
			base := strings.TrimSpace(string(prefix[len("noderampart-validator-block\n"):n]))
			child := exec.Command("/bin/sh", "-c", `echo $$ > "$1/descendant"; sleep 1 & grandchild=$!; echo "$grandchild" > "$1/grandchild"; wait "$grandchild"; echo late > "$1/late"`, "fixture", base)
			child.Stdout, child.Stderr = os.Stdout, os.Stderr
			if child.Start() != nil || os.WriteFile(filepath.Join(base, "ready"), []byte(strconv.Itoa(os.Getpid())), 0o600) != nil {
				os.Exit(1)
			}
			time.Sleep(time.Hour) // The parent cancels this owned synthetic group.
			os.Exit(1)
		}
		if bytes.HasPrefix(prefix[:n], []byte("noderampart-validator-overflow")) {
			_, _ = os.Stdout.Write(bytes.Repeat([]byte("x"), 4096))
			os.Exit(0)
		}
		if bytes.HasPrefix(prefix[:n], []byte("noderampart-validator-failure\n")) {
			_, _ = input.Seek(int64(len("noderampart-validator-failure\n")), io.SeekStart)
			_, _ = io.Copy(os.Stdout, input)
			os.Exit(1)
		}
		if err := ValidatorCommand(os.Args[3:], os.Stdout); err != nil {
			os.Exit(1)
		}
		capabilitiesCleared := validatorFixtureCapabilitiesCleared()
		runtime.KeepAlive(input)
		var cpu unix.Rlimit
		if os.Geteuid() == 0 || !capabilitiesCleared || unix.Getrlimit(unix.RLIMIT_CPU, &cpu) != nil || cpu.Max > 30 {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// The validator must lose authority after its real UID transition even when
// systemd starts the updater with ambient CAP_SETUID. Inheritable and bounding
// sets may remain nonempty; they do not grant the nobody parser effective caps.
func validatorFixtureCapabilitiesCleared() bool {
	header := unix.CapUserHeader{Version: unix.LINUX_CAPABILITY_VERSION_3}
	var capabilities [2]unix.CapUserData
	if unix.Capget(&header, &capabilities[0]) != nil {
		return false
	}
	for _, capability := range capabilities {
		if capability.Permitted != 0 || capability.Effective != 0 {
			return false
		}
	}
	status, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(status), "\n") {
		if value, ok := strings.CutPrefix(line, "CapAmb:"); ok {
			ambient, err := strconv.ParseUint(strings.TrimSpace(value), 16, 64)
			return err == nil && ambient == 0
		}
	}
	return false
}

func TestMMDBValidatorFailureDiagnosticsAreBoundedAndAllowlisted(t *testing.T) {
	for _, tc := range []struct {
		name, response, reason string
	}{
		{"rejected", `{"reason":"validation_rejected"}`, "validation_rejected"},
		{"budget", `{"reason":"resource_budget"}`, "resource_budget"},
		{"unknown", `{"reason":"SYNTHETIC_PRIVATE_CONTENT"}`, ""},
		{"extra", `{"reason":"resource_budget","secret":"SYNTHETIC_PRIVATE_CONTENT"}`, ""},
		{"trailing", `{"reason":"resource_budget"}{}`, ""},
		{"epoch", `{"build_epoch":1767225600,"reason":"resource_budget"}`, ""},
		{"digest", `{"sha256":"SYNTHETIC_PRIVATE_CONTENT","reason":"resource_budget"}`, ""},
		{"overflow", strings.Repeat(" ", 1024) + `{"reason":"resource_budget"}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "fixture.mmdb")
			if err := os.WriteFile(path, []byte("noderampart-validator-failure\n"+tc.response), 0o600); err != nil {
				t.Fatal(err)
			}
			file, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			_, err = verifyMMDBFile(context.Background(), file, "GeoLite2-ASN")
			edition, reason, ok := GeoValidationDiagnostic(err)
			if err == nil || strings.Contains(err.Error(), "SYNTHETIC") || reason != tc.reason || ok != (tc.reason != "") || ok && edition != "ASN" {
				t.Fatalf("unsafe or inaccurate child diagnostic: edition=%q reason=%q ok=%t error=%v", edition, reason, ok, err)
			}
		})
	}
}

func TestMMDBValidatorPinnedInputAndResponseBounds(t *testing.T) {
	for _, kind := range []string{"valid", "invalid", "overflow", "oversize", "wrong-edition"} {
		t.Run(kind, func(t *testing.T) {
			data := syntheticMMDB("GeoLite2-City", 1767225600)
			edition := "GeoLite2-City"
			if kind == "invalid" {
				data = []byte("synthetic-private-content")
			} else if kind == "overflow" {
				data = []byte("noderampart-validator-overflow")
			} else if kind == "wrong-edition" {
				edition = "GeoLite2-ASN"
			}
			path := filepath.Join(t.TempDir(), "fixture.mmdb")
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			file, err := os.OpenFile(path, os.O_RDWR, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			if kind == "oversize" {
				if err := file.Truncate(maxDatabase + 1); err != nil {
					t.Fatal(err)
				}
			}
			result, err := verifyMMDBFile(context.Background(), file, edition)
			if kind == "valid" {
				sum := sha256.Sum256(data)
				if err != nil || result.BuildEpoch != 1767225600 || result.Digest != hex.EncodeToString(sum[:]) {
					t.Fatalf("valid MMDB or source identity rejected: %#v %v", result, err)
				}
			} else if err == nil || strings.Contains(err.Error(), "synthetic-private-content") {
				t.Fatalf("unsafe MMDB accepted or content exposed: %v", err)
			}
		})
	}
}

func waitValidatorFixture(t *testing.T, path string) []byte {
	t.Helper()
	deadline, tick := time.NewTimer(3*time.Second), time.NewTicker(5*time.Millisecond)
	defer deadline.Stop()
	defer tick.Stop()
	for {
		if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
			return data
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatal("validator fixture did not become ready")
		}
	}
}

type validatorFixtureProcess struct {
	pid, ppid, pgid int
	state           string
	startTime       uint64
}

func readValidatorFixtureProcess(pid int) (validatorFixtureProcess, error) {
	process := validatorFixtureProcess{pid: pid}
	stat, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		// procfs can open stat just before exit, then return ESRCH on read.
		// Require independent ENOENT evidence; ESRCH alone is not success.
		if errors.Is(err, unix.ESRCH) {
			if _, gone := os.Stat(filepath.Join("/proc", strconv.Itoa(pid))); errors.Is(gone, os.ErrNotExist) {
				return process, gone
			}
		}
		return process, err
	}
	// The command name can contain spaces and ')'; fields after its final ')'
	// start at field 3 (state). Field 22 (starttime) identifies this PID's owner.
	end := bytes.LastIndexByte(stat, ')')
	fields := strings.Fields(string(stat[end+1:]))
	if end < 0 || len(fields) < 20 || len(fields[0]) != 1 {
		return process, errors.New("invalid validator fixture process stat")
	}
	process.state = fields[0]
	if process.ppid, err = strconv.Atoi(fields[1]); err != nil {
		return process, err
	}
	if process.pgid, err = strconv.Atoi(fields[2]); err != nil {
		return process, err
	}
	process.startTime, err = strconv.ParseUint(fields[19], 10, 64)
	return process, err
}

func assertValidatorNoLateOutput(t *testing.T, base string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(base, "late")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled validator descendant wrote late output or could not be checked: %v", err)
	}
}

func waitValidatorProcessesStopped(t *testing.T, base string, processes []validatorFixtureProcess) {
	t.Helper()
	// Cancellation signals the whole group, but Run waits for the direct child
	// and I/O, not every descendant's /proc state
	// transition. Allow only a bounded one-second transition (the WaitDelay
	// scale); never wait for zombies to be reaped or accept other read errors.
	deadline := time.Now().Add(time.Second)
	for {
		assertValidatorNoLateOutput(t, base)
		stopped := true
		for _, original := range processes {
			current, err := readValidatorFixtureProcess(original.pid)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				t.Fatalf("read owned validator process %d: %v", original.pid, err)
			}
			if current.startTime != original.startTime || current.state == "Z" || current.state == "X" {
				continue // The original stopped; a reused PID is not ours.
			}
			stopped = false
			if !time.Now().Before(deadline) {
				t.Fatalf("owned validator process %d survived cancellation: %+v", original.pid, current)
			}
		}
		if stopped {
			assertValidatorNoLateOutput(t, base)
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestMMDBValidatorCancellationStopsOwnedChildAndDescendant(t *testing.T) {
	base := t.TempDir()
	path := filepath.Join(base, "fixture.mmdb")
	if err := os.WriteFile(path, []byte("noderampart-validator-block\n"+base), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	finished := make(chan struct{})
	defer func() {
		cancel()
		<-finished // Cancellation alone does not synchronize File.Fd with Close.
	}()
	go func() {
		defer close(finished)
		_, err := verifyMMDBFile(ctx, file, "GeoLite2-City")
		done <- err
	}()
	parentData := waitValidatorFixture(t, filepath.Join(base, "ready"))
	childData := waitValidatorFixture(t, filepath.Join(base, "descendant"))
	grandchildData := waitValidatorFixture(t, filepath.Join(base, "grandchild"))
	var processes []validatorFixtureProcess
	for _, value := range [][]byte{parentData, childData, grandchildData} {
		pid, err := strconv.Atoi(strings.TrimSpace(string(value)))
		if err != nil || pid <= 0 {
			t.Fatalf("invalid validator fixture PID %q: %v", value, err)
		}
		process, err := readValidatorFixtureProcess(pid)
		if err != nil {
			t.Fatal("read validator fixture before cancellation:", err)
		}
		processes = append(processes, process)
	}
	for i, process := range processes {
		if process.pgid != processes[0].pid || i > 0 && process.ppid != processes[i-1].pid {
			t.Fatalf("validator fixture escaped its owned process group or parent: %+v", processes)
		}
	}
	defer assertValidatorNoLateOutput(t, base)
	cancel()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "cancelled") {
			t.Fatalf("cancellation result: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("validator cancellation did not return")
	}
	waitValidatorProcessesStopped(t, base, processes)
}

func expandedEmptyMMDB(t *testing.T, count int) []byte {
	t.Helper()
	small := syntheticMMDB("GeoLite2-City", 1767225600)
	metadata := append([]byte(nil), small[22:]...)
	key := []byte("node_count")
	index := bytes.Index(metadata, key) + len(key)
	metadata = append(append(append([]byte(nil), metadata[:index]...), 0xc3, byte(count>>16), byte(count>>8), byte(count)), metadata[index+2:]...)
	tree := make([]byte, count*6+16)
	for node := range count {
		for branch := range 2 {
			child := min(node*2+1+branch, count)
			position := node*6 + branch*3
			tree[position], tree[position+1], tree[position+2] = byte(child>>16), byte(child>>8), byte(child)
		}
	}
	return append(tree, metadata...)
}

func TestMMDBUpstreamVerifyHasNoCancellationAndLargeValidFixture(t *testing.T) {
	data := expandedEmptyMMDB(t, (1<<20)-1)
	reader, err := maxminddb.FromBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()
	// Upstream has no context argument: even a cancelled caller must wait for
	// the complete traversal. Process isolation supplies that missing boundary.
	if err := reader.Verify(); err != nil || ctx.Err() == nil {
		t.Fatalf("valid large verifier fixture: %v", err)
	}
	t.Logf("upstream Verify traversed %d valid bytes despite cancelled context in %s", len(data), time.Since(started))
	if _, err := verifyMMDB(context.Background(), data, "GeoLite2-City"); err != nil {
		t.Fatal("bounded preflight rejected the valid generated database:", err)
	}
	path := filepath.Join(t.TempDir(), "large.mmdb")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := verifyMMDBFile(context.Background(), file, "GeoLite2-City"); err != nil {
		t.Fatal("resource limits rejected a valid generated database:", err)
	}
}

func TestAssetStreamBoundsAndCancelledCopy(t *testing.T) {
	for _, size := range []int{31, 32, 33} {
		stream := &assetStream{body: io.NopCloser(bytes.NewReader(make([]byte, size))), cancel: func() {}, remaining: 33}
		data, err := io.ReadAll(stream)
		if size <= 32 && (err != nil || len(data) != size) || size > 32 && err == nil {
			t.Fatalf("stream bound size %d: %d %v", size, len(data), err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	reader := &geoContextReader{ctx: ctx, reader: strings.NewReader("data")}
	if _, err := io.Copy(io.Discard, reader); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled extraction continued reading")
	}
}
