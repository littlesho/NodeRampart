// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/littlesho/NodeRampart/internal/config"
)

func TestNativeUnsafeCredentialsAreOptionalFailClosed(t *testing.T) {
	for _, fault := range []string{"missing", "unsafe_permissions", "symlink", "invalid_url", "outside_config_dir"} {
		t.Run(fault, func(t *testing.T) {
			cfg := config.Defaults()
			dir := t.TempDir()
			path := filepath.Join(dir, "wecom.credential.json")
			data, _ := json.Marshal(config.NativeCredential{URL: "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=" + strings.Repeat("S", 32)})
			if fault == "outside_config_dir" {
				path = filepath.Join(t.TempDir(), "other.credential.json")
			}
			if fault == "invalid_url" {
				data = []byte(`{"url":"https://localhost/private-token"}`)
			}
			if fault != "missing" {
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if fault == "unsafe_permissions" {
				if err := os.Chmod(path, 0644); err != nil {
					t.Fatal(err)
				}
			}
			if fault == "symlink" {
				target := path + ".target"
				if err := os.Rename(path, target); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			}
			n := cfg.Notifications.WeCom
			n.Enabled = true
			n.CredentialFile = path
			cfg.Notifications.WeCom = n
			optional, err := loadOptionalAt(cfg, dir)
			if err != nil {
				t.Fatal("optional native stopped monitoring", err)
			}
			defer optional.geo.Close()
			if optional.native["wecom"] != nil || optional.failures["wecom"] != "wecom_credentials_unavailable" {
				t.Fatal("unsafe optional sender was active")
			}
			output, _ := json.Marshal(optional.failures)
			if strings.Contains(string(output), "private-token") || strings.Contains(string(output), "https://") {
				t.Fatal("optional error leaked URL")
			}
		})
	}
}
