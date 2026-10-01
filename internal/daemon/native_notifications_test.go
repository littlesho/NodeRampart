// SPDX-License-Identifier: MIT

package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/api"
	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/model"
	"github.com/littlesho/NodeRampart/internal/notify"
)

func nativeTestApp(t *testing.T) *App {
	t.Helper()
	base := eventTestApp(t)
	o := base.options
	o.NativeNotifiers = map[string]notify.Sender{}
	o.NativeDestinations = map[string]string{}
	for i, channel := range config.NativeChannelNames() {
		n := o.Config.Notifications.NativeChannels()[channel]
		n.Enabled = true
		if i%2 == 1 {
			n.Language = "zh"
		}
		_ = o.Config.Notifications.SetNativeChannel(channel, n)
		o.NativeNotifiers[channel] = unusedEventSender{}
		o.NativeDestinations[channel] = channel + ":" + strings.Repeat(string(rune('a'+i)), 64)
	}
	o.Config.Notifications.Webhook.Enabled = true
	o.WebhookDestination = "webhook:" + strings.Repeat("f", 64)
	o.WebhookNotifier = unusedEventSender{}
	a, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestNativeActivationKeepsHistoricalJournalEventsLocal(t *testing.T) {
	a := nativeTestApp(t)
	ctx := context.Background()
	old := model.Event{ID: "journal_before_native_enable", Kind: "ssh_auth", Phase: "start", Severity: model.SeverityHigh, ObservedAt: time.Now().Add(-time.Hour).UTC()}
	stored, m := a.prepareEvent(old)
	for row := m; row != nil; row = row.Secondary {
		if config.IsNativeChannel(row.Channel) {
			t.Fatal("new native target admitted historic journal event")
		}
	}
	if err := a.options.Store.InsertEventNotification(ctx, stored, m); err != nil {
		t.Fatal(err)
	}
	for _, channel := range config.NativeChannelNames() {
		rows, err := a.options.Store.PendingDestination(ctx, time.Now().Add(time.Minute), 20, a.options.NativeDestinations[channel])
		if err != nil || len(rows) != 0 {
			t.Fatal("historical native queue was not empty", channel, err)
		}
	}
	// Recreating the daemon with unchanged enabled targets retains the original
	// cutover, so a newly observed event is still eligible after restart.
	restarted, err := New(a.options)
	if err != nil {
		t.Fatal(err)
	}
	if restarted.options.nativeActivatedAt["slack"] != a.options.nativeActivatedAt["slack"] {
		t.Fatal("restart moved activation boundary")
	}
	_, m = restarted.prepareEvent(model.Event{ID: "journal_after_native_enable", Kind: "ssh_auth", Severity: model.SeverityHigh, ObservedAt: time.Now().UTC()})
	count := 0
	for row := m; row != nil; row = row.Secondary {
		if config.IsNativeChannel(row.Channel) {
			count++
		}
	}
	if count != 6 {
		t.Fatal("fresh native event omitted after restart", count)
	}
}

func TestNativeIdentityFallbackEnforcesConfigurationDirectory(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		for _, failure := range []bool{false, true} {
			base := eventTestApp(t)
			o := base.options
			o.ConfigDirectory = t.TempDir()
			o.NativeDestinations = nil
			path := filepath.Join(t.TempDir(), "slack.credential.json")
			if err := os.WriteFile(path, []byte(`{"url":"https://hooks.slack.com/services/T00000000/B00000000/SyntheticToken0000"}`), 0600); err != nil {
				t.Fatal(err)
			}
			cfg := o.Config.Notifications.Slack
			cfg.Enabled, cfg.CredentialFile = enabled, path
			o.Config.Notifications.Slack = cfg
			if failure {
				o.OptionalFailures = map[string]string{"slack": "slack_credentials_unavailable"}
			}
			a, err := New(o)
			if err != nil {
				t.Fatal(err)
			}
			if a.options.NativeDestinations["slack"] != "slack:unknown" {
				t.Fatal("outside-directory credential was inspected for fallback")
			}
			// A permitted disabled reference can retain its known target identity;
			// an explicit failed-load signal must never be bypassed by rereading.
			inside := filepath.Join(o.ConfigDirectory, "slack.credential.json")
			data, _ := os.ReadFile(path)
			if err := os.WriteFile(inside, data, 0600); err != nil {
				t.Fatal(err)
			}
			o.Config.Notifications.Slack.CredentialFile = inside
			a, err = New(o)
			if err != nil {
				t.Fatal(err)
			}
			if (a.options.NativeDestinations["slack"] == "slack:unknown") != failure {
				t.Fatal("permitted identity or fail-closed signal lost")
			}
		}
	}
}

