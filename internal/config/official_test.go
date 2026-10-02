// SPDX-License-Identifier: MIT

package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func officialTestSubscription() OfficialSubscription {
	return OfficialSubscription{ConfirmedAt: "2026-10-02T08:00:00Z", Purpose: "Synthetic monitoring alerts", NotificationTypes: []string{"event", "daily", "test"}, EvidenceRef: "synthetic-consent-1", BasisID: strings.Repeat("0", 32), CostConfirmed: true}
}

func officialTestCredential(channel string) OfficialCredential {
	c := OfficialCredential{Channel: channel}
	switch channel {
	case "qqbot":
		c.AppID, c.AppSecret, c.TargetType, c.TargetID = "10001", "SYNTHETIC-SECRET-0000", "group", "SYNTHETIC_OPENID_0000"
	case "line":
		c.ChannelAccessToken, c.TargetType, c.TargetID = "SYNTHETIC-TOKEN-0000", "user", "U"+strings.Repeat("0", 32)
	case "twilio_sms":
		c.AccountSID, c.AuthMode, c.AuthToken, c.From, c.To = "AC"+strings.Repeat("0", 32), "auth_token", "SYNTHETIC-TOKEN-0000", "+12025550101", "+12025550102"
	case "whatsapp_cloud":
		c.PhoneNumberID, c.AccessToken, c.Recipient, c.GraphVersion = "100000001", "SYNTHETIC-TOKEN-0000", "12025550102", WhatsAppGraphVersion
		template := func() *OfficialTemplate {
			return &OfficialTemplate{Name: "synthetic_notice", Language: "en_US", Parameters: []string{"host_alias", "event_kind", "phase", "severity", "time", "bounded_summary", "local_reference"}}
		}
		c.Templates = &OfficialTemplates{Event: template(), Daily: template(), Test: template()}
	}
	return c
}

func TestOfficialDefaultsAndLegacyUpgradeRoundTrip(t *testing.T) {
	c := Defaults()
	if len(NativeChannelNames()) != 6 || len(OfficialChannelNames()) != 4 {
		t.Fatal("channel families changed")
	}
	for _, name := range OfficialChannelNames() {
		n := c.Notifications.OfficialChannels()[name]
		if n.Enabled || OfficialChannelLanguage(n) != "en" || n.Timeout.Duration != 10*time.Second || n.DailyMessageLimit != 20 {
			t.Fatal("unsafe default", name)
		}
		if IsPaidOfficialChannel(name) {
			if n.DailyEnabled || n.MinSeverity != "high" {
				t.Fatal("paid defaults")
			}
		} else if !n.DailyEnabled || n.MinSeverity != "medium" {
			t.Fatal("ordinary defaults")
		}
		if !IsNotificationChannel(name) {
			t.Fatal("official channel not routable")
		}
	}
	c.Notifications.Telegram.ChatID = "10001"
	c.Notifications.Telegram.Enabled = true
	c.Notifications.Webhook.Enabled = true
	c.Notifications.Webhook.Endpoint = "https://synthetic.example/notify"
	c.Notifications.Webhook.ReceiverID = "synthetic"
	encoded, _ := json.Marshal(c)
	var legacy map[string]json.RawMessage
	_ = json.Unmarshal(encoded, &legacy)
	var channels map[string]json.RawMessage
	_ = json.Unmarshal(legacy["notifications"], &channels)
	for _, name := range OfficialChannelNames() {
		delete(channels, name)
	}
	legacy["notifications"], _ = json.Marshal(channels)
	encoded, _ = json.Marshal(legacy)
	path := filepath.Join(t.TempDir(), "config.json")
	if os.WriteFile(path, encoded, 0o600) != nil {
		t.Fatal("fixture")
	}
	upgraded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !upgraded.Notifications.Telegram.Enabled || !upgraded.Notifications.Webhook.Enabled || upgraded.Heartbeat.Enabled {
		t.Fatal("legacy switches changed")
	}
	for _, name := range OfficialChannelNames() {
		if upgraded.Notifications.OfficialChannels()[name].Enabled {
			t.Fatal("upgrade enabled new target")
		}
	}
	encoded, _ = json.Marshal(upgraded)
	if os.WriteFile(path, encoded, 0o600) != nil {
		t.Fatal("fixture")
	}
	again, err := Load(path)
	if err != nil || Fingerprint(again) != Fingerprint(upgraded) {
		t.Fatal("round trip changed config", err)
	}
}

func TestOfficialConsentAndBudgetValidation(t *testing.T) {
	for _, channel := range OfficialChannelNames() {
		c := DefaultOfficialChannel(channel)
		c.Enabled = true
		if ValidateOfficialChannel(channel, c) == nil {
			t.Fatal("activation without consent", channel)
		}
		c.Subscription = officialTestSubscription()
		if err := ValidateOfficialChannel(channel, c); err != nil {
			t.Fatal(err)
		}
		for _, mutate := range []func(*OfficialChannelConfig){func(c *OfficialChannelConfig) { c.Subscription.EvidenceRef = "/private/file" }, func(c *OfficialChannelConfig) { c.Subscription.Purpose = "line\ncontrol" }, func(c *OfficialChannelConfig) { c.Subscription.NotificationTypes = []string{"event", "event"} }, func(c *OfficialChannelConfig) { c.MinSeverity = "low" }, func(c *OfficialChannelConfig) { c.DailyMessageLimit = 1001 }, func(c *OfficialChannelConfig) { c.Subscription.BasisID = "target-label" }} {
			bad := c
			mutate(&bad)
			if ValidateOfficialChannel(channel, bad) == nil {
				t.Fatal("invalid official policy accepted", channel)
			}
		}
		if IsPaidOfficialChannel(channel) {
			bad := c
			bad.DailyMessageLimit = 0
			if ValidateOfficialChannel(channel, bad) == nil {
				t.Fatal("zero paid allowance allowed enabled sends")
			}
			bad = c
			bad.Subscription.CostConfirmed = false
			if ValidateOfficialChannel(channel, bad) == nil {
				t.Fatal("no cost consent")
			}
		}
		c.Subscription.Revoked = true
		if c.Subscription.Allows("event") || ValidateOfficialChannel(channel, c) != nil {
			t.Fatal("revocation cannot be stored safely")
		}
	}
}

