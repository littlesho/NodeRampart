// SPDX-License-Identifier: MIT

package manage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/replay"
)

func thresholdFixture(t *testing.T) (string, config.Config, config.Config) {
	t.Helper()
	dir := t.TempDir()
	input := filepath.Join(dir, "metadata.jsonl")
	metadata := `{"format":"noderampart-replay","version":1,"anonymized":true,"interface_limit":1}` + "\n"
	for i, kind := range []string{"failure", "failure", "success"} {
		metadata += fmt.Sprintf(`{"type":"auth","auth":{"observed_at_utc":"2000-01-01T00:00:0%dZ","kind":"%s","source_ip":"198.18.0.1","user":"user1","method":"publickey"}}`, i, kind) + "\n"
	}
	if err := os.WriteFile(input, []byte(metadata), 0o600); err != nil {
		t.Fatal(err)
	}
	current := config.Defaults()
	current.Sensor.Enabled = false
	current.Auth.Threshold = 5
	current.Paths.Database = filepath.Join(dir, "must-not-access-active.db")
	if err := os.WriteFile(current.Paths.Database, []byte("active database sentinel"), 0o600); err != nil {
		t.Fatal(err)
	}
	draft := current
	draft.Auth.Threshold = 2
	return input, current, draft
}

func savedThresholdPreview(t *testing.T) (string, ThresholdPreview) {
	t.Helper()
	input, current, draft := thresholdFixture(t)
	preview, err := PreviewThresholds(context.Background(), input, current, draft)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(preview)
	path := filepath.Join(filepath.Dir(input), "preview.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path, preview
}

func TestThresholdDraftPreviewUsesOnlyOfflineInputAndProductionRules(t *testing.T) {
	input, current, draft := thresholdFixture(t)
	baseline, _ := json.Marshal(current)
	candidate, _ := json.Marshal(draft)
	m := &Manager{Request: func(context.Context, string, any) (json.RawMessage, error) {
		t.Fatal("preview contacted daemon")
		return nil, nil
	}, runner: func(context.Context, string, ...string) (string, error) {
		t.Fatal("preview executed external process")
		return "", nil
	}}
	result, err := m.Action(context.Background(), "threshold_preview_draft", map[string]string{"input": input, "current_json": string(baseline), "draft_json": string(candidate)})
	if err != nil {
		t.Fatal(err)
	}
	var preview ThresholdPreview
	if err := json.Unmarshal([]byte(result), &preview); err != nil {
		t.Fatal(err)
	}
	if preview.Comparison.Baseline.TotalEvents != 1 || preview.Comparison.Candidate.TotalEvents != 2 || preview.Comparison.DeltaByKindPhase["ssh_brute_force/start"] != 1 {
		t.Fatalf("rules were not compared: %+v", preview.Comparison)
	}
	if preview.CurrentConfigSHA256 == preview.DraftConfigSHA256 || len(preview.Limitations) == 0 || !strings.Contains(result, "Fewer alerts do not") {
		t.Fatal("draft identity or limitations missing")
	}
	sentinel, _ := os.ReadFile(current.Paths.Database)
	if string(sentinel) != "active database sentinel" {
		t.Fatal("preview read or changed active database")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := PreviewThresholds(ctx, input, current, draft); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation ignored: %v", err)
	}
	draft.Auth.Threshold = 0
	if _, err := PreviewThresholds(context.Background(), input, current, draft); err == nil {
		t.Fatal("invalid candidate rules accepted")
	}
}

func TestFeedbackStoresOnlyScopedIdentitiesAndLabels(t *testing.T) {
	comparison, preview := savedThresholdPreview(t)
	path := filepath.Join(filepath.Dir(comparison), "feedback.json")
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	result, err := recordThresholdFeedback(context.Background(), comparison, path, "candidate", "evt_1", "reasonable", now)
	if err != nil || result.Retained != 1 || result.Replaced {
		t.Fatalf("feedback=%+v %v", result, err)
	}
	data, _ := os.ReadFile(path)
	if bytes.Contains(data, []byte("198.18.0.1")) || bytes.Contains(data, []byte("user1")) || bytes.Contains(data, []byte("source")) || bytes.Contains(data, []byte("detection")) {
		t.Fatal("feedback retained private replay content")
	}
	var saved feedbackFile
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Records[0].InputSHA256 != preview.Comparison.InputSHA256 || saved.Records[0].RulesSHA256 != replay.RulesFingerprint(preview.Comparison.Candidate.Rules) {
		t.Fatal("event was not scoped to input and rules")
	}
	result, err = recordThresholdFeedback(context.Background(), comparison, path, "candidate", "evt_1", "false_positive", now.Add(time.Minute))
	if err != nil || result.Retained != 1 || !result.Replaced {
		t.Fatalf("update=%+v %v", result, err)
	}
	result, err = recordThresholdFeedback(context.Background(), comparison, path, "baseline", "evt_1", "uncertain", now.Add(2*time.Minute))
	if err != nil || result.Retained != 2 || result.Replaced {
		t.Fatalf("side isolation=%+v %v", result, err)
	}
	preview.Comparison.Candidate.Rules.Auth.Threshold++
	changed, _ := json.Marshal(preview)
	if err := os.WriteFile(comparison, changed, 0o600); err != nil {
		t.Fatal(err)
	}
	result, err = recordThresholdFeedback(context.Background(), comparison, path, "candidate", "evt_1", "reasonable", now.Add(3*time.Minute))
	if err != nil || result.Retained != 3 || result.Replaced {
		t.Fatalf("rule isolation=%+v %v", result, err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Fatal("feedback permissions widened")
	}
	for _, bad := range []struct{ side, id, label string }{{"candidate", "evt_999", "reasonable"}, {"candidate", "evt_1", "train"}, {"other", "evt_1", "uncertain"}, {"candidate", "../../secret", "reasonable"}} {
		if _, err := recordThresholdFeedback(context.Background(), comparison, path, bad.side, bad.id, bad.label, now.Add(3*time.Minute)); err == nil {
			t.Fatal("unretained ID or unsafe label accepted")
		}
	}
}

func TestFeedbackRetentionCapsAndFutureFormatAreExplicit(t *testing.T) {
	comparison, preview := savedThresholdPreview(t)
	path := filepath.Join(filepath.Dir(comparison), "feedback.json")
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	state := feedbackFile{Version: 1}
	for i := 0; i < maxFeedbackRecords; i++ {
		state.Records = append(state.Records, ThresholdFeedback{InputSHA256: preview.Comparison.InputSHA256, RulesSHA256: replay.RulesFingerprint(preview.Comparison.Candidate.Rules), Side: "candidate", EventID: fmt.Sprintf("evt_%d", i+2), Label: "uncertain", RecordedAt: now.Add(-time.Hour)})
	}
	data, _ := json.Marshal(state)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := recordThresholdFeedback(context.Background(), comparison, path, "candidate", "evt_1", "reasonable", now)
	if err != nil || result.Retained != 1000 || result.Evicted != 1 {
		t.Fatalf("capacity=%+v %v", result, err)
	}
	result, err = recordThresholdFeedback(context.Background(), comparison, path, "candidate", "evt_1", "uncertain", now.Add(91*24*time.Hour))
	if err != nil || result.Retained != 1 || result.Evicted != 1000 {
		t.Fatalf("retention=%+v %v", result, err)
	}
	data = []byte(`{"version":999,"records":[]}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := recordThresholdFeedback(context.Background(), comparison, path, "candidate", "evt_1", "reasonable", now); err == nil {
		t.Fatal("future feedback format rewritten")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(data, after) {
		t.Fatal("invalid feedback state was overwritten")
	}
}

func TestFeedbackRefusesUnsafeFilesAndCancelledPublication(t *testing.T) {
	comparison, _ := savedThresholdPreview(t)
	dir := filepath.Dir(comparison)
	victim := filepath.Join(dir, "victim")
	if err := os.WriteFile(victim, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "feedback.json")
	if err := os.Symlink(victim, path); err != nil {
		t.Fatal(err)
	}
	if _, err := RecordThresholdFeedback(context.Background(), comparison, path, "candidate", "evt_1", "reasonable"); err == nil {
		t.Fatal("symlink feedback accepted")
	}
	data, _ := os.ReadFile(victim)
	if string(data) != "preserve" {
		t.Fatal("unsafe target was overwritten")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"version":1,"records":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := RecordThresholdFeedback(context.Background(), comparison, path, "candidate", "evt_1", "reasonable"); err == nil {
		t.Fatal("unprotected feedback accepted")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := RecordThresholdFeedback(ctx, comparison, path, "candidate", "evt_1", "reasonable"); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation ignored")
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("cancelled feedback published")
	}
}
