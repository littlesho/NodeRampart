// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/model"
)

func TestTimelineStableCursorAndBodyFreeDeliveryHistory(t *testing.T) {
	s := budgetStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	for i := range 3 {
		e, m := alertFixture(fmt.Sprintf("evt_%03d", i), "inc_timeline", "update")
		e.ObservedAt = now
		e.SourceRange = "192.0.2.0/24"
		e.Evidence = map[string]string{"synthetic_private": strings.Repeat("x", 60<<10)}
		if i == 0 {
			if err := s.InsertEvent(ctx, e); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Enqueue(ctx, *m); err != nil {
				t.Fatal(err)
			}
		} else if err := s.InsertEventNotification(ctx, e, m); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.MarkSent(ctx, "msg_evt_000", now); err != nil {
		t.Fatal(err)
	}
	q := TimelineQuery{Start: now.Add(-time.Hour), End: now.Add(time.Hour), Limit: 2}
	page, err := s.Timeline(ctx, q)
	if err != nil || len(page.Events) != 2 || !page.More || page.NextAfterID != "evt_001" || !page.NextAfterTime.Equal(now) {
		t.Fatalf("bad first page: %+v %v", page, err)
	}
	if page.Events[0].Delivery.Decision != "legacy" || page.Events[0].Delivery.State != "sent" {
		t.Fatal("legacy delivery evidence was fabricated or lost")
	}
	encoded, err := json.Marshal(page)
	if err != nil || strings.Contains(string(encoded), "synthetic_private") || strings.Contains(string(encoded), "<b>synthetic</b>") {
		t.Fatal("timeline copied detail or notification body")
	}
	q.AfterTime, q.AfterID = page.NextAfterTime, page.NextAfterID
	page, err = s.Timeline(ctx, q)
	if err != nil || len(page.Events) != 1 || page.More || page.Events[0].ID != "evt_002" {
		t.Fatalf("equal-time page skipped/repeated records: %+v %v", page, err)
	}
	if _, err := s.db.Exec(`DELETE FROM notification_outbox WHERE id='msg_evt_002'`); err != nil {
		t.Fatal(err)
	}
	page, err = s.Timeline(ctx, q)
	if err != nil || page.Events[0].Delivery.State != "history_unavailable" || page.Events[0].Delivery.Decision != "queued" {
		t.Fatal("pruned notification was reported delivered", err)
	}
}

func TestIncidentContextDoesNotInventMembershipOrRecovery(t *testing.T) {
	s := budgetStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	for i, phase := range []string{"start", "update", "recovery"} {
		e, m := alertFixture(fmt.Sprintf("phase_%d", i), "inc_main", phase)
		e.ObservedAt = now.Add(time.Duration(i) * time.Minute)
		e.SourceRange = "192.0.2.0/24"
		if err := s.InsertEventNotification(ctx, e, m); err != nil {
			t.Fatal(err)
		}
	}
	for i, source := range []string{"192.0.2.0/24", "198.51.100.0/24"} {
		e := model.Event{ID: fmt.Sprintf("ssh_%d", i), IncidentID: fmt.Sprintf("inc_ssh_%d", i), ObservedAt: now, Kind: "ssh_login_success", Phase: "observed", SourceRange: source}
		if err := s.InsertEvent(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	q := TimelineQuery{Start: now.Add(-time.Hour), End: now.Add(time.Hour), IncidentID: "inc_main", Limit: 2}
	view, err := s.Incident(ctx, q)
	if err != nil || view.Incident.Events != 3 || !view.Incident.HasStart || !view.Incident.HasRecovery || !view.Timeline.More || len(view.RelatedSSH) != 1 || view.RelatedSSH[0].ID != "ssh_0" {
		t.Fatalf("incident view failed: %+v %v", view, err)
	}
	if !strings.Contains(view.Correlation, "do not prove") || len(view.SourceKeys) != 1 {
		t.Fatal("context lacks its uncertainty and scope")
	}
	if _, err := s.db.Exec(`DELETE FROM events WHERE id IN ('phase_0','phase_2')`); err != nil {
		t.Fatal(err)
	}
	view, err = s.Incident(ctx, q)
	if err != nil || view.Incident.HasStart || view.Incident.HasRecovery || view.Incident.Events != 1 {
		t.Fatal("missing phases were inferred", err)
	}
	var links int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM event_notifications`).Scan(&links); err != nil || links != 1 {
		t.Fatal("event retention orphaned decision rows", links, err)
	}
	list, err := s.Incidents(ctx, q.Start, q.End, time.Time{}, "", 1)
	if err != nil || len(list.Incidents) != 1 || !list.More || list.Incidents[0].ID != "inc_main" {
		t.Fatal("incident list ordering/cursor failed", err)
	}
	second, err := s.Incidents(ctx, q.Start, q.End, list.NextBeforeTime, list.NextBeforeID, 10)
	if err != nil || len(second.Incidents) != 2 {
		t.Fatal("incident continuation skipped same-time incidents", err)
	}
}

func TestTimelineRejectsUnboundedAndBrokenCursors(t *testing.T) {
	s := budgetStore(t)
	now := time.Now().UTC()
	for _, q := range []TimelineQuery{
		{Start: now, End: now.Add(9 * 24 * time.Hour), Limit: 20},
		{Start: now, End: now.Add(time.Hour), Limit: 101},
		{Start: now, End: now.Add(time.Hour), Limit: 20, AfterID: "evt"},
		{Start: now, End: now.Add(time.Hour), Limit: 20, AfterTime: now},
		{Start: now, End: now.Add(time.Hour), Limit: 20, AfterTime: now.Add(time.Hour), AfterID: "evt"},
		{Start: now, End: now.Add(time.Hour), Limit: 20, IncidentID: "../private"},
	} {
		if _, err := s.Timeline(context.Background(), q); err == nil {
			t.Fatal("invalid timeline query accepted")
		}
	}
}

func TestTimelineAlertProjectionIsBoundedAndUsesRecordedFields(t *testing.T) {
	s := retentionStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	fields := map[string]string{"period": "2026-09", "observed_bytes": "9007199254740993", "threshold_bytes": "9007199254740992", "milestone": "100", "coverage": "incomplete", "basis": model.LegacyAlertBasis, "synthetic_private_token": "must_not_copy", "hostname": "must_not_copy"}
	for _, id := range []string{"known", "missing", "oversized", "malformed", "ordinary"} {
		kind := "budget_month_bytes"
		if id == "ordinary" {
			kind = "syn_flood"
		}
		event := model.Event{ID: id, Kind: kind, ObservedAt: now, Count: 1, Evidence: fields}
		if id == "missing" {
			event.Evidence = nil
		}
		if err := s.InsertEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`UPDATE events SET evidence_json=? WHERE id='oversized'`, strings.Repeat("x", 65537)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE events SET evidence_json='{"observed_bytes":"12","bad":{}}' WHERE id='malformed'`); err != nil {
		t.Fatal(err)
	}
	page, err := s.Timeline(ctx, TimelineQuery{Start: now.Add(-time.Second), End: now.Add(time.Second), Limit: 100})
	if err != nil || len(page.Events) != 5 {
		t.Fatal(page, err)
	}
	for _, event := range page.Events {
		if event.ID == "ordinary" {
			if event.Alert != nil {
				t.Fatal("ordinary event acquired alert fields")
			}
			continue
		}
		if event.Alert == nil || event.Alert.Validate(event.Kind) != nil {
			t.Fatal("invalid typed context", event.ID, event.Alert)
		}
		if event.ID == "known" {
			if event.Alert.ObservedBytes == nil || *event.Alert.ObservedBytes != 9007199254740993 || event.Alert.Availability != "partial" {
				t.Fatal("recorded integer or missing-period qualification lost", event.Alert)
			}
		} else if event.Alert.Availability != "unavailable" {
			t.Fatal("invalid legacy evidence claimed available", event.ID, event.Alert)
		}
	}
	encoded, err := json.Marshal(page)
	if err != nil || strings.Contains(string(encoded), "must_not_copy") || strings.Contains(string(encoded), "synthetic_private_token") || strings.Contains(string(encoded), "hostname") {
		t.Fatal("raw evidence leaked to timeline")
	}
}
