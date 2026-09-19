// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"errors"
	"time"
)

const maxCoverageIntervals = 10000

func (s *Store) resetCoverage(ctx context.Context, now time.Time) error {
	release, admitErr := s.beginWrite(ctx, writeCritical)
	if admitErr != nil {
		return admitErr
	}
	defer release()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// The last heartbeat is the last point we can prove the old process lived.
	if _, err := tx.ExecContext(ctx, `UPDATE coverage_intervals SET ended_at=MAX(started_at,MIN(?,COALESCE((SELECT updated_at FROM component_status c WHERE c.name=coverage_intervals.name),started_at))) WHERE ended_at IS NULL`, now.UnixMilli()); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO coverage_intervals(name,state,started_at,ended_at) SELECT name,'unknown',MIN(updated_at,?),? FROM component_status WHERE updated_at<?`, now.UnixMilli(), now.UnixMilli(), now.UnixMilli()); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM component_status`); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) setCoverage(ctx context.Context, name, state string, now time.Time) error {
	release, admitErr := s.beginWrite(ctx, writeCritical)
	if admitErr != nil {
		return admitErr
	}
	defer release()

	if name == "" || len(name) > 64 || (state != "running" && state != "degraded" && state != "disabled") {
		return errors.New("component status is invalid")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var same int
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM component_status WHERE name=? AND state=?)`, name, state).Scan(&same); err != nil {
		return err
	}
	if same == 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE coverage_intervals SET ended_at=MAX(started_at,?) WHERE name=? AND ended_at IS NULL`, now.UnixMilli(), name); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO coverage_intervals(name,state,started_at) VALUES (?,?,?)`, name, state, now.UnixMilli()); err != nil {
			return err
		}
		if _, err := pruneRows(ctx, tx, "coverage_intervals", "capacity_eviction", `SELECT rowid FROM coverage_intervals WHERE ended_at IS NOT NULL AND id NOT IN (SELECT id FROM coverage_intervals ORDER BY id DESC LIMIT ?)`, []any{maxCoverageIntervals}, now); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO component_status(name,state,updated_at) VALUES (?,?,?) ON CONFLICT(name) DO UPDATE SET state=excluded.state,updated_at=MAX(updated_at,excluded.updated_at)`, name, state, now.UnixMilli()); err != nil {
		return err
	}
	return tx.Commit()
}

// HeartbeatCoverage advances only known component states. An interrupted daemon
// leaves a conservative, visible unknown interval on its next startup.
func (s *Store) HeartbeatCoverage(ctx context.Context, now time.Time) error {
	release, admitErr := s.beginWrite(ctx, writeCritical)
	if admitErr != nil {
		return admitErr
	}
	defer release()

	_, err := s.db.ExecContext(ctx, `UPDATE component_status SET updated_at=MAX(updated_at,?)`, now.UnixMilli())
	return err
}

type CoverageTotal struct {
	Name         string `json:"name"`
	State        string `json:"state"`
	Milliseconds int64  `json:"milliseconds"`
}

type CoverageGap struct {
	Name   string    `json:"name"`
	Reason string    `json:"reason"`
	Start  time.Time `json:"start_utc"`
	End    time.Time `json:"end_utc"`
	Count  uint64    `json:"count"`
}

func (s *Store) RecordCoverageGap(ctx context.Context, gap CoverageGap) error {
	if gap.Name == "" || len(gap.Name) > 64 || gap.Reason == "" || len(gap.Reason) > 64 || gap.Start.IsZero() || gap.End.Before(gap.Start) || gap.Count > 1<<63-1 {
		return errors.New("invalid coverage gap")
	}
	release, err := s.beginWrite(ctx, writeCritical)
	if err != nil {
		return err
	}
	defer release()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO coverage_gaps(name,reason,started_at,ended_at,count) VALUES (?,?,?,?,?)`, gap.Name, gap.Reason, gap.Start.UnixMilli(), gap.End.UnixMilli(), gap.Count); err != nil {
		return err
	}
	if _, err := pruneRows(ctx, tx, "coverage_gaps", "capacity_eviction", `SELECT rowid FROM coverage_gaps WHERE id<=(SELECT MAX(id)-1000 FROM coverage_gaps)`, nil, time.Now().UTC()); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) CoverageGaps(ctx context.Context, start, end time.Time, count int) ([]CoverageGap, error) {
	if !start.Before(end) || count < 1 || count > 100 {
		return nil, errors.New("invalid coverage gap query")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT name,reason,started_at,ended_at,count FROM coverage_gaps WHERE started_at<? AND ended_at>=? ORDER BY id DESC LIMIT ?`, end.UnixMilli(), start.UnixMilli(), count)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []CoverageGap{}
	for rows.Next() {
		var g CoverageGap
		var start, end int64
		if err := rows.Scan(&g.Name, &g.Reason, &start, &end, &g.Count); err != nil {
			return nil, err
		}
		g.Start = time.UnixMilli(start).UTC()
		g.End = time.UnixMilli(end).UTC()
		result = append(result, g)
	}
	return result, rows.Err()
}

func (s *Store) Coverage(ctx context.Context, start, end time.Time) ([]CoverageTotal, error) {
	if !start.Before(end) {
		return nil, errors.New("invalid coverage range")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT i.name,i.state,SUM(MAX(0,MIN(COALESCE(i.ended_at,c.updated_at,i.started_at),?)-MAX(i.started_at,?)))
 FROM coverage_intervals i LEFT JOIN component_status c ON c.name=i.name
 WHERE i.started_at<? AND COALESCE(i.ended_at,c.updated_at,i.started_at)>? GROUP BY i.name,i.state ORDER BY i.name,i.state LIMIT 128`, end.UnixMilli(), start.UnixMilli(), end.UnixMilli(), start.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []CoverageTotal{}
	for rows.Next() {
		var value CoverageTotal
		if err := rows.Scan(&value.Name, &value.State, &value.Milliseconds); err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, rows.Err()
}
