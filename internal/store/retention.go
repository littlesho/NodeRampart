// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"time"

	"github.com/littlesho/NodeRampart/internal/api"
)

const maxRetentionEntries = 1024

type RetentionQuery struct {
	Start    time.Time `json:"start_utc"`
	End      time.Time `json:"end_utc"`
	Dataset  string    `json:"dataset,omitempty"`
	Reason   string    `json:"reason,omitempty"`
	BeforeID int64     `json:"before_id,omitempty"`
	Limit    int       `json:"limit"`
}

type RetentionEntry struct {
	ID                int64     `json:"id"`
	Dataset           string    `json:"dataset"`
	Reason            string    `json:"reason"`
	ActionDay         time.Time `json:"action_day_utc"`
	Operations        int64     `json:"operations"`
	AffectedRows      int64     `json:"affected_rows"`
	DataStart         time.Time `json:"data_start_utc"`
	DataEnd           time.Time `json:"data_end_utc"`
	FirstAction       time.Time `json:"first_action_utc"`
	LastAction        time.Time `json:"last_action_utc"`
	AggregateSurvives string    `json:"aggregate_survives"`
}

type RetentionTotal struct {
	Dataset           string    `json:"dataset"`
	Reason            string    `json:"reason"`
	Operations        int64     `json:"operations"`
	AffectedRows      int64     `json:"affected_rows"`
	DataStart         time.Time `json:"data_start_utc"`
	DataEnd           time.Time `json:"data_end_utc"`
	FirstAction       time.Time `json:"first_action_utc"`
	LastAction        time.Time `json:"last_action_utc"`
	AggregateSurvives string    `json:"aggregate_survives"`
}

type RetainedDataset struct {
	Dataset string    `json:"dataset"`
	Rows    int64     `json:"rows"`
	Start   time.Time `json:"start_utc,omitzero"`
	End     time.Time `json:"end_utc,omitzero"`
}

type RetentionView struct {
	Start           time.Time         `json:"start_utc"`
	End             time.Time         `json:"end_utc"`
	AsOf            time.Time         `json:"as_of_utc"`
	TrackingStarted time.Time         `json:"tracking_started_utc"`
	Entries         []RetentionEntry  `json:"entries"`
	Totals          []RetentionTotal  `json:"totals"`
	Retained        []RetainedDataset `json:"retained"`
	More            bool              `json:"more"`
	NextBeforeID    int64             `json:"next_before_id,omitempty"`
	EvictedEntries  int64             `json:"evicted_entries"`
	DetailLimit     int               `json:"detail_limit"`
	Notes           []string          `json:"notes"`
}

// SQL expressions here are a fixed internal allowlist, never caller input.
var retentionDatasets = []struct{ name, start, end string }{
	{"events", "observed_at", "observed_at"},
	{"traffic_hourly", "hour_utc*1000", "(hour_utc+3600)*1000"},
	{"auth_hourly", "hour_utc*1000", "(hour_utc+3600)*1000"},
	{"interface_hourly", "hour_utc*1000", "(hour_utc+3600)*1000"},
	{"interface_detail_hourly", "hour_utc*1000", "(hour_utc+3600)*1000"},
	{"collector_health_hourly", "hour_utc*1000", "(hour_utc+3600)*1000"},
	{"report_snapshots", "period_start", "period_end"},
	{"coverage_intervals", "started_at", "COALESCE(ended_at,(SELECT updated_at FROM component_status c WHERE c.name=coverage_intervals.name),started_at)"},
	{"coverage_gaps", "started_at", "ended_at"},
	{"notification_outbox", "created_at", "created_at"},
	{"notification_silences", "created_at", "expires_at"},
}

func ValidRetentionDataset(dataset string) bool {
	for _, d := range retentionDatasets {
		if d.name == dataset {
			return true
		}
	}
	return false
}

func ValidRetentionReason(reason string) bool {
	switch reason {
	case "time_expiry", "storage_pressure", "cardinality_compaction", "capacity_eviction", "silence_body_discard":
		return true
	}
	return false
}

func (q RetentionQuery) Validate() error {
	if !api.ValidTimeRange(q.Start, q.End, 400) || !api.ValidLimit(q.Limit) || q.BeforeID < 0 || q.Dataset != "" && !ValidRetentionDataset(q.Dataset) || q.Reason != "" && !ValidRetentionReason(q.Reason) {
		return errors.New("retention requires a positive range up to 400 days, fixed filters, nonnegative cursor and limit 1..100")
	}
	return nil
}

