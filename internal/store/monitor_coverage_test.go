// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestRetentionHourlyInventoriesUseIndexAndExactOverlap(t *testing.T) {
	s := retentionStore(t)
	ctx := context.Background()
	at := time.Now().UTC().Truncate(time.Hour)
	statements := map[string]string{
		"traffic_hourly":          `INSERT INTO traffic_hourly VALUES (?,'inbound','ZZ','',0,'',0,1,1)`,
		"auth_hourly":             `INSERT INTO auth_hourly VALUES (?,'failure','synthetic',1)`,
		"interface_hourly":        `INSERT INTO interface_hourly VALUES (?,1,1,1,1)`,
		"interface_detail_hourly": `INSERT INTO interface_detail_hourly VALUES (?,'lab0',1,1,1,1)`,
		"collector_health_hourly": `INSERT INTO collector_health_hourly(hour_utc,batches,overflow_bytes,overflow_packets,parse_errors,kernel_packets,kernel_drops,kernel_stats_errors,last_seen) VALUES (?,1,0,0,0,1,0,0,0)`,
	}
	for _, d := range retentionDatasets {
		statement, ok := statements[d.name]
		if !ok {
			continue
		}
		t.Run(d.name, func(t *testing.T) {
			for _, delta := range []time.Duration{-time.Hour, 0, time.Hour} {
				if _, err := s.db.Exec(statement, at.Add(delta).Unix()); err != nil {
					t.Fatal(err)
				}
			}
			query, args := retentionInventoryQuery(d.name, d.start, d.end, at.Add(30*time.Minute), at.Add(time.Hour+time.Nanosecond))
			rows, err := s.db.Query("EXPLAIN QUERY PLAN "+query, args...)
			if err != nil {
				t.Fatal(err)
			}
			plan := ""
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
					rows.Close()
					t.Fatal(err)
				}
				plan += detail
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(plan, "SCAN ") || !strings.Contains(plan, "SEARCH ") || !strings.Contains(plan, ">?") || !strings.Contains(plan, "<?") {
				t.Fatal("lost indexed time range", plan)
			}
			for _, fraction := range []time.Duration{0, time.Nanosecond} {
				view, err := s.Retention(ctx, RetentionQuery{Start: at.Add(30 * time.Minute), End: at.Add(time.Hour + fraction), Dataset: d.name, Limit: 1})
				want := int64(1)
				if fraction > 0 {
					want = 2
				}
				if err != nil || len(view.Retained) != 1 || view.Retained[0].Rows != want {
					t.Fatal("incorrect hourly overlap", fraction, view, err)
				}
			}
		})
	}
}

func TestMonitorCoverageKeepsLedgerSafeguardsWithoutInventories(t *testing.T) {
	s := retentionStore(t)
	ctx := context.Background()
	at := time.Now().UTC().Truncate(time.Hour)
	if err := s.SetComponentStatus(ctx, "interface_counter", "running", at.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.HeartbeatCoverage(ctx, at); err != nil {
		t.Fatal(err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = recordRetention(ctx, tx, "interface_hourly", "time_expiry", 3, at.Add(-time.Hour).UnixMilli(), at.UnixMilli(), at); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = recordRetention(ctx, tx, "events", "time_expiry", 9, at.UnixMilli(), at.UnixMilli(), at); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	// This internal check must not depend on a traffic inventory at all.
	if _, err = s.db.Exec(`ALTER TABLE traffic_hourly RENAME TO unavailable_traffic_inventory`); err != nil {
		t.Fatal(err)
	}
	i, r, err := s.MonitorCoverage(ctx, at.Add(-time.Hour), at)
	if err != nil {
		t.Fatal(err)
	}
	if i.Retention != nil || len(r.Retained) != 0 || len(r.Entries) != 1 || r.Entries[0].Dataset != "interface_hourly" || len(r.Totals) != 1 || r.Totals[0].AffectedRows != 3 || !i.AsOf.Equal(r.AsOf) || r.TrackingStarted.IsZero() {
		t.Fatal("coverage safeguards or inventory boundary changed", i, r)
	}
	if _, err = s.db.Exec(`DELETE FROM retention_ledger WHERE dataset='interface_hourly'; UPDATE retention_meta SET evicted_entries=1`); err != nil {
		t.Fatal(err)
	}
	_, r, err = s.MonitorCoverage(ctx, at.Add(-time.Hour), at)
	if err != nil || len(r.Entries) != 0 || r.EvictedEntries != 1 || len(r.Totals) != 1 || r.Totals[0].AffectedRows != 3 {
		t.Fatal("lifetime eviction evidence lost", r, err)
	}
	if _, _, err = s.MonitorCoverage(ctx, at, at); err == nil {
		t.Fatal("invalid coverage query accepted")
	}
}
