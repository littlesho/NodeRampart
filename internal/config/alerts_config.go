// SPDX-License-Identifier: MIT

package config

import (
	"errors"
	"math"
	"time"
)

type AlertsConfig struct {
	Budget BudgetAlertsConfig `json:"budget"`
	Health HealthAlertsConfig `json:"health"`
}

type BudgetAlertsConfig struct {
	Enabled          bool    `json:"enabled"`
	MonthlyBytes     uint64  `json:"monthly_bytes"`
	MonthlyCost      float64 `json:"monthly_cost"`
	DailyBytes       uint64  `json:"daily_bytes"`
	DailyGrowthRatio float64 `json:"daily_growth_ratio"`
	BaselineDays     int     `json:"baseline_days"`
}

type HealthAlertsConfig struct {
	Enabled               bool     `json:"enabled"`
	GracePeriod           Duration `json:"grace_period"`
	RecoveryPeriod        Duration `json:"recovery_period"`
	ReminderInterval      Duration `json:"reminder_interval"`
	GeoIPFailureThreshold int      `json:"geoip_failure_threshold"`
	GeoIPStaleAfter       Duration `json:"geoip_stale_after"`
}

func defaultAlerts() AlertsConfig {
	return AlertsConfig{Budget: BudgetAlertsConfig{BaselineDays: 7}, Health: HealthAlertsConfig{GracePeriod: Duration{2 * time.Minute}, RecoveryPeriod: Duration{time.Minute}, ReminderInterval: Duration{24 * time.Hour}, GeoIPFailureThreshold: 2, GeoIPStaleAfter: Duration{72 * time.Hour}}}
}

func (c Config) validateAlerts() error {
	b, h := c.Alerts.Budget, c.Alerts.Health
	if b.MonthlyBytes > math.MaxInt64 || b.DailyBytes > math.MaxInt64 {
		return errors.New("alerts budget byte limits must be 0..9223372036854775807")
	}
	if math.IsNaN(b.MonthlyCost) || math.IsInf(b.MonthlyCost, 0) || b.MonthlyCost < 0 || b.MonthlyCost > 1e12 {
		return errors.New("alerts.budget.monthly_cost must be finite and 0..1e12")
	}
	if math.IsNaN(b.DailyGrowthRatio) || math.IsInf(b.DailyGrowthRatio, 0) || b.DailyGrowthRatio != 0 && (b.DailyGrowthRatio <= 1 || b.DailyGrowthRatio > 100) {
		return errors.New("alerts.budget.daily_growth_ratio must be 0 or greater than 1 and at most 100")
	}
	if b.BaselineDays < 3 || b.BaselineDays > 30 {
		return errors.New("alerts.budget.baseline_days must be 3..30")
	}
	if b.Enabled && b.MonthlyBytes == 0 && b.MonthlyCost == 0 && b.DailyBytes == 0 && b.DailyGrowthRatio == 0 {
		return errors.New("enabled budget alerts require at least one threshold")
	}
	if b.Enabled && b.MonthlyCost > 0 && !c.Billing.Enabled {
		return errors.New("monthly cost alerts require billing.enabled")
	}
	if h.GracePeriod.Duration < 10*time.Second || h.GracePeriod.Duration > time.Hour {
		return errors.New("alerts.health.grace_period must be 10s..1h")
	}
	if h.RecoveryPeriod.Duration < 10*time.Second || h.RecoveryPeriod.Duration > time.Hour {
		return errors.New("alerts.health.recovery_period must be 10s..1h")
	}
	if h.ReminderInterval.Duration != 0 && (h.ReminderInterval.Duration < time.Hour || h.ReminderInterval.Duration > 7*24*time.Hour) {
		return errors.New("alerts.health.reminder_interval must be 0 or 1h..168h")
	}
	if h.GeoIPFailureThreshold < 1 || h.GeoIPFailureThreshold > 30 {
		return errors.New("alerts.health.geoip_failure_threshold must be 1..30")
	}
	if h.GeoIPStaleAfter.Duration < 24*time.Hour || h.GeoIPStaleAfter.Duration > 30*24*time.Hour {
		return errors.New("alerts.health.geoip_stale_after must be 24h..720h")
	}
	return nil
}