func retentionSurvivor(dataset, reason string) string {
	if reason == "silence_body_discard" {
		return "notification_outcome_preserved"
	}
	if reason == "cardinality_compaction" || dataset == "auth_hourly" && reason == "storage_pressure" {
		return "totals_preserved"
	}
	if dataset == "notification_outbox" {
		return "event_decisions_have_separate_retention"
	}
	return "none_guaranteed"
}

func (s *Store) Retention(ctx context.Context, q RetentionQuery) (RetentionView, error) {
	if err := q.Validate(); err != nil {
		return RetentionView{}, err
	}
	tx, asOf, err := s.beginReadSnapshot(ctx)
	if err != nil {
		return RetentionView{}, err
	}
	defer tx.Rollback()
	view, err := readRetention(ctx, tx, q, asOf)
	if err != nil {
		return view, err
	}
	return view, tx.Commit()
}

func readRetention(ctx context.Context, db timelineReader, q RetentionQuery, asOf time.Time) (RetentionView, error) {
	view, err := readRetentionDetails(ctx, db, q, asOf)
	if err != nil {
		return view, err
	}
	for _, d := range retentionDatasets {
		if q.Dataset != "" && q.Dataset != d.name {
			continue
		}
		remaining := RetainedDataset{Dataset: d.name}
		var start, end sql.NullInt64
		query, args := retentionInventoryQuery(d.name, d.start, d.end, q.Start, q.End)
		if err := db.QueryRowContext(ctx, query, args...).Scan(&remaining.Rows, &start, &end); err != nil {
			return view, err
		}
		if start.Valid {
			remaining.Start = time.UnixMilli(start.Int64).UTC()
			remaining.End = time.UnixMilli(end.Int64).UTC()
		}
		view.Retained = append(view.Retained, remaining)
	}
	return view, nil
}

