// SPDX-License-Identifier: MIT

package manage

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"strings"
	"time"

	"github.com/littlesho/NodeRampart/internal/assets"
)

func (m *Manager) previousGeoHealth() assets.Health {
	health := assets.Health{SchemaVersion: 1, Result: "unknown"}
	data, err := readFile(m.localPath("geoip-health.json"), 4096, false, -1)
	if err == nil {
		var old assets.Health
		if json.Unmarshal(data, &old) == nil {
			old = old.NormalizeLegacy()
			if old.Validate() == nil {
				health = old
			}
		}
	}
	return health
}

func (m *Manager) updateGeo(ctx context.Context, input map[string]string, refresh bool) (string, error) {
	previous := m.previousGeoHealth()
	// Older installations already have a managed timer but no public metadata.
	// Seed its known enabled state once; ordinary unchanged refreshes still do
	// not operate on services or replace configuration.
	legacyScheduled := false
	if refresh {
		if _, err := readFile(m.localPath("geoip-health.json"), 4096, false, -1); errors.Is(err, os.ErrNotExist) {
			state, err := m.command(ctx, "/usr/bin/systemctl", "show", "--property=UnitFileState", "--value", "noderampart-geoip-update.timer")
			legacyScheduled = err == nil && (strings.TrimSpace(state) == "enabled" || strings.TrimSpace(state) == "enabled-runtime")
		}
	}
	updateResult := "unknown"
	result, updateErr := m.updateGeoData(ctx, input, refresh, &updateResult)
	health := m.previousGeoHealth()
	if legacyScheduled {
		health.Scheduled = true
	}
	health.CheckedAt = time.Now().UTC()
	// A backwards clock must not invalidate the public metadata or erase its
	// last successful check. The monitor independently refuses time regression.
	if health.CheckedAt.Before(health.LastSuccessAt) {
		health.CheckedAt = health.LastSuccessAt
	}
	if health.CheckedAt.Before(previous.CheckedAt) {
		health.CheckedAt = previous.CheckedAt
	}
	health.Result = updateResult
	// Scheduling is a separate outcome even when setup performs it immediately
	// after a verified update. An operation error must not erase that success.
	if updateResult == "ok" || updateResult == "unchanged" {
		health.LastSuccessAt, health.ConsecutiveFailures = health.CheckedAt, 0
	} else {
		health.ConsecutiveFailures = previous.ConsecutiveFailures
		if health.ConsecutiveFailures < math.MaxUint32 {
			health.ConsecutiveFailures++
		}
	}
	if err := m.writeJSON(m.localPath("geoip-health.json"), health, false); err != nil && updateErr == nil {
		return result, errors.New("GeoIP update completed, but sanitized health metadata could not be saved")
	}
	return result, updateErr
}

func (m *Manager) scheduleGeo(ctx context.Context, enabled bool) (string, error) {
	result, scheduleErr := m.scheduleGeoService(ctx, enabled)
	health := m.previousGeoHealth()
	schedule := assets.ScheduleHealth{CheckedAt: time.Now().UTC(), Result: "ok"}
	if previous := health.Schedule; previous != nil {
		if schedule.CheckedAt.Before(previous.CheckedAt) {
			schedule.CheckedAt = previous.CheckedAt
		}
		if scheduleErr != nil {
			schedule.ConsecutiveFailures = previous.ConsecutiveFailures
		}
	}
	if schedule.CheckedAt.Before(health.CheckedAt) {
		schedule.CheckedAt = health.CheckedAt
	}
	if schedule.CheckedAt.Before(health.LastSuccessAt) {
		schedule.CheckedAt = health.LastSuccessAt
	}
	if scheduleErr == nil {
		health.Scheduled = enabled
	} else {
		schedule.Result = "failed"
		if schedule.ConsecutiveFailures < math.MaxUint32 {
			schedule.ConsecutiveFailures++
		}
	}
	health.Schedule = &schedule
	if err := m.writeJSON(m.localPath("geoip-health.json"), health, false); err != nil && scheduleErr == nil {
		return result, errors.New("GeoIP schedule changed, but sanitized health metadata could not be saved")
	}
	return result, scheduleErr
}
