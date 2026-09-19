// SPDX-License-Identifier: MIT

package evidence

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestOutputCancellationAndHardlinkBeforePublication(t *testing.T) {
	for _, hardlink := range []bool{false, true} {
		dir := t.TempDir()
		path := filepath.Join(dir, "result.zip")
		out, err := newOutput(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = out.file.WriteString("complete staged content"); err != nil {
			out.close()
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		if hardlink {
			if err := os.Link(filepath.Join(dir, out.temporary), filepath.Join(dir, "extra")); err != nil {
				out.close()
				cancel()
				t.Fatal(err)
			}
		} else {
			cancel()
		}
		err = out.publishContext(ctx)
		out.close()
		cancel()
		if err == nil {
			t.Fatal("cancelled or hardlinked staged file was published")
		}
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("rejected output became visible")
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if entry.Name() != "extra" {
				t.Fatal("temporary output leaked")
			}
		}
	}
}

func TestOutputRejectsSharedParentsAndAllowsPrivateStickyChild(t *testing.T) {
	for _, mode := range []os.FileMode{0o770, 0o777, 0o777 | os.ModeSticky} {
		dir := t.TempDir()
		if err := os.Chmod(dir, mode); err != nil {
			t.Fatal(err)
		}
		if output, err := newOutput(filepath.Join(dir, "result.zip")); err == nil {
			output.close()
			t.Fatalf("accepted shared final directory %v", mode)
		}
		child := filepath.Join(dir, "private")
		if err := os.Mkdir(child, 0o700); err != nil {
			t.Fatal(err)
		}
		output, err := newOutput(filepath.Join(child, "result.zip"))
		if mode&os.ModeSticky == 0 {
			if err == nil {
				output.close()
				t.Fatal("accepted private output beneath non-sticky shared ancestor")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, err := output.file.WriteString("validated metadata\n"); err != nil {
			output.close()
			t.Fatal(err)
		}
		if err := output.publish(); err != nil {
			output.close()
			t.Fatal(err)
		}
		output.close()
		info, err := os.Stat(filepath.Join(child, "result.zip"))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatal("private output mode changed")
		}
	}
}

func TestOutputRejectsReplacementAndPermissionChanges(t *testing.T) {
	for _, makeShared := range []bool{false, true} {
		dir := t.TempDir()
		final := filepath.Join(dir, "final.zip")
		output, err := newOutput(final)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := output.file.WriteString("validated metadata\n"); err != nil {
			output.close()
			t.Fatal(err)
		}
		if makeShared {
			if err := os.Chmod(dir, 0o777); err != nil {
				output.close()
				t.Fatal(err)
			}
		}
		// Same-process mutation models the previously vulnerable name replacement;
		// no privileged UID change or access to another user's files is needed.
		temporary := filepath.Join(dir, output.temporary)
		if err := os.Remove(temporary); err != nil {
			output.close()
			t.Fatal(err)
		}
		if err := os.WriteFile(temporary, []byte("unvalidated replacement\n"), 0o666); err != nil {
			output.close()
			t.Fatal(err)
		}
		if err := output.publish(); err == nil {
			output.close()
			t.Fatal("replacement was published")
		}
		output.close()
		if _, err := os.Stat(final); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("rejected output became visible")
		}
	}
}
