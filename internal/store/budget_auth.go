// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"time"
)

const (
	maxAuthCompactionRows = 256
	authPressureBucket    = "_storage_pressure"
	authPressureReason    = "storage_auth_detail_compacted"
)

type authCompactionCursor struct {
	hour         int64
	kind, source string
}

type authCompactionRow struct {
	authCompactionCursor
	id, count int64
}

// compactAuthChunkLocked walks the existing primary-key index in bounded
// windows. Summary buckets are excluded in Go, so an arbitrary run of already
// compacted hours cannot turn one invocation into an unbounded SQL scan. The
// cursor is only a work hint: restart/wrap may revisit rows but never recount a
// bucket. A pass with no remaining candidates permits event pruning.
func (s *Store) compactAuthChunkLocked(ctx context.Context) (bool, error) {
	query := `SELECT rowid,hour_utc,kind,source_range,count FROM auth_hourly`
	var args []any
	if cursor := s.budget.authCursor; cursor != nil {
		query += ` WHERE (hour_utc,kind,source_range)>(?,?,?)`
		args = append(args, cursor.hour, cursor.kind, cursor.source)
	}
	query += ` ORDER BY hour_utc,kind,source_range LIMIT ?`
	args = append(args, maxAuthCompactionRows)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return false, err
	}
	var selected []authCompactionRow
	var last *authCompactionCursor
	for rows.Next() {
		var row authCompactionRow
		if err := rows.Scan(&row.id, &row.hour, &row.kind, &row.source, &row.count); err != nil {
			rows.Close()
			return false, err
		}
		if len(selected) > 0 && (row.hour != selected[0].hour || row.kind != selected[0].kind) {
			break
		}
		cursor := row.authCompactionCursor
		last = &cursor
		if row.source != "_overflow" && row.source != authPressureBucket {
			selected = append(selected, row)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return false, err
	}
	if err := rows.Close(); err != nil {
		return false, err
	}
	// Advance even if a later transaction cannot allocate its output pages.
	// This lets a following call try a denser group; the failed rows remain
	// intact and are reconsidered after this bounded walk wraps around.
	s.budget.authCursor = last
	if len(selected) == 0 {
		if last == nil {
			s.budget.authPassCompleted = true
		}
		return last != nil, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	first := selected[0]
	var total int64
	err = tx.QueryRowContext(ctx, `SELECT count FROM auth_hourly WHERE hour_utc=? AND kind=? AND source_range=?`, first.hour, first.kind, authPressureBucket).Scan(&total)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	// Replacing the sole source of an hour/kind would add a coverage row but
	// reclaim no aggregate rows. Preserve that detail and move on instead.
	if len(selected) == 1 && errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	releasedKeys := len(selected)
	if errors.Is(err, sql.ErrNoRows) {
		// The new summary remains one retained key. Counting it prevents
		// compaction from bypassing the per-hour cap as kinds accumulate.
		releasedKeys--
	}
	for _, row := range selected {
		if total < 0 || row.count < 0 || total > math.MaxInt64-row.count {
			return false, errors.New("authentication pressure total exceeds SQLite integer bounds")
		}
		total += row.count
	}
	statement, err := tx.PrepareContext(ctx, `DELETE FROM auth_hourly WHERE rowid=?`)
	if err != nil {
		return false, err
	}
	defer statement.Close()
	for _, row := range selected {
		if _, err := statement.ExecContext(ctx, row.id); err != nil {
			return false, err
		}
	}
	// Delete first so a full page budget can reuse these pages for the summary
	// and its loss marker. All changes, including cardinality, commit together.
	if _, err := tx.ExecContext(ctx, `INSERT INTO auth_hourly(hour_utc,kind,source_range,count) VALUES (?,?,?,?)
 ON CONFLICT(hour_utc,kind,source_range) DO UPDATE SET count=excluded.count`, first.hour, first.kind, authPressureBucket, total); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE auth_cardinality_hourly SET keys=MAX(0,keys-?) WHERE hour_utc=?`, releasedKeys, first.hour); err != nil {
		return false, err
	}
	if err := recordAuthCompaction(ctx, tx, first.hour, int64(len(selected))); err != nil {
		return false, err
	}
	if err := recordRetention(ctx, tx, "auth_hourly", "storage_pressure", int64(len(selected)), first.hour*1000, (first.hour+3600)*1000, time.Now().UTC()); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	removed := uint64(len(selected))
	if s.budget.status.CompactedAuthRows > math.MaxUint64-removed {
		s.budget.status.CompactedAuthRows = math.MaxUint64
	} else {
		s.budget.status.CompactedAuthRows += removed
	}
	return true, nil
}

// Count records discarded source-aggregate rows, not observations or distinct
// addresses. Coalescing by hour keeps repeated pressure from expanding history;
// the existing 1000-gap bound still applies. Rollback cannot double-count loss.
func recordAuthCompaction(ctx context.Context, tx *sql.Tx, hour, removed int64) error {
	if hour > math.MaxInt64/1000-3600 || hour < math.MinInt64/1000 {
		return errors.New("authentication pressure hour exceeds timestamp bounds")
	}
	start, end := hour*1000, (hour+3600)*1000
	var id, previous int64
	err := tx.QueryRowContext(ctx, `SELECT id,count FROM coverage_gaps WHERE name='ssh_journal' AND reason=? AND started_at=? AND ended_at=? ORDER BY id DESC LIMIT 1`, authPressureReason, start, end).Scan(&id, &previous)
	if errors.Is(err, sql.ErrNoRows) {
		if _, err := tx.ExecContext(ctx, `INSERT INTO coverage_gaps(name,reason,started_at,ended_at,count) VALUES ('ssh_journal',?,?,?,?)`, authPressureReason, start, end, removed); err != nil {
			return err
		}
		_, err := pruneRows(ctx, tx, "coverage_gaps", "capacity_eviction", `SELECT rowid FROM coverage_gaps ORDER BY id DESC LIMIT 1 OFFSET 1000`, nil, time.Now().UTC())
		return err
	}
	if err != nil {
		return err
	}
	if previous < 0 || previous > math.MaxInt64-removed {
		return errors.New("authentication pressure loss counter exceeds SQLite integer bounds")
	}
	_, err = tx.ExecContext(ctx, `UPDATE coverage_gaps SET count=? WHERE id=?`, previous+removed, id)
	return err
}
