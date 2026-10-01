// SPDX-License-Identifier: MIT

package manage

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/littlesho/NodeRampart/internal/config"
)

func TestTelegramLanguageOnlySavePreservesIdentityAndDoesNotTest(t *testing.T) {
	m, fake := fixtureManager(t)
	ctx := context.Background()
	// A synthetic secret exercises the existing protected-file setup path only.
	if _, err := m.Action(ctx, "telegram_setup", map[string]string{"enabled": "no", "token": "123456789:" + strings.Repeat("A", 35), "chat_id": "-1001234567890", "language": "en"}); err != nil {
		t.Fatal(err)
	}
	before, err := m.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := os.ReadFile(before.Config.Notifications.Telegram.TokenFile)
	if err != nil {
		t.Fatal(err)
	}
	m.Request = func(_ context.Context, action string, _ any) (json.RawMessage, error) {
		t.Fatalf("language save sent control request %q", action)
		return nil, nil
	}
	for _, language := range []string{"zh", "en", ""} {
		if _, err := m.Action(ctx, "telegram_setup", map[string]string{"enabled": "no", "language": language}); err != nil {
			t.Fatal(err)
		}
		restarted := copyManagerFixture(m)
		after, err := restarted.Load(ctx)
		if err != nil {
			t.Fatal(err)
		}
		got := after.Config.Notifications.Telegram
		if got.Language != language || got.ChatID != before.Config.Notifications.Telegram.ChatID || got.TokenFile != before.Config.Notifications.Telegram.TokenFile {
			t.Fatal("language save changed credentials", got)
		}
		contents, err := os.ReadFile(got.TokenFile)
		if err != nil || string(contents) != string(secret) {
			t.Fatal("language save rotated token", err)
		}
		for _, call := range fake.calls {
			if !strings.HasPrefix(call, "show ") {
				t.Fatal("stopped save changed service intent", call)
			}
		}
	}
	bytesBefore, _ := os.ReadFile(m.ConfigPath)
	if _, err := m.Action(ctx, "telegram_setup", map[string]string{"enabled": "no", "language": "ZH"}); err == nil {
		t.Fatal("invalid language accepted")
	}
	bytesAfter, _ := os.ReadFile(m.ConfigPath)
	if string(bytesBefore) != string(bytesAfter) {
		t.Fatal("invalid language wrote config")
	}
	// Older setup clients omit the new field; retain the explicit existing value.
	snapshot, _ := m.Load(ctx)
	snapshot.Config.Notifications.Telegram.Language = "zh"
	if _, err := m.Save(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Action(ctx, "telegram_setup", map[string]string{"enabled": "no"}); err != nil {
		t.Fatal(err)
	}
	snapshot, err = m.Load(ctx)
	if err != nil || config.TelegramLanguage(snapshot.Config.Notifications.Telegram) != "zh" {
		t.Fatal("legacy client reset language", err)
	}
}

func TestTelegramLanguageConfigRollbackRecovery(t *testing.T) {
	m, fake := fixtureManager(t)
	fake.active["noderampartd.service"], fake.active["noderampart-sensor.service"] = true, true
	fake.restartFailures = 2
	snapshot, err := m.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Config.Notifications.Telegram.Language = "zh"
	if _, err := m.Save(context.Background(), snapshot); err == nil {
		t.Fatal("synthetic failed restart accepted")
	}
	if _, err := m.Action(context.Background(), "recover_config", map[string]string{"confirm": "RESTORE"}); err != nil {
		t.Fatal(err)
	}
	snapshot, err = m.Load(context.Background())
	if err != nil || config.TelegramLanguage(snapshot.Config.Notifications.Telegram) != "en" {
		t.Fatal("recovery did not preserve original language", err)
	}
}
