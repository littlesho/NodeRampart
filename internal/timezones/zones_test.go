// SPDX-License-Identifier: MIT

package timezones

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestCatalogCompleteOfflineNamesAliasesAndCurrent(t *testing.T) {
	at := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	zones := List(at, "US/Eastern")
	reviewed, regional := 0, 0
	for _, name := range strings.Split(directory, "\n") {
		if name == "" || strings.HasPrefix(name, "#") {
			continue
		}
		reviewed++
		if strings.Contains(name, "/") {
			regional++
		}
	}
	if reviewed != 598 || regional != 553 || len(zones) != 555 {
		t.Fatalf("directory/list counts: reviewed=%d regional=%d selected=%d", reviewed, regional, len(zones))
	}
	if zones[0].Name != "Local" || zones[1].Name != "UTC" {
		t.Fatal("Local/UTC are not the first two selector choices")
	}
	seen := map[string]Zone{}
	for _, zone := range zones {
		if _, exists := seen[zone.Name]; exists || !zone.Available || zone.Offset == "" {
			t.Fatalf("duplicate or unavailable reviewed name: %s", zone.Name)
		}
		seen[zone.Name] = zone
	}
	for _, name := range []string{"Local", "UTC", "US/Eastern", "Asia/Calcutta", "Asia/Kathmandu", "Pacific/Chatham", "America/Coyhaique", "Etc/GMT+12"} {
		if _, ok := seen[name]; !ok {
			t.Fatalf("catalog dropped %s", name)
		}
	}
	if seen["US/Eastern"].Name != "US/Eastern" || seen["US/Eastern"].Offset != "UTC-05:00" {
		t.Fatal("configured alias changed or offset was not calculated at the reference instant")
	}
	if seen["Asia/Calcutta"].Offset != "UTC+05:30" {
		t.Fatal("old alias did not preserve fractional-hour rules")
	}
}

func TestCatalogFrozenOffsetsDSTAndFractionalHours(t *testing.T) {
	for _, tc := range []struct {
		name string
		at   time.Time
		want string
	}{
		{"UTC", time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC), "UTC+00:00"},
		{"America/St_Johns", time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC), "UTC-03:30"},
		{"Etc/GMT+12", time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC), "UTC-12:00"},
		{"Etc/GMT-4", time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC), "UTC+04:00"},
		{"America/New_York", time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC), "UTC-05:00"},
		{"America/New_York", time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC), "UTC-04:00"},
		{"Asia/Kathmandu", time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC), "UTC+05:45"},
		{"Pacific/Chatham", time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC), "UTC+13:45"},
		{"Australia/Adelaide", time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC), "UTC+09:30"},
	} {
		var offset string
		for _, zone := range List(tc.at, "UTC") {
			if zone.Name == tc.name {
				offset = zone.Offset
				break
			}
		}
		if offset != tc.want {
			t.Fatalf("%s: got %q, want %s", tc.name, offset, tc.want)
		}
	}
	if got := Offset(time.Date(1900, 1, 1, 0, 0, 0, 0, time.FixedZone("historical", -1234))); got != "UTC-00:20:34" {
		t.Fatalf("historical seconds were lost: %s", got)
	}
}

func TestSearchEnglishChineseNameOffsetAndNoMatch(t *testing.T) {
	zones := List(time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), "UTC")
	for query, name := range map[string]string{"  shanghai  ": "Asia/Shanghai", "Beijing": "Asia/Shanghai", "上海": "Asia/Shanghai", "NEW YORK": "America/New_York", "us/eastern": "US/Eastern", "UTC+05:45": "Asia/Kathmandu"} {
		found := false
		for _, zone := range Search(zones, query) {
			if zone.Name == name {
				found = true
			}
		}
		if !found {
			t.Fatalf("%q did not match %s", query, name)
		}
	}
	if len(Search(zones, "no-such-synthetic-timezone")) != 0 {
		t.Fatal("empty search selected a fallback")
	}
	result := Search(zones, "")
	result[0].Name = "mutated"
	if zones[0].Name == "mutated" {
		t.Fatal("search exposed mutable catalog storage")
	}
}

