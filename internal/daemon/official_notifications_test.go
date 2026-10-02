// SPDX-License-Identifier: MIT

package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/api"
	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/model"
	"github.com/littlesho/NodeRampart/internal/notify"
	"github.com/littlesho/NodeRampart/internal/store"
)

type officialAppFixtureSender struct {
	destination string
	reject      bool
}

func (s officialAppFixtureSender) Send(context.Context, string) error {
	return errors.New("synthetic sender must not be called while saving or previewing")
}
func (s officialAppFixtureSender) Destination() string      { return s.destination }
func (s officialAppFixtureSender) RecipientPreview() string { return "***0142" }
func (s officialAppFixtureSender) PrepareMessage(m *store.OutboxMessage) error {
	if s.reject {
		return errors.New("synthetic local render refusal")
	}
	if m.SemanticPayload == "" {
		return errors.New("semantic context missing")
	}
	data, _ := json.Marshal(map[string]string{"body": m.Body, "kind": m.LogicalKind})
	m.FrozenPayload = string(data)
	if m.Channel == "twilio_sms" {
		m.EstimatedSegments = 1
		m.Encoding = "gsm7"
	}
	return nil
}

func officialTestApp(t *testing.T) *App {
	t.Helper()
	a := nativeTestApp(t)
	o := a.options
	o.OfficialNotifiers = map[string]notify.Sender{}
	o.OfficialDestinations = map[string]string{}
	for i, channel := range config.OfficialChannelNames() {
		c := o.Config.Notifications.OfficialChannels()[channel]
		c.Enabled = true
		if i%2 != 0 {
			c.Language = "zh"
		}
		c.Subscription = config.OfficialSubscription{ConfirmedAt: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano), Purpose: "synthetic tests", NotificationTypes: []string{"event", "daily", "test"}, EvidenceRef: "synthetic-consent", BasisID: strings.Repeat("b", 32), CostConfirmed: config.IsPaidOfficialChannel(channel)}
		_ = o.Config.Notifications.SetOfficialChannel(channel, c)
		dest := channel + ":" + strings.Repeat(string(rune('a'+i)), 64)
		o.OfficialNotifiers[channel] = officialAppFixtureSender{destination: dest}
		o.OfficialDestinations[channel] = dest
	}
	result, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestTwelveChannelEventPhasesAndIndependentAdmission(t *testing.T) {
	a := officialTestApp(t)
	ctx := context.Background()
	for _, phase := range []string{"start", "update", "recovery"} {
		e := model.Event{ID: "twelve_" + phase, IncidentID: "twelve_incident", Kind: "ssh_brute_force", Severity: model.SeverityHigh, Phase: phase, ObservedAt: time.Now().UTC(), Count: 7}
		stored, m := a.prepareEvent(e)
		n := 0
		for row := m; row != nil; row = row.Secondary {
			n++
			if config.IsOfficialChannel(row.Channel) && (row.LogicalKind != "event" || row.FrozenPayload == "" || row.Language != config.OfficialChannelLanguage(a.options.Config.Notifications.OfficialChannels()[row.Channel])) {
				t.Fatal("missing frozen official event context", row.Channel)
			}
		}
		if n != 12 {
			t.Fatal("incomplete fanout", n)
		}
		if err := a.options.Store.InsertEventNotification(ctx, stored, m); err != nil {
			t.Fatal(err)
		}
		if err := a.options.Store.InsertEventNotification(ctx, stored, m); err != nil {
			t.Fatal(err)
		}
	}
	for _, channel := range config.OfficialChannelNames() {
		rows, err := a.options.Store.PendingDestination(ctx, time.Now().Add(time.Minute), 20, a.options.OfficialDestinations[channel])
		if err != nil || len(rows) != 3 {
			t.Fatal("official independent dedupe", channel, len(rows), err)
		}
	}
}

