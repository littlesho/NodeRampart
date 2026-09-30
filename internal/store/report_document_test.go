// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func documentSnapshot(t *testing.T) ReportSnapshot {
	t.Helper()
	at := time.Now().UTC().Truncate(time.Millisecond)
	r := ReportSnapshot{Date: at.Add(-24 * time.Hour).Format(time.DateOnly), Title: "Full archive", Body: "Short body", PeriodStart: at.Add(-24 * time.Hour), PeriodEnd: at, GeneratedAt: at}
	data, err := json.Marshal(map[string]any{"schema_version": 1, "date": r.Date, "title": r.Title, "body": strings.Repeat("中文", 3000), "period_start_utc": r.PeriodStart, "period_end_utc": r.PeriodEnd, "generated_at_utc": r.GeneratedAt})
	if err != nil {
		t.Fatal(err)
	}
	r.Document = data
	return r
}

func TestReportDocumentImmutableBackupAndInvalidMetadata(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	r := documentSnapshot(t)
	if err := s.SaveReport(ctx, r); err != nil {
		t.Fatal(err)
	}
	got, err := s.Report(ctx, r.Date)
	if err != nil || !reflect.DeepEqual(r, got) {
		t.Fatal("full document did not persist", err)
	}
	modified := r
	modified.Document = json.RawMessage(strings.Replace(string(r.Document), "中文", "修改", 1))
	if added, err := s.SaveReportIfAbsent(ctx, modified); err != nil || added {
		t.Fatal("immutable archive replaced", err)
	}
	got, err = s.Report(ctx, r.Date)
	if err != nil || !reflect.DeepEqual(r, got) {
		t.Fatal("first full document changed", err)
	}
	backup := filepath.Join(t.TempDir(), "backup.db")
	if _, err := s.Backup(ctx, backup); err != nil {
		t.Fatal(err)
	}
	restored := filepath.Join(t.TempDir(), "restored.db")
	if _, err := RestoreBackup(ctx, backup, restored); err != nil {
		t.Fatal(err)
	}
	copy, err := Open(restored)
	if err != nil {
		t.Fatal(err)
	}
	defer copy.Close()
	got, err = copy.Report(ctx, r.Date)
	if err != nil || !reflect.DeepEqual(r, got) {
		t.Fatal("backup lost full document", err)
	}
	for _, data := range []json.RawMessage{json.RawMessage(`{}`), json.RawMessage(strings.Replace(string(r.Document), `"schema_version":1`, `"schema_version":2`, 1)), json.RawMessage(strings.Repeat("x", MaxReportDocumentBytes+1))} {
		bad := r
		bad.Document = data
		if err := s.SaveReport(ctx, bad); err == nil {
			t.Fatal("invalid full document accepted")
		}
	}
	if _, err := s.db.Exec(`UPDATE report_snapshots SET document_json=? WHERE report_date=?`, `{}`, r.Date); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Report(ctx, r.Date); err == nil {
		t.Fatal("corrupt full document returned")
	}
}

func TestReportDocumentSchema8MigrationAndFailureRollback(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "upgrade", true: "rollback"}[fail], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "legacy.db")
			s, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			r := documentSnapshot(t)
			r.Document = nil
			if err := s.SaveReport(context.Background(), r); err != nil {
				s.Close()
				t.Fatal(err)
			}
			s.Close()
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			if err := downgradeSnapshotSchemaReference(context.Background(), db, 8); err != nil {
				db.Close()
				t.Fatal(err)
			}
			if _, err := db.Exec(`DELETE FROM schema_migrations WHERE version>8`); err != nil {
				db.Close()
				t.Fatal(err)
			}
			if fail {
				if _, err := db.Exec(`ALTER TABLE report_snapshots ADD COLUMN document_json TEXT NOT NULL DEFAULT ''`); err != nil {
					db.Close()
					t.Fatal(err)
				}
			}
			db.Close()
			upgraded, err := Open(path)
			if fail {
				if err == nil {
					upgraded.Close()
					t.Fatal("partial migration accepted")
				}
				db, err := sql.Open("sqlite", path)
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				var version int
				if err := db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil || version != 8 {
					t.Fatal("failed migration advanced schema", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer upgraded.Close()
			got, err := upgraded.Report(context.Background(), r.Date)
			if err != nil || !reflect.DeepEqual(r, got) || got.Document != nil {
				t.Fatal("schema8 archive changed", err)
			}
		})
	}
}
