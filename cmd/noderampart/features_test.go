// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/store"
)

func TestEvidenceCLIWindowsAndStrictArguments(t *testing.T) {
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	end := now.Add(-48 * time.Hour)
	for _, incident := range []string{"", "inc_fixture"} {
		args := []string{"--output", "/private/fixture.zip", "--until", end.Format(time.RFC3339), "--incident", incident}
		got, err := parseEvidence(args, now)
		want := 24 * time.Hour
		if incident != "" {
			want *= 7
		}
		if err != nil || !got.Query.End.Equal(end) || !got.Query.Start.Equal(end.Add(-want)) || got.Format != "zip" || got.Query.IncidentID != incident {
			t.Fatal("explicit end must anchor the default window", err)
		}
	}
	for _, args := range [][]string{
		nil, {"--output", "/private/a", "--format", "script"},
		{"--output", "/private/a", "--since", now.Add(-9 * 24 * time.Hour).Format(time.RFC3339)},
		{"--output", "/private/a", "--until", "bad"},
		{"--output", "/private/a", "--incident", "../secret"},
		{"--output", "/private/a\nsecret"}, {"--output", "/private/a", "unexpected"},
	} {
		if _, err := parseEvidence(args, now); err == nil {
			t.Fatal("invalid evidence arguments accepted")
		}
	}
}

func TestRetentionCLIAndAlertsStatus(t *testing.T) {
	now := time.Now().UTC()
	got, err := parseCommand([]string{"--dataset", "events", "--reason", "time_expiry", "--before-id", "123", "--limit", "2"}, "retention", now)
	var q store.RetentionQuery
	if err != nil || json.Unmarshal(got.Request.Args, &q) != nil || q.Dataset != "events" || q.Reason != "time_expiry" || q.BeforeID != 123 || q.Limit != 2 || !q.End.Equal(now) {
		t.Fatal("retention query lost filters", err)
	}
	if _, err := parseCommand(nil, "alerts_status", now); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"--dataset", "private_path"}, {"--reason", "custom"}, {"--before-id", "-1"}, {"--before-id", "9223372036854775808"}, {"--limit", "101"}} {
		if _, err := parseCommand(args, "retention", now); err == nil {
			t.Fatal("invalid retention query accepted")
		}
	}
	if _, err := parseCommand([]string{"--limit", "1"}, "alerts_status", now); err == nil || strings.Contains(err.Error(), "private_path") {
		t.Fatal("alerts accepted query arguments")
	}
}
