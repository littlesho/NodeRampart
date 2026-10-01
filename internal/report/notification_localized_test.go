// SPDX-License-Identifier: MIT

package report

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/littlesho/NodeRampart/internal/billing"
	"github.com/littlesho/NodeRampart/internal/model"
	"github.com/littlesho/NodeRampart/internal/store"
)

func localizedReportFixture(t *testing.T) (store.ReportSnapshot, *Document) {
	t.Helper()
	start := time.Date(2026, 3, 8, 5, 0, 0, 0, time.UTC)
	end := start.Add(23 * time.Hour) // New York's spring transition.
	pricing, err := billing.NewSnapshot(billing.Profile{SchemaVersion: 1, Name: "方案 <A&B>", Provider: "custom", SourceRegion: "fixture", Currency: "USD", EffectiveDate: "2026-01-01", SourceURL: "https://example.invalid/pricing", FreeGB: 1, InternetEgress: []billing.Tier{{PricePerGB: .5}}}, 3_000_000_000, time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), end, end.Add(time.Hour), "Asia/Kolkata")
	if err != nil {
		t.Fatal(err)
	}
	doc := &Document{SchemaVersion: 1, Date: "2026-03-08", Title: "English original title", Hostname: "主机 <A&B>", Timezone: "America/New_York", PeriodStart: start, PeriodEnd: end, GeneratedAt: end.Add(time.Hour), Body: "Original English body with 2001:db8:1234::/48", Notes: []string{"Original English note must not be copied"}, Billing: pricing,
		Summary: store.Summary{Interface: model.InterfaceTotals{RXBytes: 4096, TXBytes: 8192}, Batches: 100, ParseErrors: 3, KernelPackets: 1000, KernelDrops: 4, KernelStatsErrors: 2, OverflowPackets: 9, OverflowBytes: 2048, IPCDroppedBatches: 5, IPCDroppedPackets: 20, IPCDroppedBytes: 4096, AttributedRXBytes: 1024, AttributedTXBytes: 2048,
			Events: []store.EventCount{{Kind: "ssh_brute_force", Severity: model.SeverityHigh, Count: 2}, {Kind: "ssh_login_success", Severity: model.SeverityInfo, Count: 3}}, Auth: []store.AuthCount{{Kind: "success", Count: 3}, {Kind: "failure", Count: 8}, {Kind: "invalid_user", Count: 2}, {Kind: "pam_failure", Count: 1}}, TopSources: []store.SourceCount{{SourceRange: "2001:db8:1234::/48", Count: 14}}},
		Integrity: store.IntegrityView{Components: []store.IntegrityComponent{{Name: "interface_counter", RunningMS: (23 * time.Hour).Milliseconds()}, {Name: "sensor_feed", RunningMS: (20 * time.Hour).Milliseconds(), DegradedMS: (3 * time.Hour).Milliseconds()}, {Name: "ssh_journal", UnknownMS: (23 * time.Hour).Milliseconds()}}, Gaps: []store.CoverageGap{{Name: "sensor_feed", Reason: "fixture_unknown_loss", Start: start, End: start.Add(time.Minute)}}, Retention: &store.RetentionView{TrackingStarted: start.AddDate(0, -1, 0), Entries: []store.RetentionEntry{{Dataset: "events", Reason: "time_expiry", AffectedRows: 10, DataStart: start, DataEnd: start.Add(time.Hour)}}}},
	}
	return localizedSnapshot(t, doc), doc
}

func localizedSnapshot(t *testing.T, doc *Document) store.ReportSnapshot {
	t.Helper()
	data, err := EncodeDocument(doc)
	if err != nil {
		t.Fatal(err)
	}
	return store.ReportSnapshot{Date: doc.Date, Title: doc.Title, PeriodStart: doc.PeriodStart, PeriodEnd: doc.PeriodEnd, GeneratedAt: doc.GeneratedAt, Document: data, Billing: doc.Billing, Body: ShortBody(doc.Body)}
}

