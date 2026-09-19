// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/littlesho/NodeRampart/internal/api"
	"github.com/littlesho/NodeRampart/internal/model"
)

type TimelineQuery struct {
	Start      time.Time `json:"start_utc"`
	End        time.Time `json:"end_utc"`
	AfterTime  time.Time `json:"after_utc,omitzero"`
	AfterID    string    `json:"after_id,omitempty"`
	IncidentID string    `json:"incident_id,omitempty"`
	Kind       string    `json:"kind,omitempty"`
	Limit      int       `json:"limit"`
}

func (q TimelineQuery) Validate() error {
	if !api.ValidTimeRange(q.Start, q.End, 8) || !api.ValidLimit(q.Limit) ||
		q.IncidentID != "" && !api.ValidID(q.IncidentID) || q.Kind != "" && !api.ValidID(q.Kind) ||
		(q.AfterID == "") != q.AfterTime.IsZero() || q.AfterID != "" && (!api.ValidID(q.AfterID) || q.AfterTime.Before(q.Start) || !q.AfterTime.Before(q.End)) {
		return errors.New("timeline requires a positive range up to eight days, valid cursor and limit 1..100")
	}
	return nil
}

type DeliveryOutcome struct {
	Decision       string    `json:"decision"`
	NotificationID string    `json:"notification_id,omitempty"`
	State          string    `json:"state"`
	Attempts       int       `json:"attempts"`
	MergedEvents   int64     `json:"merged_events"`
	SilenceID      string    `json:"silence_id,omitempty"`
	SentAt         time.Time `json:"sent_at_utc,omitzero"`
}

type TimelineEvent struct {
	Alert       *model.AlertContext `json:"alert,omitempty"`
	ID          string              `json:"id"`
	IncidentID  string              `json:"incident_id,omitempty"`
	ObservedAt  time.Time           `json:"observed_at_utc"`
	Kind        string              `json:"kind"`
	Phase       string              `json:"phase,omitempty"`
	Severity    model.Severity      `json:"severity"`
	Summary     string              `json:"summary"`
	SourceRange string              `json:"source_range,omitempty"`
	Count       uint64              `json:"count"`
	Delivery    DeliveryOutcome     `json:"delivery"`
}

type TimelinePage struct {
	Events        []TimelineEvent `json:"events"`
	More          bool            `json:"more"`
	NextAfterTime time.Time       `json:"next_after_utc,omitzero"`
	NextAfterID   string          `json:"next_after_id,omitempty"`
	HistoryNote   string          `json:"history_note"`
}

const timelineColumns = `e.id,e.incident_id,e.observed_at,e.kind,e.phase,e.severity,e.summary,e.source_range,e.count,
 COALESCE(d.decision,CASE WHEN old.id IS NOT NULL THEN 'legacy' ELSE 'unknown' END),
 COALESCE(o.id,old.id,NULLIF(d.notification_id,''),''),
 CASE WHEN COALESCE(o.sent_at,old.sent_at) IS NOT NULL THEN 'sent'
 WHEN COALESCE(o.suppressed_at,old.suppressed_at) IS NOT NULL THEN 'silenced'
 WHEN d.decision IN ('silenced','ineligible','rejected') THEN d.decision
 WHEN COALESCE(o.id,old.id) IS NULL THEN 'history_unavailable'
 WHEN COALESCE(o.expires_at,old.expires_at)<=? THEN 'expired'
 WHEN COALESCE(o.quarantined_at,old.quarantined_at) IS NOT NULL THEN 'quarantined'
 WHEN COALESCE(o.lease_until,old.lease_until,0)>? THEN 'sending'
 ELSE 'pending' END,
 COALESCE(o.attempts,old.attempts,0),COALESCE(o.merged_count,old.merged_count,0),COALESCE(d.silence_id,''),COALESCE(o.sent_at,old.sent_at,0),
 CASE WHEN e.kind IN ('budget_month_bytes','budget_month_cost','budget_day_bytes','budget_day_growth','health_sensor','health_interface_counter','health_ssh_journal','health_storage','health_geoip_update') AND length(CAST(e.evidence_json AS BLOB))<=65536 THEN e.evidence_json ELSE '{}' END`

const timelineJoins = ` FROM events e LEFT JOIN event_notifications d ON d.event_id=e.id
 LEFT JOIN notification_outbox o ON o.id=d.notification_id
 LEFT JOIN notification_outbox old ON d.event_id IS NULL AND old.dedupe_key='event:'||e.id||':telegram' `

