// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func channelTimelineFixture(t *testing.T) (*Store, time.Time, string, string) {
	t.Helper()
	s := budgetStore(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	telegram, webhook := "telegram:"+strings.Repeat("a", 64), "webhook:"+strings.Repeat("b", 64)
	for channel, target := range map[string]string{"telegram": telegram, "webhook": webhook} {
		if err := s.ConfigureNotificationTarget(context.Background(), channel, target, "prefix", true, now); err != nil {
			t.Fatal(err)
		}
	}
	return s, now, telegram, webhook
}

func insertDualTimelineEvent(t *testing.T, s *Store, id, incident, kind string, now time.Time, telegram, webhook string) (OutboxMessage, OutboxMessage) {
	t.Helper()
	event, message := alertFixture(id, incident, "update")
	event.ObservedAt = now
	event.Kind = kind
	event.SourceRange = "192.0.2.0/24"
	message.Channel, message.Destination, message.PrivacyMode = "telegram", telegram, "prefix"
	second := *message
	second.ID = "web_" + id
	second.DedupeKey = "event:" + id + ":webhook"
	second.Channel, second.Destination = "webhook", webhook
	message.Secondary = &second
	if err := s.InsertEventNotification(context.Background(), event, message); err != nil {
		t.Fatal(err)
	}
	return *message, second
}

func TestTimelineDualChannelsDoNotDuplicateEventsAndPreserveCursor(t *testing.T) {
	s, now, telegram, webhook := channelTimelineFixture(t)
	for _, id := range []string{"dual_001", "dual_002"} {
		insertDualTimelineEvent(t, s, id, "inc_dual", "syn_flood", now, telegram, webhook)
	}
	var oldRows int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM events e LEFT JOIN event_notifications d ON d.event_id=e.id`).Scan(&oldRows); err != nil || oldRows != 4 {
		t.Fatalf("original unfiltered join reproduction: count=%d err=%v", oldRows, err)
	}
	q := TimelineQuery{Start: now.Add(-time.Hour), End: now.Add(time.Hour), Limit: 1}
	page, err := s.Timeline(context.Background(), q)
	if err != nil || len(page.Events) != 1 || !page.More || page.NextAfterID != "dual_001" {
		t.Fatalf("first page=%+v %v", page, err)
	}
	first := page.Events[0]
	if first.Delivery.Channel != "telegram" || len(first.Deliveries) != 2 || first.Deliveries[0].Channel != "telegram" || first.Deliveries[1].Channel != "webhook" || first.Delivery != first.Deliveries[0] {
		t.Fatalf("channel precedence=%+v", first)
	}
	q.AfterID, q.AfterTime = page.NextAfterID, page.NextAfterTime
	page, err = s.Timeline(context.Background(), q)
	if err != nil || len(page.Events) != 1 || page.More || page.Events[0].ID != "dual_002" || len(page.Events[0].Deliveries) != 2 {
		t.Fatalf("continuation duplicated/skipped event=%+v %v", page, err)
	}
	encoded, _ := json.Marshal(page)
	if strings.Contains(string(encoded), "synthetic</b>") || strings.Contains(string(encoded), telegram) || strings.Contains(string(encoded), webhook) {
		t.Fatal("channel timeline leaked notification body or target")
	}
}

func TestTimelineChannelsKeepIsolationDiscardAndDisabledStatesTruthful(t *testing.T) {
	s, now, telegram, webhook := channelTimelineFixture(t)
	primary, _ := insertDualTimelineEvent(t, s, "dual", "inc_dual", "syn_flood", now, telegram, webhook)
	if err := s.MarkSent(context.Background(), primary.ID, now); err != nil {
		t.Fatal(err)
	}
	changed := "webhook:" + strings.Repeat("c", 64)
	if err := s.ConfigureNotificationTarget(context.Background(), "webhook", changed, "prefix", true, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	q := TimelineQuery{Start: now.Add(-time.Hour), End: now.Add(time.Hour), Limit: 10}
	page, err := s.Timeline(context.Background(), q)
	if err != nil || len(page.Events) != 1 || page.Events[0].Delivery.State != "sent" || len(page.Events[0].Deliveries) != 2 || page.Events[0].Deliveries[1].State != "isolated" {
		t.Fatalf("isolation hidden=%+v %v", page, err)
	}
	if count, err := s.DiscardIsolatedNotifications(context.Background(), "webhook", now.Add(2*time.Second)); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	page, err = s.Timeline(context.Background(), q)
	if err != nil || page.Events[0].Deliveries[1].State != "discarded" || page.Events[0].Delivery.State != "sent" {
		t.Fatalf("discard was confused with silence=%+v %v", page, err)
	}
	event, message := alertFixture("hook_only", "inc_hook", "update")
	event.ObservedAt = now
	message.Channel, message.Destination, message.PrivacyMode = "webhook", changed, "prefix"
	if err := s.InsertEventNotification(context.Background(), event, message); err != nil {
		t.Fatal(err)
	}
	if err := s.ConfigureNotificationTarget(context.Background(), "webhook", changed, "prefix", false, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	q.IncidentID = "inc_hook"
	page, err = s.Timeline(context.Background(), q)
	if err != nil || len(page.Events) != 1 || len(page.Events[0].Deliveries) != 1 || page.Events[0].Delivery.Channel != "webhook" || page.Events[0].Delivery.State != "paused" {
		t.Fatalf("webhook fallback or pause incorrect=%+v %v", page, err)
	}
	if _, err := s.db.Exec(`DELETE FROM notification_outbox WHERE id=?`, message.ID); err != nil {
		t.Fatal(err)
	}
	page, err = s.Timeline(context.Background(), q)
	if err != nil || page.Events[0].Delivery.State != "history_unavailable" || page.Events[0].Deliveries[0].State != "history_unavailable" {
		t.Fatal("missing notification history became delivered", page, err)
	}
}

type associationFaultReader struct {
	db           *sql.DB
	t            *testing.T
	associations int
	failAt       int
	fault        error
}

func (r *associationFaultReader) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if strings.HasPrefix(query, "SELECT e.id,d.channel,") {
		r.associations++
		// Direct database reads use one connection. Even a limit+1 lookahead
		// must have closed the preceding rows before this association query.
		if r.db.Stats().InUse != 0 {
			r.t.Fatal("association query started before event rows were closed")
		}
		if r.associations == r.failAt {
			return nil, r.fault
		}
	}
	return r.db.QueryContext(ctx, query, args...)
}

func (r *associationFaultReader) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return r.db.QueryRowContext(ctx, query, args...)
}

func TestTimelineAssociationFailureClosesLookaheadAndReturnsError(t *testing.T) {
	s, now, telegram, webhook := channelTimelineFixture(t)
	for _, id := range []string{"dual_001", "dual_002"} {
		insertDualTimelineEvent(t, s, id, "inc_dual", "syn_flood", now, telegram, webhook)
	}
	fault := errors.New("synthetic association read failure")
	reader := &associationFaultReader{db: s.db, t: t, failAt: 1, fault: fault}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := readTimeline(ctx, reader, TimelineQuery{Start: now.Add(-time.Hour), End: now.Add(time.Hour), Limit: 1}, now)
	if !errors.Is(err, fault) || reader.associations != 1 || s.db.Stats().InUse != 0 {
		t.Fatalf("failure hidden or connection retained: %v %+v", err, s.db.Stats())
	}
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM events`).Scan(&count); err != nil || count != 2 {
		t.Fatal("failed timeline left connection unusable", count, err)
	}
}

