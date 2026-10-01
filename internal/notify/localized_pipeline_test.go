// SPDX-License-Identifier: MIT

package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/model"
	"github.com/littlesho/NodeRampart/internal/report"
	"github.com/littlesho/NodeRampart/internal/store"
)

type deliveredBody struct {
	ChatID, Text, ParseMode string
	DisablePreview          bool
}
type localizedTelegramFixture struct {
	sender   *Telegram
	mu       sync.Mutex
	bodies   []deliveredBody
	failBody string
	failed   bool
}

func localTelegramPipeline(t *testing.T) *localizedTelegramFixture {
	t.Helper()
	fixture := &localizedTelegramFixture{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			ChatID         string `json:"chat_id"`
			Text           string `json:"text"`
			ParseMode      string `json:"parse_mode"`
			DisablePreview bool   `json:"disable_web_page_preview"`
		}
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" || json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&payload) != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		fixture.mu.Lock()
		fixture.bodies = append(fixture.bodies, deliveredBody{payload.ChatID, payload.Text, payload.ParseMode, payload.DisablePreview})
		fail := payload.Text == fixture.failBody && !fixture.failed
		if fail {
			fixture.failed = true
		}
		fixture.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if fail {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, `{"ok":false,"error_code":503}`)
			return
		}
		fmt.Fprint(w, `{"ok":true,"result":{"message_id":1}}`)
	}))
	t.Cleanup(server.Close)
	tokenPath := filepath.Join(t.TempDir(), "synthetic-token")
	if err := os.WriteFile(tokenPath, []byte(fakeToken()), 0600); err != nil {
		t.Fatal(err)
	}
	sender, err := NewTelegram(tokenPath, "-12345", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	// The real sender's payload/response code runs against only loopback. Its
	// public production API and redirect policy do not need an injection option.
	sender.endpoint = server.URL
	fixture.sender = sender
	return fixture
}
func (f *localizedTelegramFixture) delivered() []deliveredBody {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]deliveredBody(nil), f.bodies...)
}
func openPipelineStore(t *testing.T, path, destination string) *store.Store {
	t.Helper()
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.ConfigureNotificationTarget(context.Background(), "telegram", destination, "prefix", true, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	return db
}
func pipelineMessage(id, body, language, zone, destination string) store.OutboxMessage {
	return store.OutboxMessage{ID: id, DedupeKey: id, Channel: "telegram", Destination: destination, PrivacyMode: "prefix", Body: body, Language: language, Timezone: zone}
}

func TestLocalizedSQLiteWorkerTelegramRetryAndReportBodies(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	fixture := localTelegramPipeline(t)
	path := filepath.Join(t.TempDir(), "outbox.sqlite")
	destination := fixture.sender.Destination()
	db := openPipelineStore(t, path, destination)
	loc, err := time.LoadLocation("Asia/Kathmandu")
	if err != nil {
		t.Fatal(err)
	}
	zone := "Asia/Kathmandu|+05:45"
	first := localizedEventFixture("ssh_login_success", "observed")
	first.ID = "evt_old_en"
	oldBody := FormatEventLocalized("节点", first, "en", loc)
	fixture.mu.Lock()
	fixture.failBody = oldBody
	fixture.mu.Unlock()
	oldMessage := pipelineMessage("msg_old_en", oldBody, "en", zone, destination)
	if err := db.InsertEventNotification(ctx, first, &oldMessage); err != nil {
		t.Fatal(err)
	}
	worker := &Worker{Store: db, Sender: fixture.sender, Destination: destination}
	if err := worker.process(ctx); err != nil {
		t.Fatal(err)
	}
	pending, err := db.Pending(ctx, time.Now().Add(time.Hour), 20)
	if err != nil || len(pending) != 1 || pending[0].Body != oldBody || pending[0].Language != "en" || pending[0].Attempts != 1 {
		t.Fatal("failed delivery body was changed or not retained", pending, err)
	}
	// The actual restart/language change admission boundary retains the old
	// body. Only new admissions are rendered Chinese; the worker has no locale.
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db = openPipelineStore(t, path, destination)
	worker.Store = db
	chinese := localizedEventFixture("ssh_brute_force", "start")
	chinese.ID = "evt_new_zh"
	chineseBody := FormatEventLocalized("节点", chinese, "zh", loc)
	message := pipelineMessage("msg_new_zh", chineseBody, "zh", zone, destination)
	if err := db.InsertEventNotification(ctx, chinese, &message); err != nil {
		t.Fatal(err)
	}
	testBody := FormatTest("节点", "zh")
	if ok, err := db.Enqueue(ctx, pipelineMessage("msg_test_zh", testBody, "zh", zone, destination)); err != nil || !ok {
		t.Fatal(ok, err)
	}
	start := time.Date(2026, 7, 14, 18, 15, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	document := &report.Document{SchemaVersion: 1, Date: "2026-07-15", Title: "Original English title", Hostname: "节点", Timezone: "Asia/Kathmandu", PeriodStart: start, PeriodEnd: end, GeneratedAt: end.Add(time.Hour), Body: "Original English body; immutable local history", Summary: store.Summary{Batches: 1, Events: []store.EventCount{{Kind: "ssh_brute_force", Severity: model.SeverityHigh, Count: 1}}}}
	encoded, err := report.EncodeDocument(document)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := store.ReportSnapshot{Date: document.Date, Title: document.Title, Body: document.Body, Document: encoded, PeriodStart: start, PeriodEnd: end, GeneratedAt: document.GeneratedAt}
	if err := db.SaveReport(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	archived, err := db.Report(ctx, snapshot.Date)
	if err != nil {
		t.Fatal(err)
	}
	reportBody, err := report.NotificationBodyLocalized(archived, "zh")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := db.Enqueue(ctx, pipelineMessage("msg_report_zh", reportBody, "zh", zone, destination)); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if err := worker.process(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	// Change back to English and reopen again. Both previously admitted Chinese
	// deliveries and the old English retry keep their exact durable text.
	db = openPipelineStore(t, path, destination)
	worker.Store = db
	last := localizedEventFixture("ssh_login_success", "observed")
	last.ID = "evt_final_en"
	lastBody := FormatEventLocalized("节点", last, "en", loc)
	message = pipelineMessage("msg_final_en", lastBody, "en", zone, destination)
	if err := db.InsertEventNotification(ctx, last, &message); err != nil {
		t.Fatal(err)
	}
	pending, err = db.Pending(ctx, time.Now().Add(time.Hour), 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range pending {
		if message.ID != "msg_old_en" {
			continue
		}
		if message.Body != oldBody || message.Language != "en" || message.Timezone != zone {
			t.Fatal("restart rerendered old retry", message)
		}
		// Wait only until the real recorded randomized retry deadline; no fixed
		// sleep or fabricated clock advances the worker's delivery eligibility.
		wait := time.Until(message.NextAttempt) + time.Millisecond
		if wait > 0 {
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				t.Fatal(ctx.Err())
			case <-timer.C:
			}
		}
	}
	if err := worker.process(ctx); err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, delivery := range fixture.delivered() {
		assertNotificationHTML(t, delivery.Text)
		if delivery.ChatID != "-12345" || delivery.ParseMode != "HTML" || !delivery.DisablePreview {
			t.Fatal("localized sender contract changed", delivery)
		}
		counts[delivery.Text]++
	}
	want := map[string]int{oldBody: 2, chineseBody: 1, testBody: 1, reportBody: 1, lastBody: 1}
	if !reflect.DeepEqual(counts, want) {
		t.Fatalf("delivered text differs across en→zh→en/retry: counts %v", counts)
	}
	if strings.Contains(reportBody, document.Body) || !strings.Contains(reportBody, "日报") || !strings.Contains(reportBody, "UTC+05:45") {
		t.Fatal("report copied English archive or lost snapshot time", reportBody)
	}
	after, err := db.Report(ctx, snapshot.Date)
	if err != nil || string(after.Document) != string(encoded) || after.Body != snapshot.Body {
		t.Fatal("localized delivery rewrote report history", err)
	}
	status, err := db.QueueStatus(ctx, time.Now())
	if err != nil || status.Pending != 0 || status.Quarantined != 0 || status.Isolated != 0 {
		t.Fatal("language switching dropped delivery intent", status, err)
	}
}

func TestLocalizedSQLiteMergedUpdatesAcrossLanguageAndTimezone(t *testing.T) {
	ctx := context.Background()
	fixture := localTelegramPipeline(t)
	destination := fixture.sender.Destination()
	db := openPipelineStore(t, filepath.Join(t.TempDir(), "merge.sqlite"), destination)
	if err := db.ConfigureNotifications(time.Hour); err != nil {
		t.Fatal(err)
	}
	kathmandu, err := time.LoadLocation("Asia/Kathmandu")
	if err != nil {
		t.Fatal(err)
	}
	newYork, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	var pairLatest string
	for i, item := range []struct {
		language, zone string
		location       *time.Location
		month          time.Month
	}{{"zh", "Asia/Kathmandu|+05:45", kathmandu, time.July}, {"zh", "Asia/Kathmandu|+05:45", kathmandu, time.July}, {"en", "Asia/Kathmandu|+05:45", kathmandu, time.July}, {"zh", "America/New_York|-05:00", newYork, time.January}, {"zh", "America/New_York|-04:00", newYork, time.July}} {
		event := localizedEventFixture("syn_flood", "update")
		event.ID = fmt.Sprintf("evt_merge_%d", i)
		event.IncidentID = "inc_same"
		event.ObservedAt = time.Date(2026, item.month, 15, 4, 5, 6, 0, time.UTC)
		body := FormatEventLocalized("节点", event, item.language, item.location)
		if i == 1 {
			pairLatest = body
		}
		message := pipelineMessage(fmt.Sprintf("msg_merge_%d", i), body, item.language, item.zone, destination)
		if err := db.InsertEventNotification(ctx, event, &message); err != nil {
			t.Fatal(err)
		}
	}
	pending, err := db.Pending(ctx, time.Now().Add(2*time.Hour), 20)
	if err != nil || len(pending) != 4 {
		t.Fatal("cross-language/zone updates were incorrectly merged", len(pending), err)
	}
	expected := map[string]int{}
	for _, message := range pending {
		if message.ID == "msg_merge_0" {
			if !strings.HasPrefix(message.Body, "<b>2 次事件更新</b>") || !strings.HasSuffix(message.Body, pairLatest) {
				t.Fatal("Chinese coalescing did not retain latest structured body", message.Body)
			}
		}
		if message.Language == "zh" && strings.Contains(message.Body, "incident updates") {
			t.Fatal("coalescing leaked English suffix", message.Body)
		}
		if message.Language == "en" && !strings.HasPrefix(message.Body, "<b>1 incident updates</b>") {
			t.Fatal("English suffix changed", message.Body)
		}
		if len(message.Body) > 4096 {
			t.Fatal("coalescing exceeded reserved body budget")
		}
		expected[message.Body]++
	}
	// A real recovery admission releases this incident's pending updates using
	// the existing queue rule. No SQL backdoor or fake deadline is needed.
	recovery := localizedEventFixture("syn_flood", "recovery")
	recovery.ID = "evt_recovery"
	recovery.IncidentID = "inc_same"
	recoveryBody := FormatEventLocalized("节点", recovery, "zh", kathmandu)
	message := pipelineMessage("msg_recovery", recoveryBody, "zh", "Asia/Kathmandu|+05:45", destination)
	if err := db.InsertEventNotification(ctx, recovery, &message); err != nil {
		t.Fatal(err)
	}
	expected[recoveryBody]++
	worker := &Worker{Store: db, Sender: fixture.sender, Destination: destination}
	if err := worker.process(ctx); err != nil {
		t.Fatal(err)
	}
	actual := map[string]int{}
	for _, delivery := range fixture.delivered() {
		if len(delivery.Text) > 4096 {
			t.Fatal("delivered merge overflow")
		}
		actual[delivery.Text]++
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatal("worker/sender changed coalesced language/timezone bodies", len(actual), len(expected))
	}
}