type timelineReader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func scanTimeline(row scanner) (TimelineEvent, error) {
	var e TimelineEvent
	var observed, sent int64
	var evidence string
	err := row.Scan(&e.ID, &e.IncidentID, &observed, &e.Kind, &e.Phase, &e.Severity, &e.Summary, &e.SourceRange, &e.Count,
		&e.Delivery.Decision, &e.Delivery.NotificationID, &e.Delivery.State, &e.Delivery.Attempts, &e.Delivery.MergedEvents, &e.Delivery.SilenceID, &sent, &evidence)
	e.ObservedAt = time.UnixMilli(observed).UTC()
	if sent != 0 {
		e.Delivery.SentAt = time.UnixMilli(sent).UTC()
	}
	if err == nil && validMonitorKey(e.Kind) {
		var fields map[string]string
		if json.Unmarshal([]byte(evidence), &fields) != nil {
			fields = nil
		}
		e.Alert = model.ProjectAlertContext(e.Kind, fields)
	}
	return e, err
}

func (s *Store) Timeline(ctx context.Context, q TimelineQuery) (TimelinePage, error) {
	return readTimeline(ctx, s.db, q, time.Now().UTC())
}

func readTimeline(ctx context.Context, db timelineReader, q TimelineQuery, now time.Time) (TimelinePage, error) {
	page := TimelinePage{Events: []TimelineEvent{}, HistoryNote: "Retained events only; pruning, collection gaps and process restart may omit phases. Delivery history has separate retention."}
	if err := q.Validate(); err != nil {
		return page, err
	}
	rows, err := db.QueryContext(ctx, `SELECT `+timelineColumns+timelineJoins+` WHERE e.observed_at>=? AND e.observed_at<?
 AND (?='' OR e.incident_id=?) AND (?='' OR e.kind=?)
 AND (?='' OR e.observed_at>? OR (e.observed_at=? AND ? AND e.id>?)) ORDER BY e.observed_at,e.id LIMIT ?`,
		now.UnixMilli(), now.UnixMilli(), ceilUnixMilli(q.Start), ceilUnixMilli(q.End), q.IncidentID, q.IncidentID, q.Kind, q.Kind,
		q.AfterID, q.AfterTime.UnixMilli(), q.AfterTime.UnixMilli(), millisecondAligned(q.AfterTime), q.AfterID, q.Limit+1)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		e, err := scanTimeline(rows)
		if err != nil {
			return page, err
		}
		if len(page.Events) == q.Limit {
			page.More = true
			break
		}
		page.Events = append(page.Events, e)
	}
	if err := rows.Err(); err != nil {
		return page, err
	}
	if page.More {
		last := page.Events[len(page.Events)-1]
		page.NextAfterTime, page.NextAfterID = last.ObservedAt, last.ID
	}
	return page, nil
}

type IncidentSummary struct {
	ID              string    `json:"id"`
	Kind            string    `json:"kind"`
	FirstObservedAt time.Time `json:"first_observed_at_utc"`
	LastObservedAt  time.Time `json:"last_observed_at_utc"`
	Events          int64     `json:"retained_events"`
	HasStart        bool      `json:"has_retained_start"`
	HasRecovery     bool      `json:"has_retained_recovery"`
}

type IncidentView struct {
	Retention           *RetentionView  `json:"retention,omitempty"`
	Incident            IncidentSummary `json:"incident"`
	Timeline            TimelinePage    `json:"timeline"`
	RelatedSSH          []TimelineEvent `json:"related_ssh"`
	RelatedTruncated    bool            `json:"related_truncated"`
	SourceKeys          []string        `json:"context_source_keys"`
	SourceKeysTruncated bool            `json:"source_keys_truncated"`
	ContextStart        time.Time       `json:"context_start_utc"`
	ContextEnd          time.Time       `json:"context_end_utc"`
	Correlation         string          `json:"correlation"`
}

func scanIncident(row scanner) (IncidentSummary, error) {
	var value IncidentSummary
	var first, last int64
	err := row.Scan(&value.ID, &value.Kind, &first, &last, &value.Events, &value.HasStart, &value.HasRecovery)
	value.FirstObservedAt, value.LastObservedAt = time.UnixMilli(first).UTC(), time.UnixMilli(last).UTC()
	return value, err
}

const incidentColumns = `incident_id,MIN(kind),MIN(observed_at),MAX(observed_at),COUNT(*),MAX(phase='start'),MAX(phase='recovery')`

type IncidentPage struct {
	Incidents      []IncidentSummary `json:"incidents"`
	More           bool              `json:"more"`
	NextBeforeTime time.Time         `json:"next_before_utc,omitzero"`
	NextBeforeID   string            `json:"next_before_id,omitempty"`
}

