// SPDX-License-Identifier: MIT

package config

import (
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type WebhookConfig struct {
	Enabled        bool     `json:"enabled"`
	Endpoint       string   `json:"endpoint"`
	ReceiverID     string   `json:"receiver_id"`
	CredentialFile string   `json:"credential_file"`
	Timeout        Duration `json:"timeout"`
}

type HeartbeatConfig struct {
	Enabled        bool     `json:"enabled"`
	Endpoint       string   `json:"endpoint"`
	InstanceID     string   `json:"instance_id"`
	CredentialFile string   `json:"credential_file"`
	Interval       Duration `json:"interval"`
	Timeout        Duration `json:"timeout"`
}

// Fixed HTTPS URLs never contain credentials, query strings or fragments.
// Protected bearer files are the sole credential channel. Redirection is refused
// by senders rather than treating another origin as the configured receiver.
func HTTPSURL(value string) (*url.URL, error) {
	invalid := errors.New("outbound endpoint must be a fixed HTTPS URL without credentials, query or fragment")
	if len(value) == 0 || len(value) > 2048 || strings.IndexFunc(value, func(r rune) bool { return r <= 0x20 || r == 0x7f }) >= 0 {
		return nil, invalid
	}
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || strings.Contains(u.Host, "%") {
		return nil, invalid
	}
	if u.Port() != "" {
		port, err := strconv.Atoi(u.Port())
		if err != nil || port < 1 || port > 65535 {
			return nil, invalid
		}
	}
	u.Host = strings.ToLower(u.Host)
	return u, nil
}

func boundedIdentity(value string) bool {
	return len(value) > 0 && len(value) <= 64 && strings.IndexFunc(value, func(r rune) bool { return r <= 0x20 || r == 0x7f }) < 0
}

func (c Config) validateOutbound() error {
	w, h := c.Notifications.Webhook, c.Heartbeat
	if w.Enabled {
		if _, err := HTTPSURL(w.Endpoint); err != nil {
			return err
		}
		if !boundedIdentity(w.ReceiverID) || !cleanAbsolute(w.CredentialFile) || w.Timeout.Duration < time.Second || w.Timeout.Duration > 30*time.Second {
			return errors.New("webhook requires a receiver_id (1..64 bytes), protected credential_file and timeout 1s..30s")
		}
	}
	if h.Enabled {
		if _, err := HTTPSURL(h.Endpoint); err != nil {
			return err
		}
		if !boundedIdentity(h.InstanceID) || !cleanAbsolute(h.CredentialFile) || h.Interval.Duration < time.Minute || h.Interval.Duration > 24*time.Hour || h.Timeout.Duration < time.Second || h.Timeout.Duration > 30*time.Second {
			return errors.New("heartbeat requires instance_id (1..64 bytes), protected credential_file, interval 1m..24h and timeout 1s..30s")
		}
	}
	return nil
}
