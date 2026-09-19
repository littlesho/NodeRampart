// SPDX-License-Identifier: MIT

package daemon

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/api"
	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/evidence"
)

func TestEvidenceFingerprintExcludesIdentitiesAndTracksRules(t *testing.T) {
	c := config.Defaults()
	before, err := evidenceRuleFingerprint(c)
	if err != nil || len(before) != 64 {
		t.Fatal("fingerprint missing", err)
	}
	c.Hostname = "synthetic_private_hostname"
	c.Paths.Database = "/synthetic_private/database"
	c.Paths.SensorSocket = "/synthetic_private/sensor"
	c.Paths.ControlSocket = "/synthetic_private/control"
	c.Auth.Journalctl = "/synthetic_private/journalctl"
	c.Sensor.Interface = "synthetic_if"
	c.Sensor.Interfaces = []string{"synthetic_if2"}
	c.Geo.CityMMDB = "/synthetic_private/city"
	c.Geo.ASNMMDB = "/synthetic_private/asn"
	c.Notifications.Telegram.ChatID = "synthetic_private_chat"
	c.Notifications.Telegram.TokenFile = "/synthetic_private/token"
	after, err := evidenceRuleFingerprint(c)
	if err != nil || after != before {
		t.Fatal("identity or path changed rule fingerprint", err)
	}
	for _, change := range []func(*config.Config){func(c *config.Config) { c.Detection.SYNPacketsPerSecond++ }, func(c *config.Config) { c.Auth.Threshold++ }, func(c *config.Config) { c.Sensor.MaxTrackedFlows++ }, func(c *config.Config) { c.Alerts.Budget.MonthlyBytes++ }, func(c *config.Config) { c.Alerts.Health.GeoIPFailureThreshold++ }} {
		next := c
		change(&next)
		digest, err := evidenceRuleFingerprint(next)
		if err != nil || digest == before {
			t.Fatal("selected rule change was omitted", err)
		}
	}
	c.Alerts.Budget.MonthlyCost = math.Inf(1)
	if _, err := evidenceRuleFingerprint(c); err == nil || strings.Contains(err.Error(), "synthetic_private") {
		t.Fatal("invalid rule fingerprint succeeded or exposed input")
	}
}

func TestEvidenceSnapshotAddsQualifiedFingerprintWithoutLiveSecrets(t *testing.T) {
	app := controlTestApp(t)
	app.options.Config.Hostname = "synthetic_private_hostname"
	app.options.Config.Notifications.Telegram.TokenFile = "/synthetic_private/token"
	app.options.Config.Notifications.Telegram.ChatID = "synthetic_private_chat"
	now := time.Now().UTC()
	b, err := app.evidenceSnapshot(context.Background(), api.EvidenceArgs{Start: now.Add(-time.Hour), End: now})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.RuleFingerprint) != 64 || b.RuleFingerprintScope != evidence.RuleFingerprintNotice || b.Validate() != nil {
		t.Fatal("fingerprint provenance missing")
	}
	data, _ := json.Marshal(b)
	if strings.Contains(string(data), "synthetic_private") {
		t.Fatal("raw loaded config entered evidence DTO")
	}
	if _, err := app.evidenceSnapshot(context.Background(), api.EvidenceArgs{Start: now, End: now}); err == nil {
		t.Fatal("invalid period accepted")
	}
}
