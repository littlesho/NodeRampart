// SPDX-License-Identifier: MIT

package console

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/littlesho/NodeRampart/internal/config"
)

func TestSimulationDraftPreviewKeepsExistingReviewAndConfirmation(t *testing.T) {
	b := newBackend()
	b.cfg.Hostname = "fixture"
	b.action = func(_ context.Context, id string, args map[string]string) (string, error) {
		if id != "threshold_preview_draft" || args["input"] != "/private/offline.jsonl" {
			t.Errorf("unexpected offline action %s %v", id, args)
		}
		var current, draft config.Config
		if json.Unmarshal([]byte(args["current_json"]), &current) != nil || json.Unmarshal([]byte(args["draft_json"]), &draft) != nil || current.Hostname != "fixture" || draft.Hostname != "new-host" {
			t.Error("preview lost draft or baseline")
		}
		return `{"comparison":{"baseline":{"total_events":2},"candidate":{"total_events":1}}}`, nil
	}
	s, _, _ := launch(t, false, b)
	awaitFrame(t, s, "Main menu")
	selectIndex(s, 2)
	awaitFrame(t, s, "Configuration · schema")
	selectIndex(s, 0)
	awaitFrame(t, s, "Host identity")
	selectIndex(s, 0)
	awaitFrame(t, s, "Edit setting")
	for range len("fixture") {
		key(s, tcell.KeyBackspace2)
	}
	textKeys(s, "new-host")
	key(s, tcell.KeyTab)
	key(s, tcell.KeyEnter)
	awaitFrame(t, s, "Host identity")
	key(s, tcell.KeyEscape)
	awaitFrame(t, s, "unsaved edits")
	selectIndex(s, len(groups)+1)
	awaitFrame(t, s, "Preview draft using local offline metadata")
	textKeys(s, "/private/offline.jsonl")
	key(s, tcell.KeyTab)
	key(s, tcell.KeyEnter)
	frame := awaitFrame(t, s, "Fewer alerts do not prove fewer false positives")
	if !strings.Contains(frame, "Review and save draft") {
		t.Fatal("preview did not offer existing safe save path")
	}
	select {
	case call := <-b.calls:
		if call.id != "threshold_preview_draft" {
			t.Fatal("preview did not use offline action")
		}
	case <-time.After(time.Second):
		t.Fatal("preview missing")
	}
	select {
	case <-b.saves:
		t.Fatal("preview saved configuration without confirmation")
	default:
	}
	key(s, tcell.KeyTab)
	key(s, tcell.KeyEnter)
	awaitFrame(t, s, "Review configuration changes")
	select {
	case <-b.saves:
		t.Fatal("review saved without confirmation")
	default:
	}
	key(s, tcell.KeyTab)
	key(s, tcell.KeyEnter)
	awaitFrame(t, s, "Confirm action")
	key(s, tcell.KeyRight)
	key(s, tcell.KeyEnter)
	awaitFrame(t, s, "Configuration saved")
	select {
	case snapshot := <-b.saves:
		if snapshot.Config.Hostname != "new-host" || snapshot.Fingerprint != "synthetic-fingerprint" {
			t.Fatal("safe save lost draft identity")
		}
	case <-time.After(time.Second):
		t.Fatal("confirmed draft was not saved")
	}
}
