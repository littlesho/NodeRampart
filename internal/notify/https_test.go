// SPDX-License-Identifier: MIT

package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/store"
)

const syntheticHTTPSBearer = "synthetic-fixture-bearer-1234567890"

func httpsCredential(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bearer.token")
	if err := os.WriteFile(path, []byte(syntheticHTTPSBearer), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func localWebhook(t *testing.T, server *httptest.Server) *Webhook {
	t.Helper()
	w, err := NewWebhook(server.URL+"/hook", "local-fixture", httpsCredential(t), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	// Keep the production redirect policy and use only the test server's CA.
	w.http.client.Transport = server.Client().Transport
	return w
}

func privateError(t *testing.T, err error, forbidden ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected a sender failure")
	}
	for _, value := range append(forbidden, syntheticHTTPSBearer) {
		if value != "" && strings.Contains(err.Error(), value) {
			t.Fatal("sender failure disclosed request or response contents")
		}
	}
}

func TestWebhookHTTPSStableMessageAndMinimalHeartbeat(t *testing.T) {
	requests := make(chan map[string]json.RawMessage, 3)
	failures := make(chan error, 3)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Authorization") != "Bearer "+syntheticHTTPSBearer {
			failures <- errors.New("request method or protected authorization is incorrect")
		}
		var payload map[string]json.RawMessage
		if err := json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&payload); err != nil {
			failures <- errors.New("request JSON is invalid")
		}
		requests <- payload
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	webhook := localWebhook(t, server)
	message := store.OutboxMessage{ID: "message_123", DedupeKey: "event:event_456:webhook", Body: "本地合成摘要"}
	for i := 0; i < 2; i++ {
		if err := webhook.SendMessage(context.Background(), message); err != nil {
			t.Fatal(err)
		}
		payload := <-requests
		if len(payload) != 4 || string(payload["message_id"]) != `"message_123"` || string(payload["event_id"]) != `"event_456"` || string(payload["schema_version"]) != "1" {
			t.Fatal("stable message/event identifiers or payload shape changed")
		}
	}
	heartbeat, err := NewHeartbeat(server.URL+"/heartbeat", httpsCredential(t), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	heartbeat.http.client.Transport = server.Client().Transport
	if err := heartbeat.Send(context.Background(), HeartbeatPayload{InstanceID: "fixture-instance", At: time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC), Version: "local-candidate", Alive: true, Health: "degraded", Reasons: []string{"optional_unavailable"}}); err != nil {
		t.Fatal(err)
	}
	payload := <-requests
	if len(payload) != 6 || string(payload["process_alive"]) != "true" || string(payload["functional_health"]) != `"degraded"` {
		t.Fatal("heartbeat conflated process liveness and functional health")
	}
	for _, key := range []string{"instance_id", "at_utc", "version", "process_alive", "functional_health", "reason_codes"} {
		if _, ok := payload[key]; !ok {
			t.Fatal("minimal heartbeat field missing")
		}
	}
	select {
	case err := <-failures:
		t.Fatal(err)
	default:
	}
}

func TestWebhookHTTPSStatusResponseBoundAndRedaction(t *testing.T) {
	for _, tc := range []struct {
		name                        string
		status, responseBytes       int
		permanent, limited, invalid bool
		retry                       string
	}{
		{"success_at_limit", 200, maxTelegramResponseBytes, false, false, false, ""},
		{"success_oversize", 200, maxTelegramResponseBytes + 1, false, false, true, ""},
		{"permanent_forbidden", 403, 64, true, false, false, ""},
		{"retry_request_timeout", 408, 64, false, false, false, ""},
		{"retry_server_failure", 503, 64, false, false, false, ""},
		{"limited_oversize_keeps_header", 429, maxTelegramResponseBytes + 1, false, true, true, "3600"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Retry-After", tc.retry)
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, strings.Repeat("response-secret-", tc.responseBytes/16+1)[:tc.responseBytes])
			}))
			defer server.Close()
			webhook := localWebhook(t, server)
			err := webhook.Send(context.Background(), "synthetic body")
			if tc.status == 200 && !tc.invalid {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			privateError(t, err, server.URL, "response-secret", "synthetic body")
			var delivery *DeliveryError
			if !errors.As(err, &delivery) || delivery.StatusCode != tc.status || delivery.Permanent != tc.permanent || delivery.RateLimited != tc.limited || delivery.InvalidResponse != tc.invalid {
				t.Fatal("HTTP failure classification lost", err)
			}
			if tc.retry != "" && delivery.RetryAfter != time.Hour {
				t.Fatal("oversized error response lost its header cooldown")
			}
		})
	}
}

