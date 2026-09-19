// SPDX-License-Identifier: MIT

package console

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/littlesho/NodeRampart/internal/billing"
	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/rivo/tview"
)

func TestCancelNewTierPreservesValidDraft(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		p := billing.Profile{SchemaVersion: 1, Name: "fixture", Provider: "custom", SourceRegion: "fixture", Currency: "USD", EffectiveDate: "2026-09-12", SourceURL: "https://example.com/pricing", InternetEgress: []billing.Tier{{PricePerGB: .1}}}
		original := append([]billing.Tier(nil), p.InternetEgress...)
		u := &ui{app: tview.NewApplication(), lang: "en"}
		u.profileTiers(&p)
		list := u.app.GetFocus().(*tview.List)
		list.SetCurrentItem(1)
		list.InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), func(tview.Primitive) {})
		if !cancel {
			u.back()
		} else {
			clicked := false
			for range 8 {
				focus := u.app.GetFocus()
				if button, ok := focus.(*tview.Button); ok && button.GetLabel() == "Cancel" {
					button.InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), func(p tview.Primitive) { u.app.SetFocus(p) })
					clicked = true
					break
				}
				focus.InputHandler()(tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone), func(p tview.Primitive) { u.app.SetFocus(p) })
			}
			if !clicked {
				t.Fatal("Cancel not reachable")
			}
		}
		if !reflect.DeepEqual(p.InternetEgress, original) || p.Validate() != nil {
			t.Fatal("cancel changed valid tariff draft")
		}
	}
}

func TestConfigDiffUsesIndependentBaselineAndEffectiveChanges(t *testing.T) {
	c := config.Defaults()
	c.Hostname = "fixture"
	c.Sensor.Interfaces = []string{"lab4"}
	before := cloneConfig(c)
	c.Sensor.Interfaces[0] = "lab6"
	c.Hostname = "fixture" // edit then restore does not appear
	diff := configDiff(before, c)
	if strings.Contains(diff, "hostname") || !strings.Contains(diff, "lab4") || !strings.Contains(diff, "lab6") {
		t.Fatal("incorrect effective diff", diff)
	}
}

func TestBrowserCursorKeepsRangeAndDoesNotMutateResponse(t *testing.T) {
	now := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct{ id, raw, cursor, value string }{
		{"report_list", `{"next_before":"2026-09-10"}`, "before", "2026-09-10"},
		{"notify_list", `{"next_before":"message"}`, "before", "message"},
		{"incident_list", `{"more":true,"next_before_utc":"2026-09-11T00:00:00Z","next_before_id":"inc"}`, "before_id", "inc"},
		{"timeline", `{"more":true,"next_after_utc":"2026-09-11T00:00:00Z","next_after_id":"event"}`, "after_id", "event"},
		{"incident_show", `{"timeline":{"more":true,"next_after_utc":"2026-09-11T00:00:00Z","next_after_id":"event"}}`, "after_id", "event"},
		{"health", `{"history":{"more":true,"next_offset":100}}`, "offset", "100"},
		{"retention", `{"more":true,"next_before_id":123}`, "before_id", "123"},
	} {
		args := browserArguments(tc.id, map[string]string{}, now)
		m, ok := decodeBrowser(tc.raw)
		if !ok {
			t.Fatal("bad fixture")
		}
		before := len(m)
		next := browserNext(tc.id, args, m)
		if next[tc.cursor] != tc.value || next["since"] != args["since"] || next["until"] != args["until"] || args[tc.cursor] != "" || len(m) != before {
			t.Fatal("cursor lost range or mutated page", tc.id)
		}
		if browserNext(tc.id, next, m) != nil {
			t.Fatal("nonprogressing cursor accepted")
		}
	}
	if _, ok := decodeBrowser(strings.Repeat(" ", maxOutputBytes+1)); ok {
		t.Fatal("unbounded page accepted")
	}
}

func nextCall(t *testing.T, b *fakeBackend) backendCall {
	t.Helper()
	select {
	case c := <-b.calls:
		return c
	case <-time.After(time.Second):
		t.Fatal("no backend call")
		return backendCall{}
	}
}