func TestOfficialLocalRenderRefusalDoesNotBlockEventCommit(t *testing.T) {
	a := officialTestApp(t)
	a.options.OfficialNotifiers["twilio_sms"] = officialAppFixtureSender{destination: a.options.OfficialDestinations["twilio_sms"], reject: true}
	e, m := a.prepareEvent(model.Event{ID: "render_refused", Kind: "health_storage", Severity: model.SeverityCritical, ObservedAt: time.Now().UTC()})
	if err := a.options.Store.InsertEventNotification(context.Background(), e, m); err != nil {
		t.Fatal("optional render rejection blocked local commit", err)
	}
	rows, err := a.options.Store.Notifications(context.Background(), "", 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range rows {
		if row.Channel == "twilio_sms" {
			found = row.State == "blocked" && row.LastError == "official_render_rejected"
		}
	}
	if !found {
		t.Fatal("render refusal silently dropped", rows)
	}
}

func TestPaidPreviewThenExplicitTestIsOneLocalQueueAdmission(t *testing.T) {
	a := officialTestApp(t)
	ctx := context.Background()
	before, err := a.options.Store.QueueStatus(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	response := controlRequest(t, a, "notify_preview", api.NotifyChannelArgs{Channel: "twilio_sms"})
	if !response.OK {
		t.Fatal(response.Error)
	}
	data, _ := json.Marshal(response.Data)
	var preview struct {
		PreviewID   string `json:"preview_id"`
		NetworkSent bool   `json:"network_sent"`
		Target      string `json:"target"`
	}
	if json.Unmarshal(data, &preview) != nil || len(preview.PreviewID) != 64 || preview.NetworkSent || preview.Target != "***0142" {
		t.Fatal("unsafe preview", string(data))
	}
	after, _ := a.options.Store.QueueStatus(ctx, time.Now())
	if after.Pending != before.Pending {
		t.Fatal("preview queued a message")
	}
	for _, args := range []api.NotifyChannelArgs{{Channel: "twilio_sms"}, {Channel: "twilio_sms", ConfirmPaid: true, PreviewID: strings.Repeat("f", 64)}} {
		if r := controlRequest(t, a, "notify_test", args); r.OK {
			t.Fatal("paid test bypassed preview confirmation")
		}
	}
	if r := controlRequest(t, a, "notify_test", api.NotifyChannelArgs{Channel: "twilio_sms", ConfirmPaid: true, PreviewID: preview.PreviewID}); !r.OK {
		t.Fatal(r.Error)
	}
	after, _ = a.options.Store.QueueStatus(ctx, time.Now())
	if after.Pending != before.Pending+1 {
		t.Fatal("test fanned out or did not queue")
	}
	policy, _ := a.options.Store.OfficialChannelStatus(ctx, "twilio_sms", time.Now())
	if policy.MessagesReserved != 0 {
		t.Fatal("preview/test enqueue billed before worker intent")
	}
	c := a.options.Config.Notifications.TwilioSMS
	c.DailyMessageLimit = 19
	a.options.Config.Notifications.TwilioSMS = c
	if r := controlRequest(t, a, "notify_test", api.NotifyChannelArgs{Channel: "twilio_sms", ConfirmPaid: true, PreviewID: preview.PreviewID}); r.OK {
		t.Fatal("old preview accepted after policy changed")
	}
}

func TestOfficialSelectionAndActivationDoNotBackfill(t *testing.T) {
	a := officialTestApp(t)
	e := model.Event{ID: "old_official", Kind: "ssh_brute_force", Severity: model.SeverityHigh, ObservedAt: time.Now().Add(-time.Hour).UTC()}
	_, m := a.prepareEvent(e)
	for row := m; row != nil; row = row.Secondary {
		if config.IsOfficialChannel(row.Channel) {
			t.Fatal("official historical backlog admitted")
		}
	}
	c := a.options.Config.Notifications.TwilioSMS
	c.EventsEnabled = false
	a.options.Config.Notifications.TwilioSMS = c
	_, m = a.prepareEvent(model.Event{ID: "new_selection", Kind: "ssh_brute_force", Severity: model.SeverityMedium, ObservedAt: time.Now().UTC()})
	for row := m; row != nil; row = row.Secondary {
		if config.IsPaidOfficialChannel(row.Channel) {
			t.Fatal("paid high-severity selection ignored")
		}
	}
}
