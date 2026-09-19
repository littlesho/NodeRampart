// SPDX-License-Identifier: MIT

package replay

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestOutputRejectsSharedParentsAndAllowsPrivateStickyChild(t *testing.T) {
	for _, mode := range []os.FileMode{0o770, 0o777, 0o777 | os.ModeSticky} {
		dir := t.TempDir()
		if err := os.Chmod(dir, mode); err != nil {
			t.Fatal(err)
		}
		if output, err := newOutput(filepath.Join(dir, "result.jsonl")); err == nil {
			output.close()
			t.Fatalf("accepted shared final directory %v", mode)
		}
		child := filepath.Join(dir, "private")
		if err := os.Mkdir(child, 0o700); err != nil {
			t.Fatal(err)
		}
		output, err := newOutput(filepath.Join(child, "result.jsonl"))
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
		info, err := os.Stat(filepath.Join(child, "result.jsonl"))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatal("private output mode changed")
		}
	}
}

func TestOutputRejectsReplacementAndPermissionChanges(t *testing.T) {
	for _, makeShared := range []bool{false, true} {
		dir := t.TempDir()
		final := filepath.Join(dir, "final.jsonl")
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
