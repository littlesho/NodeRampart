// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestForeignKeyDiagnosticReportsBoundedOrphansWithoutRepair(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "orphan.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.db.Exec("PRAGMA foreign_keys=OFF"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<105) INSERT INTO event_notifications(event_id,notification_id,decision,silence_id,recorded_at) SELECT 'missing-'||x,'','ineligible','',0 FROM n`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		t.Fatal(err)
	}
	status, err := s.ForeignKeys(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !status.Truncated || len(status.Violations) != 100 || status.Violations[0].Table != "event_notifications" {
		t.Fatalf("unbounded or missing diagnostic: %+v", status)
	}
	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM event_notifications").Scan(&count); err != nil || count != 105 {
		t.Fatalf("diagnostic deleted history: %d %v", count, err)
	}
	if _, err := s.Backup(context.Background(), filepath.Join(t.TempDir(), "copy.db")); err == nil {
		t.Fatal("orphan backup incorrectly reported verified")
	}
}
