// SPDX-License-Identifier: MIT

package config

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAlertsLegacyDefaultsAndPartialConfiguration(t *testing.T) {
	for _, body := range []string{`{"schema_version":1}`, `{"schema_version":1,"alerts":{"budget":{"enabled":true,"monthly_bytes":100}}}`} {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Alerts.Health.Enabled || cfg.Alerts.Budget.BaselineDays != 7 || cfg.Alerts.Health.GracePeriod.Duration != 2*time.Minute {
			t.Fatal("optional alert fields did not inherit safe defaults")
		}
	}
	if Defaults().Alerts.Budget.Enabled || Defaults().Alerts.Health.Enabled {
		t.Fatal("legacy configurations enabled external notification behavior")
	}
}

func TestAlertsValidationBounds(t *testing.T) {
	cases := map[string]func(*Config){
		"empty enabled budget":    func(c *Config) { c.Alerts.Budget.Enabled = true },
		"monthly signed overflow": func(c *Config) { c.Alerts.Budget.MonthlyBytes = math.MaxInt64 + 1 },
		"daily signed overflow":   func(c *Config) { c.Alerts.Budget.DailyBytes = math.MaxInt64 + 1 },
		"cost NaN":                func(c *Config) { c.Alerts.Budget.MonthlyCost = math.NaN() },
		"cost infinite":           func(c *Config) { c.Alerts.Budget.MonthlyCost = math.Inf(1) },
		"cost negative":           func(c *Config) { c.Alerts.Budget.MonthlyCost = -1 },
		"cost too large":          func(c *Config) { c.Alerts.Budget.MonthlyCost = 1e12 + 1 },
		"cost requires billing": func(c *Config) {
			c.Alerts.Budget.Enabled = true
			c.Alerts.Budget.MonthlyCost = 10
			c.Billing.Enabled = false
		},
		"growth equal one":       func(c *Config) { c.Alerts.Budget.DailyGrowthRatio = 1 },
		"growth unbounded":       func(c *Config) { c.Alerts.Budget.DailyGrowthRatio = 101 },
		"growth infinite":        func(c *Config) { c.Alerts.Budget.DailyGrowthRatio = math.Inf(1) },
		"growth negative":        func(c *Config) { c.Alerts.Budget.DailyGrowthRatio = -1 },
		"baseline short":         func(c *Config) { c.Alerts.Budget.BaselineDays = 2 },
		"baseline long":          func(c *Config) { c.Alerts.Budget.BaselineDays = 31 },
		"grace short":            func(c *Config) { c.Alerts.Health.GracePeriod.Duration = 9 * time.Second },
		"grace long":             func(c *Config) { c.Alerts.Health.GracePeriod.Duration = time.Hour + time.Second },
		"recovery short":         func(c *Config) { c.Alerts.Health.RecoveryPeriod.Duration = 9 * time.Second },
		"reminder short":         func(c *Config) { c.Alerts.Health.ReminderInterval.Duration = time.Minute },
		"reminder long":          func(c *Config) { c.Alerts.Health.ReminderInterval.Duration = 8 * 24 * time.Hour },
		"failure threshold zero": func(c *Config) { c.Alerts.Health.GeoIPFailureThreshold = 0 },
		"failure threshold high": func(c *Config) { c.Alerts.Health.GeoIPFailureThreshold = 31 },
		"stale short":            func(c *Config) { c.Alerts.Health.GeoIPStaleAfter.Duration = 23 * time.Hour },
		"stale long":             func(c *Config) { c.Alerts.Health.GeoIPStaleAfter.Duration = 31 * 24 * time.Hour },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := Defaults()
			change(&cfg)
			if cfg.Validate() == nil {
				t.Fatal("invalid alert configuration accepted")
			}
		})
	}
	cfg := Defaults()
	cfg.Alerts.Budget.MonthlyBytes = math.MaxInt64
	cfg.Alerts.Budget.DailyBytes = math.MaxInt64
	cfg.Alerts.Budget.DailyGrowthRatio = 100
	cfg.Alerts.Health.ReminderInterval.Duration = 0
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}
