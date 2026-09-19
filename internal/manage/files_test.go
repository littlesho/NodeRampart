// SPDX-License-Identifier: MIT

package manage

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagedPublicationRejectsUnsafePathsAndPreservesExisting(t *testing.T) {
	base := t.TempDir()
	file := filepath.Join(base, "output")
	write := func(path, data string, max int64, replace bool) error {
		return writeFile(path, strings.NewReader(data), max, 0o600, os.Geteuid(), os.Getegid(), replace)
	}
	if err := write(file, "original", 100, false); err != nil {
		t.Fatal(err)
	}
	if err := write(file, "replacement", 100, false); err == nil {
		t.Fatal("new-only overwrote file")
	}
	if err := write(file, "oversized", 3, true); err == nil {
		t.Fatal("size bound ignored")
	}
	if data, _ := os.ReadFile(file); string(data) != "original" {
		t.Fatal("failed publication changed target")
	}
	link := filepath.Join(base, "hardlink")
	if err := os.Link(file, link); err != nil {
		t.Fatal(err)
	}
	if err := write(file, "replacement", 100, true); err == nil {
		t.Fatal("hardlink replaced")
	}
	if _, err := readFile(file, 100, false, -1); err == nil {
		t.Fatal("hardlink read")
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(file, link); err != nil {
		t.Fatal(err)
	}
	if err := write(link, "replacement", 100, true); err == nil {
		t.Fatal("symlink replaced")
	}
	shared := filepath.Join(base, "shared")
	if err := os.Mkdir(shared, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(shared, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := write(filepath.Join(shared, "new"), "data", 100, false); err == nil {
		t.Fatal("shared writable output directory accepted")
	}
	parentLink := filepath.Join(base, "parent-link")
	if err := os.Symlink(base, parentLink); err != nil {
		t.Fatal(err)
	}
	if err := write(filepath.Join(parentLink, "new"), "data", 100, false); err == nil {
		t.Fatal("symlink parent accepted")
	}
}

func TestGenerationCleanupChecksAllMembersBeforeDeleting(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "gen-test")
	if err := os.Mkdir(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	regular := filepath.Join(dir, "GeoLite2-City.mmdb")
	if err := os.WriteFile(regular, []byte("synthetic"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(regular, filepath.Join(dir, "unsafe")); err != nil {
		t.Fatal(err)
	}
	if err := removeGeneration(dir); err == nil {
		t.Fatal("unsafe generation accepted")
	}
	if _, err := os.Stat(regular); err != nil {
		t.Fatal("safe member deleted before all validation")
	}
	if err := os.Remove(filepath.Join(dir, "unsafe")); err != nil {
		t.Fatal(err)
	}
	if err := removeGeneration(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("old generation remains")
	}
}