func TestHTTPSRedirectAndContextCancellation(t *testing.T) {
	var redirected atomic.Int32
	other := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Add(1); w.WriteHeader(204) }))
	defer other.Close()
	redirect := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", other.URL)
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	webhook := localWebhook(t, redirect)
	err := webhook.Send(context.Background(), "redirect fixture")
	privateError(t, err, redirect.URL, other.URL)
	if redirected.Load() != 0 {
		t.Fatal("redirect sent credentials or a body to another target")
	}
	var delivery *DeliveryError
	if !errors.As(err, &delivery) || !delivery.Permanent {
		t.Fatal("refused redirect was not a permanent fixed-target failure")
	}
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	slow := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer slow.Close()
	defer close(release)
	webhook = localWebhook(t, slow)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- webhook.Send(ctx, "cancellation fixture") }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("request did not reach isolated TLS receiver")
	}
	cancel()
	select {
	case err := <-result:
		privateError(t, err, slow.URL, "cancellation fixture")
	case <-time.After(2 * time.Second):
		t.Fatal("request cancellation did not bound completion")
	}
}

func TestHTTPSPayloadBoundsAndDeadlineDoNotDispatchInvalidSummary(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(204) }))
	defer server.Close()
	webhook := localWebhook(t, server)
	for _, body := range []string{"", strings.Repeat("x", 4097), strings.Repeat("\x00", 4096)} {
		if err := webhook.Send(context.Background(), body); err == nil {
			t.Fatal("empty, oversized or JSON-expansion payload dispatched")
		}
	}
	heartbeat, err := NewHeartbeat(server.URL+"/heartbeat", httpsCredential(t), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	heartbeat.http.client.Transport = server.Client().Transport
	valid := HeartbeatPayload{InstanceID: "fixture", At: time.Now().UTC(), Version: "candidate", Alive: true, Health: "healthy", Reasons: []string{}}
	for _, change := range []func(*HeartbeatPayload){
		func(p *HeartbeatPayload) { p.InstanceID = "" },
		func(p *HeartbeatPayload) { p.InstanceID = strings.Repeat("x", 65) },
		func(p *HeartbeatPayload) { p.At = time.Time{} },
		func(p *HeartbeatPayload) { p.Version = strings.Repeat("x", 129) },
		func(p *HeartbeatPayload) { p.Alive = false },
		func(p *HeartbeatPayload) { p.Health = "anything" },
		func(p *HeartbeatPayload) { p.Reasons = make([]string, 17) },
		func(p *HeartbeatPayload) { p.Reasons = []string{"error with endpoint"} },
		func(p *HeartbeatPayload) { p.Reasons = []string{strings.Repeat("x", 65)} },
	} {
		payload := valid
		change(&payload)
		if err := heartbeat.Send(context.Background(), payload); err == nil {
			t.Fatal("invalid or unbounded heartbeat summary dispatched")
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid payload reached the receiver")
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	privateError(t, heartbeat.Send(ctx, valid), server.URL)
	if calls.Load() != 0 {
		t.Fatal("expired request deadline reached the receiver")
	}
}

func TestHTTPSCredentialsAndWebhookIdentityConservativeRotation(t *testing.T) {
	path := httpsCredential(t)
	newSender := func(endpoint, receiver string) *Webhook {
		t.Helper()
		w, err := NewWebhook(endpoint, receiver, path, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		return w
	}
	first := newSender("https://localhost/a", "tenant-a")
	unchanged := newSender("https://localhost/a", "tenant-a")
	if first.Destination() != unchanged.Destination() || strings.Contains(first.Destination(), syntheticHTTPSBearer) || len(first.Destination()) != len("webhook:")+64 {
		t.Fatal("unchanged identity is unstable or secret-bearing")
	}
	if first.Destination() == newSender("https://localhost/b", "tenant-a").Destination() || first.Destination() == newSender("https://localhost/a", "tenant-b").Destination() {
		t.Fatal("URL or receiver change reused the old destination")
	}
	// Replacing credentials may select another tenant at the same endpoint.
	// Explicit metadata movement makes the test independent of filesystem clock
	// resolution and verifies the conservative generation boundary.
	if err := os.WriteFile(path, []byte("rotated-synthetic-bearer-1234567890"), 0o600); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
	if first.Destination() == newSender("https://localhost/a", "tenant-a").Destination() {
		t.Fatal("uncertain credential rotation revived the old destination")
	}
	for _, fixture := range []struct {
		name, contents string
		mode           os.FileMode
	}{
		{"public_mode", syntheticHTTPSBearer, 0o644},
		{"short", "too-short", 0o600},
		{"oversize", strings.Repeat("x", 513), 0o600},
		{"embedded_whitespace", "synthetic bearer with spaces", 0o600},
		{"non_ascii", strings.Repeat("中", 20), 0o600},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			bad := filepath.Join(t.TempDir(), "unsafe.token")
			if err := os.WriteFile(bad, []byte(fixture.contents), fixture.mode); err != nil {
				t.Fatal(err)
			}
			_, err := NewWebhook("https://localhost/hook", "fixture", bad, time.Second)
			privateError(t, err, fixture.contents, bad)
		})
	}
	for _, linked := range []bool{false, true} {
		link := filepath.Join(t.TempDir(), "link.token")
		var err error
		if linked {
			err = os.Link(path, link)
		} else {
			err = os.Symlink(path, link)
		}
		if err != nil {
			t.Fatal(err)
		}
		_, err = NewHeartbeat("https://localhost/heartbeat", link, time.Second)
		privateError(t, err, path, link)
	}
}

func openHTTPSOutbox(t *testing.T) *store.Store {
	t.Helper()
	db, err := store.OpenWithBudget(filepath.Join(t.TempDir(), "outbox.db"), store.BudgetConfig{MaxBytes: 64 << 20})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func bindHTTPSOutbox(db *store.Store, channel, target, privacy string) error {
	return db.ConfigureNotificationTarget(context.Background(), channel, target, privacy, true, time.Now().UTC())
}

func enqueueHTTPSOutbox(db *store.Store, id, channel, target, privacy, body string, next time.Time) error {
	inserted, err := db.Enqueue(context.Background(), store.OutboxMessage{ID: id, DedupeKey: id, Channel: channel, Destination: target, PrivacyMode: privacy, Body: body, NextAttempt: next})
	if err != nil {
		return err
	}
	if !inserted {
		return errors.New("fixture message was not admitted")
	}
	return nil
}

func TestWebhookWorkerOtherChannelBacklogCannotStarveDestination(t *testing.T) {
	var delivered atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { delivered.Add(1); w.WriteHeader(204) }))
	defer server.Close()
	webhook := localWebhook(t, server)
	db := openHTTPSOutbox(t)
	ctx := context.Background()
	telegram := "telegram:" + strings.Repeat("a", 64)
	if err := bindHTTPSOutbox(db, "telegram", telegram, "hash"); err != nil {
		t.Fatal(err)
	}
	if err := bindHTTPSOutbox(db, "webhook", webhook.Destination(), "hash"); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for i := 0; i < 20; i++ {
		if err := enqueueHTTPSOutbox(db, fmt.Sprintf("unavailable_%02d", i), "telegram", telegram, "hash", "retained for unavailable sender", now.Add(-time.Duration(30-i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	if err := enqueueHTTPSOutbox(db, "ready_webhook", "webhook", webhook.Destination(), "hash", "local ready message", now.Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := (&Worker{Store: db, Sender: webhook}).process(ctx); err != nil {
		t.Fatal(err)
	}
	if delivered.Load() != 1 || queueStatus(t, db).Pending != 20 {
		t.Fatal("other channel's first twenty retained messages starved a ready sender")
	}
	rows, err := db.Notifications(ctx, "", 30)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.ID == "ready_webhook" && row.State != "sent" || row.ID != "ready_webhook" && (row.State != "pending" || row.Attempts != 0) {
			t.Fatal("processing one channel changed another channel's backlog")
		}
	}
}

func TestWebhookWorkerTargetSwitchInFlightAndPrivacyTightening(t *testing.T) {
	db := openHTTPSOutbox(t)
	var deliveredA, deliveredB atomic.Int32
	failures := make(chan error, 2)
	var a, b *Webhook
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/a" {
			deliveredA.Add(1)
			if err := bindHTTPSOutbox(db, "webhook", b.Destination(), "hash"); err != nil {
				failures <- err
			} else if err := enqueueHTTPSOutbox(db, "new_B", "webhook", b.Destination(), "hash", "hash-only new summary", time.Now().Add(-time.Second)); err != nil {
				failures <- err
			}
		} else {
			deliveredB.Add(1)
			data, _ := io.ReadAll(io.LimitReader(r.Body, 16<<10))
			if strings.Contains(string(data), "2001:db8::1234") || strings.Contains(string(data), "old_A") {
				failures <- errors.New("old target or full source reached replacement receiver")
			}
		}
		w.WriteHeader(204)
	}))
	defer server.Close()
	credential := httpsCredential(t)
	var err error
	a, err = NewWebhook(server.URL+"/a", "A", credential, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	b, err = NewWebhook(server.URL+"/b", "B", credential, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	a.http.client.Transport, b.http.client.Transport = server.Client().Transport, server.Client().Transport
	if err := bindHTTPSOutbox(db, "webhook", a.Destination(), "full"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"old_A_1", "old_A_2"} {
		if err := enqueueHTTPSOutbox(db, id, "webhook", a.Destination(), "full", id+" 2001:db8::1234", time.Now().Add(-time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	if err := (&Worker{Store: db, Sender: a}).process(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := (&Worker{Store: db, Sender: b}).process(context.Background()); err != nil {
		t.Fatal(err)
	}
	if deliveredA.Load() != 1 || deliveredB.Load() != 1 || queueStatus(t, db).Isolated != 1 {
		t.Fatal("in-flight target change dispatched an old prefetched body")
	}
	// A claimed message must be revoked on a same-target privacy tightening,
	// independent of restarting or changing the sender's immutable identity.
	if err := bindHTTPSOutbox(db, "webhook", b.Destination(), "full"); err != nil {
		t.Fatal(err)
	}
	if err := enqueueHTTPSOutbox(db, "claimed_full", "webhook", b.Destination(), "full", "2001:db8::1234", time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := db.ClaimNotification(context.Background(), "claimed_full", time.Now()); err != nil || !ok {
		t.Fatal("fixture claim failed", err)
	}
	if err := bindHTTPSOutbox(db, "webhook", b.Destination(), "hash"); err != nil {
		t.Fatal(err)
	}
	if allowed, err := db.NotificationDeliveryAllowed(context.Background(), "claimed_full", b.Destination()); err != nil || allowed {
		t.Fatal("privacy tightening retained permission for an old claim", err)
	}
	if err := (&Worker{Store: db, Sender: b}).process(context.Background()); err != nil {
		t.Fatal(err)
	}
	if deliveredB.Load() != 1 || queueStatus(t, db).Isolated != 2 {
		t.Fatal("privacy tightening dispatched a retained full body")
	}
	select {
	case err := <-failures:
		t.Fatal(err)
	default:
	}
}

func TestWebhookWorkerOversizedRateLimitPreservesDestinationCooldown(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "3600")
		w.WriteHeader(429)
		_, _ = io.WriteString(w, strings.Repeat("s", maxTelegramResponseBytes+1))
	}))
	defer server.Close()
	webhook := localWebhook(t, server)
	db := openHTTPSOutbox(t)
	if err := bindHTTPSOutbox(db, "webhook", webhook.Destination(), "hash"); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, id := range []string{"limited_first", "limited_second"} {
		if err := enqueueHTTPSOutbox(db, id, "webhook", webhook.Destination(), "hash", "rate-limit fixture", now.Add(-time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	worker := &Worker{Store: db, Sender: webhook}
	for i := 0; i < 2; i++ {
		if err := worker.process(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	status := queueStatus(t, db)
	if calls.Load() != 1 || len(status.Cooldowns) != 1 || status.Cooldowns[0].Until.Before(now.Add(time.Hour)) || status.Pending != 2 {
		t.Fatal("bounded response reading lost the destination-wide rate limit")
	}
}

func TestWebhookCredentialGenerationIsolatesBacklogAcrossRestart(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		data, _ := io.ReadAll(io.LimitReader(r.Body, 16<<10))
		if strings.Contains(string(data), "old credential body") {
			w.WriteHeader(403)
			return
		}
		w.WriteHeader(204)
	}))
	defer server.Close()
	credential := httpsCredential(t)
	old, err := NewWebhook(server.URL+"/hook", "same-receiver", credential, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "restart.db")
	db, err := store.OpenWithBudget(path, store.BudgetConfig{MaxBytes: 64 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err := bindHTTPSOutbox(db, "webhook", old.Destination(), "prefix"); err != nil {
		t.Fatal(err)
	}
	if err := enqueueHTTPSOutbox(db, "old_credentials", "webhook", old.Destination(), "prefix", "old credential body", time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	// A manual credential-file edit is detected at the next startup without UI.
	if err := os.WriteFile(credential, []byte("new-synthetic-bearer-123456789012"), 0o600); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(credential, at, at); err != nil {
		t.Fatal(err)
	}
	current, err := NewWebhook(server.URL+"/hook", "same-receiver", credential, time.Second)
	if err != nil || old.Destination() == current.Destination() {
		t.Fatal("uncertain credential generation did not change target", err)
	}
	current.http.client.Transport = server.Client().Transport
	db, err = store.OpenWithBudget(path, store.BudgetConfig{MaxBytes: 64 << 20})
	if err != nil {
		t.Fatal(err)
	}
	if err := bindHTTPSOutbox(db, "webhook", current.Destination(), "prefix"); err != nil {
		t.Fatal(err)
	}
	if err := enqueueHTTPSOutbox(db, "new_credentials", "webhook", current.Destination(), "prefix", "new credential summary", time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := (&Worker{Store: db, Sender: current}).process(context.Background()); err != nil {
		t.Fatal(err)
	}
	status := queueStatus(t, db)
	if calls.Load() != 1 || status.Isolated != 1 || status.Pending != 1 {
		t.Fatal("manual credential rotation reused or deleted retained old backlog")
	}
}
