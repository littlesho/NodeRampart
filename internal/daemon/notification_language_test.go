// SPDX-License-Identifier: MIT

package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/api"
	"github.com/littlesho/NodeRampart/internal/model"
	"github.com/littlesho/NodeRampart/internal/notify"
	"github.com/littlesho/NodeRampart/internal/privacy"
)

func TestSavedTelegramLanguageAndTimezoneArePerChannel(t *testing.T) {
	base := eventTestApp(t)
	options := base.options
	options.Config.Reports.Timezone = "Asia/Kathmandu"
	options.Config.Notifications.Webhook.Enabled = true
	options.WebhookDestination = "webhook:" + strings.Repeat("b", 64)
	options.WebhookNotifier = unusedEventSender{}
	ctx := context.Background()
	at := time.Date(2026, 1, 15, 4, 0, 0, 0, time.UTC)
	for i, language := range []string{"en", "zh", "en"} {
		// New represents the real reload/restart boundary, with the same database/target.
		options.Config.Notifications.Telegram.Language = language
		app, err := New(options)
		if err != nil {
			t.Fatal(err)
		}
		event := model.Event{ID: fmt.Sprintf("localized_%d", i), IncidentID: fmt.Sprintf("incident_%d", i), Kind: "ssh_login_success", Phase: "observed", Severity: model.SeverityMedium, ObservedAt: at, SourceIP: "2001:db8::1234", Count: 1, Summary: "Login observed", Evidence: map[string]string{"user": "synthetic<user>", "method": "publickey", "window_millis": "60000"}}
		stored, message := app.prepareEvent(event)
		if message == nil || message.Secondary == nil {
			t.Fatal("lost paired notification")
		}
		if message.Language != language || message.Timezone != "Asia/Kathmandu|UTC+05:45" || !strings.Contains(message.Body, "09:45:00") || !strings.Contains(message.Body, "UTC+05:45") {
			t.Fatal("wrong event civil time/presentation", message)
		}
		if message.Secondary.Body != notify.FormatEvent(options.Config.Hostname, stored) {
			t.Fatal("Telegram language changed Webhook", message.Secondary.Body)
		}
		if strings.Contains(message.Secondary.Body, "主机") || strings.Contains(message.Body, event.SourceIP) || strings.Contains(message.Secondary.Body, event.SourceIP) {
			t.Fatal("channel rendering or privacy crossed")
		}
		if stored.Kind != event.Kind || stored.Severity != event.Severity || !stored.ObservedAt.Equal(at) || stored.Summary != event.Summary || stored.Evidence["method"] != "publickey" {
			t.Fatal("localization changed machine event", stored)
		}
		if err := options.Store.InsertEventNotification(ctx, stored, message); err != nil {
			t.Fatal(err)
		}
		if response := controlRequest(t, app, "notify_test", api.NotifyChannelArgs{Channel: "telegram"}); !response.OK {
			t.Fatal(response.Error)
		}
		if response := controlRequest(t, app, "notify_test", api.NotifyChannelArgs{Channel: "webhook"}); !response.OK {
			t.Fatal(response.Error)
		}
	}
	pending, err := options.Store.Pending(ctx, time.Now().Add(time.Second), 20)
	if err != nil || len(pending) != 12 {
		t.Fatal("restart dropped or isolated language backlog", len(pending), err)
	}
	zh := 0
	for _, message := range pending {
		if message.Language == "zh" {
			zh++
			if !strings.Contains(message.Body, "主机") {
				t.Fatal("new Chinese test/event not localized", message.Body)
			}
		}
	}
	if zh != 2 {
		t.Fatal("old backlog was rerendered", zh)
	}
}

func TestLocalizedRenderingUsesNotificationPrivacyAndEventDSTOffset(t *testing.T) {
	base := eventTestApp(t)
	options := base.options
	options.Config.Notifications.Telegram.Language = "zh"
	options.Config.Reports.Timezone = "America/New_York"
	for _, mode := range []string{"full", "prefix", "hash"} {
		key := ""
		if mode == "hash" {
			key = filepath.Join(t.TempDir(), "synthetic.key")
			if err := os.WriteFile(key, []byte(strings.Repeat("synthetic-", 8)), 0600); err != nil {
				t.Fatal(err)
			}
		}
		transformer, err := privacy.New(mode, key)
		if err != nil {
			t.Fatal(err)
		}
		options.Config.Privacy.NotificationIP = mode
		options.NotifyPrivacy = transformer
		app, err := New(options)
		if err != nil {
			t.Fatal(err)
		}
		for _, date := range []struct {
			month  time.Month
			offset string
		}{{time.January, "UTC-05:00"}, {time.July, "UTC-04:00"}} {
			event := model.Event{Kind: "ssh_login_success", Phase: "observed", Severity: model.SeverityMedium, ObservedAt: time.Date(2026, date.month, 15, 12, 0, 0, 0, time.UTC), SourceIP: "192.0.2.19", Count: 1, Evidence: map[string]string{"user": "synthetic", "method": "password"}}
			_, message := app.prepareEvent(event)
			if !strings.Contains(message.Body, date.offset) {
				t.Fatal("used current offset instead of event offset", message.Body)
			}
			if mode != "full" && strings.Contains(message.Body, "192.0.2.19") {
				t.Fatal("Chinese rendering bypassed privacy", mode)
			}
		}
	}
}

func TestInvalidNotificationPresentationCannotChangeRecipientPolicy(t *testing.T) {
	base := eventTestApp(t)
	ctx := context.Background()
	_, message := base.prepareEvent(model.Event{ID: "presentation_existing", Kind: "ssh_login_success", Phase: "observed", Severity: model.SeverityMedium})
	if ok, err := base.options.Store.Enqueue(ctx, *message); err != nil || !ok {
		t.Fatal(ok, err)
	}
	for _, item := range []struct{ language, timezone string }{{"fr", "UTC"}, {"en", ""}, {"zh", "No/Rules"}} {
		options := base.options
		options.Config.Notifications.Telegram.Language = item.language
		options.Config.Reports.Timezone = item.timezone
		options.NotificationDestination = "telegram:" + strings.Repeat("b", 64)
		if _, err := New(options); err == nil {
			t.Fatal("invalid presentation accepted", item)
		}
		pending, err := base.options.Store.Pending(ctx, time.Now().Add(time.Second), 20)
		if err != nil || len(pending) != 1 || pending[0].ID != message.ID {
			t.Fatal("invalid config changed target/isolation", item, pending, err)
		}
	}
}
