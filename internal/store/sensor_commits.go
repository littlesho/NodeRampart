// SPDX-License-Identifier: MIT

package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/littlesho/NodeRampart/internal/model"
	"github.com/littlesho/NodeRampart/internal/protocol"
)

const MaxSensorWatermarks = 64

type SensorWatermark struct {
	SessionID              string                   `json:"session_id"`
	Interface              string                   `json:"interface"`
	Sequence               uint64                   `json:"committed_sequence"`
	SentAt                 time.Time                `json:"observation_end_utc"`
	CommittedAt            time.Time                `json:"committed_at_utc"`
	EventsCommitted        bool                     `json:"events_committed"`
	NotificationsCommitted bool                     `json:"notification_decisions_committed"`
	Complete               bool                     `json:"complete"`
	Reason                 string                   `json:"reason"`
	SequenceGaps           uint64                   `json:"sequence_gaps"`
	Duplicates             uint64                   `json:"duplicates"`
	Health                 protocol.CollectorHealth `json:"-"`
}

func readSensorWatermark(ctx context.Context, db interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, session, name string) (SensorWatermark, bool, error) {
	var w SensorWatermark
	var sent, committed int64
	var health string
	err := db.QueryRowContext(ctx, `SELECT session_id,interface,sequence,sent_at_us,committed_at,events_complete,notifications_complete,complete,reason,sequence_gaps,duplicates,health_json FROM sensor_watermarks WHERE session_id=? AND interface=?`, session, name).Scan(&w.SessionID, &w.Interface, &w.Sequence, &sent, &committed, &w.EventsCommitted, &w.NotificationsCommitted, &w.Complete, &w.Reason, &w.SequenceGaps, &w.Duplicates, &health)
	if errors.Is(err, sql.ErrNoRows) {
		return w, false, nil
	}
	if err != nil {
		return w, false, err
	}
	w.SentAt = time.UnixMicro(sent).UTC()
	w.CommittedAt = time.UnixMilli(committed).UTC()
	if err := decodeSensorHealth(&w, health); err != nil {
		return w, false, err
	}
	return w, true, nil
}

func (s *Store) SensorWatermark(ctx context.Context, session, name string) (SensorWatermark, bool, error) {
	return readSensorWatermark(ctx, s.db, session, name)
}

