// SPDX-License-Identifier: MIT

//go:build linux

package timezones

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

const fallbackChild = "NODERAMPART_TZ_LANDLOCK_HELPER"

// Keep subprocess diagnostics bounded even when the test runtime reports a
// timeout. These logs contain no environment values, host paths, or credentials.
type fallbackOutput struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (w *fallbackOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if available := (16 << 10) - w.b.Len(); available > 0 {
		w.b.Write(p[:min(available, len(p))])
	}
	return len(p), nil
}

func (w *fallbackOutput) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.String()
}

func TestEmbeddedTimezoneFallbackWithoutFilesystemReads(t *testing.T) {
	probe := filepath.Join(t.TempDir(), "read-probe")
	if err := os.WriteFile(probe, []byte("synthetic read probe"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestEmbeddedTimezoneLandlockHelper$", "-test.v", "-test.timeout=10s")
	cmd.WaitDelay = time.Second
	cmd.Env = append(os.Environ(), fallbackChild+"=1", "NODERAMPART_TZ_READ_PROBE="+probe)
	var output fallbackOutput
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Run(); err != nil {
		t.Fatalf("isolated fallback child: %v; %s", err, output.String())
	}
	if strings.Contains(output.String(), "NR_LANDLOCK_SKIP:") {
		t.Skip("kernel Landlock unavailable: actual embedded-rule fallback NOT RUN")
	}
	if !strings.Contains(output.String(), "NR_EMBEDDED_FALLBACK_PROVED") {
		t.Fatalf("child did not prove read denial and embedded rules: %s", output.String())
	}
}

func TestEmbeddedTimezoneLandlockHelper(t *testing.T) {
	if os.Getenv(fallbackChild) != "1" {
		return
	}
	// Only this disposable test subprocess changes its own security state. A
	// locked thread keeps all direct time.LoadLocation/open calls in the same
	// Landlock domain. Do not unlock it after restriction: it is terminated when
	// the testing goroutine exits, rather than reused by an unrelated goroutine.
	runtime.LockOSThread()
	thread := unix.Gettid()
	abi, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, 0, 0, unix.LANDLOCK_CREATE_RULESET_VERSION)
	if errno == unix.ENOSYS || errno == unix.EOPNOTSUPP {
		t.Skipf("NR_LANDLOCK_SKIP: %s", errno)
	}
	if errno != 0 || abi < 1 {
		t.Fatalf("Landlock ABI query: ABI=%d errno=%v", abi, errno)
	}
	probe := os.Getenv("NODERAMPART_TZ_READ_PROBE")
	if data, err := os.ReadFile(probe); err != nil || string(data) != "synthetic read probe" {
		t.Fatal("pre-restriction synthetic read control failed")
	}
	// On normal Linux installations this is a system TZif. A minimal host can
	// instead use its Go source archive as the existing external-rule control.
	var external string
	for _, source := range []string{
		"/usr/share/zoneinfo/Pacific/Chatham", "/usr/share/lib/zoneinfo/Pacific/Chatham",
		"/usr/lib/locale/TZ/Pacific/Chatham", "/etc/zoneinfo/Pacific/Chatham",
		filepath.Join(runtime.GOROOT(), "lib/time/zoneinfo.zip"),
	} {
		if data, err := os.ReadFile(source); err == nil && len(data) > 4 && (string(data[:4]) == "TZif" || string(data[:2]) == "PK") {
			external = source
			break
		}
	}
	if external == "" {
		t.Fatal("no existing external timezone-rule file for the denial control")
	}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		t.Fatal("no-new-privileges failed:", err)
	}
	// ABI 1 is exactly this eight-byte filesystem mask; do not pass the newer
	// x/sys struct's network/scope fields to old kernels. No allow rules means
	// all new file and directory reads on this thread are denied.
	handled := uint64(unix.LANDLOCK_ACCESS_FS_READ_FILE | unix.LANDLOCK_ACCESS_FS_READ_DIR)
	fd, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, uintptr(unsafe.Pointer(&handled)), unsafe.Sizeof(handled), 0)
	runtime.KeepAlive(&handled)
	if errno != 0 {
		t.Fatal("Landlock ruleset creation failed:", errno)
	}
	defer unix.Close(int(fd))
	if _, _, errno = unix.Syscall(unix.SYS_LANDLOCK_RESTRICT_SELF, fd, 0, 0); errno != 0 {
		t.Fatal("Landlock restriction failed:", errno)
	}
	for _, path := range []string{probe, external} {
		if _, err := os.ReadFile(path); !errors.Is(err, unix.EACCES) {
			t.Fatalf("new file read was not denied: %v", err)
		}
	}
	if _, err := os.ReadDir(filepath.Dir(probe)); !errors.Is(err, unix.EACCES) {
		t.Fatalf("new directory read was not denied: %v", err)
	}
	for _, tc := range []struct {
		name string
		at   time.Time
		want string
	}{
		{"Pacific/Chatham", time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC), "UTC+13:45"},
		{"Pacific/Chatham", time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC), "UTC+12:45"},
		{"Asia/Kathmandu", time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC), "UTC+05:45"},
		{"America/New_York", time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC), "UTC-05:00"},
		{"America/New_York", time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC), "UTC-04:00"},
	} {
		loc, err := time.LoadLocation(tc.name)
		if err != nil || Offset(tc.at.In(loc)) != tc.want {
			t.Fatalf("embedded rules for %s: %v", tc.name, err)
		}
	}
	if _, err := time.LoadLocation("Fixture/Unknown_Timezone"); err == nil {
		t.Fatal("embedded fallback replaced unknown timezone with UTC")
	}
	if unix.Gettid() != thread {
		t.Fatal("filesystem denial and timezone checks ran on different threads")
	}
	fmt.Println("NR_EMBEDDED_FALLBACK_PROVED")
}
