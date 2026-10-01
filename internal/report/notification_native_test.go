// SPDX-License-Identifier: MIT

package report

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/store"
)

func TestNativeDailyGoldenRetainsCoverageTimeMetrics(t *testing.T) {
	snapshot, _ := localizedReportFixture(t)
	for _, language := range []string{"en", "zh"} {
		body, err := NativeNotificationBody(snapshot, language)
		if err != nil {
			t.Fatal(err)
		}
		if len(body) > 1800 || !utf8.ValidString(body) || !strings.Contains(body, "UTC-05:00") || !strings.Contains(body, "UTC-04:00") || !strings.Contains(body, "1.00 USD") || !strings.Contains(body, "sudo noderampart report show --date 2026-03-08 --format json") || strings.Contains(body, "Original English") || strings.Contains(body, "2001:db8") {
			t.Fatalf("unsafe/incomplete native daily: %s", body)
		}
		path := filepath.Join("testdata", "native_daily_"+language+".golden")
		if os.Getenv("NR_UPDATE_NATIVE_GOLDENS") == "1" {
			if err := os.WriteFile(path, []byte(body+"\n"), 0644); err != nil {
				t.Fatal(err)
			}
		}
		want, err := os.ReadFile(path)
		if err != nil || string(want) != body+"\n" {
			t.Fatalf("native daily golden mismatch: %v", err)
		}
	}
}

func TestNativeDailyFullChannelDoesNotPreventFiveHealthyTargets(t *testing.T) {
	b, _, now := completeUsageBuilder(t)
	ctx := context.Background()
	date, _, end := PreviousDay(now, time.UTC)
	s := Scheduler{Store: b.Store, Builder: b, DailyAt: "00:00", NotificationPrivacy: "prefix", NativeLanguages: map[string]string{}}
	for i, channel := range config.NativeChannelNames() {
		dest := channel + ":" + strings.Repeat(string(rune('a'+i)), 64)
		if err := b.Store.ConfigureNotificationTarget(ctx, channel, dest, "prefix", true, end.Add(-time.Hour)); err != nil {
			t.Fatal(err)
		}
		s.Destinations = append(s.Destinations, dest)
	}
	for i := 0; i < 1250; i++ {
		id := fmt.Sprintf("full_daily_%d", i)
		if ok, err := b.Store.Enqueue(ctx, store.OutboxMessage{ID: id, DedupeKey: id, Channel: "feishu", Destination: s.Destinations[0], PrivacyMode: "prefix", Body: "synthetic retained summary"}); err != nil || !ok {
			t.Fatal("quota fixture", i, err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := s.checkPrevious(ctx, now); !errors.Is(err, store.ErrOutboxFull) {
			t.Fatal("admission failure was not observable", err)
		}
	}
	for i, dest := range s.Destinations {
		generated, err := b.Store.ReportGenerated(ctx, date, dest)
		if err != nil || generated != (i != 0) {
			t.Fatal("channel admission collapsed into partial/all success", dest, err)
		}
		if i > 0 {
			rows, err := b.Store.PendingDestination(ctx, time.Now().Add(time.Minute), 20, dest)
			if err != nil || len(rows) != 1 {
				t.Fatal("healthy daily target lost or duplicated", dest, err)
			}
		}
	}
	status, err := b.Store.QueueStatus(ctx, time.Now().UTC())
	if err != nil || status.Rejected != 2 {
		t.Fatal("daily quota refusal was silent", err, status.Rejected)
	}
}

func TestSixNativeDailyIndependentAndNoHistoricalCutover(t *testing.T) {
	b, _, now := completeUsageBuilder(t)
	ctx := context.Background()
	_, _, end := PreviousDay(now, time.UTC)
	s := Scheduler{Store: b.Store, Builder: b, DailyAt: "00:00", NotificationPrivacy: "prefix", NativeLanguages: map[string]string{}}
	for i, channel := range config.NativeChannelNames() {
		destination := channel + ":" + strings.Repeat(string(rune('a'+i)), 64)
		if err := b.Store.ConfigureNotificationTarget(ctx, channel, destination, "prefix", true, end.Add(-time.Hour)); err != nil {
			t.Fatal(err)
		}
		s.Destinations = append(s.Destinations, destination)
		s.NativeLanguages[channel] = "zh"
	}
	if err := s.checkPrevious(ctx, now); err != nil {
		t.Fatal(err)
	}
	if err := s.checkPrevious(ctx, now); err != nil {
		t.Fatal(err)
	}
	for _, destination := range s.Destinations {
		rows, err := b.Store.PendingDestination(ctx, time.Now().Add(time.Minute), 20, destination)
		if err != nil || len(rows) != 1 || rows[0].Language != "zh" {
			t.Fatal("daily channel collapsed/duplicate", err)
		}
	}
	// Activate a new identity only after the previous report's end. It must
	// neither adopt old queue rows nor auto-send yesterday's archived report.
	newTarget := "feishu:" + strings.Repeat("f", 64)
	if err := b.Store.ConfigureNotificationTarget(ctx, "feishu", newTarget, "prefix", true, end.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	s.Destinations = []string{newTarget}
	s.NativeLanguages["feishu"] = "en"
	if err := s.checkPrevious(ctx, now); err != nil {
		t.Fatal(err)
	}
	rows, err := b.Store.PendingDestination(ctx, time.Now().Add(time.Minute), 20, newTarget)
	if err != nil || len(rows) != 0 {
		t.Fatal("new target received historical daily", err)
	}
}
