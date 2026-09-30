// SPDX-License-Identifier: MIT

package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/model"
	"github.com/littlesho/NodeRampart/internal/notify"
)

func TestDaemonNotificationAdmissionSurvivesUnavailableSender(t *testing.T) {
	app := eventTestApp(t)
	options := app.options
	options.Notifier = nil
	options.NotificationDestination = ""
	options.Config.Notifications.Telegram.TokenFile = filepath.Join(t.TempDir(), "unavailable.token")
	options.Config.Notifications.Telegram.ChatID = "-100123"
	degraded, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	_, message := degraded.prepareEvent(model.Event{ID: "enabled_unavailable", Kind: "ssh_auth", Severity: model.SeverityHigh, ObservedAt: time.Now().UTC()})
	if message == nil || message.Channel != "telegram" || message.Destination != "telegram:unknown" {
		t.Fatal("enabled notification intent dropped", message)
	}
	if ok, err := options.Store.Enqueue(context.Background(), *message); err != nil || !ok {
		t.Fatal("unavailable sender admission", ok, err)
	}
	status, err := options.Store.QueueStatus(context.Background(), time.Now().UTC())
	if err != nil || status.Isolated != 1 {
		t.Fatal("uncertain target not isolated", status, err)
	}
}

func TestDaemonManualConfigRestartAndSameTargetDisable(t *testing.T) {
	app := eventTestApp(t)
	options := app.options
	path := filepath.Join(t.TempDir(), "synthetic.token")
	if err := os.WriteFile(path, []byte("123456789:"+strings.Repeat("A", 35)), 0o600); err != nil {
		t.Fatal(err)
	}
	options.Config.Notifications.Telegram.TokenFile = path
	options.Config.Notifications.Telegram.ChatID = "-100123"
	options.NotificationDestination = ""
	options.Notifier = nil
	first, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	_, message := first.prepareEvent(model.Event{ID: "before_disable", Kind: "ssh_auth", Severity: model.SeverityHigh, ObservedAt: time.Now().UTC()})
	if message == nil {
		t.Fatal("missing notification")
	}
	if ok, err := options.Store.Enqueue(context.Background(), *message); err != nil || !ok {
		t.Fatal("enqueue", ok, err)
	}
	options.Config.Notifications.Telegram.Enabled = false
	if _, err := New(options); err != nil {
		t.Fatal(err)
	}
	pending, err := options.Store.Pending(context.Background(), time.Now().Add(time.Second), 20)
	if err != nil || len(pending) != 0 {
		t.Fatal("disabled delivery not paused", pending, err)
	}
	options.Config.Notifications.Telegram.Enabled = true
	if _, err := New(options); err != nil {
		t.Fatal(err)
	}
	pending, err = options.Store.Pending(context.Background(), time.Now().Add(time.Second), 20)
	if err != nil || len(pending) != 1 || pending[0].ID != message.ID {
		t.Fatal("same-target disable lost backlog", pending, err)
	}
	// An edited configuration is enforced in daemon.New, without a TUI guard.
	options.Config.Notifications.Telegram.ChatID = "-100124"
	if _, err := New(options); err != nil {
		t.Fatal(err)
	}
	pending, err = options.Store.Pending(context.Background(), time.Now().Add(time.Second), 20)
	if err != nil || len(pending) != 0 {
		t.Fatal("manually changed receiver inherited backlog", pending, err)
	}
	identity, err := notify.TelegramDestination(path, "-100124")
	if err != nil || identity == message.Destination {
		t.Fatal("target identity unchanged", err)
	}
}
