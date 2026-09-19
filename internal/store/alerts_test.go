// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/model"
)

func alertFixture(id, incident, phase string) (model.Event, *OutboxMessage) {
	event := model.Event{ID: id, IncidentID: incident, Kind: "syn_flood", Phase: phase, Severity: model.SeverityHigh, ObservedAt: time.Now().UTC(), Summary: "synthetic observation"}
	return event, &OutboxMessage{ID: "msg_" + id, DedupeKey: "event:" + id + ":telegram", Destination: "telegram", Body: "<b>synthetic</b> " + id}
}

func TestAlertMergeRetainsEventsAndClaimsLatestBody(t *testing.T) {
	s := budgetStore(t)
	ctx := context.Background()
	if err := s.ConfigureNotifications(time.Hour); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"first", "second", "third", "first"} {
		e, m := alertFixture(id, "inc_a", "update")
		if err := s.InsertEventNotification(ctx, e, m); err != nil {
			t.Fatal(err)
		}
	}
	var events, decisions, messages, count int
	var body string
	for query, dest := range map[string]*int{`SELECT COUNT(*) FROM events`: &events, `SELECT COUNT(*) FROM event_notifications`: &decisions, `SELECT COUNT(*) FROM notification_outbox`: &messages} {
		if err := s.db.QueryRow(query).Scan(dest); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.db.QueryRow(`SELECT merged_count,body FROM notification_outbox`).Scan(&count, &body); err != nil {
		t.Fatal(err)
	}
	if events != 3 || decisions != 3 || messages != 1 || count != 3 || !strings.Contains(body, "third") || strings.Contains(body, "second") {
		t.Fatalf("coalescing lost history or replayed admission: %d %d %d %d %q", events, decisions, messages, count, body)
	}
	if pending, err := s.Pending(ctx, time.Now().Add(time.Minute), 20); err != nil || len(pending) != 0 {
		t.Fatalf("window delivered early: %d %v", len(pending), err)
	}
	// Recovery releases the held update without discarding it or merging the
	// recovery itself. A different incident keeps its own fixed window.
	other, otherMessage := alertFixture("other", "inc_b", "update")
	if err := s.InsertEventNotification(ctx, other, otherMessage); err != nil {
		t.Fatal(err)
	}
	recovery, recoveryMessage := alertFixture("recovery", "inc_a", "recovery")
	if err := s.InsertEventNotification(ctx, recovery, recoveryMessage); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Add(time.Second)
	pending, err := s.Pending(ctx, now, 20)
	if err != nil || len(pending) != 2 {
		t.Fatalf("recovery did not release exactly its update: %d %v", len(pending), err)
	}
	var claims atomic.Int32
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			message, ok, err := s.ClaimNotification(ctx, "msg_first", now)
			if err != nil {
				t.Error(err)
				return
			}
			if ok {
				claims.Add(1)
				if message.Body != body {
					t.Error("claim used a stale merged body")
				}
			}
		}()
	}
	wg.Wait()
	if claims.Load() != 1 {
		t.Fatal("concurrent workers claimed the same row")
	}
	if err := s.RetryNotification(ctx, "msg_first", now); err == nil {
		t.Fatal("manual retry revoked an active delivery lease")
	}
	if _, ok, err := s.ClaimNotification(ctx, "msg_first", now.Add(3*time.Minute)); err != nil || !ok {
		t.Fatalf("interrupted lease did not recover: %v %v", ok, err)
	}
}