func TestEightChannelsEventAndRecoveryAdmissionIndependent(t *testing.T) {
	a := nativeTestApp(t)
	ctx := context.Background()
	at := time.Now().UTC()
	for _, phase := range []string{"start", "recovery"} {
		e := model.Event{ID: "eight_" + phase, Kind: "health_storage", Phase: phase, Severity: model.SeverityHigh, ObservedAt: at, Evidence: map[string]string{"reason": "storage_pressure", "coverage": "incomplete"}}
		stored, message := a.prepareEvent(e)
		seen := map[string]bool{}
		for m := message; m != nil; m = m.Secondary {
			if seen[m.Channel] {
				t.Fatal("duplicate channel")
			}
			seen[m.Channel] = true
			if config.IsNativeChannel(m.Channel) && (len(m.Body) > 1800 || !strings.Contains(m.Body, "sudo noderampart events show --id "+e.ID)) {
				t.Fatal("unsafe native summary")
			}
		}
		if len(seen) != 8 {
			t.Fatalf("missing event channels: %v", seen)
		}
		if err := a.options.Store.InsertEventNotification(ctx, stored, message); err != nil {
			t.Fatal(err)
		}
	}
	for _, target := range a.notificationTargets() {
		pending, err := a.options.Store.PendingDestination(ctx, at.Add(time.Minute), 20, target.destination)
		if err != nil || len(pending) != 2 {
			t.Fatalf("%s independent admission: %d %v", target.channel, len(pending), err)
		}
	}
	// One target changes while seven retain their own pending records.
	if err := a.options.Store.ConfigureNotificationTarget(ctx, "feishu", "feishu:"+strings.Repeat("f", 64), "prefix", true, at.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	for _, target := range a.notificationTargets() {
		pending, err := a.options.Store.PendingDestination(ctx, at.Add(time.Minute), 20, target.destination)
		want := 2
		if target.channel == "feishu" {
			want = 0
		}
		if err != nil || len(pending) != want {
			t.Fatalf("%s isolation affected other target", target.channel)
		}
	}
}

func TestNativeTestExplicitSelectionAndFrozenPresentation(t *testing.T) {
	a := nativeTestApp(t)
	for _, channel := range config.NativeChannelNames() {
		before, _ := a.options.Store.QueueStatus(context.Background(), time.Now().UTC())
		if r := controlRequest(t, a, "notify_test", api.NotifyChannelArgs{Channel: channel}); !r.OK {
			t.Fatalf("%s test rejected: %+v", channel, r)
		}
		after, _ := a.options.Store.QueueStatus(context.Background(), time.Now().UTC())
		if after.Pending != before.Pending+1 {
			t.Fatal("test affected more than selected target")
		}
		messages, err := a.options.Store.PendingDestination(context.Background(), time.Now().Add(time.Minute), 20, a.options.NativeDestinations[channel])
		if err != nil || len(messages) != 1 {
			t.Fatal("test target not bound", err)
		}
		old := messages[0]
		n := a.options.Config.Notifications.NativeChannels()[channel]
		n.Language = "zh"
		_ = a.options.Config.Notifications.SetNativeChannel(channel, n)
		again, _ := a.options.Store.PendingDestination(context.Background(), time.Now().Add(time.Minute), 20, a.options.NativeDestinations[channel])
		if again[0].Body != old.Body || again[0].Language != old.Language {
			t.Fatal("presentation mutated queued body")
		}
	}
	data, _ := json.Marshal(a.Status(context.Background()))
	for _, secret := range []string{"https://hooks.", "signature-secret"} {
		if strings.Contains(string(data), secret) {
			t.Fatal("status leaked credentials")
		}
	}
}

func TestNativeUnavailableAndDisabledChannelDoNotStopMonitoring(t *testing.T) {
	a := nativeTestApp(t)
	o := a.options
	o.NativeNotifiers["wecom"] = nil
	o.OptionalFailures = map[string]string{"wecom": "wecom_credentials_unavailable"}
	degraded, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	_, m := degraded.prepareEvent(model.Event{ID: "degraded_native", Kind: "syn_flood", Severity: model.SeverityHigh, ObservedAt: time.Now().UTC()})
	seen := 0
	for ; m != nil; m = m.Secondary {
		seen++
	}
	if seen != 8 {
		t.Fatal("optional failure suppressed healthy channels")
	}
	for _, channel := range config.NativeChannelNames() {
		n := o.Config.Notifications.NativeChannels()[channel]
		n.Enabled = false
		_ = o.Config.Notifications.SetNativeChannel(channel, n)
	}
	o.Config.Notifications.Telegram.Enabled = false
	o.Config.Notifications.Webhook.Enabled = false
	disabled, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	_, m = disabled.prepareEvent(model.Event{ID: "all_disabled_native", Kind: "syn_flood", Severity: model.SeverityHigh, ObservedAt: time.Now().UTC()})
	if m != nil {
		t.Fatal("disabled channels admitted outbound work")
	}
}