// readRetentionDetails leaves inventory empty for internal coverage checks.
func readRetentionDetails(ctx context.Context, db timelineReader, q RetentionQuery, asOf time.Time) (RetentionView, error) {
	view := RetentionView{Start: q.Start.UTC(), End: q.End.UTC(), AsOf: asOf.UTC(), Entries: []RetentionEntry{}, Totals: []RetentionTotal{}, Retained: []RetainedDataset{}, DetailLimit: maxRetentionEntries, Notes: []string{
		"Tracking begins at the recorded schema migration; earlier removal is unknown. Counts saturate at the signed 64-bit maximum.",
		"Affected rows count deleted, compacted or body-cleared source rows, according to reason; they are not lost packets or people. Data spans are bounding intervals, not proof of continuous loss.",
		"Lifetime totals honor dataset/reason filters but are not restricted to the requested time window. Detailed history is bounded; empty details do not prove no earlier removal.",
		"Remaining rows overlap the requested period; their full time bounds may extend beyond it and do not prove uninterrupted collection. Pagination takes a new snapshot per request.",
	}}
	if err := q.Validate(); err != nil {
		return view, err
	}
	var tracking int64
	if err := db.QueryRowContext(ctx, `SELECT tracking_started,evicted_entries FROM retention_meta WHERE id=1`).Scan(&tracking, &view.EvictedEntries); err != nil {
		return view, err
	}
	view.TrackingStarted = time.UnixMilli(tracking).UTC()
	rows, err := db.QueryContext(ctx, `SELECT id,dataset,reason,action_day,operations,affected_rows,data_start,data_end,first_action,last_action FROM retention_ledger WHERE data_start<? AND (data_end>? OR (data_end=? AND (dataset IN ('events','notification_outbox','coverage_gaps','coverage_intervals') OR data_start=data_end))) AND (?='' OR dataset=?) AND (?='' OR reason=?) AND (?=0 OR id<?) ORDER BY id DESC LIMIT ?`, q.End.UnixMilli(), q.Start.UnixMilli(), q.Start.UnixMilli(), q.Dataset, q.Dataset, q.Reason, q.Reason, q.BeforeID, q.BeforeID, q.Limit+1)
	if err != nil {
		return view, err
	}
	for rows.Next() {
		var entry RetentionEntry
		var day, start, end, first, last int64
		if err := rows.Scan(&entry.ID, &entry.Dataset, &entry.Reason, &day, &entry.Operations, &entry.AffectedRows, &start, &end, &first, &last); err != nil {
			rows.Close()
			return view, err
		}
		if !ValidRetentionDataset(entry.Dataset) || !ValidRetentionReason(entry.Reason) || entry.ID < 1 || entry.Operations < 1 || entry.AffectedRows < 1 || end < start || last < first {
			rows.Close()
			return view, errors.New("invalid retention history")
		}
		if len(view.Entries) == q.Limit {
			view.More = true
			break
		}
		entry.ActionDay, entry.DataStart, entry.DataEnd = time.UnixMilli(day).UTC(), time.UnixMilli(start).UTC(), time.UnixMilli(end).UTC()
		entry.FirstAction, entry.LastAction = time.UnixMilli(first).UTC(), time.UnixMilli(last).UTC()
		entry.AggregateSurvives = retentionSurvivor(entry.Dataset, entry.Reason)
		view.Entries = append(view.Entries, entry)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return view, err
	}
	if err := rows.Close(); err != nil {
		return view, err
	}
	if view.More {
		view.NextBeforeID = view.Entries[len(view.Entries)-1].ID
	}
	rows, err = db.QueryContext(ctx, `SELECT dataset,reason,operations,affected_rows,data_start,data_end,first_action,last_action FROM retention_totals WHERE (?='' OR dataset=?) AND (?='' OR reason=?) ORDER BY dataset,reason LIMIT 56`, q.Dataset, q.Dataset, q.Reason, q.Reason)
	if err != nil {
		return view, err
	}
	for rows.Next() {
		var total RetentionTotal
		var start, end, first, last int64
		if err := rows.Scan(&total.Dataset, &total.Reason, &total.Operations, &total.AffectedRows, &start, &end, &first, &last); err != nil {
			rows.Close()
			return view, err
		}
		if len(view.Totals) >= 55 || !ValidRetentionDataset(total.Dataset) || !ValidRetentionReason(total.Reason) || total.Operations < 1 || total.AffectedRows < 1 || end < start || last < first {
			rows.Close()
			return view, errors.New("invalid retention totals")
		}
		total.DataStart, total.DataEnd = time.UnixMilli(start).UTC(), time.UnixMilli(end).UTC()
		total.FirstAction, total.LastAction = time.UnixMilli(first).UTC(), time.UnixMilli(last).UTC()
		total.AggregateSurvives = retentionSurvivor(total.Dataset, total.Reason)
		view.Totals = append(view.Totals, total)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return view, err
	}
	if err := rows.Close(); err != nil {
		return view, err
	}

	return view, nil
}

// Keep indexed time columns bare in predicates. Projection may still convert
// bounds to milliseconds, but unrelated retained hours are never scanned.
func retentionInventoryQuery(dataset, first, last string, start, end time.Time) (string, []any) {
	query := `SELECT COUNT(*),MIN(` + first + `),MAX(` + last + `) FROM ` + dataset + ` WHERE `
	switch dataset {
	case "traffic_hourly", "auth_hourly", "interface_hourly", "interface_detail_hourly", "collector_health_hourly":
		upper := end.Unix()
		if end.Nanosecond() != 0 {
			upper++
		}
		return query + `hour_utc<? AND hour_utc>?`, []any{upper, start.Unix() - 3600}
	default:
		return query + first + `<? AND (` + last + `>? OR (` + first + `=` + last + ` AND ` + first + `>=?))`, []any{end.UnixMilli(), start.UnixMilli(), start.UnixMilli()}
	}
}