func TestSimulationIncidentSelectionAndPagesKeepOriginalPeriod(t *testing.T) {
	b := newBackend()
	b.action = func(_ context.Context, id string, args map[string]string) (string, error) {
		switch id {
		case "incident_list":
			if args["before_id"] != "" {
				return `{"incidents":[{"id":"older","kind":"udp"}],"more":false}`, nil
			}
			return `{"incidents":[{"id":"fixture_incident","kind":"syn"}],"more":true,"next_before_utc":"2026-09-11T00:00:00Z","next_before_id":"fixture_incident"}`, nil
		case "incident_show":
			return `{"incident":{"id":"fixture_incident"},"timeline":{"events":[{"id":"start_event","summary":"synthetic start"}],"more":false}}`, nil
		}
		return "unexpected", nil
	}
	s, _, _ := launch(t, false, b)
	awaitFrame(t, s, "Main menu")
	selectIndex(s, 4)
	awaitFrame(t, s, "Events and incidents")
	selectIndex(s, 0)
	awaitFrame(t, s, "Before cursor ID")
	for range 5 {
		key(s, tcell.KeyTab)
	}
	key(s, tcell.KeyEnter)
	awaitFrame(t, s, "fixture_incident")
	first := nextCall(t, b)
	if first.args["since"] == "" || first.args["until"] == "" {
		t.Fatal("default range not frozen")
	}
	selectIndex(s, 0)
	awaitFrame(t, s, "start_event")
	detail := nextCall(t, b)
	if detail.id != "incident_show" || detail.args["id"] != "fixture_incident" || detail.args["since"] != first.args["since"] || detail.args["until"] != first.args["until"] {
		t.Fatal("selection lost ID or period")
	}
	key(s, tcell.KeyEscape)
	awaitFrame(t, s, "fixture_incident")
	selectIndex(s, 2)
	awaitFrame(t, s, "older")
	next := nextCall(t, b)
	if next.args["before_id"] != "fixture_incident" || next.args["since"] != first.args["since"] || next.args["until"] != first.args["until"] {
		t.Fatal("next page moved range")
	}
	selectIndex(s, 2)
	awaitFrame(t, s, "fixture_incident")
	previous := nextCall(t, b)
	if !reflect.DeepEqual(previous.args, first.args) {
		t.Fatal("previous cursor not preserved")
	}
}

func TestSimulationReportSelectionPassesDate(t *testing.T) {
	b := newBackend()
	b.action = func(_ context.Context, id string, _ map[string]string) (string, error) {
		if id == "report_list" {
			return `{"reports":[{"date":"2026-09-10","title":"Daily fixture"}]}`, nil
		}
		return `{"date":"2026-09-10","body":"Archived synthetic report"}`, nil
	}
	s, _, _ := launch(t, false, b)
	awaitFrame(t, s, "Main menu")
	selectIndex(s, 3)
	awaitFrame(t, s, "Saved daily reports")
	selectIndex(s, 1)
	awaitFrame(t, s, "Earlier than date")
	key(s, tcell.KeyTab)
	key(s, tcell.KeyTab)
	key(s, tcell.KeyEnter)
	awaitFrame(t, s, "Daily fixture")
	nextCall(t, b)
	selectIndex(s, 0)
	awaitFrame(t, s, "Archived synthetic report")
	c := nextCall(t, b)
	if c.id != "report_show" || c.args["date"] != "2026-09-10" {
		t.Fatal("report selection lost date")
	}
}

func TestSimulationAWSChoosesRegionWithoutTyping(t *testing.T) {
	b := newBackend()
	b.action = func(_ context.Context, id string, _ map[string]string) (string, error) {
		if id == "prices_regions" {
			return `{"aws":["us-east-1","eu-west-1"]}`, nil
		}
		return "Synthetic tariff applied", nil
	}
	s, _, _ := launch(t, false, b)
	awaitFrame(t, s, "Main menu")
	selectIndex(s, 7)
	awaitFrame(t, s, "Cloud egress estimates")
	selectIndex(s, 1)
	awaitFrame(t, s, "Choose a provider")
	selectIndex(s, 0)
	awaitFrame(t, s, "us-east-1")
	if nextCall(t, b).id != "prices_regions" {
		t.Fatal("region catalog not requested")
	}
	key(s, tcell.KeyTab)
	key(s, tcell.KeyEnter)
	key(s, tcell.KeyDown)
	key(s, tcell.KeyEnter)
	for range 3 {
		key(s, tcell.KeyTab)
	}
	key(s, tcell.KeyEnter)
	awaitFrame(t, s, "Confirm action")
	key(s, tcell.KeyRight)
	key(s, tcell.KeyEnter)
	awaitFrame(t, s, "Synthetic tariff applied")
	c := nextCall(t, b)
	if c.id != "prices_fetch" || c.args["region"] != "eu-west-1" || c.args["provider"] != "aws" {
		t.Fatal("selected region not applied")
	}
}

func TestEditingQueryRestartsPaginationAndKeepsFilters(t *testing.T) {
	for _, id := range []string{"report_list", "notify_list", "incident_list", "incident_show", "timeline", "health", "retention"} {
		a, _ := findAction(id)
		args := map[string]string{"since": "2026-09-01T00:00:00Z", "until": "2026-09-12T00:00:00Z", "id": "fixture", "limit": "10", "before": "2026-09-05T00:00:00Z", "before_id": "old", "after": "2026-09-06T00:00:00Z", "after_id": "old", "offset": "100"}
		edited := browserQueryAction(a, args)
		for _, p := range edited.params {
			switch p.key {
			case "before", "before_id", "after", "after_id":
				if p.value != "" {
					t.Fatal("new query kept stale cursor", id)
				}
			case "offset":
				if p.value != "0" {
					t.Fatal("new health query skips segments")
				}
			default:
				if p.value != args[p.key] {
					t.Fatal("new query lost filters", id, p.key)
				}
			}
		}
	}
}
