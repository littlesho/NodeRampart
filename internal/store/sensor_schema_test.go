// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/model"
)

func legacySensorSchema9(t *testing.T) *Store {
	t.Helper()
	s := budgetStore(t)
	if err := s.InsertEvent(context.Background(), model.Event{ID: "immutable-old", ObservedAt: time.Now().UTC(), Kind: "synthetic", Severity: model.SeverityInfo, Summary: "old history"}); err != nil {
		t.Fatal(err)
	}
	if err := downgradeSnapshotSchemaReference(context.Background(), s.db, 9); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DELETE FROM schema_migrations WHERE version>=10`); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSensorSchemaUpgradeRollbackAndFutureRefusal(t *testing.T) {
	for _, scenario := range []string{"upgrade", "rollback", "future"} {
		t.Run(scenario, func(t *testing.T) {
			s := legacySensorSchema9(t)
			path := s.path
			if scenario == "rollback" {
				if _, err := s.db.Exec(`CREATE TABLE sensor_commit_state (unexpected TEXT)`); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "future" {
				if _, err := s.db.Exec(`INSERT INTO schema_migrations(version,applied_at) VALUES(999,0)`); err != nil {
					t.Fatal(err)
				}
			}
			s.Close()
			reopened, err := OpenWithBudget(path, BudgetConfig{MaxBytes: 64 << 20})
			if scenario == "upgrade" {
				if err != nil {
					t.Fatal(err)
				}
				defer reopened.Close()
				if err := verifySensorCommitState(context.Background(), reopened.db); err != nil {
					t.Fatal(err)
				}
				var summary string
				if err := reopened.db.QueryRow(`SELECT summary FROM events WHERE id='immutable-old'`).Scan(&summary); err != nil || summary != "old history" {
					t.Fatal("upgrade changed immutable history", summary, err)
				}
				return
			}
			if err == nil {
				reopened.Close()
				t.Fatal("unsafe schema accepted")
			}
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			var count int
			if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE type='table' AND name='sensor_watermarks'`).Scan(&count); err != nil || count != 0 {
				t.Fatal("failed migration did not roll back", count, err)
			}
			if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE id='immutable-old' AND summary='old history'`).Scan(&count); err != nil || count != 1 {
				t.Fatal("failure altered old history", count, err)
			}
		})
	}
}

func TestSensorBackupRejectsUnavailableStateAndPrivatePayload(t *testing.T) {
	for _, scenario := range []string{"missing_state", "extra_private_field", "trailing_json"} {
		t.Run(scenario, func(t *testing.T) {
			s := budgetStore(t)
			b, traffic := sensorCommitFixture(time.Now().UTC(), 1)
			if _, err := s.CommitSensorBatch(context.Background(), b, traffic, true, true, true, "committed"); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "snapshot.db")
			if _, err := s.Backup(context.Background(), path); err != nil {
				t.Fatal(err)
			}
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			statement := `DELETE FROM sensor_commit_state`
			if scenario == "extra_private_field" {
				statement = `UPDATE sensor_watermarks SET health_json='{"remote_ip":"192.0.2.1"}'`
			}
			if scenario == "trailing_json" {
				statement = `UPDATE sensor_watermarks SET health_json='{} {}'`
			}
			if _, err := db.Exec(statement); err != nil {
				db.Close()
				t.Fatal(err)
			}
			db.Close()
			if _, err := VerifyBackup(context.Background(), path); err == nil {
				t.Fatal("unreliable/private snapshot verified")
			}
		})
	}
}
