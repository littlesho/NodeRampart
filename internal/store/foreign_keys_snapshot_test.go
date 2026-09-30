// SPDX-License-Identifier: MIT

package store

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func orphanSnapshotFixture(t *testing.T, version, count int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orphan-snapshot.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if err := downgradeSnapshotSchemaReference(context.Background(), db, version); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM schema_migrations WHERE version>?`, version); err != nil {
		t.Fatal(err)
	}
	// Construct damaged historical input only in the synthetic fixture. The
	// diagnostic and production migration never disable foreign-key checks.
	if _, err := db.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < count; index++ {
		if _, err := db.Exec(`INSERT INTO event_notifications(event_id,notification_id,decision,silence_id,recorded_at) VALUES(?,'','ineligible','',0)`, fmt.Sprintf("missing-%d", index)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`PRAGMA journal_mode=DELETE`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestForeignKeySnapshotReportsLegacyOrphansReadOnlyWithoutMigration(t *testing.T) {
	for _, version := range []int{6, 10} {
		t.Run(fmt.Sprintf("schema_%d", version), func(t *testing.T) {
			path := orphanSnapshotFixture(t, version, 105)
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			for run := 0; run < 2; run++ {
				status, err := InspectForeignKeysSnapshot(context.Background(), path)
				if err != nil || !status.Truncated || len(status.Violations) != 100 || status.Violations[0].Table != "event_notifications" || status.Violations[0].Parent != "events" {
					t.Fatalf("status=%+v err=%v", status, err)
				}
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("read-only diagnostic changed snapshot bytes", err)
			}
			db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			var actual, count int
			if err := db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&actual); err != nil || actual != version {
				t.Fatal("diagnostic migrated historical schema", actual, err)
			}
			if err := db.QueryRow(`SELECT COUNT(*) FROM event_notifications`).Scan(&count); err != nil || count != 105 {
				t.Fatal("diagnostic deleted orphan history", count, err)
			}
			for _, suffix := range []string{"-wal", "-shm", "-journal"} {
				if _, err := os.Lstat(path + suffix); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("diagnostic left source sidecar", suffix, err)
				}
			}
		})
	}
}

func TestForeignKeySnapshotRejectsFutureUnknownUnsafeAndActiveInputs(t *testing.T) {
	for _, fault := range []string{"future", "unknown", "wal", "shm", "journal", "symlink", "hardlink", "writable", "parent_symlink", "fifo"} {
		t.Run(fault, func(t *testing.T) {
			path := orphanSnapshotFixture(t, 10, 0)
			inspect := path
			switch fault {
			case "future", "unknown":
				db, err := sql.Open("sqlite", path)
				if err != nil {
					t.Fatal(err)
				}
				if fault == "future" {
					for value := 11; value <= schemaVersion+1; value++ {
						if _, err := db.Exec(`INSERT INTO schema_migrations(version,applied_at) VALUES(?,0)`, value); err != nil {
							t.Fatal(err)
						}
					}
				} else {
					if _, err := db.Exec(`DELETE FROM schema_migrations WHERE version=5`); err != nil {
						t.Fatal(err)
					}
				}
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}
			case "wal", "shm", "journal":
				if err := os.WriteFile(path+"-"+fault, []byte("preserve unresolved evidence"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				inspect = filepath.Join(filepath.Dir(path), "linked.db")
				if err := os.Symlink(path, inspect); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(path, filepath.Join(filepath.Dir(path), "other.db")); err != nil {
					t.Fatal(err)
				}
			case "writable":
				if err := os.Chmod(path, 0o666); err != nil {
					t.Fatal(err)
				}
			case "parent_symlink":
				link := filepath.Join(t.TempDir(), "linked-parent")
				if err := os.Symlink(filepath.Dir(path), link); err != nil {
					t.Fatal(err)
				}
				inspect = filepath.Join(link, filepath.Base(path))
			case "fifo":
				inspect = filepath.Join(filepath.Dir(path), "input.fifo")
				if err := unix.Mkfifo(inspect, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := InspectForeignKeysSnapshot(context.Background(), inspect); !errors.Is(err, ErrForeignKeySnapshotUnavailable) {
				t.Fatalf("unsafe/unsupported %s became healthy: %v", fault, err)
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("failed inspection modified source", err)
			}
			if fault == "wal" || fault == "shm" || fault == "journal" {
				data, err := os.ReadFile(path + "-" + fault)
				if err != nil || string(data) != "preserve unresolved evidence" {
					t.Fatal("inspection discarded sidecar evidence", err)
				}
			}
		})
	}
}

func TestForeignKeySnapshotCancellationAndCleanLegacyInput(t *testing.T) {
	path := orphanSnapshotFixture(t, 6, 0)
	status, err := InspectForeignKeysSnapshot(context.Background(), path)
	if err != nil || status.Truncated || len(status.Violations) != 0 {
		t.Fatalf("clean supported snapshot=%+v %v", status, err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := InspectForeignKeysSnapshot(ctx, path); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled inspection proceeded", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("cancelled inspection changed source", err)
	}
}

func TestForeignKeySnapshotCancelsDuringLockedSourceAndRejectsOversize(t *testing.T) {
	path := orphanSnapshotFixture(t, 10, 1)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()
	if _, err := db.Exec(`BEGIN EXCLUSIVE`); err != nil {
		t.Fatal(err)
	}
	defer db.Exec(`ROLLBACK`)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	if _, err := InspectForeignKeysSnapshot(ctx, path); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("locked inspection ignored cancellation: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("cancelled locked inspection changed source", err)
	}
	oversize := filepath.Join(t.TempDir(), "oversize.db")
	file, err := os.OpenFile(oversize, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate((1 << 40) + 1); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectForeignKeysSnapshot(context.Background(), oversize); !errors.Is(err, ErrForeignKeySnapshotUnavailable) {
		t.Fatal("oversize snapshot accepted", err)
	}
	info, err := os.Stat(oversize)
	if err != nil || info.Size() != (1<<40)+1 {
		t.Fatal("oversize diagnostic changed source", err)
	}
}
