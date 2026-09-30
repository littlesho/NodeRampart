// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/littlesho/NodeRampart/internal/config"
)

func TestOptionalResourceAbsenceKeepsBaseDependencies(t *testing.T) {
	cfg := config.Defaults()
	root := t.TempDir()
	cfg.Geo.CityMMDB = filepath.Join(root, "missing.mmdb")
	cfg.Billing.Enabled = true
	cfg.Billing.ProfilePath = filepath.Join(root, "missing-price.json")
	cfg.Notifications.Telegram.Enabled = true
	cfg.Notifications.Telegram.TokenFile = filepath.Join(root, "missing-token")
	cfg.Notifications.Telegram.ChatID = "123"
	d, err := loadOptional(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer d.geo.Close()
	if d.geo == nil || d.sender != nil || d.billing != nil || len(d.failures) != 3 {
		t.Fatalf("lost explicit optional failures: %+v", d.failures)
	}
	// Correct the local credential and explicitly reload/restart. No HTTP sent.
	if err := os.WriteFile(cfg.Notifications.Telegram.TokenFile, []byte("123456789:abcdefghijklmnopqrstuvwxyzABCDEFGHIJ"), 0o600); err != nil {
		t.Fatal(err)
	}
	d2, err := loadOptional(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer d2.geo.Close()
	if d2.sender == nil || d2.failures["telegram"] != "" {
		t.Fatal("corrected optional credential did not recover")
	}
}

func TestOptionalIsolationPreservesUnsafePathAndInvalidConfigFailures(t *testing.T) {
	for _, kind := range []string{"symlink", "permissions", "billing_json", "billing_future"} {
		t.Run(kind, func(t *testing.T) {
			cfg := config.Defaults()
			path := filepath.Join(t.TempDir(), "resource")
			data := []byte("invalid")
			if kind == "billing_future" {
				data = []byte(`{"schema_version":999}`)
			}
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			if kind == "symlink" {
				link := path + "-link"
				if err := os.Symlink(path, link); err != nil {
					t.Fatal(err)
				}
				cfg.Geo.CityMMDB = link
			}
			if kind == "permissions" {
				if err := os.Chmod(path, 0o666); err != nil {
					t.Fatal(err)
				}
				cfg.Geo.CityMMDB = path
			}
			if kind == "billing_json" || kind == "billing_future" {
				cfg.Billing.Enabled = true
				cfg.Billing.ProfilePath = path
			}
			if d, err := loadOptional(cfg); err == nil {
				d.geo.Close()
				t.Fatal("unsafe resource or invalid configuration silently downgraded")
			}
		})
	}
}
