// SPDX-License-Identifier: MIT

package manage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestGeoStreamingCopyPreservesBoundsIdentityAndCancellation(t *testing.T) {
	for _, kind := range []string{"valid", "symlink", "hardlink", "oversize", "cancel", "digest"} {
		t.Run(kind, func(t *testing.T) {
			m, _ := fixtureManager(t)
			base := filepath.Dir(m.ConfigPath)
			source, target := filepath.Join(base, "source"), filepath.Join(base, "target")
			data := bytes.Repeat([]byte("synthetic-data"), 5000)
			if err := os.WriteFile(source, data, 0o600); err != nil {
				t.Fatal(err)
			}
			maximum := int64(len(data))
			expected := digest(data)
			ctx := context.Background()
			switch kind {
			case "symlink":
				link := filepath.Join(base, "link")
				if err := os.Symlink(source, link); err != nil {
					t.Fatal(err)
				}
				source = link
			case "hardlink":
				if err := os.Link(source, filepath.Join(base, "link")); err != nil {
					t.Fatal(err)
				}
			case "oversize":
				maximum--
			case "cancel":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "digest":
				expected = digest([]byte("different-validated-data"))
			}
			err := m.copyGeoFile(ctx, source, target, maximum, expected)
			if kind == "valid" {
				copied, readErr := os.ReadFile(target)
				info, statErr := os.Stat(target)
				if err != nil || readErr != nil || statErr != nil || !bytes.Equal(data, copied) || info.Mode().Perm() != 0o640 {
					t.Fatalf("stream copy failed: %v %v %v", err, readErr, statErr)
				}
			} else {
				if err == nil {
					t.Fatal("unsafe/cancelled source was published")
				}
				if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("failed streaming copy left published output")
				}
			}
		})
	}
}

func TestGeoCopyDetectsSourceMutationBeforePublishing(t *testing.T) {
	m, _ := fixtureManager(t)
	source := filepath.Join(filepath.Dir(m.ConfigPath), "source")
	if err := os.WriteFile(source, []byte("original bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, before, err := openManagedFile(source, 64, false, -1)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	reader := &geoCopyReader{ctx: context.Background(), file: f, before: before, hash: sha256.New()}
	buffer := make([]byte, 3)
	if _, err := reader.Read(buffer); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("changed bytes larger!"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, reader); err == nil {
		t.Fatal("mutated source accepted at end of copy")
	}
	var current unix.Stat_t
	if unix.Fstat(int(f.Fd()), &current) != nil || current.Size == before.Size {
		t.Fatal("fixture did not change source size")
	}
}

func TestGeoUnchangedCannotBypassValidatedSourceDigest(t *testing.T) {
	m, _, _, _ := installedGeoFixture(t)
	snapshot, err := m.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(snapshot.Config.Geo.CityMMDB)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, entry := range entries {
		files[entry.Name()] = filepath.Join(dir, entry.Name())
	}
	unchanged, err := m.geoUnchanged(context.Background(), snapshot, files, map[string]string{"GeoLite2-City.mmdb": digest([]byte("different verified contents"))})
	if unchanged || err == nil {
		t.Fatal("unchanged detection bypassed the pinned validation digest")
	}
}

func BenchmarkGeoFileStreamingCopy(b *testing.B) {
	base := b.TempDir()
	source, target := filepath.Join(base, "source"), filepath.Join(base, "target")
	file, err := os.OpenFile(source, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		b.Fatal(err)
	}
	const size = 64 << 20
	if err := file.Truncate(size); err != nil {
		b.Fatal(err)
	}
	_ = file.Close()
	m := &Manager{daemonGID: os.Getegid()}
	b.SetBytes(size)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if err := m.copyGeoFile(context.Background(), source, target, size, ""); err != nil {
			b.Fatal(err)
		}
		if err := os.Remove(target); err != nil {
			b.Fatal(err)
		}
	}
}
