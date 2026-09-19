// SPDX-License-Identifier: MIT

package console

import (
	"context"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

func TestIncidentExportPreservesSelectedContext(t *testing.T) {
	args := map[string]string{"id": "inc_fixture", "since": "2026-09-11T00:00:00Z", "until": "2026-09-12T00:00:00Z"}
	a := incidentEvidenceAction(args)
	for _, p := range a.params {
		switch p.key {
		case "incident":
			if p.value != args["id"] {
				t.Fatal("selected incident lost")
			}
		case "since", "until":
			if p.value != args[p.key] {
				t.Fatal("selected window lost")
			}
		}
	}
	base, _ := findAction("evidence_export")
	for _, p := range base.params {
		if p.key == "incident" && p.value != "" {
			t.Fatal("export leaked selection to next query")
		}
	}
}

func TestRetentionRejectsBrokenNextCursor(t *testing.T) {
	args := browserArguments("retention", map[string]string{"dataset": "events", "reason": "time_expiry"}, time.Now().UTC())
	for _, raw := range []string{`{"more":true,"next_before_id":-1}`, `{"more":true,"next_before_id":0}`, `{"more":true,"next_before_id":9223372036854775808}`, `{"more":false,"next_before_id":123}`} {
		m, ok := decodeBrowser(raw)
		if !ok || browserNext("retention", args, m) != nil {
			t.Fatal("bad cursor accepted")
		}
	}
	m, _ := decodeBrowser(`{"more":true,"next_before_id":123}`)
	next := browserNext("retention", args, m)
	if next["dataset"] != "events" || next["reason"] != "time_expiry" {
		t.Fatal("pagination lost filters")
	}
}

func TestSimulationSelectedIncidentOpensExportForm(t *testing.T) {
	b := newBackend()
	b.action = func(_ context.Context, id string, _ map[string]string) (string, error) {
		if id == "incident_list" {
			return `{"incidents":[{"id":"inc_export_fixture","kind":"syn_flood"}],"more":false}`, nil
		}
		return `{"incident":{"id":"inc_export_fixture"},"timeline":{"events":[],"more":false}}`, nil
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
	awaitFrame(t, s, "inc_export_fixture")
	nextCall(t, b)
	selectIndex(s, 0)
	awaitFrame(t, s, "Export this incident")
	nextCall(t, b)
	selectIndex(s, 0)
	awaitFrame(t, s, "inc_export_fixture")
	key(s, tcell.KeyEscape)
	awaitFrame(t, s, "Export this incident")
	select {
	case <-b.calls:
		t.Fatal("cancelled export called backend")
	default:
	}
}
