// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/model"
	"github.com/littlesho/NodeRampart/internal/store"
)

func doctorSnapshotFixture(t *testing.T, dir string) string {
	t.Helper()
	source, err := store.Open(filepath.Join(dir, "synthetic-snapshot-source.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	event := model.Event{ID: "private-fixture-event-id", ObservedAt: time.Now().UTC(), Kind: "test", Severity: model.SeverityInfo, Summary: "synthetic-private-event-body", SourceRange: "192.0.2.0/24", Evidence: map[string]string{"synthetic_private": "synthetic-private-evidence"}}
	if err := source.InsertEvent(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "private-fixture-snapshot.db")
	if _, err := source.Backup(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	return path
}

func decodeDoctorSnapshotResult(t *testing.T, output []byte) doctorResult {
	t.Helper()
	var result doctorResult
	if !json.Valid(output) || json.Unmarshal(output, &result) != nil {
		t.Fatal("doctor stdout is not machine-readable JSON")
	}
	for _, secret := range []string{"private-fixture-event-id", "synthetic-private-event-body", "synthetic-private-evidence", "private-fixture-snapshot.db", "SELECT", "PRAGMA"} {
		if bytes.Contains(output, []byte(secret)) {
			t.Fatalf("snapshot diagnosis leaked private/SQL data: %s", secret)
		}
	}
	return result
}

func snapshotDoctorCheck(t *testing.T, result doctorResult) doctorCheck {
	t.Helper()
	for _, check := range result.Checks {
		if check.Name == "snapshot_foreign_keys" {
			return check
		}
	}
	t.Fatal("snapshot check is missing")
	return doctorCheck{}
}

func TestDoctorForeignKeySnapshotIsReadOnlyAndDoesNotExportPrivateData(t *testing.T) {
	diagnosticSocketFixture(t, "doctor", func(configPath, dir string) {
		snapshot := doctorSnapshotFixture(t, dir)
		before, err := os.ReadFile(snapshot)
		if err != nil {
			t.Fatal(err)
		}
		var output bytes.Buffer
		if err := doctorCommand([]string{"--strict", "--manual-current-uid", "--config", configPath, "--foreign-keys-snapshot", snapshot}, &output); err != nil {
			t.Fatal(err)
		}
		result := decodeDoctorSnapshotResult(t, output.Bytes())
		if result.SnapshotForeignKeys == nil || len(result.SnapshotForeignKeys.Violations) != 0 || snapshotDoctorCheck(t, result).State != "valid" || result.StrictExitCode != 0 {
			t.Fatalf("clean snapshot result=%+v", result)
		}
		after, err := os.ReadFile(snapshot)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatal("doctor changed supplied snapshot", err)
		}
	})
}

func TestDoctorForeignKeySnapshotUnknownNeverMeansHealthy(t *testing.T) {
	for _, fault := range []string{"future", "active_sidecar", "symlink", "malformed"} {
		t.Run(fault, func(t *testing.T) {
			diagnosticSocketFixture(t, "doctor", func(configPath, dir string) {
				snapshot := doctorSnapshotFixture(t, dir)
				input := snapshot
				switch fault {
				case "future":
					db, err := sql.Open("sqlite", snapshot)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := db.Exec(`INSERT INTO schema_migrations(version,applied_at) VALUES(?,0)`, store.SchemaVersion()+1); err != nil {
						t.Fatal(err)
					}
					if err := db.Close(); err != nil {
						t.Fatal(err)
					}
				case "active_sidecar":
					if err := os.WriteFile(snapshot+"-journal", []byte("synthetic unresolved evidence"), 0o600); err != nil {
						t.Fatal(err)
					}
				case "symlink":
					input = filepath.Join(dir, "linked-snapshot.db")
					if err := os.Symlink(snapshot, input); err != nil {
						t.Fatal(err)
					}
				case "malformed":
					if err := os.WriteFile(snapshot, []byte("synthetic-private-event-body"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				before, err := os.ReadFile(snapshot)
				if err != nil {
					t.Fatal(err)
				}
				var output bytes.Buffer
				err = doctorCommand([]string{"--strict", "--manual-current-uid", "--config", configPath, "--foreign-keys-snapshot", input}, &output)
				var exit *diagnosticExit
				if !errors.As(err, &exit) || exit.code != 2 {
					t.Fatalf("unknown snapshot became passed/confirmed: %v", err)
				}
				result := decodeDoctorSnapshotResult(t, output.Bytes())
				if result.SnapshotForeignKeys != nil || snapshotDoctorCheck(t, result).State != "unavailable" || result.Overall == "healthy" {
					t.Fatalf("unknown snapshot was presented as healthy: %+v", result)
				}
				after, err := os.ReadFile(snapshot)
				if err != nil || !bytes.Equal(before, after) {
					t.Fatal("failed doctor inspection modified source", err)
				}
				if fault == "active_sidecar" {
					data, err := os.ReadFile(snapshot + "-journal")
					if err != nil || string(data) != "synthetic unresolved evidence" {
						t.Fatal("doctor discarded unresolved sidecar", err)
					}
				}
			})
		})
	}
}

func TestDoctorForeignKeySnapshotReportsOrphansAsConfirmedDegradation(t *testing.T) {
	diagnosticSocketFixture(t, "doctor", func(configPath, dir string) {
		snapshot := doctorSnapshotFixture(t, dir)
		db, err := sql.Open("sqlite", snapshot)
		if err != nil {
			t.Fatal(err)
		}
		// Corrupt only this synthetic standalone fixture, never production SQL.
		if _, err := db.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO event_notifications(event_id,channel,decision,recorded_at) VALUES('private-fixture-event-id-missing','telegram','ineligible',0)`); err != nil {
			t.Fatal(err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		before, err := os.ReadFile(snapshot)
		if err != nil {
			t.Fatal(err)
		}
		var output bytes.Buffer
		err = doctorCommand([]string{"--strict", "--manual-current-uid", "--config", configPath, "--foreign-keys-snapshot", snapshot}, &output)
		var exit *diagnosticExit
		if !errors.As(err, &exit) || exit.code != 1 {
			t.Fatalf("confirmed orphan not diagnosed as degradation: %v", err)
		}
		result := decodeDoctorSnapshotResult(t, output.Bytes())
		if result.SnapshotForeignKeys == nil || len(result.SnapshotForeignKeys.Violations) != 1 || snapshotDoctorCheck(t, result).State != "degraded" {
			t.Fatalf("orphan result=%+v", result)
		}
		after, err := os.ReadFile(snapshot)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatal("orphan diagnosis repaired or rewrote source", err)
		}
	})
}
