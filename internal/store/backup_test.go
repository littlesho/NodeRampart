// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/model"
)

func fileSizeIfPresent(path string) (int64, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

func TestBackupContainsCommittedWALAndRestoresNewDatabase(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "live.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.InsertEvent(ctx, model.Event{ID: "from-wal", ObservedAt: time.Now().UTC(), Kind: "test", Severity: model.SeverityInfo}); err != nil {
		t.Fatal(err)
	}
	walSize, err := fileSizeIfPresent(s.path + "-wal")
	if err != nil || walSize == 0 {
		t.Fatal("fixture has no committed WAL data")
	}
	target := filepath.Join(dir, "backups", "snapshot.db")
	info, err := s.Backup(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Path != target || info.Bytes == 0 || info.SchemaVersion != schemaVersion {
		t.Fatalf("bad backup metadata: %#v", info)
	}
	permissions, err := os.Stat(target)
	if err != nil || permissions.Mode().Perm() != 0o600 {
		t.Fatal("backup permissions are not 0600")
	}
	parent, err := os.Stat(filepath.Dir(target))
	if err != nil || parent.Mode().Perm() != 0o700 {
		t.Fatal("backup parent permissions are not 0700")
	}
	if _, err := VerifyBackup(ctx, target); err != nil {
		t.Fatal(err)
	}
	restored := filepath.Join(dir, "restored.db")
	if _, err := RestoreBackup(ctx, target, restored); err != nil {
		t.Fatal(err)
	}
	r, err := Open(restored)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var count int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE id='from-wal'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("restored snapshot lost committed WAL event: count=%d error=%v", count, err)
	}
	if _, err := RestoreBackup(ctx, target, restored); err == nil {
		t.Fatal("restore overwrote existing database")
	}
}

func TestBackupWorksWhileStrongBudgetOwnsDatabase(t *testing.T) {
	s := budgetStore(t)
	ctx := context.Background()
	if err := s.InsertEvent(ctx, model.Event{ID: "critical", ObservedAt: time.Now().UTC(), Kind: "test", Severity: model.SeverityInfo}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Backup(ctx, filepath.Join(t.TempDir(), "snapshot.db")); err != nil {
		t.Fatal(err)
	}
}

func TestBackupRejectsSymlinksExistingTargetsAndSidecars(t *testing.T) {
	s := budgetStore(t)
	ctx := context.Background()
	dir := t.TempDir()
	target := filepath.Join(dir, "exists.db")
	marker := []byte("keep")
	if err := os.WriteFile(target, marker, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Backup(ctx, target); err == nil {
		t.Fatal("existing target overwritten")
	}
	got, _ := os.ReadFile(target)
	if string(got) != string(marker) {
		t.Fatal("existing target changed")
	}
	link := filepath.Join(dir, "link.db")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Backup(ctx, link); err == nil {
		t.Fatal("target symlink followed")
	}
	parentLink := filepath.Join(dir, "parent")
	if err := os.Symlink(dir, parentLink); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Backup(ctx, filepath.Join(parentLink, "new.db")); err == nil {
		t.Fatal("ancestor symlink followed")
	}
	if err := os.WriteFile(filepath.Join(dir, "side.db-wal"), marker, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Backup(ctx, filepath.Join(dir, "side.db")); err == nil {
		t.Fatal("preexisting sidecar ignored")
	}
	if _, err := VerifyBackup(ctx, link); err == nil {
		t.Fatal("verification followed source symlink")
	}
}

func TestBackupCancellationLeavesNoPublishedOrTemporaryFile(t *testing.T) {
	s := budgetStore(t)
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Backup(ctx, filepath.Join(dir, "cancelled.db")); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation ignored: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("cancelled snapshot leaked files: %#v error=%v", entries, err)
	}
}

func TestVerifyBackupRejectsCorruptionAndFutureSchema(t *testing.T) {
	dir := t.TempDir()
	corrupt := filepath.Join(dir, "corrupt.db")
	if err := os.WriteFile(corrupt, []byte("SQLite format 3\x00broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyBackup(context.Background(), corrupt); err == nil {
		t.Fatal("corrupt backup accepted")
	}
	s := budgetStore(t)
	future := filepath.Join(dir, "future.db")
	if _, err := s.Backup(context.Background(), future); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", future)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE schema_migrations SET version=999 WHERE version=?`, schemaVersion); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()
	if _, err := VerifyBackup(context.Background(), future); err == nil {
		t.Fatal("unsupported future schema accepted")
	}
}

func TestVerifyBackupRejectsIncompleteClaimedSchema(t *testing.T) {
	for _, change := range []string{
		`DROP TABLE coverage_gaps`,
		`DROP TABLE journal_seen`,
		`DROP TABLE collector_checkpoints`,
		`DROP TABLE report_snapshots`,
		`ALTER TABLE notification_outbox DROP COLUMN expires_at`,
		`ALTER TABLE events DROP COLUMN summary`,
		`DELETE FROM schema_migrations WHERE version=2`,
	} {
		t.Run(change, func(t *testing.T) {
			s := budgetStore(t)
			path := filepath.Join(t.TempDir(), "incomplete.db")
			if _, err := s.Backup(context.Background(), path); err != nil {
				t.Fatal(err)
			}
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(change); err != nil {
				db.Close()
				t.Fatal(err)
			}
			db.Close()
			if _, err := VerifyBackup(context.Background(), path); err == nil {
				t.Fatal("incomplete/mislabeled backup passed schema validation")
			}
		})
	}
}

func TestVerifyBackupRefusesExclusivelyOwnedDatabase(t *testing.T) {
	s := budgetStore(t)
	if _, err := VerifyBackup(context.Background(), s.path); err == nil {
		t.Fatal("active exclusively owned database accepted as standalone snapshot")
	}
}
