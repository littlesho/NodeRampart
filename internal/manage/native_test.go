// SPDX-License-Identifier: MIT

package manage

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/littlesho/NodeRampart/internal/api"
	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/notify"
)

func nativeFixtureURL(channel string) string {
	switch channel {
	case "feishu":
		return "https://open.feishu.cn/open-apis/bot/v2/hook/SYNTHETIC-PRIVATE-WEBHOOK-123456"
	case "wecom":
		return "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=SYNTHETIC-PRIVATE-KEY-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	case "discord":
		return "https://discord.com/api/webhooks/123456789012345678/SYNTHETIC-PRIVATE-TOKEN-123456"
	case "slack":
		return "https://hooks.slack.com/services/TSYNTHETIC1/BSYNTHETIC2/SyntheticToken0000"
	case "teams":
		return "https://prod-12.westeurope.logic.azure.com/workflows/SYNTHETIC-WORKFLOW-123456/triggers/manual/paths/invoke?api-version=2016-06-01&sp=%2Ftriggers%2Fmanual%2Frun&sv=1.0&sig=SYNTHETIC-PRIVATE-SIGNATURE-123456"
	case "google_chat":
		return "https://chat.googleapis.com/v1/spaces/SYNTHETIC_SPACE/messages?key=SYNTHETIC-PRIVATE-KEY-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA&token=SYNTHETIC-PRIVATE-TOKEN-123456"
	}
	return ""
}

func nativeSetupInput(channel string) map[string]string {
	input := map[string]string{"enabled": "yes", "language": "zh", "credential_action": "replace", "url": nativeFixtureURL(channel)}
	if channel == "feishu" {
		input["secret_action"] = "replace"
		input["secret"] = "SYNTHETIC-PRIVATE-SIGNING-SECRET"
	}
	return input
}

func TestSixNativeManagementSettingsAreProtectedIndependentAndNeverSend(t *testing.T) {
	m, _ := fixtureManager(t)
	m.Request = func(context.Context, string, any) (json.RawMessage, error) {
		t.Error("settings contacted daemon notification API")
		return nil, errors.New("unexpected request")
	}
	for _, channel := range config.NativeChannelNames() {
		input := nativeSetupInput(channel)
		result, err := m.Action(context.Background(), channel+"_setup", input)
		if err != nil {
			t.Fatalf("%s: %v", channel, err)
		}
		if strings.Contains(result, input["url"]) || input["secret"] != "" && strings.Contains(result, input["secret"]) {
			t.Fatal("credential leaked in result")
		}
		snapshot, _ := m.Load(context.Background())
		n := snapshot.Config.Notifications.NativeChannels()[channel]
		if !n.Enabled || n.Language != "zh" || filepath.Dir(n.CredentialFile) != m.localPath("secrets") {
			t.Fatal("wrong independent config")
		}
		info, err := os.Stat(n.CredentialFile)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatal("credential not protected", err)
		}
		credential, err := m.readNativeCredential(channel, n.CredentialFile)
		if err != nil || credential.URL != input["url"] || credential.Secret != input["secret"] {
			t.Fatalf("credential snapshot incorrect: %v", err)
		}
		ordinary, _ := os.ReadFile(m.ConfigPath)
		journal, _ := os.ReadFile(m.journalPath())
		for _, data := range [][]byte{ordinary, journal} {
			if strings.Contains(string(data), input["url"]) || input["secret"] != "" && strings.Contains(string(data), input["secret"]) {
				t.Fatal("secret persisted in ordinary config/journal")
			}
		}
	}
	snapshot, _ := m.Load(context.Background())
	if snapshot.Config.Notifications.Telegram.Enabled || snapshot.Config.Notifications.Webhook.Enabled || snapshot.Config.Heartbeat.Enabled {
		t.Fatal("native setup changed legacy switches")
	}
	entries, err := os.ReadDir(m.localPath("secrets"))
	if err != nil || len(entries) != 6 {
		t.Fatal("one channel removed another credential", err)
	}
}

