// SPDX-License-Identifier: MIT

package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/api"
	"github.com/littlesho/NodeRampart/internal/model"
	"github.com/littlesho/NodeRampart/internal/notify"
	"github.com/littlesho/NodeRampart/internal/store"
)

func TestDaemonWebhookAdmissionIsolationAndGlobalSilence(t *testing.T) {
	a := eventTestApp(t)
	options := a.options
	options.Config.Notifications.Webhook.Enabled = true
	options.WebhookDestination = "webhook:" + strings.Repeat("b", 64)
	options.WebhookNotifier = nil // Enabled intent survives local sender absence.
	paired, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	at := time.Now().UTC().Truncate(time.Millisecond)
	event := model.Event{ID: "paired_event", IncidentID: "paired_incident", ObservedAt: at, Kind: "ssh_login_success", Phase: "observed", Severity: model.SeverityMedium, SourceIP: "2001:db8::1234", Summary: "Synthetic login"}
	stored, message := paired.prepareEvent(event)
	if message == nil || message.Channel != "telegram" || message.Secondary == nil || message.Secondary.Channel != "webhook" || message.ID == message.Secondary.ID || strings.Contains(message.Secondary.Body, event.SourceIP) {
		t.Fatal("paired admission or privacy failed")
	}
	if err := options.Store.InsertEventNotification(ctx, stored, message); err != nil {
		t.Fatal(err)
	}
	page, err := options.Store.Timeline(ctx, store.TimelineQuery{Start: at.Add(-time.Minute), End: at.Add(time.Minute), Limit: 20})
	if err != nil || len(page.Events) != 1 {
		t.Fatal("multi-channel event duplicated", len(page.Events), err)
	}
	if err := options.Store.ConfigureNotificationTarget(ctx, "webhook", "webhook:"+strings.Repeat("c", 64), "prefix", true, at.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	telegram, err := options.Store.PendingDestination(ctx, at.Add(time.Minute), 20, options.NotificationDestination)
	webhook, otherErr := options.Store.PendingDestination(ctx, at.Add(time.Minute), 20, options.WebhookDestination)
	if err != nil || otherErr != nil || len(telegram) != 1 || len(webhook) != 0 {
		t.Fatal("target switch affected another channel", err, otherErr)
	}
	if _, err := options.Store.AddSilence(ctx, store.Silence{ID: "paired_silence", IncidentID: event.IncidentID, CreatedAt: at, ExpiresAt: at.Add(time.Hour)}, at); err != nil {
		t.Fatal(err)
	}
	pending, err := options.Store.Pending(ctx, at.Add(time.Minute), 20)
	if err != nil || len(pending) != 0 {
		t.Fatal("global silence did not suppress both targets", err)
	}
}

func TestControlWebhookTestAndSelectedIsolatedDiscard(t *testing.T) {
	a := eventTestApp(t)
	options := a.options
	options.Config.Notifications.Webhook.Enabled = true
	options.WebhookNotifier = unusedEventSender{}
	options.WebhookDestination = "webhook:" + strings.Repeat("b", 64)
	a, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	if response := controlRequest(t, a, "notify_test", api.NotifyChannelArgs{Channel: "webhook"}); !response.OK {
		t.Fatal(response.Error)
	}
	status, err := options.Store.QueueStatus(context.Background(), time.Now().UTC())
	if err != nil || status.Pending != 1 {
		t.Fatal("test not queued", err)
	}
	if err := options.Store.ConfigureNotificationTarget(context.Background(), "webhook", "webhook:"+strings.Repeat("c", 64), "prefix", true, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if response := controlRequest(t, a, "notify_discard_isolated", api.NotifyChannelArgs{Channel: "telegram"}); !response.OK {
		t.Fatal(response.Error)
	}
	status, _ = options.Store.QueueStatus(context.Background(), time.Now().UTC())
	if status.Isolated != 1 {
		t.Fatal("wrong channel discarded")
	}
	if response := controlRequest(t, a, "notify_discard_isolated", api.NotifyChannelArgs{Channel: "webhook"}); !response.OK {
		t.Fatal(response.Error)
	}
	status, _ = options.Store.QueueStatus(context.Background(), time.Now().UTC())
	if status.Isolated != 0 || status.Suppressed != 1 {
		t.Fatal("explicit selected discard missing")
	}
	if response := controlRequest(t, a, "notify_test", api.NotifyChannelArgs{Channel: "arbitrary"}); response.OK {
		t.Fatal("unknown channel accepted")
	}
}

type heartbeatFunc func(context.Context, notify.HeartbeatPayload) error

func (f heartbeatFunc) Send(ctx context.Context, p notify.HeartbeatPayload) error { return f(ctx, p) }

func TestHeartbeatSummarySeparatesLivenessAndHealth(t *testing.T) {
	a := eventTestApp(t)
	a.options.Config.Heartbeat.InstanceID = "synthetic-instance"
	var received notify.HeartbeatPayload
	a.options.HeartbeatSender = heartbeatFunc(func(_ context.Context, p notify.HeartbeatPayload) error { received = p; return nil })
	if err := a.sendHeartbeat(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !received.Alive || received.Health == "healthy" || len(received.Reasons) == 0 || received.InstanceID != "synthetic-instance" {
		t.Fatal("unready monitoring presented as healthy", received)
	}
	data, err := json.Marshal(received)
	if err != nil || strings.Contains(string(data), "token") || strings.Contains(string(data), "Source") || len(data) > 4096 {
		t.Fatal("nonminimal heartbeat payload")
	}
}

func TestHeartbeatRetryIsBoundedAndCancellationJoins(t *testing.T) {
	a := eventTestApp(t)
	a.options.Config.Heartbeat.InstanceID = "synthetic-instance"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var attempts atomic.Int32
	second := make(chan struct{})
	a.options.HeartbeatSender = heartbeatFunc(func(ctx context.Context, _ notify.HeartbeatPayload) error {
		if attempts.Add(1) == 2 {
			close(second)
		}
		return errors.New("synthetic delivery failure")
	})
	done := make(chan error, 1)
	go func() { done <- a.runHeartbeat(ctx) }()
	select {
	case <-second:
	case <-time.After(3 * time.Second):
		t.Fatal("bounded retry did not execute")
	}
	// Observe the actual persisted failure marker, without assuming timer order.
	deadline := time.Now().Add(time.Second)
	for {
		components, err := a.options.Store.ComponentStatuses(context.Background())
		degraded := false
		for _, component := range components {
			degraded = degraded || component.Name == "heartbeat" && component.State == "degraded"
		}
		if err == nil && degraded {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("heartbeat failure was not reported")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("heartbeat worker did not join")
	}
	if attempts.Load() != 2 {
		t.Fatal("unbounded heartbeat retries", attempts.Load())
	}
	queue, err := a.options.Store.QueueStatus(context.Background(), time.Now().UTC())
	if err != nil || queue.Pending != 0 {
		t.Fatal("heartbeat formed an outbox backlog", err)
	}
}

func TestHeartbeatHonorsRateLimitWithoutImmediateRetry(t *testing.T) {
	a := eventTestApp(t)
	a.options.Config.Heartbeat.InstanceID = "synthetic-instance"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sent := make(chan struct{})
	var calls atomic.Int32
	a.options.HeartbeatSender = heartbeatFunc(func(context.Context, notify.HeartbeatPayload) error {
		if calls.Add(1) == 1 {
			close(sent)
		}
		return &notify.DeliveryError{Channel: "webhook", StatusCode: 429, RateLimited: true, RetryAfter: time.Hour}
	})
	done := make(chan error, 1)
	go func() { done <- a.runHeartbeat(ctx) }()
	select {
	case <-sent:
	case <-time.After(time.Second):
		t.Fatal("no first heartbeat")
	}
	deadline := time.Now().Add(time.Second)
	for {
		statuses, err := a.options.Store.ComponentStatuses(context.Background())
		reported := false
		for _, status := range statuses {
			reported = reported || status.Name == "heartbeat" && status.State == "degraded"
		}
		if err == nil && reported {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("rate limit not reported")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("rate limited worker did not join")
	}
	if calls.Load() != 1 {
		t.Fatal("rate limit bypassed", calls.Load())
	}
}
