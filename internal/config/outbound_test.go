// SPDX-License-Identifier: MIT

package config

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOutboundFixedHTTPSURLBounds(t *testing.T) {
	for _, value := range []string{"https://localhost/receive", "https://[::1]:8443/hook", "https://LOCALHOST:443/path%20with%20spaces"} {
		u, err := HTTPSURL(value)
		if err != nil || strings.Contains(u.Host, "LOCALHOST") {
			t.Fatal("valid fixed HTTPS URL was rejected or not canonicalized", err)
		}
	}
	for _, value := range []string{
		"", "http://localhost/hook", "https:///hook", "https://user:synthetic-secret@localhost/hook",
		"https://localhost/hook?token=synthetic-secret", "https://localhost/hook?", "https://localhost/hook#secret",
		"https://localhost:0/hook", "https://localhost:65536/hook", "https://localhost:bad/hook",
		"https://localhost/a b", "https://localhost/a\n", "https://localhost/" + strings.Repeat("x", 2048),
	} {
		if _, err := HTTPSURL(value); err == nil {
			t.Fatal("unsafe or unbounded outbound URL accepted")
		} else if strings.Contains(err.Error(), "synthetic-secret") || strings.Contains(err.Error(), value) && value != "" {
			t.Fatal("URL validation error disclosed the endpoint")
		}
	}
}

func TestOutboundDisabledDefaultsAndEnabledBounds(t *testing.T) {
	cfg := Defaults()
	if cfg.Notifications.Webhook.Enabled || cfg.Heartbeat.Enabled {
		t.Fatal("outbound capability is enabled by default")
	}
	// Disabled optional capabilities do not require local secrets or a receiver.
	cfg.Notifications.Webhook.Endpoint = "unconfigured"
	cfg.Heartbeat.Endpoint = "unconfigured"
	if err := cfg.Validate(); err != nil {
		t.Fatal("disabled optional sender failed validation", err)
	}
	valid := func() Config {
		cfg := Defaults()
		credential := filepath.Join(t.TempDir(), "protected.token")
		cfg.Notifications.Webhook = WebhookConfig{Enabled: true, Endpoint: "https://localhost/hook", ReceiverID: "local-fixture", CredentialFile: credential, Timeout: Duration{time.Second}}
		cfg.Heartbeat = HeartbeatConfig{Enabled: true, Endpoint: "https://localhost/heartbeat", InstanceID: "local-fixture", CredentialFile: credential, Interval: Duration{time.Minute}, Timeout: Duration{30 * time.Second}}
		return cfg
	}
	if err := valid().Validate(); err != nil {
		t.Fatal("legal minimum/maximum outbound settings rejected", err)
	}
	for _, change := range []func(*Config){
		func(c *Config) { c.Notifications.Webhook.Timeout = Duration{0} },
		func(c *Config) { c.Notifications.Webhook.Timeout = Duration{30*time.Second + time.Nanosecond} },
		func(c *Config) { c.Notifications.Webhook.CredentialFile = "relative.token" },
		func(c *Config) { c.Notifications.Webhook.ReceiverID = "" },
		func(c *Config) { c.Notifications.Webhook.ReceiverID = strings.Repeat("x", 65) },
		func(c *Config) { c.Notifications.Webhook.ReceiverID = "hidden\nidentity" },
		func(c *Config) { c.Heartbeat.Interval = Duration{time.Minute - time.Nanosecond} },
		func(c *Config) { c.Heartbeat.Interval = Duration{24*time.Hour + time.Nanosecond} },
		func(c *Config) { c.Heartbeat.Timeout = Duration{time.Second - time.Nanosecond} },
		func(c *Config) { c.Heartbeat.Timeout = Duration{31 * time.Second} },
		func(c *Config) { c.Heartbeat.CredentialFile = "/tmp/../unsafe.token" },
		func(c *Config) { c.Heartbeat.InstanceID = "" },
		func(c *Config) { c.Heartbeat.InstanceID = strings.Repeat("x", 65) },
		func(c *Config) { c.Heartbeat.InstanceID = "instance with spaces" },
	} {
		cfg := valid()
		change(&cfg)
		if err := cfg.Validate(); err == nil {
			t.Fatal("unsafe outbound setting accepted")
		}
	}
	max := valid()
	max.Notifications.Webhook.ReceiverID = strings.Repeat("x", 64)
	max.Notifications.Webhook.Timeout = Duration{30 * time.Second}
	max.Heartbeat.InstanceID = strings.Repeat("x", 64)
	max.Heartbeat.Interval = Duration{24 * time.Hour}
	if err := max.Validate(); err != nil {
		t.Fatal("legal outbound identity/interval maximum rejected", err)
	}
}