func TestRelatedSSHDualChannelsStayUniqueAndCloseTruncatedRows(t *testing.T) {
	s, now, telegram, webhook := channelTimelineFixture(t)
	insertDualTimelineEvent(t, s, "main", "inc_main", "syn_flood", now, telegram, webhook)
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 101; index++ {
		id := fmt.Sprintf("ssh_%03d", index)
		if _, err := tx.Exec(`INSERT INTO events(id,incident_id,observed_at,kind,phase,severity,summary,source_range,count,evidence_json) VALUES(?, ?, ?, 'ssh_login_success', 'observed', 'info', '', '192.0.2.0/24', 1, '{}')`, id, "inc_"+id, now.UnixMilli()); err != nil {
			t.Fatal(err)
		}
		for _, channel := range []string{"telegram", "webhook"} {
			if _, err := tx.Exec(`INSERT INTO event_notifications(event_id,channel,decision,recorded_at) VALUES(?,?,'ineligible',?)`, id, channel, now.UnixMilli()); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	q := TimelineQuery{Start: now.Add(-time.Hour), End: now.Add(time.Hour), IncidentID: "inc_main", Limit: 1}
	reader := &associationFaultReader{db: s.db, t: t}
	view, err := readIncident(ctx, reader, q, now)
	if err != nil || len(view.RelatedSSH) != 100 || !view.RelatedTruncated || reader.associations != 2 {
		t.Fatalf("related context=%+v %v", view, err)
	}
	seen := map[string]bool{}
	for _, event := range view.RelatedSSH {
		if seen[event.ID] || len(event.Deliveries) != 2 {
			t.Fatal("related SSH event was duplicated or lost channel", event)
		}
		seen[event.ID] = true
	}
	reader = &associationFaultReader{db: s.db, t: t, failAt: 2, fault: errors.New("synthetic related association failure")}
	if _, err := readIncident(ctx, reader, q, now); !errors.Is(err, reader.fault) {
		t.Fatal("related association failure hidden", err)
	}
	if s.db.Stats().InUse != 0 {
		t.Fatal("failed related query held connection")
	}
	// The public path also reads one consistent SQLite snapshot.
	if view, err := s.Incident(ctx, q); err != nil || len(view.RelatedSSH) != 100 || !view.RelatedTruncated {
		t.Fatal("snapshot incident read failed", len(view.RelatedSSH), err)
	}
}
