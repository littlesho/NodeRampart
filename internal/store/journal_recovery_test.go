// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// This helper deliberately exits without closing SQLite, modelling process
// death with committed WAL data and with an uncommitted recovery transaction.
func TestJournalRecoveryCrashHelper(t *testing.T) {
	mode := os.Getenv("NR_TEST_JOURNAL_CRASH")
	if mode == "" {
		return
	}
	if mode == "migration_uncommitted" {
		db, err := sql.Open("sqlite", os.Getenv("NR_TEST_JOURNAL_DB"))
		if err != nil {
			t.Fatal(err)
		}
		tx, err := db.BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := migrateV7(context.Background(), tx); err != nil {
			t.Fatal(err)
		}
		os.Exit(0)
	}
	s, err := Open(os.Getenv("NR_TEST_JOURNAL_DB"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	switch mode {
	case "pending":
		err = s.RecordJournalDegradation(ctx, recoveryGap(time.Now().UTC()))
	case "ready":
		_, err = s.CommitJournal(ctx, JournalWrite{Cursor: "after-crash", ReceivedAt: time.Now().UTC(), Trusted: true})
	case "uncommitted":
		tx, e := s.db.BeginTx(ctx, nil)
		if e != nil {
			t.Fatal(e)
		}
		if err = setJournalRecovery(ctx, tx, false); err == nil {
			_, err = tx.Exec(`UPDATE collector_checkpoints SET cursor='uncommitted' WHERE name='ssh_journal'`)
		}
	default:
		t.Fatal("invalid crash helper mode")
	}
	if err != nil {
		t.Fatal(err)
	}
	os.Exit(0)
}

func TestJournalRecoveryProcessCrashCommitBoundaries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "crash.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CommitJournal(context.Background(), JournalWrite{Cursor: "before-crash", ReceivedAt: time.Now().UTC(), Trusted: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"pending", "uncommitted", "ready"} {
		command := exec.Command(os.Args[0], "-test.run=^TestJournalRecoveryCrashHelper$")
		command.Env = append(os.Environ(), "NR_TEST_JOURNAL_CRASH="+mode, "NR_TEST_JOURNAL_DB="+path)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("crash helper: %v %s", err, output)
		}
		s, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		state, err := s.JournalCheckpoint(context.Background())
		gaps, gapErr := s.CoverageGaps(context.Background(), time.Now().Add(-time.Hour), time.Now().Add(time.Hour), 10)
		s.Close()
		wantCursor := "before-crash"
		if mode == "ready" {
			wantCursor = "after-crash"
		}
		if err != nil || gapErr != nil || state.RecoveryPending != (mode != "ready") || state.Cursor != wantCursor || len(gaps) != 1 {
			t.Fatalf("%s lost atomic recovery: %+v gaps=%d err=%v/%v", mode, state, len(gaps), err, gapErr)
		}
	}
}

func recoveryGap(at time.Time) CoverageGap {
	return CoverageGap{Name: "ssh_journal", Reason: "malformed_record", Start: at, End: at, Count: 1}
}

func TestJournalRecoveryTransactionsAndRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	at := time.Now().UTC().Truncate(time.Millisecond)
	before := JournalWrite{Cursor: "trusted-before", ReceivedAt: at, ObservedAt: at, Trusted: true}
	if _, err := s.CommitJournal(ctx, before); err != nil {
		t.Fatal(err)
	}
	// Historical gaps are audit data, not current pending state.
	if err := s.RecordCoverageGap(ctx, recoveryGap(at.Add(-time.Hour))); err != nil {
		t.Fatal(err)
	}
	state, err := s.JournalCheckpoint(ctx)
	if err != nil || state.RecoveryPending {
		t.Fatalf("historical gap invented pending: %+v %v", state, err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER reject_gap BEFORE INSERT ON coverage_gaps BEGIN SELECT RAISE(ABORT,'synthetic gap failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordJournalDegradation(ctx, recoveryGap(at)); err == nil {
		t.Fatal("failed degradation transaction acknowledged")
	}
	state, err = s.JournalCheckpoint(ctx)
	if err != nil || state.RecoveryPending || state.Cursor != before.Cursor {
		t.Fatalf("failed gap partially committed: %+v %v", state, err)
	}
	if _, err := s.db.Exec(`DROP TRIGGER reject_gap`); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordJournalDegradation(ctx, recoveryGap(at)); err != nil {
		t.Fatal(err)
	}
	// A skipped/rejected entry may advance the cursor, but cannot clear pending.
	if _, err := s.CommitJournal(ctx, JournalWrite{Cursor: "rejected", ReceivedAt: at, ObservedAt: at}); err != nil {
		t.Fatal(err)
	}
	wantGaps, err := s.CoverageGaps(ctx, at.Add(-2*time.Hour), at.Add(time.Hour), 10)
	if err != nil || len(wantGaps) != 2 {
		t.Fatalf("gap transaction incorrect: %v %v", wantGaps, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	state, err = s.JournalCheckpoint(ctx)
	if err != nil || !state.RecoveryPending || state.Cursor != "rejected" {
		t.Fatalf("pending lost across reopen: %+v %v", state, err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER reject_checkpoint BEFORE INSERT ON collector_checkpoints BEGIN SELECT RAISE(ABORT,'synthetic checkpoint failure'); END`); err != nil {
		t.Fatal(err)
	}
	trusted := JournalWrite{Cursor: "trusted-after", ReceivedAt: at.Add(time.Second), ObservedAt: at, Trusted: true}
	if _, err := s.CommitJournal(ctx, trusted); err == nil {
		t.Fatal("failed trusted transaction acknowledged")
	}
	state, err = s.JournalCheckpoint(ctx)
	seen, seenErr := s.JournalSeen(ctx, trusted.Cursor)
	if err != nil || seenErr != nil || seen || !state.RecoveryPending || state.Cursor != "rejected" {
		t.Fatalf("failed checkpoint cleared recovery: %+v seen=%v err=%v/%v", state, seen, err, seenErr)
	}
	if _, err := s.db.Exec(`DROP TRIGGER reject_checkpoint`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CommitJournal(ctx, trusted); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	state, err = s.JournalCheckpoint(ctx)
	gotGaps, gapErr := s.CoverageGaps(ctx, at.Add(-2*time.Hour), at.Add(time.Hour), 10)
	if err != nil || gapErr != nil || state.RecoveryPending || state.Cursor != trusted.Cursor || !reflect.DeepEqual(wantGaps, gotGaps) {
		t.Fatalf("committed recovery/history lost: %+v %v/%v", state, err, gapErr)
	}
}

func TestJournalRecoveryWithoutCheckpointSurvivesMaintenanceAndBackup(t *testing.T) {
	ctx := context.Background()
	s := budgetStore(t)
	at := time.Now().UTC().Truncate(time.Millisecond)
	if err := s.RecordJournalDegradation(ctx, recoveryGap(at)); err != nil {
		t.Fatal(err)
	}
	if err := s.ResetComponentStatus(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Prune(ctx, at.AddDate(1, 0, 0)); err != nil {
		t.Fatal(err)
	}
	state, err := s.JournalCheckpoint(ctx)
	if err != nil || !state.RecoveryPending || state.Cursor != "" {
		t.Fatalf("maintenance lost current state: %+v %v", state, err)
	}
	for _, pending := range []bool{true, false} {
		if !pending {
			if _, err := s.CommitJournal(ctx, JournalWrite{Cursor: "recovered", ReceivedAt: at, ObservedAt: at, Trusted: true}); err != nil {
				t.Fatal(err)
			}
		}
		dir := t.TempDir()
		backup := filepath.Join(dir, "snapshot.db")
		restored := filepath.Join(dir, "restored.db")
		if _, err := s.Backup(ctx, backup); err != nil {
			t.Fatal(err)
		}
		if _, err := RestoreBackup(ctx, backup, restored); err != nil {
			t.Fatal(err)
		}
		copy, err := Open(restored)
		if err != nil {
			t.Fatal(err)
		}
		got, err := copy.JournalCheckpoint(ctx)
		copy.Close()
		if err != nil || got.RecoveryPending != pending || !pending && got.Cursor != "recovered" {
			t.Fatalf("restore changed readiness/cursor: %+v %v", got, err)
		}
	}
}

func TestJournalRecoveryMissingMarkerIsNotReady(t *testing.T) {
	s := budgetStore(t)
	ctx := context.Background()
	if _, err := s.db.Exec(`DELETE FROM journal_recovery`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.JournalCheckpoint(ctx); !errors.Is(err, ErrJournalRecoveryUnavailable) {
		t.Fatalf("missing marker became ready: %v", err)
	}
	if _, err := s.CommitJournal(ctx, JournalWrite{Cursor: "no-marker", ReceivedAt: time.Now(), Trusted: true}); err == nil {
		t.Fatal("trusted checkpoint committed without readiness marker")
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM collector_checkpoints`).Scan(&count); err != nil || count != 0 {
		t.Fatal("partial checkpoint after marker failure")
	}
	if _, err := s.Backup(ctx, filepath.Join(t.TempDir(), "invalid.db")); err == nil {
		t.Fatal("backup accepted missing marker")
	}
}

func legacyJournalV6(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "legacy.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Truncate(time.Millisecond)
	if _, err := s.CommitJournal(context.Background(), JournalWrite{Cursor: "legacy", ReceivedAt: at, ObservedAt: at}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordCoverageGap(context.Background(), recoveryGap(at)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DROP TABLE journal_recovery; DELETE FROM schema_migrations WHERE version=7`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	return path
}

func TestJournalRecoveryMigrationFromV6AndRollback(t *testing.T) {
	for _, abort := range []bool{false, true} {
		path := legacyJournalV6(t)
		if abort {
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			_, err = db.Exec(`CREATE TRIGGER reject_seven BEFORE INSERT ON schema_migrations WHEN NEW.version=7 BEGIN SELECT RAISE(ABORT,'synthetic migration failure'); END`)
			db.Close()
			if err != nil {
				t.Fatal(err)
			}
			if s, err := Open(path); err == nil {
				s.Close()
				t.Fatal("failed v7 migration acknowledged")
			}
			db, err = sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			var version, table int
			if err = db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
				t.Fatal(err)
			}
			if err = db.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE name='journal_recovery'`).Scan(&table); err != nil {
				t.Fatal(err)
			}
			if version != 6 || table != 0 {
				t.Fatal("v7 DDL/version partially committed")
			}
			if _, err = db.Exec(`DROP TRIGGER reject_seven`); err != nil {
				t.Fatal(err)
			}
			db.Close()
		}
		if info, err := VerifyBackup(context.Background(), path); err != nil || info.SchemaVersion != 6 {
			t.Fatalf("legacy backup invalid: %+v %v", info, err)
		}
		s, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		state, err := s.JournalCheckpoint(context.Background())
		var gapCount int
		if e := s.db.QueryRow(`SELECT COUNT(*) FROM coverage_gaps`).Scan(&gapCount); e != nil {
			t.Fatal(e)
		}
		s.Close()
		if err != nil || state.RecoveryPending || state.Cursor != "legacy" || gapCount != 1 {
			t.Fatalf("migration inferred pending or changed history: %+v gaps=%d err=%v", state, gapCount, err)
		}
	}
}
