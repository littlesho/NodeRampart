// SPDX-License-Identifier: MIT

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/littlesho/NodeRampart/internal/billing"
	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/enrich"
	"github.com/littlesho/NodeRampart/internal/notify"
)

type optionalDependencies struct {
	geo       *enrich.Resolver
	native    map[string]notify.Sender
	webhook   notify.Sender
	heartbeat notify.HeartbeatSender
	sender    notify.Sender
	billing   *billing.Profile
	failures  map[string]string
}

// Resource absence is optional; unsafe paths/permissions and malformed billing
// configuration are not made valid by disabling a feature. Retry is an explicit
// restart after correcting the local resource, rather than a new scheduler.
func loadOptional(cfg config.Config) (optionalDependencies, error) {
	return loadOptionalAt(cfg, "")
}

func loadOptionalAt(cfg config.Config, configDir string) (optionalDependencies, error) {
	d := optionalDependencies{failures: map[string]string{}, native: map[string]notify.Sender{}}
	for _, path := range []string{cfg.Geo.CityMMDB, cfg.Geo.ASNMMDB} {
		if err := checkOptionalFile(path, false); err != nil {
			return d, fmt.Errorf("unsafe GeoIP resource: %w", err)
		}
	}
	var err error
	d.geo, err = enrich.Open(cfg.Geo.CityMMDB, cfg.Geo.ASNMMDB)
	if err != nil {
		d.failures["geoip"] = "geoip_resource_unavailable"
		d.geo, _ = enrich.Open("", "")
	}
	fail := func(err error) (optionalDependencies, error) { _ = d.geo.Close(); return d, err }
	if cfg.Notifications.Telegram.Enabled {
		if err := checkOptionalFile(cfg.Notifications.Telegram.TokenFile, true); err != nil {
			return fail(fmt.Errorf("unsafe notification credentials: %w", err))
		}
		sender, senderErr := notify.NewTelegram(cfg.Notifications.Telegram.TokenFile, cfg.Notifications.Telegram.ChatID, cfg.Notifications.Telegram.Timeout.Duration)
		err = senderErr
		if err != nil {
			d.failures["telegram"] = "telegram_credentials_unavailable"
		} else {
			d.sender = sender
		}
	}
	if cfg.Notifications.Webhook.Enabled {
		w := cfg.Notifications.Webhook
		if err := checkOptionalFile(w.CredentialFile, true); err != nil {
			return fail(errors.New("unsafe webhook credentials"))
		}
		sender, err := notify.NewWebhook(w.Endpoint, w.ReceiverID, w.CredentialFile, w.Timeout.Duration)
		if err != nil {
			d.failures["webhook"] = "webhook_credentials_unavailable"
		} else {
			d.webhook = sender
		}
	}
	for _, channel := range config.NativeChannelNames() {
		native := cfg.Notifications.NativeChannels()[channel]
		if !native.Enabled {
			continue
		}
		if configDir != "" && filepath.Dir(native.CredentialFile) != configDir && filepath.Dir(native.CredentialFile) != filepath.Join(configDir, "secrets") {
			d.failures[channel] = channel + "_credentials_unavailable"
			continue
		}
		sender, err := notify.NewNative(channel, native)
		if err != nil {
			d.failures[channel] = channel + "_credentials_unavailable"
		} else {
			d.native[channel] = sender
		}
	}
	if cfg.Heartbeat.Enabled {
		h := cfg.Heartbeat
		if err := checkOptionalFile(h.CredentialFile, true); err != nil {
			return fail(errors.New("unsafe heartbeat credentials"))
		}
		sender, err := notify.NewHeartbeat(h.Endpoint, h.CredentialFile, h.Timeout.Duration)
		if err != nil {
			d.failures["heartbeat"] = "heartbeat_credentials_unavailable"
		} else {
			d.heartbeat = sender
		}
	}
	if cfg.Billing.Enabled {
		if err := checkOptionalFile(cfg.Billing.ProfilePath, false); err != nil {
			return fail(fmt.Errorf("unsafe billing resource: %w", err))
		}
		d.billing, err = billing.Load(cfg.Billing.ProfilePath)
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) {
			d.failures["billing"] = "billing_resource_unavailable"
		} else if err != nil {
			return fail(errors.New("billing profile configuration is invalid"))
		}
	}
	return d, nil
}

func checkOptionalFile(path string, secret bool) error {
	if path == "" {
		return nil
	}
	// Reject a symlink in any existing path component. Missing files or denied
	// reads may degrade availability, without broadening their permissions.
	for current := path; current != "/"; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("optional resource path contains a symlink")
		}
		if current == path && (!info.Mode().IsRegular() || info.Mode().Perm()&0o022 != 0 || secret && info.Mode().Perm()&0o077 != 0) {
			return errors.New("optional resource type or permissions are unsafe")
		}
	}
	return nil
}