func TestOfficialCredentialStrictContractMatrix(t *testing.T) {
	for _, channel := range OfficialChannelNames() {
		t.Run(channel, func(t *testing.T) {
			c := officialTestCredential(channel)
			data, _ := json.Marshal(c)
			if _, err := DecodeOfficialCredential(channel, data); err != nil {
				t.Fatal(err)
			}
			cases := [][]byte{nil, []byte(`{}`), []byte(`[]`), []byte(`null`), append(append([]byte(nil), data...), []byte(` {}`)...), []byte(`{"channel":"` + channel + `","channel":"` + channel + `"}`), []byte(`{"channel":"` + channel + `","app_secret":null}`), []byte(`{"channel":"` + channel + `","unknown":"SYNTHETIC-SECRET"}`), []byte(strings.Repeat("x", MaxOfficialCredentialBytes+1))}
			for _, bad := range cases {
				_, err := DecodeOfficialCredential(channel, bad)
				if err == nil || strings.Contains(err.Error(), "SYNTHETIC-SECRET") {
					t.Fatal("malformed credential or leakage")
				}
			}
			c.Channel = "different"
			if ValidateOfficialCredential(channel, c) == nil {
				t.Fatal("credential channel not bound")
			}
		})
	}
	for _, mode := range []string{"api_key", "auth_token"} {
		c := officialTestCredential("twilio_sms")
		c.AuthMode = mode
		if mode == "api_key" {
			c.AuthToken = ""
			c.APIKeySID = "SK" + strings.Repeat("0", 32)
			c.APIKeySecret = "SYNTHETIC-SECRET-0000"
		}
		if ValidateOfficialCredential("twilio_sms", c) != nil {
			t.Fatal("auth mode rejected")
		}
		c.MessagingServiceSID = "MG" + strings.Repeat("0", 32)
		if ValidateOfficialCredential("twilio_sms", c) == nil {
			t.Fatal("ambiguous sender accepted")
		}
		c.From = ""
		if ValidateOfficialCredential("twilio_sms", c) != nil {
			t.Fatal("messaging service sender rejected")
		}
	}
	meta := officialTestCredential("whatsapp_cloud")
	meta.GraphVersion = "v18.0"
	if ValidateOfficialCredential("whatsapp_cloud", meta) == nil {
		t.Fatal("unsupported Graph version accepted")
	}
	meta = officialTestCredential("whatsapp_cloud")
	meta.Templates.Event.Parameters = []string{"raw_logs"}
	if ValidateOfficialCredential("whatsapp_cloud", meta) == nil {
		t.Fatal("arbitrary template body source accepted")
	}
	meta = officialTestCredential("whatsapp_cloud")
	n := DefaultOfficialChannel("whatsapp_cloud")
	n.Language = "zh"
	if ValidateOfficialCredentialPolicy("whatsapp_cloud", n, meta) == nil {
		t.Fatal("wrong approved template language accepted")
	}
	meta.Templates.Event.Language = "zh_CN"
	if ValidateOfficialCredentialPolicy("whatsapp_cloud", n, meta) != nil {
		t.Fatal("approved Chinese event route rejected")
	}
	n.DailyEnabled = true
	if ValidateOfficialCredentialPolicy("whatsapp_cloud", n, meta) == nil {
		t.Fatal("daily route language silently inherited")
	}
}

func TestOfficialTemplatePreservesRequiredNotificationFields(t *testing.T) {
	required := []string{"event_kind", "phase", "severity", "time", "bounded_summary", "local_reference"}
	template := OfficialTemplate{Name: "synthetic_notice", Language: "en_US", Parameters: append([]string(nil), required...)}
	if err := ValidateOfficialTemplate(&template); err != nil {
		t.Fatal("six-field route rejected", err)
	}
	template.Parameters = []string{"local_reference", "time", "severity", "host_alias", "phase", "event_kind", "bounded_summary"}
	if err := ValidateOfficialTemplate(&template); err != nil {
		t.Fatal("operator positional order rejected", err)
	}
	for _, absent := range required {
		parameters := []string{"host_alias"}
		for _, field := range required {
			if field != absent {
				parameters = append(parameters, field)
			}
		}
		template.Parameters = parameters
		if ValidateOfficialTemplate(&template) == nil {
			t.Fatal("critical notification field omitted", absent)
		}
	}
	for _, parameters := range [][]string{nil, {}, {"host_alias"}, append(append([]string(nil), required...), "severity"), append(append([]string(nil), required...), "raw_logs")} {
		template.Parameters = parameters
		if ValidateOfficialTemplate(&template) == nil {
			t.Fatal("empty, repeated or unapproved mapping accepted")
		}
	}
}
