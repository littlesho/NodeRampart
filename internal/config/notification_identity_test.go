// SPDX-License-Identifier: MIT

package config

import "testing"

func TestTelegramConfigRequiresImmutableNumericChatIdentity(t *testing.T) {
	for _, chat := range []string{"1", "-1001234567890"} {
		cfg := Defaults()
		cfg.Notifications.Telegram.Enabled = true
		cfg.Notifications.Telegram.ChatID = chat
		if err := cfg.Validate(); err != nil {
			t.Fatal("valid numeric identity rejected", err)
		}
	}
	for _, chat := range []string{"@mutable", "0", "", "1.5", " 1", "9223372036854775808"} {
		cfg := Defaults()
		cfg.Notifications.Telegram.Enabled = true
		cfg.Notifications.Telegram.ChatID = chat
		if err := cfg.Validate(); err == nil {
			t.Fatal("uncertain identity accepted")
		}
	}
}
