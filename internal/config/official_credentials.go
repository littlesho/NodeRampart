// SPDX-License-Identifier: MIT

package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"
)

const MaxOfficialCredentialBytes = 16384
const WhatsAppGraphVersion = "v26.0"

// OfficialCredential is stored only in a protected, immutable managed file.
// Even non-secret account/recipient/template identifiers use this boundary.
type OfficialCredential struct {
	Channel             string             `json:"channel"`
	AppID               string             `json:"app_id,omitempty"`
	AppSecret           string             `json:"app_secret,omitempty"`
	TargetType          string             `json:"target_type,omitempty"`
	TargetID            string             `json:"target_id,omitempty"`
	ChannelAccessToken  string             `json:"channel_access_token,omitempty"`
	AccountSID          string             `json:"account_sid,omitempty"`
	AuthMode            string             `json:"auth_mode,omitempty"`
	APIKeySID           string             `json:"api_key_sid,omitempty"`
	APIKeySecret        string             `json:"api_key_secret,omitempty"`
	AuthToken           string             `json:"auth_token,omitempty"`
	From                string             `json:"from,omitempty"`
	MessagingServiceSID string             `json:"messaging_service_sid,omitempty"`
	To                  string             `json:"to,omitempty"`
	PhoneNumberID       string             `json:"phone_number_id,omitempty"`
	AccessToken         string             `json:"access_token,omitempty"`
	Recipient           string             `json:"recipient,omitempty"`
	GraphVersion        string             `json:"graph_version,omitempty"`
	Templates           *OfficialTemplates `json:"templates,omitempty"`
}

type OfficialTemplates struct {
	Event *OfficialTemplate `json:"event,omitempty"`
	Daily *OfficialTemplate `json:"daily,omitempty"`
	Test  *OfficialTemplate `json:"test,omitempty"`
}

type OfficialTemplate struct {
	Name       string   `json:"name"`
	Language   string   `json:"language"`
	Parameters []string `json:"parameters"`
}

var officialDecimalID = regexp.MustCompile(`^[0-9]{5,32}$`)
var officialOpaqueID = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`)
var officialLINEID = regexp.MustCompile(`^[UCR][0-9a-fA-F]{32}$`)
var officialTwilioAccount = regexp.MustCompile(`^AC[0-9a-fA-F]{32}$`)
var officialTwilioKey = regexp.MustCompile(`^SK[0-9a-fA-F]{32}$`)
var officialTwilioService = regexp.MustCompile(`^MG[0-9a-fA-F]{32}$`)
var officialE164 = regexp.MustCompile(`^\+[1-9][0-9]{1,14}$`)
var officialWhatsAppRecipient = regexp.MustCompile(`^[1-9][0-9]{1,14}$`)
var officialTemplateName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,127}$`)
var officialTemplateLanguage = regexp.MustCompile(`^[a-z]{2,3}(_[A-Z]{2})?$`)

func officialASCII(value string, minimum, maximum int) bool {
	return len(value) >= minimum && len(value) <= maximum && strings.IndexFunc(value, func(r rune) bool { return r < 0x21 || r > 0x7e }) < 0
}

func ValidateOfficialTemplate(t *OfficialTemplate) error {
	invalid := errors.New("approved template mapping is invalid / 已获批准的模板映射无效")
	if t == nil || !officialTemplateName.MatchString(t.Name) || !officialTemplateLanguage.MatchString(t.Language) || len(t.Parameters) < 6 || len(t.Parameters) > 7 {
		return invalid
	}
	allowed := map[string]bool{"host_alias": true, "event_kind": true, "phase": true, "severity": true, "time": true, "bounded_summary": true, "local_reference": true}
	seen := make(map[string]bool, len(t.Parameters))
	for _, parameter := range t.Parameters {
		if !allowed[parameter] || seen[parameter] {
			return invalid
		}
		seen[parameter] = true
	}
	for _, parameter := range []string{"event_kind", "phase", "severity", "time", "bounded_summary", "local_reference"} {
		if !seen[parameter] {
			return invalid
		}
	}
	return nil
}

