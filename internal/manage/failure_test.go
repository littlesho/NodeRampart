// SPDX-License-Identifier: MIT

package manage

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestManagementStopFailureText(t *testing.T) {
	m, _ := fixtureManager(t)
	original := m.runner
	m.runner = func(ctx context.Context, p string, args ...string) (string, error) {
		if len(args) > 0 && args[0] == "stop" {
			return "", errors.New("synthetic stop failure")
		}
		return original(ctx, p, args...)
	}
	result, err := m.Action(context.Background(), "service_stop", nil)
	if err == nil {
		t.Fatal("fixture did not fail")
	}
	if strings.Contains(result, "Services stopped") {
		t.Fatal("failed stop returned success text")
	}
}
func TestManagementDisableTimerFailure(t *testing.T) {
	m, _ := fixtureManager(t)
	m.runner = func(context.Context, string, ...string) (string, error) {
		return "", errors.New("synthetic systemctl failure while timer state is unknown")
	}
	if _, err := m.scheduleGeo(context.Background(), false); err == nil {
		t.Fatal("unknown timer state and failed disable were reported as success")
	}
}
func TestManagementGeoApplyFailureState(t *testing.T) {
	m, services := fixtureManager(t)
	fail := false
	fixture := newManagedGeoFixture()
	m.Assets = syntheticGeoClientWithFixture(t, &fail, fixture)
	input := map[string]string{"account_id": "123", "license_key": "synthetic-private-key", "accepted_terms": "yes", "auto_update": "no"}
	if _, err := m.Action(context.Background(), "geo_download", input); err != nil {
		t.Fatal(err)
	}
	beforeBytes, err := readFile(m.localPath("geoip-state.json"), 4096, true, -1)
	if err != nil {
		t.Fatal(err)
	}
	var before geoState
	if json.Unmarshal(beforeBytes, &before) != nil {
		t.Fatal("state unavailable")
	}
	services.active["noderampartd.service"], services.active["noderampart-sensor.service"] = true, true
	services.restartFailures = 1
	fixture.cityBuild-- // Different valid data must reach activation and its injected failure.
	if _, err := m.Action(context.Background(), "geo_refresh", nil); err == nil {
		t.Fatal("fixture did not fail activation")
	}
	afterBytes, err := readFile(m.localPath("geoip-state.json"), 4096, true, -1)
	if err != nil {
		t.Fatal(err)
	}
	var after geoState
	if json.Unmarshal(afterBytes, &after) != nil {
		t.Fatal("state unavailable")
	}
	if after.Result == "ok" && after.Checked.Equal(before.Checked) {
		t.Fatal("failed GeoIP activation kept the preceding success result and check time")
	}
}
