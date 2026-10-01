// SPDX-License-Identifier: MIT

package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestNativeDefaultsAndOldConfigurationUpgradeRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	old := `{"schema_version":1,"notifications":{"telegram":{"enabled":true,"token_file":"/etc/noderampart/old.token","chat_id":"12345","language":"zh","timeout":"11s"},"webhook":{"enabled":true,"endpoint":"https://receiver.example/hook","receiver_id":"old","credential_file":"/etc/noderampart/old.bearer","timeout":"12s"},"merge_window":"3m"},"heartbeat":{"enabled":true,"endpoint":"https://receiver.example/ping","instance_id":"old-instance","credential_file":"/etc/noderampart/old.heartbeat","timeout":"13s","interval":"2m"}}`
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Notifications.Telegram.Enabled || c.Notifications.Telegram.Language != "zh" || !c.Notifications.Webhook.Enabled || c.Notifications.Webhook.ReceiverID != "old" || !c.Heartbeat.Enabled || c.Heartbeat.InstanceID != "old-instance" || c.Notifications.MergeWindow.Duration != 3*time.Minute {
		t.Fatal("old outbound settings changed")
	}
	for _, name := range NativeChannelNames() {
		n := c.Notifications.NativeChannels()[name]
		if n.Enabled || NativeChannelLanguage(n) != "en" || n.Timeout.Duration != 10*time.Second || n.CredentialFile != "/etc/noderampart/"+name+".credential.json" {
			t.Fatalf("unsafe default for %s: %#v", name, n)
		}
		n.Language = "zh"
		if err := c.Notifications.SetNativeChannel(name, n); err != nil {
			t.Fatal(err)
		}
	}
	encoded, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	round, err := Load(path)
	if err != nil || !reflect.DeepEqual(round, c) {
		t.Fatalf("round trip changed settings: %v", err)
	}
}

func TestNativeConfigurationRejectsInvalidSettingsWithoutReadingSecrets(t *testing.T) {
	for _, name := range NativeChannelNames() {
		for _, mutate := range []func(*NativeChannelConfig){
			func(n *NativeChannelConfig) { n.Language = "de" },
			func(n *NativeChannelConfig) { n.CredentialFile = "https://hooks.invalid/SECRET" },
			func(n *NativeChannelConfig) { n.Enabled = true; n.CredentialFile = "" },
			func(n *NativeChannelConfig) { n.Timeout = Duration{31 * time.Second} },
			func(n *NativeChannelConfig) { n.Enabled = true; n.Timeout = Duration{} },
		} {
			c := Defaults()
			n := c.Notifications.NativeChannels()[name]
			mutate(&n)
			_ = c.Notifications.SetNativeChannel(name, n)
			if err := c.Validate(); err == nil || strings.Contains(err.Error(), "SECRET") {
				t.Fatalf("%s invalid settings accepted or disclosed: %v", name, err)
			}
		}
		c := Defaults()
		n := c.Notifications.NativeChannels()[name]
		n.Enabled = true
		n.CredentialFile = "/etc/noderampart/missing-file"
		_ = c.Notifications.SetNativeChannel(name, n)
		if err := c.Validate(); err != nil {
			t.Fatal("structural configuration validation read optional runtime credentials", err)
		}
	}
}

func TestNativeNamesAndIndependentLanguages(t *testing.T) {
	c := Defaults()
	c.Notifications.Telegram.Language = "zh"
	names := NativeChannelNames()
	names[0] = "changed"
	if NativeChannelNames()[0] != "feishu" {
		t.Fatal("mutable channel catalog escaped")
	}
	for _, name := range NativeChannelNames() {
		if !IsNativeChannel(name) || !IsNotificationChannel(name) || NativeChannelLanguage(c.Notifications.NativeChannels()[name]) != "en" {
			t.Fatal("channel language inherited Telegram")
		}
	}
	if IsNativeChannel("telegram") || IsNotificationChannel("teams.evil") || c.Notifications.SetNativeChannel("unknown", NativeChannelConfig{}) == nil {
		t.Fatal("unknown channel accepted")
	}
	if NativeChannelLanguage(NativeChannelConfig{}) != "en" || NativeChannelLanguage(NativeChannelConfig{Language: "invalid"}) != "invalid" {
		t.Fatal("language silently replaced")
	}
}
