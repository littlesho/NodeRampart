// SPDX-License-Identifier: MIT

package config

import (
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// OfficialChannelConfig never contains account, sender or recipient identifiers.
// Subscription records are local operator declarations, not platform approval.
type OfficialChannelConfig struct {
	Enabled           bool                 `json:"enabled"`
	CredentialFile    string               `json:"credential_file"`
	Language          string               `json:"language"`
	Timeout           Duration             `json:"timeout"`
	EventsEnabled     bool                 `json:"events_enabled"`
	MinSeverity       string               `json:"min_severity"`
	DailyEnabled      bool                 `json:"daily_enabled"`
	Subscription      OfficialSubscription `json:"subscription"`
	DailyMessageLimit int                  `json:"daily_message_limit"`
	DailySegmentLimit int                  `json:"daily_segment_limit"`
	MaxSegments       int                  `json:"max_segments"`
}

type OfficialSubscription struct {
	ConfirmedAt               string   `json:"confirmed_at"`
	Purpose                   string   `json:"purpose"`
	NotificationTypes         []string `json:"notification_types"`
	EvidenceRef               string   `json:"evidence_ref"`
	BasisID                   string   `json:"basis_id"`
	Revoked                   bool     `json:"revoked"`
	CostConfirmed             bool     `json:"cost_confirmed"`
	PlatformRecoveryConfirmed bool     `json:"platform_recovery_confirmed"`
}

func OfficialChannelNames() []string {
	return []string{"qqbot", "line", "twilio_sms", "whatsapp_cloud"}
}

func IsOfficialChannel(name string) bool {
	for _, candidate := range OfficialChannelNames() {
		if name == candidate {
			return true
		}
	}
	return false
}

func IsPaidOfficialChannel(name string) bool { return name == "twilio_sms" || name == "whatsapp_cloud" }

func OfficialChannelLanguage(c OfficialChannelConfig) string {
	if c.Language == "" {
		return "en"
	}
	return c.Language
}

func (n NotificationsConfig) OfficialChannels() map[string]OfficialChannelConfig {
	return map[string]OfficialChannelConfig{"qqbot": n.QQBot, "line": n.LINE, "twilio_sms": n.TwilioSMS, "whatsapp_cloud": n.WhatsAppCloud}
}

func (n *NotificationsConfig) SetOfficialChannel(name string, value OfficialChannelConfig) error {
	switch name {
	case "qqbot":
		n.QQBot = value
	case "line":
		n.LINE = value
	case "twilio_sms":
		n.TwilioSMS = value
	case "whatsapp_cloud":
		n.WhatsAppCloud = value
	default:
		return errors.New("unsupported official notification channel / 不支持的官方通知渠道")
	}
	return nil
}

func DefaultOfficialChannel(name string) OfficialChannelConfig {
	c := OfficialChannelConfig{CredentialFile: "/etc/noderampart/" + name + ".credential.json", Language: "en", Timeout: Duration{10 * time.Second}, EventsEnabled: true, MinSeverity: "medium", DailyEnabled: true, DailyMessageLimit: 20}
	if IsPaidOfficialChannel(name) {
		c.MinSeverity, c.DailyEnabled = "high", false
	}
	if name == "twilio_sms" {
		c.DailySegmentLimit, c.MaxSegments = 40, 2
	}
	return c
}

var officialEvidenceRef = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
var officialBasisID = regexp.MustCompile(`^[0-9a-f]{32}$`)

func (s OfficialSubscription) Allows(kind string) bool {
	if s.Revoked || s.ConfirmedAt == "" || s.BasisID == "" {
		return false
	}
	for _, v := range s.NotificationTypes {
		if v == kind {
			return true
		}
	}
	return false
}

func (s OfficialSubscription) Validate() error {
	invalid := errors.New("subscription needs a consent time, purpose, notification types and bounded local evidence reference / 订阅须填写同意时间、用途、通知类型与有界的本地依据编号")
	if s.ConfirmedAt == "" && s.Purpose == "" && len(s.NotificationTypes) == 0 && s.EvidenceRef == "" && s.BasisID == "" && !s.CostConfirmed && !s.PlatformRecoveryConfirmed {
		return nil
	}
	if _, err := time.Parse(time.RFC3339Nano, s.ConfirmedAt); err != nil {
		return invalid
	}
	if !utf8.ValidString(s.Purpose) || len(s.Purpose) > 256 || strings.Contains(s.Purpose, "://") || utf8.RuneCountInString(s.Purpose) < 1 || utf8.RuneCountInString(s.Purpose) > 160 || strings.TrimSpace(s.Purpose) != s.Purpose || strings.IndexFunc(s.Purpose, unicode.IsControl) >= 0 || !officialEvidenceRef.MatchString(s.EvidenceRef) || strings.Contains(s.EvidenceRef, "..") || !officialBasisID.MatchString(s.BasisID) || len(s.NotificationTypes) == 0 || len(s.NotificationTypes) > 3 {
		return invalid
	}
	seen := map[string]bool{}
	for _, kind := range s.NotificationTypes {
		if (kind != "event" && kind != "daily" && kind != "test") || seen[kind] {
			return invalid
		}
		seen[kind] = true
	}
	return nil
}

func ValidateOfficialChannel(name string, c OfficialChannelConfig) error {
	if name != "twilio_sms" && c.Subscription.PlatformRecoveryConfirmed {
		return errors.New("official opt-out recovery confirmation applies only to Twilio SMS / 官方退订恢复确认仅适用于 Twilio SMS")
	}
	if !IsOfficialChannel(name) {
		return errors.New("unsupported official notification channel / 不支持的官方通知渠道")
	}
	if c.Language != "" && c.Language != "en" && c.Language != "zh" {
		return errors.New("message language must be en or zh / 消息语言必须为 en 或 zh")
	}
	if c.CredentialFile != "" && (!cleanAbsolute(c.CredentialFile) || c.CredentialFile == "/" || len(c.CredentialFile) > 4096) {
		return errors.New("official credential file must be a clean absolute path / 官方渠道凭据文件须使用规范绝对路径")
	}
	if c.Timeout.Duration != 0 && (c.Timeout.Duration < time.Second || c.Timeout.Duration > 30*time.Second) {
		return errors.New("official channel timeout must be 1s..30s / 官方渠道超时须为 1s..30s")
	}
	if c.MinSeverity != "" && c.MinSeverity != "medium" && c.MinSeverity != "high" && c.MinSeverity != "critical" {
		return errors.New("minimum severity must be medium, high or critical / 最低严重程度须为 medium、high 或 critical")
	}
	if c.DailyMessageLimit < 0 || c.DailyMessageLimit > 1000 || c.DailySegmentLimit < 0 || c.DailySegmentLimit > 2000 || c.MaxSegments < 0 || c.MaxSegments > 2 {
		return errors.New("notification limits exceed bounded ranges; zero forbids sending / 通知限额超出有界范围；零表示禁止发送")
	}
	if name != "twilio_sms" && (c.DailySegmentLimit != 0 || c.MaxSegments != 0) {
		return errors.New("segment limits apply only to Twilio SMS / 分段限额仅适用于 Twilio SMS")
	}
	if err := c.Subscription.Validate(); err != nil {
		return err
	}
	if c.Enabled {
		if c.CredentialFile == "" || c.Timeout.Duration == 0 || c.MinSeverity == "" {
			return errors.New("enabled official channel needs protected credentials, timeout and severity / 启用官方渠道须配置受保护凭据、超时与严重程度")
		}
		if !c.Subscription.Revoked {
			if c.Subscription.ConfirmedAt == "" || c.EventsEnabled && !c.Subscription.Allows("event") || c.DailyEnabled && !c.Subscription.Allows("daily") {
				return errors.New("enabled notifications require consent for each selected type / 启用的每种通知类型均须获得同意")
			}
			if IsPaidOfficialChannel(name) && (!c.Subscription.CostConfirmed || c.DailyMessageLimit == 0 || name == "twilio_sms" && (c.DailySegmentLimit == 0 || c.MaxSegments == 0)) {
				return errors.New("paid notifications require cost confirmation and nonzero bounded limits / 付费通知须确认费用且设置非零有界额度")
			}
		}
	}
	return nil
}

func (c Config) validateOfficialChannels() error {
	for _, name := range OfficialChannelNames() {
		if err := ValidateOfficialChannel(name, c.Notifications.OfficialChannels()[name]); err != nil {
			return err
		}
	}
	return nil
}
