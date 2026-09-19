// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/api"
	"github.com/littlesho/NodeRampart/internal/model"
)

func TestEvidenceSnapshotBoundsAndSharedAsOf(t *testing.T) {
	s := budgetStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	for i := range 101 {
		e := model.Event{ID: fmt.Sprintf("evidence_%03d", i), IncidentID: "incident_evidence", ObservedAt: now, Kind: "syn_flood", Phase: "update", Severity: model.SeverityHigh, Count: 1}
		if err := s.InsertEvent(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetComponentStatus(ctx, "sensor_feed", "running", now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordCoverageGap(ctx, CoverageGap{Name: "sensor_feed", Reason: "interface_selection_changed", Start: now, End: now}); err != nil {
		t.Fatal(err)
	}
	if err := s.CommitMonitorState(ctx, MonitorState{Key: "health_sensor", UpdatedAt: now, Data: json.RawMessage(`{"schema_version":1,"active":true}`)}, 0, nil, nil); err != nil {
		t.Fatal(err)
	}
	args := api.EvidenceArgs{Start: now.Add(-time.Hour), End: now.Add(time.Hour), IncidentID: "incident_evidence"}
	snapshot, err := s.Evidence(ctx, args)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Incident == nil || len(snapshot.Timeline.Events) != 100 || !snapshot.Timeline.More || snapshot.Incident.Incident.Events != 101 || len(snapshot.Monitors) != 1 || len(snapshot.Integrity.Gaps) != 1 || len(snapshot.Retention.Retained) != 11 {
		t.Fatal("snapshot omitted bounded sections or truncation evidence")
	}
	if !snapshot.AsOf.Equal(snapshot.Integrity.AsOf) || !snapshot.AsOf.Equal(snapshot.Retention.AsOf) {
		t.Fatal("sections used different observation times")
	}
	view, err := s.Incident(ctx, TimelineQuery{Start: args.Start, End: args.End, IncidentID: args.IncidentID, Limit: 1})
	if err != nil || view.Retention == nil {
		t.Fatal("incident did not gain retention summary", err)
	}
	args.IncidentID = ""
	snapshot, err = s.Evidence(ctx, args)
	if err != nil || snapshot.Incident != nil || len(snapshot.Timeline.Events) != 100 {
		t.Fatal("diagnostic snapshot incorrectly depended on incident", err)
	}
}

func TestEvidenceSnapshotAtomicReadWithWriter(t *testing.T) {
	s := budgetStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	now := time.Now().UTC().Truncate(time.Millisecond)
	e := model.Event{ID: "atomic_evidence", IncidentID: "atomic_incident", ObservedAt: now, Kind: "health_storage", Phase: "start", Severity: model.SeverityHigh, Count: 1}
	if err := s.InsertEvent(ctx, e); err != nil {
		t.Fatal(err)
	}
	if err := s.CommitMonitorState(ctx, MonitorState{Key: "health_storage", UpdatedAt: now, Data: json.RawMessage(`{"counter":1}`)}, 0, nil, nil); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		for n := 2; n <= 35; n++ {
			tx, err := s.db.BeginTx(ctx, nil)
			if err != nil {
				done <- err
				return
			}
			if _, err = tx.ExecContext(ctx, `UPDATE events SET count=? WHERE id='atomic_evidence'`, n); err == nil {
				_, err = tx.ExecContext(ctx, `UPDATE monitor_state SET revision=?,data=? WHERE key='health_storage'`, n, fmt.Sprintf(`{"counter":%d}`, n))
			}
			if err != nil {
				tx.Rollback()
				done <- err
				return
			}
			if err = tx.Commit(); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	for range 35 {
		snapshot, err := s.Evidence(ctx, api.EvidenceArgs{Start: now.Add(-time.Hour), End: now.Add(time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		var state struct {
			Counter uint64 `json:"counter"`
		}
		if len(snapshot.Monitors) != 1 || json.Unmarshal(snapshot.Monitors[0].Data, &state) != nil || len(snapshot.Timeline.Events) != 1 || snapshot.Timeline.Events[0].Count != state.Counter {
			t.Fatal("event and monitor came from different commits")
		}
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestEvidenceRejectsInvalidQueriesAndCancelledRead(t *testing.T) {
	s := budgetStore(t)
	now := time.Now().UTC()
	base := api.EvidenceArgs{Start: now.Add(-time.Hour), End: now}
	for _, args := range []api.EvidenceArgs{{Start: now, End: now}, {Start: now.Add(-9 * 24 * time.Hour), End: now}, {Start: base.Start, End: base.End, IncidentID: "invalid/id"}} {
		if _, err := s.Evidence(context.Background(), args); err == nil {
			t.Fatal("invalid query accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Evidence(ctx, base); err == nil {
		t.Fatal("cancelled read succeeded")
	}
	if _, err := s.Evidence(context.Background(), base); err != nil {
		t.Fatal("cancelled transaction leaked connection", err)
	}
}

func TestEvidenceAsOfFollowsConnectionWaitAndVisibleCommit(t *testing.T) {
	s := budgetStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	blocker, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback()
	type result struct {
		snapshot EvidenceSnapshot
		err      error
	}
	done := make(chan result, 1)
	waits := s.db.Stats().WaitCount
	initial := time.Now().UTC()
	go func() {
		v, e := s.Evidence(ctx, api.EvidenceArgs{Start: initial.Add(-time.Hour), End: initial.Add(time.Hour)})
		done <- result{v, e}
	}()
	for s.db.Stats().WaitCount == waits {
		select {
		case <-ctx.Done():
			t.Fatal("reader never waited")
		case <-time.After(time.Millisecond):
		}
	}
	// Ensure the committed millisecond lies strictly after the original request.
	select {
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	case <-time.After(5 * time.Millisecond):
	}
	at := time.Now().UTC().Truncate(time.Millisecond)
	event := model.Event{ID: "after_evidence_wait", ObservedAt: at, Kind: "health_sensor", Phase: "start", Count: 1}
	if err := insertEvent(ctx, blocker, event); err != nil {
		t.Fatal(err)
	}
	if _, err := blocker.ExecContext(ctx, `INSERT INTO monitor_state(key,revision,updated_at,data) VALUES ('health_sensor',1,?,'{}')`, at.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if err := blocker.Commit(); err != nil {
		t.Fatal(err)
	}
	got := <-done
	if got.err != nil {
		t.Fatal(got.err)
	}
	if len(got.snapshot.Timeline.Events) != 1 || len(got.snapshot.Monitors) != 1 || got.snapshot.AsOf.Before(at) || !got.snapshot.AsOf.Equal(got.snapshot.Integrity.AsOf) || !got.snapshot.AsOf.Equal(got.snapshot.Retention.AsOf) {
		t.Fatal("snapshot label predates its visible commit", got.snapshot)
	}
}

func TestReadSnapshotPinsDatabaseBeforeReturningAsOf(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "snapshot.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	// WAL permits the independent writer to commit while this reader remains
	// open. A deferred transaction without the initial read would see it.
	s.db.SetMaxOpenConns(2)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	tx, asOf, err := s.beginReadSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if asOf.IsZero() {
		t.Fatal("missing snapshot observation time")
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO monitor_state(key,revision,updated_at,data) VALUES ('health_sensor',1,?,'{}')`, time.Now().UTC().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	states, err := readMonitorStates(ctx, tx)
	if err != nil || len(states) != 0 {
		t.Fatal("snapshot acquired only after its AsOf was returned", states, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	states, err = s.MonitorStates(ctx)
	if err != nil || len(states) != 1 {
		t.Fatal("independent writer did not commit", states, err)
	}
}
