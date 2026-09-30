// SPDX-License-Identifier: MIT

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestLoadRejectsFIFOAndOversizeEvenWithValidJSONPrefix(t *testing.T) {
	directory := t.TempDir()
	fifo := filepath.Join(directory, "fifo")
	if err := unix.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(fifo); err == nil {
		t.Fatal("FIFO configuration accepted")
	}
	path := filepath.Join(directory, "oversize.json")
	if err := os.WriteFile(path, []byte("{}"+strings.Repeat(" ", maxConfigSize)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("valid JSON prefix bypassed the input size limit")
	}
}
