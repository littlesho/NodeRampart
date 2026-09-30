// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/store"
)

func TestUpgradeCLIHasReadOnlyUnknownAndIsolatedRestore(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Defaults()
	cfg.Storage.MinFreeBytes = 0
	path := filepath.Join(dir, "config.json")
	data, _ := json.Marshal(cfg)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(dir, "source.db"))
	if err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(dir, "backup.db")
	if _, err := db.Backup(context.Background(), backup); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	args := []string{"preflight", "--config", path, "--backup", backup, "--directory", dir, "--strict"}
	var output bytes.Buffer
	err = upgradeCommand(args, &output)
	var diagnostic *diagnosticExit
	if !errors.As(err, &diagnostic) || diagnostic.code != 2 || !json.Valid(output.Bytes()) || !strings.Contains(output.String(), "TARGET_SCHEMA_SUPPORT_UNKNOWN") {
		t.Fatalf("unknown preflight=%s %v", output.String(), err)
	}
	output.Reset()
	args[0] = "rehearse"
	args = args[:len(args)-1]
	if err := upgradeCommand(args, &output); err != nil || !json.Valid(output.Bytes()) || !strings.Contains(output.String(), `"cleanup_completed": true`) {
		t.Fatalf("rehearsal=%s %v", output.String(), err)
	}
	for _, bad := range [][]string{{"preflight"}, {"rehearse", "--backup", backup}, {"preflight", "--backup", backup, "--directory", dir, "--execute"}, {"install"}} {
		if err := upgradeCommand(bad, &bytes.Buffer{}); err == nil {
			t.Fatal("invalid upgrade args accepted")
		}
	}
}

func TestThresholdCLIWorksOfflineAndFeedbackContainsNoAddress(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "metadata.jsonl")
	current := filepath.Join(dir, "current.json")
	candidate := filepath.Join(dir, "candidate.json")
	metadata := `{"format":"noderampart-replay","version":1,"anonymized":true,"interface_limit":1}` + "\n" + `{"type":"auth","auth":{"observed_at_utc":"2000-01-01T00:00:00Z","kind":"success","source_ip":"198.18.0.1","user":"user1","method":"publickey"}}` + "\n"
	for path, data := range map[string]string{input: metadata, current: `{"schema_version":1,"sensor":{"enabled":false}}`, candidate: `{"schema_version":1,"sensor":{"enabled":false},"auth":{"threshold":2}}`} {
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var output bytes.Buffer
	if err := thresholdCommand([]string{"preview", "--input", input, "--current", current, "--candidate", candidate}, &output); err != nil || !json.Valid(output.Bytes()) {
		t.Fatalf("preview=%s %v", output.String(), err)
	}
	comparison := filepath.Join(dir, "preview.json")
	if err := os.WriteFile(comparison, output.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	feedback := filepath.Join(dir, "feedback.json")
	if err := thresholdCommand([]string{"feedback", "--comparison", comparison, "--file", feedback, "--event", "evt_1", "--label", "reasonable"}, &output); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(feedback)
	if bytes.Contains(data, []byte("198.18.0.1")) || bytes.Contains(data, []byte("user1")) {
		t.Fatal("feedback copied source identity")
	}
	for _, bad := range [][]string{{"preview"}, {"preview", "--input", input, "--current", current, "--candidate", candidate, "--notify"}, {"feedback", "--comparison", comparison, "--file", feedback, "--event", "evt_99", "--label", "reasonable"}, {"apply"}} {
		if err := thresholdCommand(bad, &bytes.Buffer{}); err == nil {
			t.Fatal("unsafe/unimplemented threshold command accepted")
		}
	}
}