func TestNativeKeepRotateClearAndCredentialIdentity(t *testing.T) {
	for _, channel := range config.NativeChannelNames() {
		t.Run(channel, func(t *testing.T) {
			m, _ := fixtureManager(t)
			ctx := context.Background()
			if _, err := m.Action(ctx, channel+"_setup", nativeSetupInput(channel)); err != nil {
				t.Fatal(err)
			}
			before, _ := m.Load(ctx)
			n := before.Config.Notifications.NativeChannels()[channel]
			a, err := notify.NewNative(channel, n)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := m.Action(ctx, channel+"_setup", map[string]string{"enabled": "no", "language": "en", "credential_action": "keep"}); err != nil {
				t.Fatal(err)
			}
			paused, _ := m.Load(ctx)
			p := paused.Config.Notifications.NativeChannels()[channel]
			if p.CredentialFile != n.CredentialFile || p.Enabled || p.Language != "en" {
				t.Fatal("disable/language change replaced credentials")
			}
			if _, err := m.Action(ctx, channel+"_setup", map[string]string{"enabled": "yes", "credential_action": "keep"}); err != nil {
				t.Fatal(err)
			}
			resumed, _ := m.Load(ctx)
			r, err := notify.NewNative(channel, resumed.Config.Notifications.NativeChannels()[channel])
			if err != nil || r.Destination() != a.Destination() {
				t.Fatal("same credentials changed identity on resume", err)
			}
			if _, err := m.Action(ctx, channel+"_setup", nativeSetupInput(channel)); err != nil {
				t.Fatal(err)
			}
			rotated, _ := m.Load(ctx)
			second := rotated.Config.Notifications.NativeChannels()[channel]
			b, err := notify.NewNative(channel, second)
			if err != nil || b.Destination() == a.Destination() || second.CredentialFile == n.CredentialFile {
				t.Fatal("uncertain webhook replacement reused prior identity", err)
			}
			if _, err := os.Stat(n.CredentialFile); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("unreferenced managed file retained")
			}
			if _, err := m.Action(ctx, channel+"_setup", map[string]string{"enabled": "yes", "credential_action": "clear"}); err == nil {
				t.Fatal("enabled credential clearing accepted")
			}
			userOwned := filepath.Join(m.localPath("secrets"), channel+"-user.secret")
			if err := os.WriteFile(userOwned, []byte("user owned synthetic"), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := m.Action(ctx, channel+"_setup", map[string]string{"enabled": "no", "credential_action": "clear"}); err != nil {
				t.Fatal(err)
			}
			cleared, _ := m.Load(ctx)
			if cleared.Config.Notifications.NativeChannels()[channel].CredentialFile != "" {
				t.Fatal("explicit clearing ignored")
			}
			if _, err := os.Stat(second.CredentialFile); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("managed cleared credential retained")
			}
			if _, err := os.Stat(userOwned); err != nil {
				t.Fatal("user-owned prefixed file deleted", err)
			}
		})
	}
}