// recordRetention must run inside the mutation transaction, under the caller's
// write guard. It never calls public write methods or recursively records its
// own eviction. The small fixed totals table survives detail eviction.
func recordRetention(ctx context.Context, tx *sql.Tx, dataset, reason string, count, start, end int64, now time.Time) error {
	if count == 0 {
		return nil
	}
	if !ValidRetentionDataset(dataset) || !ValidRetentionReason(reason) || count < 0 || end < start || now.IsZero() {
		return errors.New("invalid retention accounting")
	}
	var tracking int64
	if err := tx.QueryRowContext(ctx, `SELECT tracking_started FROM retention_meta WHERE id=1`).Scan(&tracking); err != nil {
		return err
	}
	day, at := now.UTC().Truncate(24*time.Hour).UnixMilli(), now.UnixMilli()
	const update = `operations=MIN(operations,9223372036854775806)+1,affected_rows=CASE WHEN affected_rows>9223372036854775807-excluded.affected_rows THEN 9223372036854775807 ELSE affected_rows+excluded.affected_rows END,data_start=MIN(data_start,excluded.data_start),data_end=MAX(data_end,excluded.data_end),first_action=MIN(first_action,excluded.first_action),last_action=MAX(last_action,excluded.last_action)`
	// Explicitly reject exhausted IDs rather than letting SQLite choose a random
	// rowid and invalidate the monotonic pagination contract.
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM retention_ledger WHERE dataset=? AND reason=? AND action_day=?)`, dataset, reason, day).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		var last int64
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(id),0) FROM retention_ledger`).Scan(&last); err != nil {
			return err
		}
		if last == math.MaxInt64 {
			return errors.New("retention history ID exhausted")
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO retention_ledger(id,dataset,reason,action_day,operations,affected_rows,data_start,data_end,first_action,last_action) VALUES (?,?,?,?,1,?,?,?,?,?)`, last+1, dataset, reason, day, count, start, end, at, at); err != nil {
			return err
		}
	} else {
		if _, err := tx.ExecContext(ctx, `INSERT INTO retention_ledger(dataset,reason,action_day,operations,affected_rows,data_start,data_end,first_action,last_action) VALUES (?,?,?,1,?,?,?,?,?) ON CONFLICT(dataset,reason,action_day) DO UPDATE SET `+update, dataset, reason, day, count, start, end, at, at); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO retention_totals(dataset,reason,operations,affected_rows,data_start,data_end,first_action,last_action) VALUES (?,?,1,?,?,?,?,?) ON CONFLICT(dataset,reason) DO UPDATE SET `+update, dataset, reason, count, start, end, at, at); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM retention_ledger WHERE id NOT IN(SELECT id FROM retention_ledger ORDER BY id DESC LIMIT 1024)`)
	if err != nil {
		return err
	}
	evicted, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if evicted > 0 {
		_, err = tx.ExecContext(ctx, `UPDATE retention_meta SET evicted_entries=CASE WHEN evicted_entries>9223372036854775807-? THEN 9223372036854775807 ELSE evicted_entries+? END WHERE id=1`, evicted, evicted)
	}
	return err
}

// pruneRows accounts for the rows actually deleted, not a separately evaluated
// LIMIT selector. SQLite buffers RETURNING rows inside this same transaction;
// callers retain the existing bounded chunk/capacity sizes.
func pruneRows(ctx context.Context, tx *sql.Tx, dataset, reason, selector string, args []any, now time.Time) (int64, error) {
	var first, last string
	for _, d := range retentionDatasets {
		if dataset == d.name {
			first, last = d.start, d.end
			break
		}
	}
	if first == "" {
		return 0, errors.New("invalid retained dataset")
	}
	rows, err := tx.QueryContext(ctx, `DELETE FROM `+dataset+` WHERE rowid IN (`+selector+`) RETURNING `+first+`,`+last, args...)
	if err != nil {
		return 0, err
	}
	var count, start, end int64
	for rows.Next() {
		var low, high int64
		if err := rows.Scan(&low, &high); err != nil {
			rows.Close()
			return 0, err
		}
		if count == math.MaxInt64 {
			rows.Close()
			return 0, errors.New("retention row count exhausted")
		}
		if count == 0 || low < start {
			start = low
		}
		if count == 0 || high > end {
			end = high
		}
		count++
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	if err := recordRetention(ctx, tx, dataset, reason, count, start, end, now); err != nil {
		return 0, err
	}
	return count, nil
}

func (s *Store) pruneRetentionChunkLocked(ctx context.Context, dataset, reason, selector string, args []any, now time.Time) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var count int64
	if ValidRetentionDataset(dataset) {
		count, err = pruneRows(ctx, tx, dataset, reason, selector, args, now)
	} else {
		// Only the fixed Prune table list uses this path. These helpers do not
		// represent additional user observations and are not double-counted.
		switch dataset {
		case "auth_cardinality_hourly", "traffic_cardinality_hourly", "report_runs":
		default:
			return 0, errors.New("invalid retention housekeeping dataset")
		}
		var result sql.Result
		result, err = tx.ExecContext(ctx, `DELETE FROM `+dataset+` WHERE rowid IN (`+selector+`)`, args...)
		if err == nil {
			count, err = result.RowsAffected()
		}
	}
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return count, nil
}