func (s *Store) SensorWatermarks(ctx context.Context) ([]SensorWatermark, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT session_id,interface,sequence,sent_at_us,committed_at,events_complete,notifications_complete,complete,reason,sequence_gaps,duplicates,health_json FROM sensor_watermarks ORDER BY committed_at DESC,session_id,interface LIMIT 65`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]SensorWatermark, 0)
	for rows.Next() {
		var w SensorWatermark
		var sent, committed int64
		var health string
		if err := rows.Scan(&w.SessionID, &w.Interface, &w.Sequence, &sent, &committed, &w.EventsCommitted, &w.NotificationsCommitted, &w.Complete, &w.Reason, &w.SequenceGaps, &w.Duplicates, &health); err != nil {
			return nil, err
		}
		w.SentAt = time.UnixMicro(sent).UTC()
		w.CommittedAt = time.UnixMilli(committed).UTC()
		if err := decodeSensorHealth(&w, health); err != nil {
			return nil, err
		}
		result = append(result, w)
	}
	if len(result) > MaxSensorWatermarks {
		return nil, errors.New("sensor watermark capacity invalid")
	}
	return result, rows.Err()
}

func decodeSensorHealth(w *SensorWatermark, value string) error {
	if len(value) > 4096 || !protocol.ValidSessionID(w.SessionID) || !protocol.ValidInterfaceName(w.Interface) || w.Sequence == 0 || w.Sequence > 1<<63-1 || w.SentAt.Year() < 1970 || w.SentAt.Year() > 9999 || w.CommittedAt.Year() < 1970 || w.CommittedAt.Year() > 9999 {
		return errors.New("sensor watermark invalid")
	}
	decoder := json.NewDecoder(bytes.NewBufferString(value))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&w.Health); err != nil {
		return errors.New("sensor health snapshot invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errors.New("sensor health snapshot has trailing data")
	}
	if w.Complete != (w.Reason == "committed") || w.Complete && (!w.EventsCommitted || !w.NotificationsCommitted) {
		return errors.New("sensor watermark completion invalid")
	}
	switch w.Reason {
	case "committed", "derived_events_pending", "derived_events_rejected", "notification_rejected":
	default:
		return errors.New("sensor watermark reason invalid")
	}
	_, err := w.Health.Delta(protocol.CollectorHealth{})
	return err
}

func verifySensorCommitState(ctx context.Context, db *sql.DB) error {
	var retired int64
	if err := db.QueryRowContext(ctx, `SELECT retired_before_us FROM sensor_commit_state WHERE id=1`).Scan(&retired); err != nil || retired < 0 {
		return errors.New("sensor commit state unavailable")
	}
	_, err := (&Store{db: db}).SensorWatermarks(ctx)
	return err
}

func sensorACK(b protocol.Batch, w SensorWatermark, duplicate bool) protocol.CommitACK {
	ack := protocol.CommitACK{ProtocolVersion: protocol.Version, SessionID: b.SessionID, Interface: b.Interface, Sequence: b.Sequence, CommittedSequence: w.Sequence, HealthCommitted: true, TrafficCommitted: true, EventsCommitted: w.EventsCommitted, NotificationsCommitted: w.NotificationsCommitted, Complete: w.Complete, Duplicate: duplicate, Reason: w.Reason}
	if duplicate && b.Sequence < w.Sequence {
		ack.Complete = false
		ack.EventsCommitted = false
		ack.NotificationsCommitted = false
		ack.Reason = "duplicate_history_unknown"
	}
	return ack
}

// CommitSensorBatch commits collector health, traffic aggregation and the
// corresponding interface/session watermark together. The supplied derived
// result describes separately completed event/outbox decisions, not delivery.
func (s *Store) CommitSensorBatch(ctx context.Context, b protocol.Batch, traffic []model.Traffic, events, notifications, complete bool, reason string) (protocol.CommitACK, error) {
	ack := protocol.CommitACK{ProtocolVersion: protocol.Version, SessionID: b.SessionID, Interface: b.Interface, Sequence: b.Sequence, Reason: "storage_failed"}
	if b.ProtocolVersion < 5 || !protocol.ValidSessionID(b.SessionID) || b.Sequence == 0 || b.Sequence > 1<<63-1 || !protocol.ValidInterfaceName(b.Interface) || b.SentAt.IsZero() {
		return ack, errors.New("invalid sensor commit identity")
	}
	if err := b.ValidateAt(b.SentAt); err != nil {
		return ack, err
	}
	if complete != (reason == "committed") || complete && (!events || !notifications) {
		return ack, errors.New("inconsistent sensor completion")
	}
	switch reason {
	case "committed", "derived_events_pending", "derived_events_rejected", "notification_rejected":
	default:
		return ack, errors.New("invalid sensor commit reason")
	}
	release, err := s.beginWrite(ctx, writeTraffic)
	if err != nil {
		return ack, err
	}
	defer release()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ack, err
	}
	defer tx.Rollback()
	w, exists, err := readSensorWatermark(ctx, tx, b.SessionID, b.Interface)
	if err != nil {
		return ack, err
	}
	if exists && b.Sequence <= w.Sequence {
		if _, err := tx.ExecContext(ctx, `UPDATE sensor_watermarks SET duplicates=MIN(9223372036854775807,duplicates+1) WHERE session_id=? AND interface=?`, b.SessionID, b.Interface); err != nil {
			return ack, err
		}
		if err := tx.Commit(); err != nil {
			return ack, err
		}
		return sensorACK(b, w, true), nil
	}
	ack.CommittedSequence = w.Sequence
	if len(traffic) != len(b.Flows) {
		return ack, errors.New("sensor traffic dataset is incomplete")
	}
	var previousEnd, retired int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sent_at_us),0) FROM sensor_watermarks WHERE interface=?`, b.Interface).Scan(&previousEnd); err != nil {
		return ack, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT retired_before_us FROM sensor_commit_state WHERE id=1`).Scan(&retired); err != nil {
		return ack, err
	}
	if (!exists && b.SentAt.UnixMicro() <= retired) || b.SentAt.UnixMicro() <= previousEnd {
		ack.Reason = "retired_or_nonmonotonic_session"
		return ack, errors.New("sensor session history is unavailable or nonmonotonic")
	}
	gapCount := b.Sequence - 1
	if exists {
		gapCount = b.Sequence - w.Sequence - 1
	}
	windowStart := b.SentAt.Add(-time.Duration(b.IntervalMillis) * time.Millisecond)
	gapStart := windowStart
	if previousEnd > 0 {
		gapStart = time.UnixMicro(previousEnd).UTC()
	}
	if gapCount > 0 || windowStart.Sub(gapStart) > time.Millisecond {
		if windowStart.Before(gapStart) {
			gapStart = windowStart
		}
		gapReason := "sensor_observation_gap"
		if gapCount > 0 {
			gapReason = "sensor_sequence_gap"
		}
		if err := insertSensorGap(ctx, tx, gapReason, gapStart, windowStart, gapCount); err != nil {
			return ack, err
		}
	}
	currentHealth := b.CollectorHealth()
	delta, err := currentHealth.Delta(w.Health)
	if err != nil {
		return ack, err
	}
	healthBatch := b
	delta.Apply(&healthBatch)
	if err := recordBatchHealth(ctx, tx, healthBatch); err != nil {
		return ack, err
	}
	if err := addTrafficBatch(ctx, tx, traffic); err != nil {
		return ack, err
	}
	if !complete {
		if err := insertSensorGap(ctx, tx, "sensor_derived_events_partial", windowStart, b.SentAt, 0); err != nil {
			return ack, err
		}
	}
	if w.SequenceGaps > uint64(1<<63-1)-gapCount {
		return ack, errors.New("sensor gap counter exhausted")
	}
	w = SensorWatermark{SessionID: b.SessionID, Interface: b.Interface, Sequence: b.Sequence, SentAt: b.SentAt, CommittedAt: time.Now().UTC(), EventsCommitted: events, NotificationsCommitted: notifications, Complete: complete, Reason: reason, SequenceGaps: w.SequenceGaps + gapCount, Duplicates: w.Duplicates, Health: currentHealth}
	healthJSON, err := json.Marshal(w.Health)
	if err != nil {
		return ack, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO sensor_watermarks(session_id,interface,sequence,sent_at_us,committed_at,events_complete,notifications_complete,complete,reason,sequence_gaps,duplicates,health_json) VALUES(?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(session_id,interface) DO UPDATE SET sequence=excluded.sequence,sent_at_us=excluded.sent_at_us,committed_at=excluded.committed_at,events_complete=excluded.events_complete,notifications_complete=excluded.notifications_complete,complete=excluded.complete,reason=excluded.reason,sequence_gaps=excluded.sequence_gaps,health_json=excluded.health_json`, w.SessionID, w.Interface, w.Sequence, w.SentAt.UnixMicro(), w.CommittedAt.UnixMilli(), w.EventsCommitted, w.NotificationsCommitted, w.Complete, w.Reason, w.SequenceGaps, w.Duplicates, string(healthJSON)); err != nil {
		return ack, err
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sensor_watermarks`).Scan(&count); err != nil {
		return ack, err
	}
	if count > MaxSensorWatermarks {
		var oldestSession, oldestInterface string
		var cutoff int64
		if err := tx.QueryRowContext(ctx, `SELECT session_id,interface,sent_at_us FROM sensor_watermarks ORDER BY committed_at,rowid LIMIT 1`).Scan(&oldestSession, &oldestInterface, &cutoff); err != nil {
			return ack, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE sensor_commit_state SET retired_before_us=MAX(retired_before_us,?) WHERE id=1`, cutoff); err != nil {
			return ack, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM sensor_watermarks WHERE session_id=? AND interface=?`, oldestSession, oldestInterface); err != nil {
			return ack, err
		}
		if err := insertSensorGap(ctx, tx, "sensor_watermark_retired", w.CommittedAt, w.CommittedAt, 0); err != nil {
			return ack, err
		}
	}
	if err := tx.Commit(); err != nil {
		return ack, err
	}
	return sensorACK(b, w, false), nil
}

func insertSensorGap(ctx context.Context, tx *sql.Tx, reason string, start, end time.Time, count uint64) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO coverage_gaps(name,reason,started_at,ended_at,count) VALUES('sensor_feed',?,?,?,?)`, reason, start.UnixMilli(), end.UnixMilli(), count); err != nil {
		return err
	}
	_, err := pruneRows(ctx, tx, "coverage_gaps", "capacity_eviction", `SELECT rowid FROM coverage_gaps WHERE id<=(SELECT MAX(id)-1000 FROM coverage_gaps)`, nil, time.Now().UTC())
	return err
}

// SensorEventOutcome checks retained immutable events and their notification
// decisions. A rejected admission is durable evidence but not complete output.
func (s *Store) SensorEventOutcome(ctx context.Context, ids []string) (events, decisions, admitted bool, err error) {
	if len(ids) > 1024 {
		return false, false, false, errors.New("sensor event outcome exceeds bound")
	}
	events, decisions, admitted = true, true, true
	for _, id := range ids {
		var e, d, rejected bool
		err = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM events WHERE id=?),EXISTS(SELECT 1 FROM event_notifications WHERE event_id=?),EXISTS(SELECT 1 FROM event_notifications WHERE event_id=? AND decision='rejected')`, id, id, id).Scan(&e, &d, &rejected)
		if err != nil {
			return false, false, false, err
		}
		events = events && e
		decisions = decisions && d
		admitted = admitted && !rejected
	}
	return events, decisions, admitted, nil
}
