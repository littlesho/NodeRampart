// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/api"
)

func TestOfflineReplayCLIWorksWithoutDaemonOrLiveData(t *testing.T) {
	dir := t.TempDir()
	input, anon, rules := filepath.Join(dir, "input"), filepath.Join(dir, "anon"), filepath.Join(dir, "rules")
	metadata := `{"format":"noderampart-replay","version":1,"anonymized":false,"interface_limit":1}` + "\n" + `{"type":"auth","auth":{"observed_at_utc":"2026-01-01T00:00:00Z","kind":"success","source_ip":"192.0.2.1","user":"private-user","method":"publickey"}}` + "\n"
	for path, data := range map[string]string{input: metadata, rules: `{"schema_version":1,"hostname":"private-host","sensor":{"enabled":false},"auth":{"threshold":2}}`} {
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	if err := replayCommand([]string{"anonymize", "--input", input, "--output", anon}, &out); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := replayCommand([]string{"compare", "--input", anon, "--baseline", rules, "--candidate", rules}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"total_events": 1`) || strings.Contains(out.String(), "private-") || strings.Contains(out.String(), "192.0.2.1") {
		t.Fatal("offline result is missing or leaked live identifiers")
	}
}

func TestBackfillCLIRangeContract(t *testing.T) {
	now := time.Now().UTC()
	options, err := parseCommand([]string{"--from", "2026-09-01", "--through", "2026-09-10"}, "report_backfill", now)
	if err != nil {
		t.Fatal(err)
	}
	var args api.BackfillArgs
	if api.DecodeArgs(options.Request.Args, &args) != nil || args.From != "2026-09-01" || args.Through != "2026-09-10" {
		t.Fatal("backfill wire arguments changed")
	}
	for _, flags := range [][]string{nil, {"--from", "2026-09-11", "--through", "2026-09-10"}, {"--from", "2026-08-01", "--through", "2026-09-10"}, {"--from", "2026-09-01", "--through", "2026-09-10", "--send"}} {
		if _, err := parseCommand(flags, "report_backfill", now); err == nil {
			t.Fatal("invalid/unbounded backfill admitted")
		}
	}
}
