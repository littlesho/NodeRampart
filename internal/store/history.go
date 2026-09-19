// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/littlesho/NodeRampart/internal/billing"
	"github.com/littlesho/NodeRampart/internal/model"
)

type ReportSnapshot struct {
	Billing     *billing.Snapshot `json:"billing,omitempty"`
	Date        string            `json:"date"`
	Title       string            `json:"title"`
	Body        string            `json:"body"`
	PeriodStart time.Time         `json:"period_start_utc"`
	PeriodEnd   time.Time         `json:"period_end_utc"`
	GeneratedAt time.Time         `json:"generated_at_utc"`
}

func (s *Store) SaveReport(ctx context.Context, report ReportSnapshot) error {
	_, err := s.SaveReportIfAbsent(ctx, report)
	return err
}

var ErrReportPeriodConflict = errors.New("archived report has different period bounds")

// SaveReportIfAbsent preserves the first body and rejects timezone conflicts.
// The existence check and insertion share the writer transaction.
func (s *Store) SaveReportIfAbsent(ctx context.Context, report ReportSnapshot) (bool, error) {
	release, admitErr := s.beginWrite(ctx, writeNormal)
	if admitErr != nil {
		return false, admitErr
	}
	defer release()

	if date, err := time.Parse("2006-01-02", report.Date); err != nil || date.Format("2006-01-02") != report.Date {
		return false, errors.New("invalid report date")
	}
	if !report.PeriodStart.Before(report.PeriodEnd) || len(report.Title) > 256 || report.Body == "" || len(report.Body) > 4096 || report.GeneratedAt.IsZero() {
		return false, errors.New("invalid report snapshot")
	}
	if report.Billing != nil && (!report.Billing.PeriodEnd.Equal(report.PeriodEnd) || report.Billing.PeriodStart.After(report.PeriodStart) || report.Billing.GeneratedAt.UnixMilli() != report.GeneratedAt.UnixMilli()) {
		return false, errors.New("pricing snapshot does not match report period or generation")
	}
	pricing, err := billing.EncodeSnapshot(report.Billing)
	if err != nil {
		return false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO report_snapshots(report_date,title,body,period_start,period_end,generated_at,billing_json) VALUES (?,?,?,?,?,?,?)`, report.Date, report.Title, report.Body, report.PeriodStart.UnixMilli(), report.PeriodEnd.UnixMilli(), report.GeneratedAt.UnixMilli(), pricing)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	var start, end int64
	if err := tx.QueryRowContext(ctx, `SELECT period_start,period_end FROM report_snapshots WHERE report_date=?`, report.Date).Scan(&start, &end); err != nil {
		return false, err
	}
	if start != report.PeriodStart.UnixMilli() || end != report.PeriodEnd.UnixMilli() {
		return false, ErrReportPeriodConflict
	}
	return count == 1, tx.Commit()
}

func (s *Store) Report(ctx context.Context, date string) (ReportSnapshot, error) {
	var report ReportSnapshot
	var start, end, generated int64
	var pricing string
	err := s.db.QueryRowContext(ctx, `SELECT report_date,title,body,period_start,period_end,generated_at,CASE WHEN length(CAST(billing_json AS BLOB))<=16384 THEN billing_json ELSE NULL END FROM report_snapshots WHERE report_date=?`, date).Scan(&report.Date, &report.Title, &report.Body, &start, &end, &generated, &pricing)
	if err != nil {
		return report, err
	}
	report.Billing, err = billing.DecodeSnapshot(pricing)
	if err != nil {
		return ReportSnapshot{}, err
	}
	report.PeriodStart = time.UnixMilli(start).UTC()
	report.PeriodEnd = time.UnixMilli(end).UTC()
	report.GeneratedAt = time.UnixMilli(generated).UTC()
	return report, nil
}

func (s *Store) Reports(ctx context.Context, before string, count int) ([]ReportSnapshot, error) {
	if count < 1 || count > 100 {
		return nil, errors.New("report limit must be 1..100")
	}
	if before == "" {
		before = "9999-12-31"
	}
	rows, err := s.db.QueryContext(ctx, `SELECT report_date,title,period_start,period_end,generated_at FROM report_snapshots WHERE report_date<? ORDER BY report_date DESC LIMIT ?`, before, count)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	reports := []ReportSnapshot{}
	for rows.Next() {
		var r ReportSnapshot
		var start, end, generated int64
		if err := rows.Scan(&r.Date, &r.Title, &start, &end, &generated); err != nil {
			return nil, err
		}
		r.PeriodStart = time.UnixMilli(start).UTC()
		r.PeriodEnd = time.UnixMilli(end).UTC()
		r.GeneratedAt = time.UnixMilli(generated).UTC()
		reports = append(reports, r)
	}
	return reports, rows.Err()
}

type EventQuery struct {
	Start    time.Time `json:"start_utc"`
	End      time.Time `json:"end_utc"`
	BeforeID string    `json:"before_id,omitempty"`
	Kind     string    `json:"kind,omitempty"`
	Limit    int       `json:"limit"`
}

// Events returns stored, already privacy-transformed evidence. Timestamp/ID form
// a stable descending pagination key. Callers pass the final item's timestamp
// as End and ID as BeforeID for the next page.
func (s *Store) Events(ctx context.Context, q EventQuery) ([]model.Event, error) {
	if q.Limit < 1 || q.Limit > 100 || q.Start.IsZero() || q.End.IsZero() || q.Start.After(q.End) || len(q.Kind) > 128 || len(q.BeforeID) > 128 {
		return nil, errors.New("invalid event query")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,incident_id,observed_at,kind,phase,severity,summary,source_ip,source_range,target,count,geo_json,evidence_json FROM events
 WHERE observed_at>=? AND (observed_at<? OR (observed_at=? AND ? AND ?<>'' AND id<?)) AND (?='' OR kind=?) ORDER BY observed_at DESC,id DESC LIMIT ?`, ceilUnixMilli(q.Start), ceilUnixMilli(q.End), q.End.UnixMilli(), millisecondAligned(q.End), q.BeforeID, q.BeforeID, q.Kind, q.Kind, q.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := []model.Event{}
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (s *Store) Event(ctx context.Context, id string) (model.Event, error) {
	if id == "" || len(id) > 128 {
		return model.Event{}, errors.New("invalid event id")
	}
	return scanEvent(s.db.QueryRowContext(ctx, `SELECT id,incident_id,observed_at,kind,phase,severity,summary,source_ip,source_range,target,count,geo_json,evidence_json FROM events WHERE id=?`, id))
}

type scanner interface{ Scan(...any) error }

func scanEvent(row scanner) (model.Event, error) {
	var event model.Event
	var observed int64
	var geo, evidence string
	if err := row.Scan(&event.ID, &event.IncidentID, &observed, &event.Kind, &event.Phase, &event.Severity, &event.Summary, &event.SourceIP, &event.SourceRange, &event.Target, &event.Count, &geo, &evidence); err != nil {
		return event, err
	}
	event.ObservedAt = time.UnixMilli(observed).UTC()
	if err := json.Unmarshal([]byte(geo), &event.Geo); err != nil {
		return event, err
	}
	if err := json.Unmarshal([]byte(evidence), &event.Evidence); err != nil {
		return event, err
	}
	return event, nil
}

func IsNotFound(err error) bool { return errors.Is(err, sql.ErrNoRows) }
