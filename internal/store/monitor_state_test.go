// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/model"
)

func monitorFixture(id string, at time.Time) (MonitorState, model.Event, *OutboxMessage) {
	e, m := alertFixture(id, "inc_budget", "start")
	e.Kind = "budget_month_bytes"
	e.ObservedAt = at
	return MonitorState{Key: e.Kind, UpdatedAt: at, Data: json.RawMessage(`{"version":1,"milestone":80}`)}, e, m
}

func TestMonitorCASAtomicConcurrentAndRestart(t *testing.T) {
	s := budgetStore(t)
	ctx := context.Background()
	at := time.Now().UTC().Truncate(time.Millisecond)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, id := range []string{"first_monitor", "second_monitor"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			next, e, m := monitorFixture(id, at)
			next.Revision = 999
			results <- s.CommitMonitorState(ctx, next, 0, []model.Event{e}, []*OutboxMessage{m})
		}(id)
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, ErrMonitorConflict) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("success=%d conflicts=%d", success, conflict)
	}
	states, err := s.MonitorStates(ctx)
	if err != nil || len(states) != 1 || states[0].Revision != 1 || !states[0].UpdatedAt.Equal(at) {
		t.Fatalf("states %+v err=%v", states, err)
	}
	for _, table := range []string{"events", "event_notifications", "notification_outbox"} {
		var count int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil || count != 1 {
			t.Fatalf("%s count %d err=%v", table, count, err)
		}
	}
	path := s.path
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	states, err = reopened.MonitorStates(ctx)
	if err != nil || len(states) != 1 || states[0].Revision != 1 {
		t.Fatal(states, err)
	}
	if err := reopened.CommitMonitorState(ctx, states[0], 0, nil, nil); !errors.Is(err, ErrMonitorConflict) {
		t.Fatal("restart replay accepted", err)
	}
}

func TestMonitorRollbackAndSilence(t *testing.T) {
	s := budgetStore(t)
	ctx := context.Background()
	at := time.Now().UTC().Truncate(time.Millisecond)
	next, e, m := monitorFixture("failed_monitor", at)
	if _, err := s.db.Exec(`CREATE TRIGGER fail_monitor_event BEFORE INSERT ON events BEGIN SELECT RAISE(ABORT,'synthetic'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.CommitMonitorState(ctx, next, 0, []model.Event{e}, []*OutboxMessage{m}); err == nil {
		t.Fatal("failure swallowed")
	}
	states, err := s.MonitorStates(ctx)
	if err != nil || len(states) != 0 {
		t.Fatal("state survived failed event", states, err)
	}
	if _, err := s.db.Exec(`DROP TRIGGER fail_monitor_event`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddSilence(ctx, Silence{Kind: e.Kind, ExpiresAt: at.Add(time.Hour)}, at); err != nil {
		t.Fatal(err)
	}
	if err := s.CommitMonitorState(ctx, next, 0, []model.Event{e}, []*OutboxMessage{m}); err != nil {
		t.Fatal(err)
	}
	var decision string
	if err := s.db.QueryRow(`SELECT decision FROM event_notifications WHERE event_id=?`, e.ID).Scan(&decision); err != nil || decision != "silenced" {
		t.Fatal(decision, err)
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM notification_outbox`).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
}

func TestMonitorDelayedAdmissionUsesCurrentSilenceAndMergePolicy(t *testing.T) {
	for _, cleaned := range []bool{false, true} {
		t.Run(fmt.Sprintf("expired_silence_cleaned_%t", cleaned), func(t *testing.T) {
			s := budgetStore(t)
			ctx := context.Background()
			observed := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Millisecond)
			next, event, message := monitorFixture("delayed_admission", observed)
			event.Phase = "update"
			if _, err := s.AddSilence(ctx, Silence{Kind: event.Kind, ExpiresAt: observed.Add(time.Hour)}, observed); err != nil {
				t.Fatal(err)
			}
			if err := s.ConfigureNotifications(time.Minute); err != nil {
				t.Fatal(err)
			}
			space := s.budget.freeSpace
			s.budget.freeSpace = func(string) (uint64, error) { return 0, nil }
			if err := s.CommitMonitorState(ctx, next, 0, []model.Event{event}, []*OutboxMessage{message}); !errors.Is(err, ErrStorageBudget) {
				t.Fatal("expected retryable admission failure", err)
			}
			s.budget.freeSpace = space
			if cleaned {
				now := time.Now().UTC()
				if _, err := s.AddSilence(ctx, Silence{Kind: "unrelated", ExpiresAt: now.Add(time.Hour)}, now); err != nil {
					t.Fatal(err)
				}
			}
			admitted := time.Now().UTC().Truncate(time.Millisecond)
			if err := s.CommitMonitorState(ctx, next, 0, []model.Event{event}, []*OutboxMessage{message}); err != nil {
				t.Fatal(err)
			}
			var decision string
			var recorded, mergeUntil int64
			if err := s.db.QueryRow(`SELECT d.decision,d.recorded_at,o.merge_until FROM event_notifications d JOIN notification_outbox o ON o.id=d.notification_id WHERE d.event_id=?`, event.ID).Scan(&decision, &recorded, &mergeUntil); err != nil {
				t.Fatal(err)
			}
			if decision != "queued" || recorded < admitted.UnixMilli() || mergeUntil < admitted.Add(time.Minute).UnixMilli() {
				t.Fatal("retry used stale notification policy time", decision, recorded, mergeUntil)
			}
			stored, err := s.Event(ctx, event.ID)
			if err != nil || !stored.ObservedAt.Equal(observed) {
				t.Fatal("admission rewrote the frozen observation", stored, err)
			}
			states, err := s.MonitorStates(ctx)
			if err != nil || len(states) != 1 || states[0].Revision != 1 || !states[0].UpdatedAt.Equal(observed) {
				t.Fatal("admission rewrote monitor identity", states, err)
			}
		})
	}
}