func TestNativeInvalidAndCancelledSettingsLeaveFilesAndConfigurationUnchanged(t *testing.T) {
	for _, channel := range config.NativeChannelNames() {
		t.Run(channel, func(t *testing.T) {
			m, _ := fixtureManager(t)
			before, _ := os.ReadFile(m.ConfigPath)
			for _, input := range []map[string]string{
				{"enabled": "yes", "credential_action": "replace", "url": "https://evil.invalid/SYNTHETIC-SECRET"},
				{"enabled": "yes", "credential_action": "keep", "url": nativeFixtureURL(channel)},
				{"enabled": "yes", "credential_action": "replace", "url": nativeFixtureURL(channel), "language": "fr"},
				{"enabled": "no", "credential_action": "clear", "secret_action": "replace", "secret": "SYNTHETIC-SECRET"},
			} {
				result, err := m.Action(context.Background(), channel+"_setup", input)
				if err == nil {
					t.Fatal("invalid config accepted")
				}
				if strings.Contains(result, input["url"]) && input["url"] != "" || strings.Contains(err.Error(), "SYNTHETIC-SECRET") {
					t.Fatal("invalid credential echoed")
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if _, err := m.Action(ctx, channel+"_setup", nativeSetupInput(channel)); err == nil {
				t.Fatal("cancelled settings accepted")
			}
			after, _ := os.ReadFile(m.ConfigPath)
			if string(before) != string(after) {
				t.Fatal("invalid/cancelled action changed config")
			}
			if entries, _ := os.ReadDir(m.localPath("secrets")); len(entries) != 0 {
				t.Fatal("invalid/cancelled action created credentials")
			}
		})
	}
}

func TestFeishuSigningKeepReplaceClear(t *testing.T) {
	m, _ := fixtureManager(t)
	ctx := context.Background()
	if _, err := m.Action(ctx, "feishu_setup", nativeSetupInput("feishu")); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"replace", "keep", "clear"} {
		input := map[string]string{"enabled": "yes", "credential_action": "keep", "secret_action": mode}
		if mode == "replace" {
			input["secret"] = "SYNTHETIC-REPLACEMENT-SIGNING-SECRET"
		}
		if _, err := m.Action(ctx, "feishu_setup", input); err != nil {
			t.Fatal(err)
		}
		snapshot, _ := m.Load(ctx)
		credential, err := m.readNativeCredential("feishu", snapshot.Config.Notifications.Feishu.CredentialFile)
		if err != nil || credential.URL != nativeFixtureURL("feishu") {
			t.Fatal("signing edit changed URL", err)
		}
		want := "SYNTHETIC-REPLACEMENT-SIGNING-SECRET"
		if mode == "clear" {
			want = ""
		}
		if credential.Secret != want {
			t.Fatal("signing mode incorrect")
		}
	}
}

func TestNativeSelectedTestAndDiscardUseOnlySpecifiedChannel(t *testing.T) {
	m, _ := fixtureManager(t)
	var calls []string
	m.Request = func(_ context.Context, command string, args any) (json.RawMessage, error) {
		a, ok := args.(api.NotifyChannelArgs)
		if !ok {
			t.Fatal("wrong channel args")
		}
		calls = append(calls, command+":"+a.Channel)
		return json.RawMessage(`{"ok":true}`), nil
	}
	for _, channel := range append([]string{"telegram", "webhook"}, config.NativeChannelNames()...) {
		for _, action := range []string{"notify_test", "notify_discard_isolated"} {
			if _, err := m.Action(context.Background(), action, map[string]string{"channel": channel}); err != nil {
				t.Fatal(err)
			}
			if calls[len(calls)-1] != action+":"+channel {
				t.Fatal("wrong selected target")
			}
		}
	}
	if len(calls) != 16 {
		t.Fatal("additional notifications")
	}
	if _, err := m.Action(context.Background(), "notify_test", map[string]string{"channel": "unknown"}); err == nil || len(calls) != 16 {
		t.Fatal("unknown channel contacted daemon")
	}
}

func TestNativeUpgradeDependenciesFailClosedPerEnabledChannel(t *testing.T) {
	m, _ := fixtureManager(t)
	ctx := context.Background()
	for _, channel := range config.NativeChannelNames() {
		if _, err := m.Action(ctx, channel+"_setup", nativeSetupInput(channel)); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, _ := m.Load(ctx)
	if err := upgradeKeyDependencies(snapshot.Config); err != nil {
		t.Fatal(err)
	}
	for _, channel := range config.NativeChannelNames() {
		c := snapshot.Config
		n := c.Notifications.NativeChannels()[channel]
		n.CredentialFile = m.localPath("missing")
		_ = c.Notifications.SetNativeChannel(channel, n)
		if err := upgradeKeyDependencies(c); err == nil || !strings.Contains(err.Error(), channel) {
			t.Fatal("missing enabled credentials accepted", err)
		}
		n.Enabled = false
		_ = c.Notifications.SetNativeChannel(channel, n)
		if err := upgradeKeyDependencies(c); err != nil {
			t.Fatal("disabled channel blocked upgrade", err)
		}
	}
}
