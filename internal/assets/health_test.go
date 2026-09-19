// SPDX-License-Identifier: MIT

package assets

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestSanitizedHealthReadAndBounds(t *testing.T) {
	now := time.Now().UTC()
	health := Health{SchemaVersion: 1, CheckedAt: now, LastSuccessAt: now, Result: "unchanged", Scheduled: true}
	encoded, err := json.Marshal(health)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		edit func(*testing.T, string)
		ok   bool
	}{
		{"valid", func(*testing.T, string) {}, true},
		{"world_readable", func(t *testing.T, p string) { t.Helper(); _ = os.Chmod(p, 0o644) }, false},
		{"writable_group", func(t *testing.T, p string) { t.Helper(); _ = os.Chmod(p, 0o660) }, false},
		{"unknown_field", func(t *testing.T, p string) {
			t.Helper()
			_ = os.WriteFile(p, []byte(`{"schema_version":1,"result":"unknown","token":"synthetic-sensitive"}`), 0o640)
		}, false},
		{"oversized", func(t *testing.T, p string) {
			t.Helper()
			_ = os.WriteFile(p, []byte(strings.Repeat(" ", 4097)), 0o640)
		}, false},
		{"trailing_json", func(t *testing.T, p string) {
			t.Helper()
			_ = os.WriteFile(p, append(append([]byte{}, encoded...), []byte(" {}")...), 0o640)
		}, false},
		{"hardlink", func(t *testing.T, p string) {
			t.Helper()
			if err := os.Link(p, p+".link"); err != nil {
				t.Fatal(err)
			}
		}, false},
		{"symlink", func(t *testing.T, p string) {
			t.Helper()
			if err := os.Rename(p, p+".original"); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(p+".original", p); err != nil {
				t.Fatal(err)
			}
		}, false},
		{"fifo", func(t *testing.T, p string) {
			t.Helper()
			if err := os.Remove(p); err != nil {
				t.Fatal(err)
			}
			if err := unix.Mkfifo(p, 0o640); err != nil {
				t.Fatal(err)
			}
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "geoip-health.json")
			if err := os.WriteFile(path, encoded, 0o640); err != nil {
				t.Fatal(err)
			}
			tc.edit(t, path)
			got, err := readHealth(path, uint32(os.Geteuid()))
			if (err == nil) != tc.ok {
				t.Fatalf("read success=%v, want %v", err == nil, tc.ok)
			}
			if tc.ok && (got.Result != "unchanged" || !got.LastSuccessAt.Equal(now)) {
				t.Fatal("successful checked metadata changed")
			}
			if err != nil && (strings.Contains(err.Error(), path) || strings.Contains(err.Error(), "synthetic-sensitive")) {
				t.Fatal("metadata error leaked its input")
			}
		})
	}
	if _, err := readHealth(filepath.Join(t.TempDir(), "missing.json"), uint32(os.Geteuid())); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("missing metadata must remain distinguishable from invalid metadata")
	}
}

func TestSanitizedHealthRejectsParentSymlinkAndFalseSuccess(t *testing.T) {
	base := t.TempDir()
	actual := filepath.Join(base, "actual")
	if err := os.Mkdir(actual, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(actual, "geoip-health.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":1,"result":"unknown"}`), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(actual, filepath.Join(base, "alias")); err != nil {
		t.Fatal(err)
	}
	if _, err := readHealth(filepath.Join(base, "alias", "geoip-health.json"), uint32(os.Geteuid())); err == nil {
		t.Fatal("accepted a symlink ancestor")
	}
	if err := os.Chmod(actual, 0o777); err != nil {
		t.Fatal(err)
	}
	if _, err := readHealth(path, uint32(os.Geteuid())); err == nil {
		t.Fatal("accepted a shared final directory")
	}
	for _, h := range []Health{
		{SchemaVersion: 1, Result: "ok"},
		{SchemaVersion: 2, Result: "unknown"},
		{SchemaVersion: 1, Result: "synthetic-sensitive"},
		{SchemaVersion: 1, Result: "unknown", LastSuccessAt: time.Now()},
	} {
		if h.Validate() == nil {
			t.Fatal("accepted inconsistent health metadata")
		}
	}
}

func TestSanitizedHealthSeparatesScheduleAndNormalizesLegacy(t *testing.T) {
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	for _, legacy := range []bool{false, true} {
		h := Health{SchemaVersion: 1, CheckedAt: now, LastSuccessAt: now.Add(-time.Hour), Result: "download_failed", ConsecutiveFailures: 2, Scheduled: true, Schedule: &ScheduleHealth{CheckedAt: now, Result: "failed", ConsecutiveFailures: 3}}
		if legacy {
			h.Result, h.Schedule, h.ConsecutiveFailures = "schedule_failed", nil, 9
		}
		encoded, err := json.Marshal(h)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "health.json")
		if err := os.WriteFile(path, encoded, 0o640); err != nil {
			t.Fatal(err)
		}
		got, err := readHealth(path, uint32(os.Geteuid()))
		if err != nil {
			t.Fatal(err)
		}
		wantResult, wantFailures, wantSchedule := "download_failed", uint32(2), uint32(3)
		if legacy {
			wantResult, wantFailures, wantSchedule = "unknown", 0, 1
		}
		if got.Result != wantResult || got.ConsecutiveFailures != wantFailures || got.Schedule == nil || got.Schedule.ConsecutiveFailures != wantSchedule || got.Schedule.Result != "failed" || got.LastSuccessAt != h.LastSuccessAt || !got.Scheduled {
			t.Fatal("health channels or legacy verified timestamp changed")
		}
		after, err := os.ReadFile(path)
		if err != nil || string(after) != string(encoded) {
			t.Fatal("read-side migration wrote metadata")
		}
		if legacy && got.CheckedAt != h.LastSuccessAt {
			t.Fatal("legacy schedule timestamp claimed to be update timestamp")
		}
	}
	legacy := Health{SchemaVersion: 1, CheckedAt: now.Add(-time.Hour), LastSuccessAt: now, Result: "schedule_failed"}
	normal := legacy.NormalizeLegacy()
	if normal.Schedule.CheckedAt != now || normal.CheckedAt != now || legacy.Schedule != nil {
		t.Fatal("legacy normalization failed to clamp or mutated caller")
	}
}

func TestSanitizedHealthScheduleValidation(t *testing.T) {
	now := time.Now().UTC()
	for _, s := range []ScheduleHealth{
		{Result: "ok"},
		{Result: "ok", CheckedAt: now, ConsecutiveFailures: 1},
		{Result: "failed", CheckedAt: now},
		{Result: "failed", ConsecutiveFailures: 1},
		{Result: "unknown", ConsecutiveFailures: 1},
		{Result: "synthetic-secret", CheckedAt: now},
		{Result: "unknown", CheckedAt: time.Date(1969, 1, 1, 0, 0, 0, 0, time.UTC)},
	} {
		h := Health{SchemaVersion: 1, Result: "unknown", Schedule: &s}
		if h.Validate() == nil {
			t.Fatal("invalid schedule metadata accepted")
		}
	}
	for _, s := range []ScheduleHealth{{Result: "ok", CheckedAt: now}, {Result: "failed", CheckedAt: now, ConsecutiveFailures: 2}, {Result: "unknown"}} {
		h := Health{SchemaVersion: 1, Result: "unknown", Schedule: &s}
		if err := h.Validate(); err != nil {
			t.Fatal(err)
		}
		h.Result = "schedule_failed"
		if h.Validate() == nil {
			t.Fatal("accepted ambiguous combined and separate failures")
		}
	}
}
