// SPDX-License-Identifier: MIT

package config

import (
	"errors"
	"time"
)

// NativeChannelConfig contains only a protected-file reference. The complete
// vendor webhook URL and optional Feishu signing secret are never config values.
type NativeChannelConfig struct {
	Enabled        bool     `json:"enabled"`
	CredentialFile string   `json:"credential_file"`
	Language       string   `json:"language"`
	Timeout        Duration `json:"timeout"`
}

func NativeChannelNames() []string {
	return []string{"feishu", "wecom", "discord", "slack", "teams", "google_chat"}
}

func IsNativeChannel(name string) bool {
	for _, candidate := range NativeChannelNames() {
		if name == candidate {
			return true
		}
	}
	return false
}

func IsNotificationChannel(name string) bool {
	return name == "telegram" || name == "webhook" || IsNativeChannel(name)
}

func NativeChannelLanguage(c NativeChannelConfig) string {
	if c.Language == "" {
		return "en"
	}
	return c.Language
}

func (n NotificationsConfig) NativeChannels() map[string]NativeChannelConfig {
	return map[string]NativeChannelConfig{"feishu": n.Feishu, "wecom": n.WeCom, "discord": n.Discord, "slack": n.Slack, "teams": n.Teams, "google_chat": n.GoogleChat}
}

func (n *NotificationsConfig) SetNativeChannel(name string, value NativeChannelConfig) error {
	switch name {
	case "feishu":
		n.Feishu = value
	case "wecom":
		n.WeCom = value
	case "discord":
		n.Discord = value
	case "slack":
		n.Slack = value
	case "teams":
		n.Teams = value
	case "google_chat":
		n.GoogleChat = value
	default:
		return errors.New("unsupported native notification channel / 不支持的原生通知渠道")
	}
	return nil
}

func defaultNotifications() NotificationsConfig {
	n := NotificationsConfig{Telegram: TelegramConfig{Language: "en", TokenFile: "/etc/noderampart/telegram.token", Timeout: Duration{10 * time.Second}}, Webhook: WebhookConfig{CredentialFile: "/etc/noderampart/webhook.token", Timeout: Duration{10 * time.Second}}, MergeWindow: Duration{10 * time.Minute}}
	for _, name := range NativeChannelNames() {
		_ = n.SetNativeChannel(name, NativeChannelConfig{CredentialFile: "/etc/noderampart/" + name + ".credential.json", Language: "en", Timeout: Duration{10 * time.Second}})
	}
	return n
}

func (c Config) validateNativeChannels() error {
	for _, name := range NativeChannelNames() {
		n := c.Notifications.NativeChannels()[name]
		if n.Language != "" && n.Language != "en" && n.Language != "zh" {
			return errors.New(name + " language must be en or zh / 消息语言必须为 en 或 zh")
		}
		if n.CredentialFile != "" && (!cleanAbsolute(n.CredentialFile) || n.CredentialFile == "/" || len(n.CredentialFile) > 4096) {
			return errors.New(name + " credential_file must be a clean absolute path / 凭据文件必须使用规范的绝对路径")
		}
		if n.Enabled && (n.CredentialFile == "" || n.Timeout.Duration < time.Second || n.Timeout.Duration > 30*time.Second) {
			return errors.New(name + " requires protected credential_file and timeout 1s..30s / 需受保护凭据文件且超时须为 1s..30s")
		}
		if n.Timeout.Duration != 0 && (n.Timeout.Duration < time.Second || n.Timeout.Duration > 30*time.Second) {
			return errors.New(name + " timeout must be 1s..30s / 超时须为 1s..30s")
		}
	}
	return nil
}
