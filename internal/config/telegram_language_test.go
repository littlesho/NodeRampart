// SPDX-License-Identifier: MIT

package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTelegramLanguageDefaultCompatibilityAndValidation(t *testing.T) {
	if Defaults().Notifications.Telegram.Language != "en" {
		t.Fatal("new configurations do not explicitly default to English")
	}
	for _, language := range []string{"", "en", "zh"} {
		cfg := Defaults()
		cfg.Notifications.Telegram.Language = language
		if err := cfg.Validate(); err != nil {
			t.Fatalf("valid language %q: %v", language, err)
		}
		want := language
		if want == "" {
			want = "en"
		}
		if got := TelegramLanguage(cfg.Notifications.Telegram); got != want {
			t.Fatalf("%q resolved to %q", language, got)
		}
	}
	for _, language := range []string{"EN", "中文", "fr", "en ", " zh", "en\x00", "en\n"} {
		for _, enabled := range []bool{false, true} {
			cfg := Defaults()
			cfg.Notifications.Telegram.ChatID = "12345"
			cfg.Notifications.Telegram.Enabled = enabled
			cfg.Notifications.Telegram.Language = language
			if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "telegram.language") {
				t.Fatalf("invalid language %q enabled=%v accepted: %v", language, enabled, err)
			}
		}
	}
}

func TestLoadTelegramLanguageOmittedOrEmptyAndRoundTrip(t *testing.T) {
	for _, language := range []string{"omitted", "", "zh"} {
		cfg := Defaults()
		data, err := json.Marshal(cfg)
		if err != nil {
			t.Fatal(err)
		}
		var tree map[string]any
		if err := json.Unmarshal(data, &tree); err != nil {
			t.Fatal(err)
		}
		telegram := tree["notifications"].(map[string]any)["telegram"].(map[string]any)
		if language == "omitted" {
			delete(telegram, "language")
		} else {
			telegram["language"] = language
		}
		data, _ = json.Marshal(tree)
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		want := "en"
		if language == "zh" {
			want = "zh"
		}
		if TelegramLanguage(got.Notifications.Telegram) != want || got.Notifications.Telegram.Enabled || got.Notifications.Telegram.ChatID != cfg.Notifications.Telegram.ChatID || got.Notifications.Telegram.TokenFile != cfg.Notifications.Telegram.TokenFile {
			t.Fatal("language loading changed target, credential path, or enablement")
		}
		encoded, err := json.Marshal(got)
		if err != nil || !strings.Contains(string(encoded), `"language":`) {
			t.Fatal("language not preserved in JSON configuration")
		}
	}
}

func TestReportTimezoneMissingDefaultsButExplicitEmptyRejected(t *testing.T) {
	cfg := Defaults()
	cfg.Reports.Timezone = ""
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "reports.timezone") {
		t.Fatal("explicit empty timezone was silently interpreted as UTC")
	}
	data, err := json.Marshal(Defaults())
	if err != nil {
		t.Fatal(err)
	}
	var tree map[string]any
	if err := json.Unmarshal(data, &tree); err != nil {
		t.Fatal(err)
	}
	delete(tree["reports"].(map[string]any), "timezone")
	data, _ = json.Marshal(tree)
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil || got.Reports.Timezone != "Local" {
		t.Fatal("omitted timezone no longer uses the established Local default")
	}
}

func TestReportLocationRejectsEmptyAndUnknownPreservesValidNames(t *testing.T) {
	for _, name := range []string{"", "Fixture/Unknown_Timezone"} {
		if loc, err := ReportLocation(ReportsConfig{Timezone: name}); err == nil || loc != nil {
			t.Fatalf("invalid timezone %q resolved to %v without error: %v", name, loc, err)
		}
	}
	loc, err := ReportLocation(ReportsConfig{Timezone: "Local"})
	if err != nil || loc != time.Local {
		t.Fatal("Local no longer returns the system local location")
	}
	loc, err = ReportLocation(ReportsConfig{Timezone: "UTC"})
	if err != nil || loc != time.UTC {
		t.Fatal("UTC loading changed")
	}
	at := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	for name, seconds := range map[string]int{"Asia/Shanghai": 8 * 3600, "US/Eastern": -5 * 3600, "Asia/Calcutta": 5*3600 + 30*60, "EST": -5 * 3600} {
		loc, err := ReportLocation(ReportsConfig{Timezone: name})
		if err != nil || loc == nil || loc.String() != name {
			t.Fatalf("valid name/alias %q changed: %v %v", name, loc, err)
		}
		_, got := at.In(loc).Zone()
		if got != seconds {
			t.Fatalf("%q rules changed: got %d want %d", name, got, seconds)
		}
	}
}

func TestTelegramLanguageDoesNotHideInvalidNonemptyValues(t *testing.T) {
	for _, language := range []string{"fr", "EN", "中文", "en ", " zh", "en\x00", "en\n"} {
		if got := TelegramLanguage(TelegramConfig{Language: language}); got != language {
			t.Fatalf("invalid language %q hidden as %q", language, got)
		}
	}
}