func ValidateOfficialCredential(channel string, c OfficialCredential) error {
	invalid := errors.New("official channel credential document is invalid / 官方渠道凭据文档无效")
	if !IsOfficialChannel(channel) || c.Channel != channel {
		return invalid
	}
	// Removing exactly the permitted fields and comparing the remainder rejects
	// mixed identities, including otherwise valid credentials for another API.
	rest := c
	rest.Channel = ""
	switch channel {
	case "qqbot":
		if !officialDecimalID.MatchString(c.AppID) || !officialASCII(c.AppSecret, 16, 2048) || c.TargetType != "user" && c.TargetType != "group" || !officialOpaqueID.MatchString(c.TargetID) {
			return invalid
		}
		rest.AppID, rest.AppSecret, rest.TargetType, rest.TargetID = "", "", "", ""
	case "line":
		prefix := map[string]byte{"user": 'U', "group": 'C', "room": 'R'}[c.TargetType]
		if !officialASCII(c.ChannelAccessToken, 16, 2048) || !officialLINEID.MatchString(c.TargetID) || prefix == 0 || c.TargetID[0] != prefix {
			return invalid
		}
		rest.ChannelAccessToken, rest.TargetType, rest.TargetID = "", "", ""
	case "twilio_sms":
		if !officialTwilioAccount.MatchString(c.AccountSID) || !officialE164.MatchString(c.To) || (c.From == "") == (c.MessagingServiceSID == "") || c.From != "" && !officialE164.MatchString(c.From) || c.MessagingServiceSID != "" && !officialTwilioService.MatchString(c.MessagingServiceSID) {
			return invalid
		}
		if c.AuthMode == "api_key" {
			if !officialTwilioKey.MatchString(c.APIKeySID) || !officialASCII(c.APIKeySecret, 16, 256) || c.AuthToken != "" {
				return invalid
			}
		} else if c.AuthMode == "auth_token" {
			if !officialASCII(c.AuthToken, 16, 256) || c.APIKeySID != "" || c.APIKeySecret != "" {
				return invalid
			}
		} else {
			return invalid
		}
		rest.AccountSID, rest.AuthMode, rest.APIKeySID, rest.APIKeySecret, rest.AuthToken, rest.From, rest.MessagingServiceSID, rest.To = "", "", "", "", "", "", "", ""
	case "whatsapp_cloud":
		if !officialDecimalID.MatchString(c.PhoneNumberID) || !officialASCII(c.AccessToken, 16, 4096) || !officialWhatsAppRecipient.MatchString(c.Recipient) || c.GraphVersion != WhatsAppGraphVersion || c.Templates == nil {
			return invalid
		}
		if c.Templates.Event == nil && c.Templates.Daily == nil && c.Templates.Test == nil {
			return invalid
		}
		for _, t := range []*OfficialTemplate{c.Templates.Event, c.Templates.Daily, c.Templates.Test} {
			if t != nil {
				if err := ValidateOfficialTemplate(t); err != nil {
					return invalid
				}
			}
		}
		rest.PhoneNumberID, rest.AccessToken, rest.Recipient, rest.GraphVersion, rest.Templates = "", "", "", "", nil
	}
	if rest != (OfficialCredential{}) {
		return invalid
	}
	return nil
}

// DecodeOfficialCredential rejects duplicates, nulls, unknown fields, trailing
// values and pathological nesting before validating channel-specific fields.
func DecodeOfficialCredential(channel string, data []byte) (OfficialCredential, error) {
	var c OfficialCredential
	invalid := errors.New("official channel credential document is invalid / 官方渠道凭据文档无效")
	if len(data) == 0 || len(data) > MaxOfficialCredentialBytes {
		return c, invalid
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if err := checkOfficialJSON(d, 0); err != nil {
		return c, invalid
	}
	if _, err := d.Token(); err != io.EOF {
		return c, invalid
	}
	d = json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil || ValidateOfficialCredential(channel, c) != nil {
		return OfficialCredential{}, invalid
	}
	return c, nil
}

func checkOfficialJSON(d *json.Decoder, depth int) error {
	if depth > 8 {
		return errors.New("invalid credential JSON")
	}
	t, err := d.Token()
	if err != nil || t == nil {
		return errors.New("invalid credential JSON")
	}
	delim, isDelim := t.(json.Delim)
	if !isDelim {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			k, err := d.Token()
			key, ok := k.(string)
			if err != nil || !ok || seen[key] || key != strings.ToLower(key) {
				return errors.New("invalid credential JSON")
			}
			seen[key] = true
			if err := checkOfficialJSON(d, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for d.More() {
			if err := checkOfficialJSON(d, depth+1); err != nil {
				return err
			}
		}
	default:
		return errors.New("invalid credential JSON")
	}
	end, err := d.Token()
	if err != nil || delim == '{' && end != json.Delim('}') || delim == '[' && end != json.Delim(']') {
		return errors.New("invalid credential JSON")
	}
	return nil
}

func ValidateOfficialCredentialPolicy(channel string, cfg OfficialChannelConfig, c OfficialCredential) error {
	if err := ValidateOfficialCredential(channel, c); err != nil {
		return err
	}
	if channel != "whatsapp_cloud" {
		return nil
	}
	for kind, t := range map[string]*OfficialTemplate{"event": c.Templates.Event, "daily": c.Templates.Daily, "test": c.Templates.Test} {
		needed := kind == "event" && cfg.EventsEnabled || kind == "daily" && cfg.DailyEnabled || kind == "test" && cfg.Subscription.Allows("test")
		if needed && t == nil {
			return errors.New("each subscribed notification type needs its own approved template / 每种已订阅通知均须配置独立的已批准模板")
		}
		if needed && (OfficialChannelLanguage(cfg) == "zh" && t.Language != "zh_CN" || OfficialChannelLanguage(cfg) == "en" && t.Language != "en" && t.Language != "en_US" && t.Language != "en_GB") {
			return errors.New("approved template language must match the selected notification language / 已批准模板语言须匹配所选通知语言")
		}
	}
	return nil
}