func TestSilenceSuppressesQueuedAndNewEventsWithoutExpiryReplay(t *testing.T) {
	s := budgetStore(t)
	ctx := context.Background()
	for _, id := range []string{"claimed", "pending", "legacy"} {
		e, m := alertFixture(id, "inc_silence", "start")
		var err error
		if id == "legacy" {
			err = s.InsertEvent(ctx, e)
			if err == nil {
				_, err = s.Enqueue(ctx, *m)
			}
		} else {
			err = s.InsertEventNotification(ctx, e, m)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().Add(time.Millisecond)
	claimed, ok, err := s.ClaimNotification(ctx, "msg_claimed", now)
	if err != nil || !ok {
		t.Fatal("fixture claim failed", err)
	}
	if _, err := s.Enqueue(ctx, OutboxMessage{ID: "report", DedupeKey: "daily:synthetic", Destination: "telegram", Body: "synthetic report"}); err != nil {
		t.Fatal(err)
	}
	result, err := s.AddSilence(ctx, Silence{IncidentID: "inc_silence", ExpiresAt: now.Add(time.Hour)}, now)
	if err != nil || result.Suppressed != 3 || result.InFlight != 1 {
		t.Fatalf("silence missed legacy/pending/in-flight rows: %+v %v", result, err)
	}
	e, m := alertFixture("during", "inc_silence", "update")
	if err := s.InsertEventNotification(ctx, e, m); err != nil {
		t.Fatal(err)
	}
	var decision string
	if err := s.db.QueryRow(`SELECT decision FROM event_notifications WHERE event_id='during'`).Scan(&decision); err != nil || decision != "silenced" {
		t.Fatalf("silenced event not recorded: %q %v", decision, err)
	}
	if err := s.RetryNotification(ctx, "msg_pending", now); err == nil {
		t.Fatal("manual retry revived a suppressed message")
	}
	if err := s.MarkFailed(ctx, claimed.ID, now.Add(time.Minute), "synthetic failure"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(claimed.Body, "claimed") {
		t.Fatal("silence changed the in-flight immutable copy")
	}
	if err := s.RemoveSilence(ctx, result.Silence.ID, now); err != nil {
		t.Fatal(err)
	}
	e, m = alertFixture("after", "inc_silence", "update")
	if err := s.InsertEventNotification(ctx, e, m); err != nil {
		t.Fatal(err)
	}
	pending, err := s.Pending(ctx, now.Add(2*time.Hour), 20)
	if err != nil || len(pending) != 2 {
		t.Fatalf("revocation/expiry released a suppressed backlog: %d %v", len(pending), err)
	}
	for _, message := range pending {
		if message.ID != "report" && message.ID != "msg_after" {
			t.Fatalf("unexpected delivery %s", message.ID)
		}
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM events`).Scan(&count); err != nil || count != 5 {
		t.Fatal("silence removed immutable event history", count, err)
	}
}

func TestSilenceBoundsAndMatchingConjunction(t *testing.T) {
	s := budgetStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	for _, rule := range []Silence{{ExpiresAt: now.Add(time.Hour)}, {Kind: "syn_flood", ExpiresAt: now}, {Kind: "syn_flood", ExpiresAt: now.Add(8 * 24 * time.Hour)}, {Kind: "syn_flood", ExpiresAt: now.Add(time.Hour), Reason: "bad\nreason"}} {
		if _, err := s.AddSilence(ctx, rule, now); err == nil {
			t.Fatal("invalid silence accepted")
		}
	}
	for i := range MaxSilences {
		if _, err := s.AddSilence(ctx, Silence{IncidentID: fmt.Sprintf("inc_%d", i), Kind: "udp_flood", ExpiresAt: now.Add(time.Hour)}, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.AddSilence(ctx, Silence{Kind: "syn_flood", ExpiresAt: now.Add(time.Hour)}, now); err == nil {
		t.Fatal("silence cap bypassed")
	}
	e, m := alertFixture("conjunction", "inc_0", "start")
	if err := s.InsertEventNotification(ctx, e, m); err != nil {
		t.Fatal(err)
	}
	if pending, err := s.Pending(ctx, now.Add(time.Minute), 20); err != nil || len(pending) != 1 {
		t.Fatal("matching only one selector suppressed the event", err)
	}
	if _, err := s.AddSilence(ctx, Silence{Kind: "syn_flood", ExpiresAt: now.Add(3 * time.Hour)}, now.Add(2*time.Hour)); err != nil {
		t.Fatal("expired rules did not release bounded capacity", err)
	}
}

func TestEventDecisionRollsBackWithNotificationFailure(t *testing.T) {
	s := budgetStore(t)
	ctx := context.Background()
	e, m := alertFixture("bad", "inc_bad", "start")
	m.Body = strings.Repeat("x", 4097)
	if err := s.InsertEventNotification(ctx, e, m); err == nil {
		t.Fatal("oversized notification accepted")
	}
	if _, err := s.Event(ctx, e.ID); !IsNotFound(err) {
		t.Fatal("failed atomic write retained its event", err)
	}
	if err := s.ConfigureNotifications(time.Hour); err != nil {
		t.Fatal(err)
	}
	e, m = alertFixture("initial", "inc_overflow", "update")
	if err := s.InsertEventNotification(ctx, e, m); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE notification_outbox SET merged_count=9223372036854775807`); err != nil {
		t.Fatal(err)
	}
	e, m = alertFixture("overflow", "inc_overflow", "update")
	if err := s.InsertEventNotification(ctx, e, m); err == nil {
		t.Fatal("merged count wrapped")
	}
	if _, err := s.Event(ctx, e.ID); !IsNotFound(err) {
		t.Fatal("merge overflow committed a partial event", err)
	}
	if len(mergedBody(1<<63-1, strings.Repeat("x", 4096-mergeBodyReserve))) > 4096 {
		t.Fatal("count header exceeds reserved body limit")
	}
}
