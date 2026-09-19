// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/api"
)

func TestTimelineAndSilenceCLIArguments(t *testing.T) {
	now := time.Now().UTC()
	if _, err := parseCommand([]string{"--limit", "1"}, "health", now); err != nil {
		t.Fatal(err)
	}
	since := now.Add(-time.Hour).Format(time.RFC3339Nano)
	options, err := parseCommand([]string{"--id", "inc_example", "--since", since}, "incident_show", now)
	if err != nil {
		t.Fatal(err)
	}
	var args api.TimelineArgs
	if json.Unmarshal(options.Request.Args, &args) != nil || args.IncidentID != "inc_example" || !args.End.Equal(now) {
		t.Fatal("incident query did not preserve exact range and identity")
	}
	if _, err := parseCommand([]string{"--incident", "inc_example", "--until", now.Add(time.Hour).Format(time.RFC3339Nano)}, "notify_silence_add", now); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"incident_show", nil},
		{"events_timeline", []string{"--after-id", "evt_missing_time"}},
		{"incident_list", []string{"--before", now.Add(-time.Minute).Format(time.RFC3339Nano)}},
		{"notify_silence_add", []string{"--kind", "syn_flood"}},
		{"notify_silence_add", []string{"--until", now.Add(time.Hour).Format(time.RFC3339Nano)}},
		{"notify_silence_remove", []string{"--id", "../private"}},
		{"health", []string{"--offset", "20257"}},
	} {
		if _, err := parseCommand(tc.args, tc.name, now); err == nil {
			t.Fatal("invalid CLI arguments accepted", tc.name)
		}
	}
}
