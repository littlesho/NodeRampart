// SPDX-License-Identifier: MIT

package notify

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/littlesho/NodeRampart/internal/model"
)

func TestNativeEventLanguageCategoriesGoldenAndBounds(t *testing.T) {
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	for _, language := range []string{"en", "zh"} {
		var golden strings.Builder
		for _, kind := range []string{"ssh_login_success", "ssh_brute_force", "syn_flood", "udp_flood", "icmp_flood", "bandwidth_spike", "port_scan", "budget_month_bytes", "budget_month_cost", "budget_day_bytes", "budget_day_growth", "health_sensor", "health_interface_counter", "health_ssh_journal", "health_storage", "health_geoip_update"} {
			for _, phase := range []string{"start", "update", "recovery"} {
				e := model.Event{ID: "evt_native", IncidentID: "inc_native", Kind: kind, Phase: phase, Severity: model.SeverityCritical, ObservedAt: time.Date(2026, 3, 8, 7, 30, 0, 0, time.UTC), Count: 123, Summary: "arbitrary raw English credential prose", Evidence: map[string]string{"coverage_complete": "false", "coverage": "incomplete", "observed_rate": "123", "threshold_rate": "100", "window_millis": "60000", "observed_bytes": "4096", "threshold_bytes": "2048", "observed_cost": "3.5", "threshold_cost": "2", "currency": "USD", "milestone": "100", "reason": "insufficient_coverage"}}
				body := FormatNativeEvent("主机 @everyone\x1b[31m", e, language, location)
				if len(body) > 1800 || !utf8.ValidString(body) || strings.Contains(body, "\x1b") || strings.Contains(body, "@everyone") || strings.Contains(body, e.Summary) || (!strings.Contains(body, "2026-03-08 03:30:00") || !strings.Contains(body, "UTC-04:00")) || !strings.Contains(body, "sudo noderampart events show --id evt_native") {
					t.Fatalf("%s/%s unsafe/incomplete body: %s", kind, phase, body)
				}
				if language == "zh" && (!strings.Contains(body, "覆盖不足") || !strings.Contains(body, "严重")) {
					t.Fatal("localized severity/coverage missing")
				}
				golden.WriteString("\n== " + kind + " " + phase + " ==\n" + body + "\n")
			}
		}
		path := filepath.Join("testdata", "native_events_"+language+".golden")
		if os.Getenv("NR_UPDATE_NATIVE_GOLDENS") == "1" {
			if err := os.WriteFile(path, []byte(golden.String()), 0644); err != nil {
				t.Fatal(err)
			}
		}
		want, err := os.ReadFile(path)
		if err != nil || string(want) != golden.String() {
			t.Fatalf("%s golden mismatch: %v", language, err)
		}
	}
}

func TestNativeSummaryUnicodeMaliceKeepsCoreAndLocalCommand(t *testing.T) {
	e := model.Event{ID: "evt_huge", Kind: "syn_flood", Severity: model.SeverityCritical, ObservedAt: time.Now(), Count: 999, Evidence: map[string]string{"observed_rate": "999", "threshold_rate": "100", "coverage_complete": "false"}, Target: strings.Repeat("😀@here\n\x1b", 10000)}
	for _, language := range []string{"en", "zh"} {
		body := FormatNativeEvent(strings.Repeat("😀", 10000), e, language, time.FixedZone("UTC+05:30", 19800))
		if len(body) > 1800 || !utf8.ValidString(body) || !strings.Contains(body, "999") || !strings.Contains(body, "sudo noderampart events show --id evt_huge") {
			t.Fatal("unbounded or lost core summary")
		}
	}
}
