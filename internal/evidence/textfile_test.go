// SPDX-License-Identifier: MIT

package evidence

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestTextfileAtomicReplacementAndPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "noderampart.prom")
	ctx := context.Background()
	if err := PublishTextfile(ctx, path, []byte("collection_success 1\n")); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Fatal("first export was not private")
	}
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := PublishTextfile(ctx, path, []byte("collection_success 0\n")); err != nil {
		t.Fatal(err)
	}
	info, _ = os.Stat(path)
	data, _ := os.ReadFile(path)
	if info.Mode().Perm() != 0o640 || string(data) != "collection_success 0\n" {
		t.Fatal("failed collection or group permission hidden")
	}
	cancelled, stop := context.WithCancel(ctx)
	stop()
	if err := PublishTextfile(cancelled, path, []byte("collection_success 1\n")); err == nil {
		t.Fatal("cancelled export published")
	}
	data, _ = os.ReadFile(path)
	if string(data) != "collection_success 0\n" {
		t.Fatal("cancellation changed old file")
	}
	if err := PublishTextfile(ctx, path, make([]byte, 16385)); err == nil {
		t.Fatal("oversize export accepted")
	}
}

func TestTextfileRejectsSymlinksHardlinksAndChangedTargets(t *testing.T) {
	dir := t.TempDir()
	original := filepath.Join(dir, "original")
	if err := os.WriteFile(original, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"symlink", "hardlink"} {
		path := filepath.Join(dir, kind)
		var err error
		if kind == "symlink" {
			err = os.Symlink(original, path)
		} else {
			err = os.Link(original, path)
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := PublishTextfile(context.Background(), path, []byte("changed")); err == nil {
			t.Fatal("unsafe target accepted", kind)
		}
	}
	if err := os.Remove(filepath.Join(dir, "hardlink")); err != nil {
		t.Fatal(err)
	}
	output, err := newOutputWithReplacement(original, true)
	if err != nil {
		t.Fatal(err)
	}
	defer output.close()
	if err := os.Remove(original); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(original, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := output.publish(); err == nil {
		t.Fatal("changed target overwritten")
	}
}
