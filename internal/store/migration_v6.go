// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

const retentionDatasetSQL = "'events','traffic_hourly','auth_hourly','interface_hourly','interface_detail_hourly','collector_health_hourly','report_snapshots','coverage_intervals','coverage_gaps','notification_outbox','notification_silences'"
const retentionReasonSQL = "'time_expiry','storage_pressure','cardinality_compaction','capacity_eviction','silence_body_discard'"

func migrateV6(ctx context.Context, tx *sql.Tx) error {
	for _, statement := range []string{
		`CREATE TABLE monitor_state (key TEXT PRIMARY KEY NOT NULL CHECK(key IN ('budget_month_bytes','budget_month_cost','budget_day_bytes','budget_day_growth','health_sensor','health_interface_counter','health_ssh_journal','health_storage','health_geoip_update')),revision INTEGER NOT NULL CHECK(typeof(revision)='integer' AND revision>0),updated_at INTEGER NOT NULL,data TEXT NOT NULL CHECK(length(CAST(data AS BLOB))<=16384))`,
		`CREATE TABLE retention_meta (id INTEGER PRIMARY KEY CHECK(id=1),tracking_started INTEGER NOT NULL,evicted_entries INTEGER NOT NULL DEFAULT 0 CHECK(typeof(evicted_entries)='integer' AND evicted_entries>=0))`,
		`CREATE TABLE retention_ledger (id INTEGER PRIMARY KEY CHECK(id>0),dataset TEXT NOT NULL CHECK(dataset IN (` + retentionDatasetSQL + `)),reason TEXT NOT NULL CHECK(reason IN (` + retentionReasonSQL + `)),action_day INTEGER NOT NULL,operations INTEGER NOT NULL CHECK(typeof(operations)='integer' AND operations>0),affected_rows INTEGER NOT NULL CHECK(typeof(affected_rows)='integer' AND affected_rows>0),data_start INTEGER NOT NULL,data_end INTEGER NOT NULL,first_action INTEGER NOT NULL,last_action INTEGER NOT NULL,CHECK(data_end>=data_start),CHECK(last_action>=first_action),UNIQUE(dataset,reason,action_day))`,
		`CREATE TABLE retention_totals (dataset TEXT NOT NULL CHECK(dataset IN (` + retentionDatasetSQL + `)),reason TEXT NOT NULL CHECK(reason IN (` + retentionReasonSQL + `)),operations INTEGER NOT NULL CHECK(typeof(operations)='integer' AND operations>0),affected_rows INTEGER NOT NULL CHECK(typeof(affected_rows)='integer' AND affected_rows>0),data_start INTEGER NOT NULL,data_end INTEGER NOT NULL,first_action INTEGER NOT NULL,last_action INTEGER NOT NULL,CHECK(data_end>=data_start),CHECK(last_action>=first_action),PRIMARY KEY(dataset,reason))`,
		`INSERT INTO schema_migrations(version,applied_at) VALUES (6,unixepoch())`,
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("apply database migration 6: %w", err)
		}
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO retention_meta(id,tracking_started) VALUES (1,?)`, time.Now().UTC().UnixMilli())
	return err
}