func TestCatalogUsesIsolatedOfflineRulesAndShowsMissingRules(t *testing.T) {
	// This loader is deliberately restricted to the checked-in TZif fixture.
	// It never calls time.LoadLocation or reads system/GOROOT timezone paths.
	// The runtime fallback is the standard-library time/tzdata import, rather
	// than a second bundled rule engine. This fixture proves directory behavior
	// without using the host's installed timezone rules as the test oracle.
	data, err := os.ReadFile("testdata/New_York.tzif")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	load := func(name string) (*time.Location, error) {
		calls++
		switch name {
		case "Local", "UTC":
			return time.UTC, nil
		case "America/New_York", "Fixture/Old_Alias":
			return time.LoadLocationFromTZData(name, data)
		default:
			return nil, errors.New("no offline rules")
		}
	}
	at := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	zones := list(at, "Fixture/Old_Alias", load)
	if calls != 556 || len(zones) != 556 {
		t.Fatal("current host-specific alias or bounded catalog count changed")
	}
	old := Search(zones, "Fixture/Old_Alias")
	if len(old) != 1 || old[0].Name != "Fixture/Old_Alias" || !old[0].Available || old[0].Offset != "UTC-04:00" {
		t.Fatal("valid current name was rewritten or isolated TZif rules were not used")
	}
	missing := Search(zones, "Asia/Shanghai")
	if len(missing) != 1 || missing[0].Available || missing[0].Offset != "" {
		t.Fatal("missing rules were silently replaced by healthy-looking UTC")
	}
	if _, err := time.LoadLocationFromTZData("bad", []byte(strings.Repeat("x", 80))); err == nil {
		t.Fatal("corrupt rule fixture accepted")
	}
}

func TestUntranslatedNamesKeepReadableProperNamesAndChineseRegion(t *testing.T) {
	english, chinese := readableNames("America/Argentina/Buenos_Aires")
	if english != "America · Argentina · Buenos Aires" || chinese != "美洲 · Argentina · Buenos Aires" {
		t.Fatalf("untranslated proper names changed: %q %q", english, chinese)
	}
	for _, name := range []string{"Europe/Isle_of_Man", "Asia/Ust-Nera", "Africa/Dar_es_Salaam", "Pacific/Port_Moresby"} {
		_, label := readableNames(name)
		if strings.Contains(label, "_") || strings.Contains(label, "/") {
			t.Fatal("raw directory separators remain in readable labels")
		}
	}
	zones := List(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), "UTC")
	found := false
	for _, z := range Search(zones, "美洲") {
		if z.Name == "America/New_York" {
			found = true
		}
	}
	if !found {
		t.Fatal("regional Chinese search excludes common labeled entries")
	}
}

func TestTopLevelAbbreviationsAreOnlyAvailableAsCurrentLegacyValues(t *testing.T) {
	at := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	normal := List(at, "UTC")
	for _, z := range normal {
		if z.Name != "Local" && z.Name != "UTC" && !strings.Contains(z.Name, "/") {
			t.Fatalf("new selection exposes a top-level name/abbreviation: %s", z.Name)
		}
	}
	for _, name := range []string{"EST", "HST", "MST", "CET", "EET", "EST5EDT", "GMT"} {
		for _, z := range normal {
			if z.Name == name {
				t.Fatalf("new selection offers %s", name)
			}
		}
		withCurrent := List(at, name)
		found := false
		for _, z := range withCurrent {
			if z.Name == name {
				found = true
				if !z.Available {
					t.Fatalf("valid legacy name unavailable: %s", name)
				}
			}
		}
		if !found || len(withCurrent) != 556 {
			t.Fatalf("current legacy name was lost or rewritten: %s", name)
		}
	}
	est := Search(List(at, "EST"), "EST")
	found := false
	for _, z := range est {
		if z.Name == "EST" && z.Offset == "UTC-05:00" {
			found = true
		}
	}
	if !found {
		t.Fatal("current EST abbreviation lost its exact fixed-offset semantics")
	}
}
