// SPDX-License-Identifier: MIT

package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/littlesho/NodeRampart/internal/api"
	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/evidence"
)

func (a *App) evidenceSnapshot(ctx context.Context, args api.EvidenceArgs) (evidence.Bundle, error) {
	if err := args.Validate(); err != nil {
		return evidence.Bundle{}, err
	}
	value, err := a.options.Store.Evidence(ctx, args)
	if err != nil {
		return evidence.Bundle{}, errors.New("retained evidence is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return evidence.Bundle{}, errors.New("evidence collection cancelled")
	}
	bundle, err := evidence.Build(value)
	if err != nil {
		return evidence.Bundle{}, err
	}
	bundle.RuleFingerprint, err = evidenceRuleFingerprint(a.options.Config)
	if err != nil {
		return evidence.Bundle{}, err
	}
	return bundle, bundle.Validate()
}

// The explicit scalar projection cannot accidentally acquire identity fields
// when Config grows. Durations are canonical integer nanoseconds; the schema
// number and stable JSON field order are part of this fingerprint's basis.
func evidenceRuleFingerprint(c config.Config) (string, error) {
	v := struct {
		Schema              int     `json:"schema"`
		SYN                 uint64  `json:"syn_packets_per_second"`
		UDP                 uint64  `json:"udp_packets_per_second"`
		ICMP                uint64  `json:"icmp_packets_per_second"`
		Bytes               uint64  `json:"bytes_per_second"`
		RecoveryRatio       float64 `json:"recovery_ratio"`
		RecoveryWindows     int     `json:"recovery_windows"`
		UpdateInterval      int64   `json:"update_interval_ns"`
		ScanPorts           int     `json:"scan_unique_ports"`
		ScanWindow          int64   `json:"scan_window_ns"`
		AuthEnabled         bool    `json:"auth_enabled"`
		AuthThreshold       int     `json:"auth_threshold"`
		AuthWindow          int64   `json:"auth_window_ns"`
		AuthCooldown        int64   `json:"auth_cooldown_ns"`
		SensorEnabled       bool    `json:"sensor_enabled"`
		BatchInterval       int64   `json:"sensor_batch_interval_ns"`
		MaxTrackedFlows     int     `json:"sensor_max_tracked_flows"`
		BudgetEnabled       bool    `json:"budget_enabled"`
		MonthlyBytes        uint64  `json:"monthly_bytes"`
		MonthlyCost         float64 `json:"monthly_cost"`
		DailyBytes          uint64  `json:"daily_bytes"`
		DailyGrowthRatio    float64 `json:"daily_growth_ratio"`
		BaselineDays        int     `json:"baseline_days"`
		HealthEnabled       bool    `json:"health_enabled"`
		GracePeriod         int64   `json:"health_grace_period_ns"`
		RecoveryPeriod      int64   `json:"health_recovery_period_ns"`
		ReminderInterval    int64   `json:"health_reminder_interval_ns"`
		GeoFailureThreshold int     `json:"geoip_failure_threshold"`
		GeoStaleAfter       int64   `json:"geoip_stale_after_ns"`
	}{Schema: 1, SYN: c.Detection.SYNPacketsPerSecond, UDP: c.Detection.UDPPacketsPerSecond, ICMP: c.Detection.ICMPPacketsPerSecond, Bytes: c.Detection.BytesPerSecond, RecoveryRatio: c.Detection.RecoveryRatio, RecoveryWindows: c.Detection.RecoveryWindows, UpdateInterval: int64(c.Detection.UpdateInterval.Duration), ScanPorts: c.Detection.ScanUniquePorts, ScanWindow: int64(c.Detection.ScanWindow.Duration), AuthEnabled: c.Auth.Enabled, AuthThreshold: c.Auth.Threshold, AuthWindow: int64(c.Auth.Window.Duration), AuthCooldown: int64(c.Auth.Cooldown.Duration), SensorEnabled: c.Sensor.Enabled, BatchInterval: int64(c.Sensor.BatchInterval.Duration), MaxTrackedFlows: c.Sensor.MaxTrackedFlows, BudgetEnabled: c.Alerts.Budget.Enabled, MonthlyBytes: c.Alerts.Budget.MonthlyBytes, MonthlyCost: c.Alerts.Budget.MonthlyCost, DailyBytes: c.Alerts.Budget.DailyBytes, DailyGrowthRatio: c.Alerts.Budget.DailyGrowthRatio, BaselineDays: c.Alerts.Budget.BaselineDays, HealthEnabled: c.Alerts.Health.Enabled, GracePeriod: int64(c.Alerts.Health.GracePeriod.Duration), RecoveryPeriod: int64(c.Alerts.Health.RecoveryPeriod.Duration), ReminderInterval: int64(c.Alerts.Health.ReminderInterval.Duration), GeoFailureThreshold: c.Alerts.Health.GeoIPFailureThreshold, GeoStaleAfter: int64(c.Alerts.Health.GeoIPStaleAfter.Duration)}
	data, err := json.Marshal(v)
	if err != nil {
		return "", errors.New("rule fingerprint is unavailable")
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
