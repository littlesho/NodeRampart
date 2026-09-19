// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/api"
	"github.com/littlesho/NodeRampart/internal/model"
)

func TestFractionalMillisecondsAcrossEventReads(t *testing.T) {
	s := retentionStore(t)
	ctx := context.Background()
	at := time.Now().UTC().Truncate(time.Millisecond)
	for n := 0; n < 3; n++ {
		for _, kind := range []string{"syn_flood", "ssh_login_success"} {
			prefix, incident := "main", "inc_main"
			if kind == "ssh_login_success" {
				prefix, incident = "ssh", fmt.Sprintf("inc_ssh_%d", n)
			}
			e := model.Event{ID: fmt.Sprintf("%s_%d", prefix, n), IncidentID: incident, ObservedAt: at.Add(time.Duration(n) * time.Millisecond), Kind: kind, Phase: "update", SourceRange: "synthetic_source", Count: 1}
			if err := s.InsertEvent(ctx, e); err != nil {
				t.Fatal(err)
			}
		}
	}
	start, end := at.Add(500*time.Microsecond), at.Add(1500*time.Microsecond)
	q := TimelineQuery{Start: start, End: end, IncidentID: "inc_main", Limit: 100}
	page, err := s.Timeline(ctx, q)
	if err != nil || len(page.Events) != 1 || page.Events[0].ID != "main_1" {
		t.Fatal("fractional timeline range", page, err)
	}
	events, err := s.Events(ctx, EventQuery{Start: start, End: end, Kind: "syn_flood", Limit: 100})
	if err != nil || len(events) != 1 || events[0].ID != "main_1" {
		t.Fatal("fractional events range", events, err)
	}
	view, err := s.Incident(ctx, q)
	if err != nil || view.Incident.Events != 1 || view.Timeline.Events[0].ID != "main_1" || len(view.RelatedSSH) != 1 || view.RelatedSSH[0].ID != "ssh_1" {
		t.Fatal("fractional incident or context", view, err)
	}
	list, err := s.Incidents(ctx, start, end, time.Time{}, "", 100)
	if err != nil || len(list.Incidents) != 2 {
		t.Fatal("fractional incident list", list, err)
	}
	snapshot, err := s.Evidence(ctx, api.EvidenceArgs{Start: start, End: end})
	if err != nil || len(snapshot.Timeline.Events) != 2 {
		t.Fatal("fractional evidence", snapshot, err)
	}
	for _, e := range snapshot.Timeline.Events {
		if e.ObservedAt.Before(start) || !e.ObservedAt.Before(end) {
			t.Fatal("event outside declared bounds")
		}
	}
}

func TestFractionalCursorsOnlyUseIDAtAnExactMillisecond(t *testing.T) {
	s := retentionStore(t)
	ctx := context.Background()
	at := time.Now().UTC().Truncate(time.Millisecond)
	for _, id := range []string{"a", "z"} {
		e := model.Event{ID: id, IncidentID: "inc_" + id, ObservedAt: at, Kind: "syn_flood", Count: 1}
		if err := s.InsertEvent(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.InsertEvent(ctx, model.Event{ID: "next", IncidentID: "inc_next", ObservedAt: at.Add(time.Millisecond), Kind: "syn_flood", Count: 1}); err != nil {
		t.Fatal(err)
	}
	q := TimelineQuery{Start: at.Add(-time.Millisecond), End: at.Add(2 * time.Millisecond), AfterTime: at.Add(500 * time.Microsecond), AfterID: "a", Limit: 100}
	page, err := s.Timeline(ctx, q)
	if err != nil || len(page.Events) != 1 || page.Events[0].ID != "next" {
		t.Fatal("fractional ascending cursor reused ID tie", page, err)
	}
	q.AfterTime = at
	page, err = s.Timeline(ctx, q)
	if err != nil || len(page.Events) != 2 || page.Events[0].ID != "z" {
		t.Fatal("aligned ascending cursor lost ID tie", page, err)
	}
	for _, fractional := range []bool{true, false} {
		before := at
		if fractional {
			before = before.Add(500 * time.Microsecond)
		}
		list, err := s.Incidents(ctx, q.Start, q.End, before, "inc_m", 100)
		want := 1
		if fractional {
			want = 2
		}
		if err != nil || len(list.Incidents) != want {
			t.Fatal("descending incident cursor", fractional, list, err)
		}
		events, err := s.Events(ctx, EventQuery{Start: q.Start, End: before, BeforeID: "m", Limit: 100})
		if err != nil || len(events) != want {
			t.Fatal("descending event cursor", fractional, events, err)
		}
	}
}

func TestCeilMillisecondsIncludesPreEpochFractions(t *testing.T) {
	for _, ns := range []int64{-1500000, -1000000, -500000, 0, 500000, 1000000, 1500000} {
		at := time.Unix(0, ns)
		got := ceilUnixMilli(at)
		want := ns / int64(time.Millisecond)
		if ns > 0 && ns%int64(time.Millisecond) != 0 {
			want++
		}
		if got != want {
			t.Fatal(ns, got, want)
		}
	}
}
