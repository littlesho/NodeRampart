// SPDX-License-Identifier: MIT

package daemon

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/notify"
)

type notificationTarget struct {
	channel, destination string
	enabled              bool
}

func configureNativeTargets(options *Options) error {
	// Copy injected maps so a caller cannot mutate a running worker's identity.
	senders := make(map[string]notify.Sender, 6)
	destinations := make(map[string]string, 6)
	activatedAt := make(map[string]time.Time, 6)
	for _, channel := range config.NativeChannelNames() {
		native := options.Config.Notifications.NativeChannels()[channel]
		if language := config.NativeChannelLanguage(native); language != "en" && language != "zh" {
			return errors.New("invalid native notification language")
		}
		sender := options.NativeNotifiers[channel]
		destination := options.NativeDestinations[channel]
		if identified, ok := sender.(interface{ Destination() string }); ok {
			if destination != "" && destination != identified.Destination() {
				return errors.New("native sender target identity mismatch")
			}
			destination = identified.Destination()
		}
		allowedReference := options.ConfigDirectory != "" && (filepath.Dir(native.CredentialFile) == options.ConfigDirectory || filepath.Dir(native.CredentialFile) == filepath.Join(options.ConfigDirectory, "secrets"))
		if destination == "" && allowedReference && options.OptionalFailures[channel] == "" {
			destination, _ = notify.NativeDestination(channel, native)
		}
		if destination == "" {
			destination = channel + ":unknown"
		}
		senders[channel], destinations[channel] = sender, destination
		if err := options.Store.ConfigureNotificationTarget(context.Background(), channel, destination, options.Config.Privacy.NotificationIP, native.Enabled, time.Now().UTC()); err != nil {
			return err
		}
		activated, err := options.Store.NotificationTargetActivatedAt(context.Background(), channel, destination)
		if err != nil {
			return err
		}
		activatedAt[channel] = activated
	}
	options.nativeActivatedAt = activatedAt
	options.NativeNotifiers, options.NativeDestinations = senders, destinations
	return nil
}

func (a *App) notificationTargets() []notificationTarget {
	targets := []notificationTarget{{"telegram", a.options.NotificationDestination, a.options.Config.Notifications.Telegram.Enabled}, {"webhook", a.options.WebhookDestination, a.options.Config.Notifications.Webhook.Enabled}}
	for _, channel := range config.NativeChannelNames() {
		targets = append(targets, notificationTarget{channel, a.options.NativeDestinations[channel], a.options.Config.Notifications.NativeChannels()[channel].Enabled})
	}
	for _, channel := range config.OfficialChannelNames() {
		targets = append(targets, notificationTarget{channel, a.options.OfficialDestinations[channel], a.options.Config.Notifications.OfficialChannels()[channel].Enabled})
	}
	return targets
}

func (a *App) notificationsEnabled() bool {
	for _, target := range a.notificationTargets() {
		if target.enabled {
			return true
		}
	}
	return false
}

func (a *App) startNativeWorkers(ctx, child context.Context, start func(string, string, bool, func() error)) {
	for _, channel := range config.NativeChannelNames() {
		native := a.options.Config.Notifications.NativeChannels()[channel]
		name := channel + "_worker"
		if sender := a.options.NativeNotifiers[channel]; sender != nil && native.Enabled {
			worker := &notify.Worker{Store: a.options.Store, Sender: sender, Destination: a.options.NativeDestinations[channel], Logger: a.options.Logger}
			start(name, "running", false, func() error { return worker.Run(child) })
		} else {
			state := "disabled"
			if native.Enabled {
				state = "degraded"
			}
			if err := a.options.Store.SetComponentStatus(ctx, name, state, time.Now().UTC()); err != nil {
				a.options.Logger.Warn("record native notification component", "component", name, "error", err)
			}
		}
	}
}