func TestNotificationLocalizedCompleteGoldenAndImmutableHistory(t *testing.T) {
	snapshot, _ := localizedReportFixture(t)
	before, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, language := range []string{"en", "zh"} {
		t.Run(language, func(t *testing.T) {
			body, err := NotificationBodyLocalized(snapshot, language)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join("testdata", "notification_"+language+".golden")
			if os.Getenv("NR_UPDATE_REPORT_GOLDENS") == "1" {
				if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(body+"\n"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil || body+"\n" != string(want) {
				t.Fatalf("complete %s golden mismatch: %v\n%s", language, err, body)
			}
			if len(body) > 4096 || !utf8.ValidString(body) || strings.Contains(body, "truncated") || strings.Contains(body, "已截断") {
				t.Fatal("ordinary complete summary was truncated or invalid")
			}
			for _, absent := range []string{"Original English", "2001:db8:1234::/48", "<A&B>"} {
				if strings.Contains(body, absent) {
					t.Fatalf("original prose, source or unescaped HTML leaked: %s", absent)
				}
			}
			for _, fact := range []string{"4.00 KiB", "8.00 KiB", "3.00 KiB", "6.00 KiB", "2026-03-08 00:00 UTC-05:00", "2026-03-09 00:00 UTC-04:00", "2026-03-01 05:30 UTC+05:30", "America/New_York", "Asia/Kolkata", "1.00 GB", "2.00 GB", "1.00 USD", "方案 &lt;A&amp;B&gt;"} {
				if !strings.Contains(body, fact) {
					t.Fatalf("retained fact missing in %s: %s", language, fact)
				}
			}
		})
	}
	after, err := json.Marshal(snapshot)
	if err != nil || string(before) != string(after) {
		t.Fatal("rendering changed immutable report or billing snapshot", err)
	}
	english, _ := NotificationBody(snapshot)
	localized, _ := NotificationBodyLocalized(snapshot, "en")
	defaultBody, _ := NotificationBodyLocalized(snapshot, "")
	if english != localized || english != defaultBody {
		t.Fatal("English wrapper or default contract changed")
	}
}

func TestNotificationLocalizedBoundsEscapeAndUnknown(t *testing.T) {
	_, doc := localizedReportFixture(t)
	doc.Date = "<date & value>"
	doc.Summary.Auth = nil
	doc.Summary.Events = nil
	for i := range 300 {
		doc.Summary.Events = append(doc.Summary.Events, store.EventCount{Kind: fmt.Sprintf("%03d<script>%s</script>", i, strings.Repeat("中", 40)), Severity: model.SeverityHigh, Count: 1})
	}
	for _, language := range []string{"en", "zh"} {
		body, err := NotificationBodyLocalized(localizedSnapshot(t, doc), language)
		if err != nil || len(body) > 4096 || !utf8.ValidString(body) || strings.Contains(body, "<script>") || strings.Contains(body, "<date") || !strings.Contains(body, "&lt;date &amp; value&gt;") {
			t.Fatal("bounded report lost UTF-8/HTML safety", language, err)
		}
		marker := "… truncated; open the full local archive."
		if language == "zh" {
			marker = "… 已截断，请查看完整本地归档。"
		}
		if !strings.HasSuffix(body, marker) || strings.Count(body, "<b>") != strings.Count(body, "</b>") {
			t.Fatal("truncation lost reserved marker or cut a markup line", language)
		}
	}
	_, doc = localizedReportFixture(t)
	doc.Summary.Events = []store.EventCount{{Kind: "port_scan", Count: math.MaxUint64}, {Kind: "port_scan", Count: 1}}
	doc.Summary.HealthCounterSaturations = 1
	doc.Integrity.HistoryTruncated = true
	doc.Integrity.Retention = nil
	zh, err := NotificationBodyLocalized(localizedSnapshot(t, doc), "zh")
	if err != nil || !strings.Contains(zh, "18446744073709551615") || !strings.Contains(zh, "仅为下界") || !strings.Contains(zh, "网卡计数: 部分") || !strings.Contains(zh, "裁剪台账不可用") {
		t.Fatal("saturation, retention uncertainty or truncated history became healthy", err)
	}
}

func TestNotificationLocalizedLegacyAndHistoricalTimezone(t *testing.T) {
	legacy := store.ReportSnapshot{Date: "2026-03-08", Body: "source 203.0.113.9 / Original English", PeriodStart: time.Date(2026, 3, 8, 0, 0, 0, 0, time.UTC), PeriodEnd: time.Date(2026, 3, 9, 0, 0, 0, 0, time.UTC)}
	before := legacy
	for _, language := range []string{"en", "zh"} {
		body, err := NotificationBodyLocalized(legacy, language)
		if err != nil || strings.Contains(body, "203.0.113.9") || strings.Contains(body, "Original English") || strings.Contains(body, "security events: 0") {
			t.Fatal("legacy content reconstructed or source leaked", err)
		}
		if language == "zh" && (!strings.Contains(body, "原始完整内容") || !strings.Contains(body, "时区未知") || !strings.Contains(body, "缺失历史不等于零")) {
			t.Fatal("legacy uncertainty not localized")
		}
	}
	if !reflect.DeepEqual(before, legacy) {
		t.Fatal("legacy history modified")
	}
	snapshot, doc := localizedReportFixture(t)
	if body, err := NotificationBodyLocalized(snapshot, "fr"); err == nil || body != "" {
		t.Fatal("unsupported language accepted")
	}
	snapshot.PeriodEnd = snapshot.PeriodEnd.Add(time.Hour)
	if _, err := NotificationBodyLocalized(snapshot, "zh"); err == nil {
		t.Fatal("conflicting period rendered as historical truth")
	}
	doc.Timezone = "Missing/Invalid"
	if _, err := NotificationBodyLocalized(localizedSnapshot(t, doc), "zh"); err == nil {
		t.Fatal("invalid snapshot timezone silently used current timezone")
	}
	for _, name := range []string{"", "Local"} {
		doc.Timezone = name
		body, err := NotificationBodyLocalized(localizedSnapshot(t, doc), "zh")
		if err != nil || !strings.Contains(body, "未固定原始时区") || !strings.Contains(body, "2026-03-08 05:00 UTC+00:00") {
			t.Fatal("ambiguous historical Local timezone used host settings", err)
		}
	}
}

func TestNotificationLocalizedMissingCountersAndCycleCoverage(t *testing.T) {
	b, _ := backfillBuilder(t, "UTC")
	_, fixture := localizedReportFixture(t)
	b.Billing = &fixture.Billing.Profile
	now := fixture.PeriodEnd.Add(time.Hour)
	date, _, _ := PreviousDay(now, time.UTC)
	snapshot, _, err := b.archiveDate(context.Background(), date, now)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := DecodeDocument(snapshot.Document)
	if err != nil || doc.Billing == nil || doc.Billing.OutboundBytes != 0 || doc.Summary.Interface.TXBytes != 0 {
		t.Fatal("empty retained history fixture is not a saved zero-counter estimate", err)
	}
	for _, language := range []string{"en", "zh"} {
		body, err := NotificationBodyLocalized(snapshot, language)
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"missing observations are unknown; zero does not prove no activity", "Whole-cycle data completeness is unknown", "retained counters may understate usage or cost"}
		if language == "zh" {
			want = []string{"缺失观测仍未知；零不证明没有活动", "整个结算周期的数据完整性未知", "保留计数可能低估用量或费用"}
		}
		for _, statement := range want {
			if !strings.Contains(body, statement) {
				t.Errorf("%s missing-counter estimate lost uncertainty: %s", language, statement)
			}
		}
	}
}

func TestNotificationLocalizedTruncationKeepsCoverageAndBillingLimits(t *testing.T) {
	_, doc := localizedReportFixture(t)
	doc.Hostname = strings.Repeat("\"", 256)
	doc.Timezone = "Local"
	profile := doc.Billing.Profile
	profile.Name = strings.Repeat("\"", 128)
	profile.FreeGB = 1e308
	pricing, err := billing.NewSnapshot(profile, doc.Billing.OutboundBytes, doc.Billing.PeriodStart, doc.Billing.PeriodEnd, doc.Billing.GeneratedAt, "Local")
	if err != nil {
		t.Fatal(err)
	}
	doc.Billing = pricing
	doc.Summary.Batches = 0
	doc.Summary.ParseErrors = math.MaxUint64
	doc.Summary.KernelPackets = math.MaxUint64
	doc.Summary.KernelDrops = math.MaxUint64
	doc.Summary.KernelStatsErrors = math.MaxUint64
	doc.Summary.OverflowPackets = math.MaxUint64
	doc.Summary.IPCDroppedBatches = math.MaxUint64
	doc.Summary.IPCDroppedPackets = math.MaxUint64
	doc.Summary.Events[0].Count = math.MaxUint64
	doc.Summary.Auth[0].Count = math.MaxUint64
	doc.Summary.HealthCounterSaturations = 1
	doc.Integrity.HistoryTruncated = true
	doc.Integrity.Retention.TrackingStarted = time.Time{}
	for i := range 100 {
		doc.Summary.Events = append(doc.Summary.Events, store.EventCount{Kind: fmt.Sprintf("bounded_event_%d", i), Count: 1})
	}
	for _, language := range []string{"en", "zh"} {
		body, err := NotificationBodyLocalized(localizedSnapshot(t, doc), language)
		if err != nil || len(body) > 4096 || !utf8.ValidString(body) || strings.Count(body, "<b>") != strings.Count(body, "</b>") {
			t.Fatal("truncated boundary report lost byte/UTF-8/markup bounds", language, err)
		}
		want := []string{"Missing history is not zero", "not the provider billing meter or invoice", "exclude fees outside the saved tariff", "… truncated; open the full local archive."}
		if language == "zh" {
			want = []string{"缺失历史不等于零", "不等于服务商计费流量或账单", "不包含保存的价格模型之外的费用", "… 已截断，请查看完整本地归档。"}
		}
		for _, statement := range want {
			if !strings.Contains(body, statement) {
				t.Errorf("%s truncated estimate lost required uncertainty: %s (body %d bytes)", language, statement, len(body))
			}
		}
	}
}

func TestSchedulerLocalizedChannelsRetainSnapshotTimezone(t *testing.T) {
	b, _, now := completeUsageBuilder(t)
	ctx := context.Background()
	date, _, _ := PreviousDay(now, time.UTC)
	snapshot, _, err := b.archiveDate(ctx, date, now)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := DecodeDocument(snapshot.Document)
	if err != nil {
		t.Fatal(err)
	}
	telegram, webhook := "telegram:"+strings.Repeat("c", 64), "webhook:"+strings.Repeat("d", 64)
	for _, target := range []struct{ channel, destination string }{{"telegram", telegram}, {"webhook", webhook}} {
		if err := b.Store.ConfigureNotificationTarget(ctx, target.channel, target.destination, "prefix", true, now); err != nil {
			t.Fatal(err)
		}
	}
	// The new location has identical daily bounds; historical spelling remains UTC.
	b.Location = reportZone(t, "Etc/UTC")
	s := Scheduler{Store: b.Store, Builder: b, DailyAt: "00:00", Destinations: []string{telegram, webhook}, NotificationPrivacy: "prefix", TelegramLanguage: "zh"}
	if err := s.checkPrevious(ctx, now); err != nil {
		t.Fatal(err)
	}
	messages, err := b.Store.Pending(ctx, time.Now().UTC().Add(time.Second), 10)
	if err != nil || len(messages) != 2 {
		t.Fatal("two localized channels not admitted", err)
	}
	for _, m := range messages {
		language := "en"
		if m.Channel == "telegram" {
			language = "zh"
		}
		want, err := NotificationBodyLocalized(snapshot, language)
		if err != nil || m.Language != language || m.Timezone != doc.Timezone || m.Body != want {
			t.Fatal("channel language/current timezone replaced retained presentation", err, m.Language, m.Timezone)
		}
	}
	again, err := b.Store.Report(ctx, date)
	if err != nil || !reflect.DeepEqual(snapshot, again) {
		t.Fatal("delivery changed original structured or short archive", err)
	}
	s.TelegramLanguage = "en"
	if err := s.checkPrevious(ctx, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	after, err := b.Store.Pending(ctx, time.Now().UTC().Add(time.Second), 10)
	if err != nil || !reflect.DeepEqual(messages, after) {
		t.Fatal("language change retranslates old queued bodies or duplicates daily delivery", err)
	}
	// A truly different civil period still conflicts rather than rewriting history.
	b.Location = reportZone(t, "Asia/Shanghai")
	conflictNow := civilDate(now, time.UTC).Add(12 * time.Hour)
	if err := s.checkPrevious(ctx, conflictNow); err != store.ErrReportPeriodConflict {
		t.Fatal("timezone conflict overwrote historical date", err)
	}
}

func TestSchedulerLocalizedLegacyTimezoneAndInvalidLanguage(t *testing.T) {
	for _, language := range []string{"zh", "not-supported"} {
		t.Run(language, func(t *testing.T) {
			b, _ := backfillBuilder(t, "UTC")
			now := time.Now().UTC()
			ctx := context.Background()
			date, start, end := PreviousDay(now, time.UTC)
			legacy := store.ReportSnapshot{Date: date, Title: "Old English report", Body: "Old English source 203.0.113.1", PeriodStart: start, PeriodEnd: end, GeneratedAt: now.Truncate(time.Millisecond)}
			if err := b.Store.SaveReport(ctx, legacy); err != nil {
				t.Fatal(err)
			}
			telegram, webhook := "telegram:"+strings.Repeat("e", 64), "webhook:"+strings.Repeat("f", 64)
			for _, target := range []struct{ channel, destination string }{{"telegram", telegram}, {"webhook", webhook}} {
				if err := b.Store.ConfigureNotificationTarget(ctx, target.channel, target.destination, "prefix", true, now); err != nil {
					t.Fatal(err)
				}
			}
			b.Location = reportZone(t, "Etc/UTC")
			s := Scheduler{Store: b.Store, Builder: b, DailyAt: "00:00", Destinations: []string{telegram, webhook}, NotificationPrivacy: "prefix", TelegramLanguage: language}
			err := s.checkPrevious(ctx, now)
			if language == "not-supported" && err == nil || language == "zh" && err != nil {
				t.Fatal("language admission result incorrect", err)
			}
			pending, err := b.Store.Pending(ctx, now.Add(time.Second), 10)
			if err != nil {
				t.Fatal(err)
			}
			if language == "not-supported" {
				if len(pending) != 0 {
					t.Fatal("invalid Telegram language partially queued another channel")
				}
			} else {
				if len(pending) != 2 {
					t.Fatal("legacy summary channels missing")
				}
				for _, m := range pending {
					if m.Timezone != "" || strings.Contains(m.Body, "203.0.113.1") || strings.Contains(m.Body, "Old English") {
						t.Fatal("legacy delivery copied source/prose or assigned today's timezone")
					}
					if m.Channel == "telegram" && m.Language != "zh" || m.Channel == "webhook" && m.Language != "en" {
						t.Fatal("legacy delivery mixed channel languages")
					}
				}
			}
			again, err := b.Store.Report(ctx, date)
			if err != nil || !reflect.DeepEqual(legacy, again) {
				t.Fatal("legacy immutable body was rewritten", err)
			}
		})
	}
}