func TestMonitorBoundsAndQueueRejectionRetainState(t *testing.T) {
	s := budgetStore(t)
	ctx := context.Background()
	at := time.Now().UTC()
	next, e, m := monitorFixture("quota_monitor", at)
	for _, change := range []func(*MonitorState){func(n *MonitorState) { n.Key = "arbitrary" }, func(n *MonitorState) { n.Data = json.RawMessage(`{`) }, func(n *MonitorState) { n.Data = json.RawMessage(`"` + strings.Repeat("x", 16384) + `"`) }, func(n *MonitorState) { n.UpdatedAt = time.Time{} }} {
		copy := next
		change(&copy)
		if err := s.CommitMonitorState(ctx, copy, 0, nil, nil); err == nil {
			t.Fatal("invalid state accepted")
		}
	}
	if err := s.CommitMonitorState(ctx, next, math.MaxInt64, nil, nil); err == nil {
		t.Fatal("revision wrapped")
	}
	if err := s.CommitMonitorState(ctx, next, 0, []model.Event{e}, nil); err == nil {
		t.Fatal("mismatched decisions accepted")
	}
	if _, err := s.db.Exec(`WITH RECURSIVE n(v) AS (SELECT 1 UNION ALL SELECT v+1 FROM n WHERE v<10000) INSERT INTO notification_outbox(id,dedupe_key,destination,body,created_at,next_attempt,expires_at) SELECT 'full_'||v,'full_'||v,'telegram','x',?,?,? FROM n`, at.UnixMilli(), at.UnixMilli(), at.Add(time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if err := s.CommitMonitorState(ctx, next, 0, []model.Event{e}, []*OutboxMessage{m}); err != nil {
		t.Fatal(err)
	}
	var decision string
	if err := s.db.QueryRow(`SELECT decision FROM event_notifications WHERE event_id=?`, e.ID).Scan(&decision); err != nil || decision != "rejected" {
		t.Fatal(decision, err)
	}
	states, err := s.MonitorStates(ctx)
	if err != nil || len(states) != 1 || states[0].Revision != 1 {
		t.Fatal(states, err)
	}
}

func TestMonitorTwoEventTransactionRollsBackFirstNotification(t *testing.T) {
	s := retentionStore(t)
	ctx := context.Background()
	at := time.Now().UTC()
	next, first, firstMessage := monitorFixture("atomic_first", at)
	_, second, secondMessage := monitorFixture("atomic_second", at)
	second.Phase = "recovery"
	if _, err := s.db.Exec(`CREATE TRIGGER refuse_second BEFORE INSERT ON events WHEN NEW.id='atomic_second' BEGIN SELECT RAISE(ABORT,'synthetic second event failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.CommitMonitorState(ctx, next, 0, []model.Event{first, second}, []*OutboxMessage{firstMessage, secondMessage}); err == nil {
		t.Fatal("second event error swallowed")
	}
	for _, table := range []string{"monitor_state", "events", "event_notifications", "notification_outbox"} {
		var count int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil || count != 0 {
			t.Fatal("partial monitoring transaction", table, count, err)
		}
	}
	if _, err := s.db.Exec(`DROP TRIGGER refuse_second`); err != nil {
		t.Fatal(err)
	}
	if err := s.CommitMonitorState(ctx, next, 0, []model.Event{first, second}, []*OutboxMessage{firstMessage, secondMessage}); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM event_notifications`).Scan(&count); err != nil || count != 2 {
		t.Fatal(count, err)
	}
}

func TestMonitorAndRetentionSurviveBackupRestore(t *testing.T) {
	s := budgetStore(t)
	ctx := context.Background()
	at := time.Now().UTC().Truncate(time.Millisecond)
	next, event, message := monitorFixture("archived_monitor", at)
	event.ObservedAt = at.Add(-8 * 24 * time.Hour)
	if err := s.CommitMonitorState(ctx, next, 0, []model.Event{event}, []*OutboxMessage{message}); err != nil {
		t.Fatal(err)
	}
	if err := s.Prune(ctx, at); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "snapshot.db")
	if _, err := s.Backup(ctx, backup); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "restored.db")
	if _, err := RestoreBackup(ctx, backup, target); err != nil {
		t.Fatal(err)
	}
	restored, err := OpenWithBudget(target, BudgetConfig{MaxBytes: 64 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	states, err := restored.MonitorStates(ctx)
	if err != nil || len(states) != 1 || states[0].Revision != 1 || string(states[0].Data) != string(next.Data) {
		t.Fatal(states, err)
	}
	if retentionCount(t, restored, "events", "time_expiry") != 1 {
		t.Fatal("backup lost retention accounting")
	}
	states[0].UpdatedAt = at.Add(time.Second)
	if err := restored.CommitMonitorState(ctx, states[0], 1, nil, nil); err != nil {
		t.Fatal("restored CAS cannot write", err)
	}
}