func (s *Store) Incidents(ctx context.Context, start, end, before time.Time, beforeID string, count int) (IncidentPage, error) {
	page := IncidentPage{Incidents: []IncidentSummary{}}
	if err := (TimelineQuery{Start: start, End: end, AfterTime: before, AfterID: beforeID, Limit: count}).Validate(); err != nil {
		return page, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+incidentColumns+` FROM events WHERE incident_id<>'' AND observed_at>=? AND observed_at<? GROUP BY incident_id
 HAVING (?='' OR MAX(observed_at)<? OR (MAX(observed_at)=? AND ? AND incident_id<?)) ORDER BY MAX(observed_at) DESC,incident_id DESC LIMIT ?`, ceilUnixMilli(start), ceilUnixMilli(end), beforeID, ceilUnixMilli(before), before.UnixMilli(), millisecondAligned(before), beforeID, count+1)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		value, err := scanIncident(rows)
		if err != nil {
			return page, err
		}
		if len(page.Incidents) == count {
			page.More = true
			break
		}
		page.Incidents = append(page.Incidents, value)
	}
	if err := rows.Err(); err != nil {
		return page, err
	}
	if page.More {
		last := page.Incidents[len(page.Incidents)-1]
		page.NextBeforeTime, page.NextBeforeID = last.LastObservedAt, last.ID
	}
	return page, nil
}

func (s *Store) Incident(ctx context.Context, q TimelineQuery) (IncidentView, error) {
	if err := q.Validate(); err != nil {
		return IncidentView{}, err
	}
	tx, asOf, err := s.beginReadSnapshot(ctx)
	if err != nil {
		return IncidentView{}, err
	}
	defer tx.Rollback()
	now := asOf
	view, err := readIncident(ctx, tx, q, now)
	if err != nil {
		return view, err
	}
	retention, err := readRetention(ctx, tx, RetentionQuery{Start: q.Start, End: q.End, Limit: 20}, now)
	if err != nil {
		return view, err
	}
	view.Retention = &retention
	if err := tx.Commit(); err != nil {
		return view, err
	}
	return view, nil
}

func readIncident(ctx context.Context, db timelineReader, q TimelineQuery, now time.Time) (IncidentView, error) {
	view := IncidentView{RelatedSSH: []TimelineEvent{}, SourceKeys: []string{}, Correlation: "Stored source/time context only; shared prefixes or pseudonyms do not prove a common host, actor or causal relationship."}
	if err := q.Validate(); err != nil {
		return view, err
	}
	if q.IncidentID == "" {
		return view, errors.New("incident ID required")
	}
	var err error
	view.Incident, err = scanIncident(db.QueryRowContext(ctx, `SELECT `+incidentColumns+` FROM events WHERE incident_id=? AND observed_at>=? AND observed_at<? GROUP BY incident_id`, q.IncidentID, ceilUnixMilli(q.Start), ceilUnixMilli(q.End)))
	if err != nil {
		return view, err
	}
	view.Timeline, err = readTimeline(ctx, db, q, now)
	if err != nil {
		return view, err
	}
	rows, err := db.QueryContext(ctx, `SELECT DISTINCT source_range FROM events WHERE incident_id=? AND observed_at>=? AND observed_at<? AND source_range<>'' ORDER BY source_range LIMIT 9`, q.IncidentID, ceilUnixMilli(q.Start), ceilUnixMilli(q.End))
	if err != nil {
		return view, err
	}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			rows.Close()
			return view, err
		}
		if len(view.SourceKeys) == 8 {
			view.SourceKeysTruncated = true
			break
		}
		view.SourceKeys = append(view.SourceKeys, key)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return view, err
	}
	if err := rows.Close(); err != nil {
		return view, err
	}
	view.ContextStart = view.Incident.FirstObservedAt.Add(-5 * time.Minute)
	view.ContextEnd = view.Incident.LastObservedAt.Add(5 * time.Minute).Add(time.Millisecond)
	if view.ContextStart.Before(q.Start) {
		view.ContextStart = q.Start
	}
	if view.ContextEnd.After(q.End) {
		view.ContextEnd = q.End
	}
	if len(view.SourceKeys) > 0 {
		args := []any{now.UnixMilli(), now.UnixMilli(), ceilUnixMilli(view.ContextStart), ceilUnixMilli(view.ContextEnd), q.IncidentID}
		for _, key := range view.SourceKeys {
			args = append(args, key)
		}
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(view.SourceKeys)), ",")
		rows, err := db.QueryContext(ctx, `SELECT `+timelineColumns+timelineJoins+` WHERE e.observed_at>=? AND e.observed_at<? AND e.incident_id<>? AND e.kind IN ('ssh_login_success','ssh_brute_force') AND e.source_range IN (`+placeholders+`) ORDER BY e.observed_at,e.id LIMIT 101`, args...)
		if err != nil {
			return view, err
		}
		for rows.Next() {
			e, err := scanTimeline(rows)
			if err != nil {
				rows.Close()
				return view, err
			}
			if len(view.RelatedSSH) == 100 {
				view.RelatedTruncated = true
				break
			}
			view.RelatedSSH = append(view.RelatedSSH, e)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return view, err
		}
		if err := rows.Close(); err != nil {
			return view, err
		}
	}
	return view, nil
}
