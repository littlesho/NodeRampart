// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/littlesho/NodeRampart/internal/config"
)

func TestExplicitConfigValidationInspectsNativeCredentialsWithoutSending(t *testing.T) {
	for _, channel := range config.NativeChannelNames() {
		t.Run(channel, func(t *testing.T) {
			dir := t.TempDir()
			cfg := config.Defaults()
			ref := filepath.Join(dir, channel+".credential.json")
			if err := cfg.Notifications.SetNativeChannel(channel, config.NativeChannelConfig{Enabled: true, CredentialFile: ref, Language: "en"}); err != nil {
				t.Fatal(err)
			}
			// Validation is local only; a missing file and an unapproved URL must
			// fail without disclosing any part of the protected JSON.
			if err := validateNativeReferences(cfg, filepath.Join(dir, "config.json")); err == nil {
				t.Fatal("missing credentials accepted")
			}
			if err := os.WriteFile(ref, []byte(`{"url":"https://private.invalid/secret-synthetic-value"}`), 0600); err != nil {
				t.Fatal(err)
			}
			err := validateNativeReferences(cfg, filepath.Join(dir, "config.json"))
			if err == nil || strings.Contains(err.Error(), "secret-synthetic") || strings.Contains(err.Error(), "private.invalid") {
				t.Fatal("invalid protected credential accepted or disclosed")
			}
			cfg.Notifications.SetNativeChannel(channel, config.NativeChannelConfig{CredentialFile: ref})
			if err := validateNativeReferences(cfg, filepath.Join(dir, "config.json")); err != nil {
				t.Fatal("disabled optional credentials stopped config validation", err)
			}
			cfg.Notifications.SetNativeChannel(channel, config.NativeChannelConfig{Enabled: true, CredentialFile: filepath.Join(t.TempDir(), "outside.json")})
			if err := validateNativeReferences(cfg, filepath.Join(dir, "config.json")); err == nil {
				t.Fatal("out-of-directory credential accepted")
			}
		})
	}
}
