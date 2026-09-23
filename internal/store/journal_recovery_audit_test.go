// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"

	"github.com/littlesho/NodeRampart/internal/model"
	"testing"
	"time"
)

func TestJournalRecoveryDuplicateCannotClearPending(t *testing.T) {
	s := budgetStore(t)
	ctx := context.Background()
	at := time.Now().UTC()
	for _, cursor := range []string{"previous-trusted", "latest-trusted"} {
		if _, err := s.CommitJournal(ctx, JournalWrite{Cursor: cursor, ReceivedAt: at, Trusted: true}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RecordJournalDegradation(ctx, recoveryGap(at)); err != nil {
		t.Fatal(err)
	}
	for _, cursor := range []string{"previous-trusted", "latest-trusted"} {
		inserted, err := s.CommitJournal(ctx, JournalWrite{Cursor: cursor, ReceivedAt: at, Trusted: true})
		if err != nil || inserted {
			t.Fatalf("duplicate not deduplicated: inserted=%v err=%v", inserted, err)
		}
		state, err := s.JournalCheckpoint(ctx)
		if err != nil || !state.RecoveryPending {
			t.Fatalf("already committed cursor cleared unresolved recovery: %+v err=%v", state, err)
		}
	}
}

func TestJournalRecoveryReadErrorIsSanitized(t *testing.T) {
	for _, fault := range []string{"missing_table", "closed_database"} {
		t.Run(fault, func(t *testing.T) {
			s := budgetStore(t)
			if fault == "missing_table" {
				if _, err := s.db.Exec(`DROP TABLE journal_recovery`); err != nil {
					t.Fatal(err)
				}
			} else if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			_, err := s.JournalCheckpoint(context.Background())
			if err == nil || err.Error() != ErrJournalRecoveryUnavailable.Error() {
				t.Fatalf("recovery read exposed an unstable backend error: %v", err)
			}
		})
	}
}

// Snapshot persisted user data independently of the new readiness singleton.
func recoveryDataSnapshot(t *testing.T, db *sql.DB) map[string]string {
	t.Helper()
	result := map[string]string{}
	for _, table := range []string{"auth_hourly", "events", "event_notifications", "notification_outbox", "report_runs", "report_snapshots", "collector_checkpoints", "journal_seen", "coverage_gaps"} {
		rows, err := db.Query("SELECT * FROM " + table + " ORDER BY rowid")
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		data := [][]any{}
		for rows.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for i := range values {
				pointers[i] = &values[i]
			}
			if err := rows.Scan(pointers...); err != nil {
				t.Fatal(err)
			}
			data = append(data, values)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		encoded, err := json.Marshal(data)
		if err != nil {
			t.Fatal(err)
		}
		result[table] = string(encoded)
	}
	return result
}

func TestJournalRecoveryMigrationPreservesDataAndRestores(t *testing.T) {
	for _, rich := range []bool{false, true} {
		name := "empty"
		if rich {
			name = "populated"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "schema-six.db")
			s, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			at := time.Now().UTC().Truncate(time.Millisecond)
			if rich {
				event := model.Event{ID: "synthetic-event", IncidentID: "synthetic-incident", ObservedAt: at, Kind: "ssh_bruteforce", Phase: "start", Severity: model.SeverityHigh, Summary: "Synthetic migration event"}
				message := &OutboxMessage{ID: "synthetic-notification", DedupeKey: "synthetic-key", Destination: "test-sink", Body: "Synthetic notification", NextAttempt: at.Add(time.Hour)}
				if _, err := s.CommitJournal(ctx, JournalWrite{Cursor: "synthetic-checkpoint", ReceivedAt: at, ObservedAt: at, Kind: "failure", SourceRange: "192.0.2.0/24", Event: &event, Notification: message, Trusted: true}); err != nil {
					t.Fatal(err)
				}
				if err := s.RecordCoverageGap(ctx, recoveryGap(at)); err != nil {
					t.Fatal(err)
				}
				date := at.Format("2006-01-02")
				if err := s.SaveReport(ctx, ReportSnapshot{Date: date, Title: "Synthetic report", Body: "Synthetic migration report", PeriodStart: at.Add(-time.Hour), PeriodEnd: at, GeneratedAt: at}); err != nil {
					t.Fatal(err)
				}
				if err := s.MarkReportGenerated(ctx, date, "test-sink", at); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.db.Exec(`DROP TABLE journal_recovery; DELETE FROM schema_migrations WHERE version=7`); err != nil {
				t.Fatal(err)
			}
			want := recoveryDataSnapshot(t, s.db)
			legacyBackup := filepath.Join(t.TempDir(), "backup-six.db")
			if _, err := s.Backup(ctx, legacyBackup); err != nil {
				t.Fatal(err)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			if info, err := VerifyBackup(ctx, legacyBackup); err != nil || info.SchemaVersion != 6 {
				t.Fatalf("v6 backup: %+v %v", info, err)
			}
			restored := filepath.Join(t.TempDir(), "restored-six.db")
			if _, err := RestoreBackup(ctx, legacyBackup, restored); err != nil {
				t.Fatal(err)
			}
			for _, database := range []string{path, restored} {
				for reopen := 0; reopen < 2; reopen++ {
					upgraded, err := Open(database)
					if err != nil {
						t.Fatal(err)
					}
					state, err := upgraded.JournalCheckpoint(ctx)
					if err != nil || state.RecoveryPending {
						t.Fatalf("v6 history invented pending: %+v %v", state, err)
					}
					if got := recoveryDataSnapshot(t, upgraded.db); !reflect.DeepEqual(got, want) {
						t.Fatalf("migration/reopen changed existing data: got=%v want=%v", got, want)
					}
					if reopen == 1 {
						if err := upgraded.RecordJournalDegradation(ctx, recoveryGap(at.Add(time.Second))); err != nil {
							t.Fatal(err)
						}
						wantPending := recoveryDataSnapshot(t, upgraded.db)
						backup := filepath.Join(t.TempDir(), "schema-seven.db")
						if _, err := upgraded.Backup(ctx, backup); err != nil {
							t.Fatal(err)
						}
						target := filepath.Join(t.TempDir(), "restored-seven.db")
						if info, err := RestoreBackup(ctx, backup, target); err != nil || info.SchemaVersion != 7 {
							t.Fatalf("v7 restore: %+v %v", info, err)
						}
						copy, err := Open(target)
						if err != nil {
							t.Fatal(err)
						}
						recovered, err := copy.JournalCheckpoint(ctx)
						if err != nil || !recovered.RecoveryPending || recovered.Cursor != state.Cursor || !reflect.DeepEqual(recoveryDataSnapshot(t, copy.db), wantPending) {
							t.Fatalf("v7 restore lost pending/checkpoint/history: %+v %v", recovered, err)
						}
						copy.Close()
					}
					upgraded.Close()
				}
			}
		})
	}
}

func TestJournalRecoveryMigrationProcessCrash(t *testing.T) {
	path := legacyJournalV6(t)
	command := exec.Command(os.Args[0], "-test.run=^TestJournalRecoveryCrashHelper$")
	command.Env = append(os.Environ(), "NR_TEST_JOURNAL_CRASH=migration_uncommitted", "NR_TEST_JOURNAL_DB="+path)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("crash helper: %v %s", err, output)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	var version, marker int
	if err := db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE name='journal_recovery'`).Scan(&marker); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if version != 6 || marker != 0 {
		t.Fatalf("interrupted migration partially committed: version=%d marker=%d", version, marker)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	state, err := s.JournalCheckpoint(context.Background())
	if err != nil || state.RecoveryPending || state.Cursor != "legacy" {
		t.Fatalf("migration after crash: %+v %v", state, err)
	}
}

func TestJournalRecoveryFutureSchemaRefused(t *testing.T) {
	path := legacyJournalV6(t)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO schema_migrations(version,applied_at) VALUES (999,0)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if s, err := Open(path); err == nil {
		s.Close()
		t.Fatal("future schema opened for writes")
	}
	db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var marker int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE name='journal_recovery'`).Scan(&marker); err != nil || marker != 0 {
		t.Fatalf("future schema partially migrated: %d %v", marker, err)
	}
}

func TestJournalRecoveryCommitFailureRollsBack(t *testing.T) {
	for _, target := range []string{"coverage_gaps", "collector_checkpoints"} {
		t.Run(target, func(t *testing.T) {
			s := budgetStore(t)
			ctx := context.Background()
			at := time.Now().UTC()
			if _, err := s.CommitJournal(ctx, JournalWrite{Cursor: "before", ReceivedAt: at, Trusted: true}); err != nil {
				t.Fatal(err)
			}
			if target == "collector_checkpoints" {
				if err := s.RecordJournalDegradation(ctx, recoveryGap(at)); err != nil {
					t.Fatal(err)
				}
			}
			wantState, err := s.JournalCheckpoint(ctx)
			if err != nil {
				t.Fatal(err)
			}
			wantData := recoveryDataSnapshot(t, s.db)
			// A deferred constraint fails at COMMIT, after all checkpoint/marker SQL.
			operation := "INSERT"
			if target == "collector_checkpoints" {
				operation = "UPDATE"
			}
			for _, statement := range []string{
				`CREATE TABLE audit_parent (id INTEGER PRIMARY KEY)`,
				`CREATE TABLE audit_child (id INTEGER REFERENCES audit_parent(id) DEFERRABLE INITIALLY DEFERRED)`,
				`CREATE TRIGGER fail_at_commit AFTER ` + operation + ` ON ` + target + ` BEGIN INSERT INTO audit_child VALUES (1); END`,
			} {
				if _, err := s.db.Exec(statement); err != nil {
					t.Fatal(err)
				}
			}
			if target == "coverage_gaps" {
				err = s.RecordJournalDegradation(ctx, recoveryGap(at))
			} else {
				_, err = s.CommitJournal(ctx, JournalWrite{Cursor: "uncommitted", ReceivedAt: at, Trusted: true})
			}
			if err == nil {
				t.Fatal("commit failure acknowledged")
			}
			got, readErr := s.JournalCheckpoint(ctx)
			if readErr != nil || got != wantState || !reflect.DeepEqual(recoveryDataSnapshot(t, s.db), wantData) {
				t.Fatalf("commit failure partially persisted: got=%+v want=%+v err=%v", got, wantState, readErr)
			}
		})
	}
}
