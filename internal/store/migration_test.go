// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/protocol"
)

func makeLegacyDatabase(t *testing.T, path string, version int, hour time.Time) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range []string{
		`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL)`,
		`CREATE TABLE collector_health_hourly (
			hour_utc INTEGER PRIMARY KEY, batches INTEGER NOT NULL, overflow_bytes INTEGER NOT NULL,
			overflow_packets INTEGER NOT NULL, parse_errors INTEGER NOT NULL, kernel_packets INTEGER NOT NULL,
			kernel_drops INTEGER NOT NULL, kernel_stats_errors INTEGER NOT NULL, last_seen INTEGER NOT NULL)`,
		`CREATE TABLE interface_hourly (hour_utc INTEGER PRIMARY KEY, rx_bytes INTEGER NOT NULL,
			tx_bytes INTEGER NOT NULL, rx_packets INTEGER NOT NULL, tx_packets INTEGER NOT NULL)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO schema_migrations VALUES (?, 1)`, version); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO collector_health_hourly VALUES (?, 4, 50, 2, 3, 100, 5, 1, ?)`, hour.Unix(), hour.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO interface_hourly VALUES (?, 4000, 2000, 40, 20)`, hour.Unix()); err != nil {
		t.Fatal(err)
	}
}

func TestHealthMigrationPreservesVersion1DataAcrossReopens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	hour := time.Now().UTC().Truncate(time.Hour)
	ctx := context.Background()
	makeLegacyDatabase(t, path, 1, hour)
	for attempt := 0; attempt < 3; attempt++ {
		db, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer db.Close()
			var versions, maximum int
			if err := db.db.QueryRow(`SELECT COUNT(*), MAX(version) FROM schema_migrations`).Scan(&versions, &maximum); err != nil {
				t.Fatal(err)
			}
			if versions != schemaVersion || maximum != schemaVersion {
				t.Fatalf("migration version count=%d, maximum=%d", versions, maximum)
			}
			summary, err := db.Summary(ctx, hour, hour.Add(time.Hour), 5)
			if err != nil {
				t.Fatal(err)
			}
			if summary.Interface.RXBytes != 4000 || summary.Interface.TXBytes != 2000 || summary.OverflowBytes != 50 || summary.ParseErrors != 3 {
				t.Fatalf("migration lost historical data: %#v", summary)
			}
			if attempt == 0 {
				if summary.Batches != 4 || summary.KernelDrops != 5 || summary.IPCDroppedBatches != 0 || summary.IPCDroppedPackets != 0 || summary.IPCDroppedBytes != 0 || summary.HealthCounterSaturations != 0 {
					t.Fatalf("migration did not initialize new health fields safely: %#v", summary)
				}
				// Exercise both INSERT and same-hour UPDATE of the new columns.
				for _, at := range []time.Time{hour, hour, hour.Add(time.Hour)} {
					if err := db.RecordBatchHealth(ctx, protocol.Batch{SentAt: at, KernelDrops: 2, IPCDroppedBatches: 1, IPCDroppedPackets: 7, IPCDroppedBytes: 700, HealthCountersSaturated: true}); err != nil {
						t.Fatal(err)
					}
				}
			} else if summary.Batches != 6 || summary.KernelDrops != 9 || summary.IPCDroppedBatches != 2 || summary.IPCDroppedPackets != 14 || summary.IPCDroppedBytes != 1400 || summary.HealthCounterSaturations != 2 {
				t.Fatalf("new health data lost or migration repeated: %#v", summary)
			}
			total, err := db.Summary(ctx, hour, hour.Add(2*time.Hour), 5)
			if err != nil || total.Batches != 7 || total.IPCDroppedBatches != 3 || total.IPCDroppedPackets != 21 || total.IPCDroppedBytes != 2100 || total.HealthCounterSaturations != 3 {
				t.Fatalf("new hourly insert missing from summary: %#v, %v", total, err)
			}
		}()
	}
}

func TestHealthMigrationRejectsFutureSchemaWithoutChangingIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	makeLegacyDatabase(t, path, schemaVersion+1, time.Now().UTC().Truncate(time.Hour))
	if db, err := Open(path); err == nil {
		db.Close()
		t.Fatal("future database schema accepted")
	} else if !strings.Contains(err.Error(), fmt.Sprintf("unsupported database schema version %d", schemaVersion+1)) {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var versions, columns int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&versions); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('collector_health_hourly')`).Scan(&columns); err != nil {
		t.Fatal(err)
	}
	if versions != 1 || columns != 9 {
		t.Fatalf("rejected migration changed database: versions=%d columns=%d", versions, columns)
	}
}

func TestSummaryDoesNotHideUnavailableCounters(t *testing.T) {
	for _, table := range []string{"interface_hourly", "collector_health_hourly"} {
		t.Run(table, func(t *testing.T) {
			db, err := Open(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err := db.db.Exec(`DROP TABLE ` + table); err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			if _, err := db.Summary(context.Background(), now.Add(-time.Hour), now, 5); err == nil {
				t.Fatal("unavailable counters were presented as a successful zero-value summary")
			}
		})
	}
}
