// SPDX-License-Identifier: MIT

package privacy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestHashIPv6CanonicalizationAndKeySeparation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "synthetic-key")
	if err := os.WriteFile(path, []byte(strings.Repeat("a", 32)), 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := New("hash", path)
	if err != nil {
		t.Fatal(err)
	}
	first, _ := a.IP("2001:db8::1")
	canonical, _ := a.IP("2001:0db8:0:0:0:0:0:1")
	if first != canonical || !strings.HasPrefix(first, "ip_") || strings.Contains(first, ":") {
		t.Fatal("IPv6 hash leaked raw address or lost canonicalization")
	}
	v4, _ := a.IP("192.0.2.1")
	mapped, _ := a.IP("::ffff:192.0.2.1")
	if v4 != mapped {
		t.Fatal("IPv4 mapped representation changed identity")
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("b", 32)), 0o600); err != nil {
		t.Fatal(err)
	}
	b, err := New("hash", path)
	if err != nil {
		t.Fatal(err)
	}
	other, _ := b.IP("2001:db8::1")
	if first == other {
		t.Fatal("different privacy keys produced equal identity")
	}
	if full, prefix := a.IP("invalid"); full != "" || prefix != "" {
		t.Fatal("invalid address did not fail closed")
	}
}

func TestPrivacyKeyUnsafeInputsNeverFallBackToRawIP(t *testing.T) {
	for _, kind := range []string{"missing", "symlink", "fifo", "permissions", "short", "large", "whitespace"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "key")
			data := []byte(strings.Repeat("x", 32))
			if kind == "short" {
				data = []byte("short")
			}
			if kind == "large" {
				data = []byte(strings.Repeat("x", 4097))
			}
			if kind == "whitespace" {
				data = []byte(strings.Repeat(" ", 32))
			}
			if kind != "missing" && kind != "fifo" {
				if err := os.WriteFile(path, data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "fifo" {
				if err := unix.Mkfifo(path, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "symlink" {
				link := path + "-link"
				if err := os.Symlink(path, link); err != nil {
					t.Fatal(err)
				}
				path = link
			}
			if kind == "permissions" {
				if err := os.Chmod(path, 0o640); err != nil {
					t.Fatal(err)
				}
			}
			if transformer, err := New("hash", path); err == nil || transformer != nil {
				t.Fatal("unsafe key produced a usable transformer")
			}
		})
	}
}
