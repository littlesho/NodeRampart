// SPDX-License-Identifier: MIT

package manage

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestServiceIdentityBounds(t *testing.T) {
	for _, id := range []uint32{0, 1, 1000, 65534, 1<<31 - 1, 1 << 31, 1<<32 - 2, 1<<32 - 1} {
		t.Run(strconv.FormatUint(uint64(id), 10), func(t *testing.T) {
			wantOK := id != 1<<32-1 && (strconv.IntSize == 64 || id <= 1<<31-1)
			uid, uidErr := checkedServiceID(id)
			gid, gidErr := serviceGroupID(strconv.FormatUint(uint64(id), 10))
			if (uidErr == nil) != wantOK || (gidErr == nil) != wantOK {
				t.Fatalf("ID %d on int%d: UID error=%v, GID error=%v", id, strconv.IntSize, uidErr, gidErr)
			}
			if wantOK && (uid < 0 || gid < 0 || uint64(uid) != uint64(id) || uint64(gid) != uint64(id)) {
				t.Fatal("valid identity changed during conversion")
			}
		})
	}
}

func TestServiceGroupIDRejectsInvalidOwnership(t *testing.T) {
	// Atoi alone accepts negatives and (on our 64-bit release targets) values
	// above uint32. These can alias a different group or chown's -1 sentinel.
	for _, value := range []string{
		"", "-1", "-2", "+1", " 1000", "1000 ", "group", "1.5", "0x10",
		"4294967295", "4294967296", "4294968296", "9223372036854775807",
		"18446744073709551615", "18446744073709551616",
	} {
		t.Run(value, func(t *testing.T) {
			if _, err := serviceGroupID(value); err == nil {
				t.Fatal("invalid group identity accepted for DAC and chown")
			}
		})
	}
	if id, err := serviceGroupID("001000"); err != nil || id != 1000 {
		t.Fatal("decimal group identity with leading zeroes changed")
	}
}

func TestServiceGroupIDPreservesDAC(t *testing.T) {
	for _, value := range []string{"0", "1000", "2147483647", "2147483648", "4294967294"} {
		t.Run(value, func(t *testing.T) {
			if strconv.IntSize == 32 && (value == "2147483648" || value == "4294967294") {
				t.Skip("identity intentionally rejected on 32-bit builds")
			}
			gid, err := serviceGroupID(value)
			if err != nil {
				t.Fatal(err)
			}
			m := &Manager{daemonUID: 123, daemonGID: gid}
			st := unix.Stat_t{Uid: 456, Gid: uint32(gid), Mode: 0o040}
			if !m.dac(st, 4) {
				t.Fatal("matching service group lost read permission")
			}
			st.Gid = 42
			if m.dac(st, 4) {
				t.Fatal("unrelated group gained read permission")
			}
			st.Gid, st.Uid = uint32(gid), 123
			if m.dac(st, 4) {
				t.Fatal("group permissions overrode matching owner's restrictions")
			}
		})
	}
}

func TestServiceIdentityPreservesTemporaryFileOwnership(t *testing.T) {
	uid, err := checkedServiceID(uint32(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	gid, err := serviceGroupID(strconv.Itoa(os.Getegid()))
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "managed")
	if err := ensureDirectory(dir, 0o750, gid); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "synthetic")
	if err := writeFile(file, strings.NewReader("synthetic"), 64, 0o640, uid, gid, false); err != nil {
		t.Fatal(err)
	}
	for path, mode := range map[string]uint32{dir: 0o750, file: 0o640} {
		var st unix.Stat_t
		if err := unix.Stat(path, &st); err != nil {
			t.Fatal(err)
		}
		if uint64(st.Uid) != uint64(uid) || uint64(st.Gid) != uint64(gid) || st.Mode&0o7777 != mode {
			t.Fatal("managed temporary file/directory lost exact ownership or mode")
		}
	}
}
