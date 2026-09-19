// SPDX-License-Identifier: MIT

package manage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/littlesho/NodeRampart/internal/assets"
)

func readGeoState(t *testing.T, m *Manager) geoState {
	t.Helper()
	data, err := readFile(m.localPath("geoip-state.json"), 4096, true, -1)
	var state geoState
	if err != nil || json.Unmarshal(data, &state) != nil || state.Version != 1 {
		t.Fatal("valid GeoIP update state unavailable")
	}
	return state
}

func installedGeoFixture(t *testing.T) (*Manager, *fakeServices, *managedGeoFixture, *bool) {
	t.Helper()
	m, fake := fixtureManager(t)
	fake.active["noderampartd.service"], fake.active["noderampart-sensor.service"] = true, true
	fixture, failASN := newManagedGeoFixture(), new(bool)
	m.Assets = syntheticGeoClientWithFixture(t, failASN, fixture)
	if _, err := m.Action(context.Background(), "geo_download", map[string]string{"account_id": "123", "license_key": "synthetic-private-key", "accepted_terms": "yes", "auto_update": "no"}); err != nil {
		t.Fatal(err)
	}
	fake.calls = nil
	return m, fake, fixture, failASN
}

func TestGeoUnchangedPreservesConfigGenerationAndServices(t *testing.T) {
	m, fake, fixture, _ := installedGeoFixture(t)
	before, err := os.ReadFile(m.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	beforeFile, err := os.Stat(m.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	previous := readGeoState(t, m)
	result, err := m.Action(context.Background(), "geo_refresh", nil)
	if err != nil || !strings.Contains(result, "unchanged") {
		t.Fatalf("identical refresh: %v", err)
	}
	after, err := os.ReadFile(m.ConfigPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("identical refresh changed configuration")
	}
	afterFile, err := os.Stat(m.ConfigPath)
	if err != nil || !os.SameFile(beforeFile, afterFile) {
		t.Fatal("identical refresh replaced configuration inode")
	}
	current := readGeoState(t, m)
	if current.Result != "unchanged" || !current.Checked.After(previous.Checked) || current.Updated != previous.Updated || current.Generation != previous.Generation || current.CityBuild != previous.CityBuild || current.ASNBuild != previous.ASNBuild {
		t.Fatal("identical refresh changed activation metadata or failed to record a check")
	}
	entries, err := os.ReadDir(m.localPath("geoip"))
	if err != nil || len(entries) != 1 || entries[0].Name() != filepath.Base(previous.Generation) {
		t.Fatal("identical refresh replaced or added a generation")
	}
	if len(fake.calls) != 0 || fixture.requests != 4 {
		t.Fatal("identical refresh must verify both downloads and issue no service operations")
	}
	if _, err := os.Lstat(m.journalPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("identical refresh created a recovery journal")
	}
}

func TestGeoUnchangedStillAppliesExplicitCredentialsAndSchedule(t *testing.T) {
	m, fake, _, _ := installedGeoFixture(t)
	previous := readGeoState(t, m)
	input := map[string]string{"account_id": "456", "license_key": "replacement-synthetic-key", "accepted_terms": "yes", "auto_update": "yes"}
	if _, err := m.Action(context.Background(), "geo_download", input); err != nil {
		t.Fatal(err)
	}
	data, err := readFile(m.localPath("maxmind.credentials.json"), 4096, true, -1)
	var credentials assets.Credentials
	if err != nil || json.Unmarshal(data, &credentials) != nil || credentials.AccountID != input["account_id"] || credentials.LicenseKey != input["license_key"] {
		t.Fatal("identical download did not store the explicit new credentials safely")
	}
	current := readGeoState(t, m)
	if current.Result != "unchanged" || current.Updated != previous.Updated || current.Generation != previous.Generation {
		t.Fatal("explicit settings unnecessarily activated identical databases")
	}
	if strings.Join(fake.calls, "\n") != "daemon-reload\nenable --now noderampart-geoip-update.timer" {
		t.Fatalf("unexpected services touched by explicit scheduling: %v", fake.calls)
	}
}

func TestGeoChangedEditionOrNoticeActivatesPairedGeneration(t *testing.T) {
	for _, name := range []string{"city", "asn", "city_notice", "asn_notice"} {
		t.Run(name, func(t *testing.T) {
			m, fake, fixture, _ := installedGeoFixture(t)
			previous := readGeoState(t, m)
			switch name {
			case "city":
				fixture.cityBuild--
			case "asn":
				fixture.asnBuild--
			case "city_notice":
				fixture.cityLicense += " revision"
			case "asn_notice":
				fixture.asnLicense += " revision"
			}
			if _, err := m.Action(context.Background(), "geo_refresh", nil); err != nil {
				t.Fatal(err)
			}
			current := readGeoState(t, m)
			if current.Result != "ok" || current.Generation == previous.Generation || !current.Updated.After(previous.Updated) {
				t.Fatal("changed edition or notice was incorrectly treated as unchanged")
			}
			snapshot, err := m.Load(context.Background())
			if err != nil || filepath.Dir(snapshot.Config.Geo.CityMMDB) != current.Generation || filepath.Dir(snapshot.Config.Geo.ASNMMDB) != current.Generation {
				t.Fatal("replacement did not activate both databases together")
			}
			if !strings.Contains(strings.Join(fake.calls, "\n"), "restart noderampartd.service") {
				t.Fatal("changed data was not activated in the running daemon")
			}
			if _, err := os.Lstat(previous.Generation); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("superseded ordinary generation was not cleaned")
			}
		})
	}
}

func TestGeoDamagedManagedGenerationIsNeverUnchanged(t *testing.T) {
	for _, name := range []string{"corrupt_city", "missing_asn", "missing_notice", "unreadable_city", "unsearchable_generation", "extra_notice", "symlink_city", "hardlink_city"} {
		t.Run(name, func(t *testing.T) {
			m, _, _, _ := installedGeoFixture(t)
			previous := readGeoState(t, m)
			city := filepath.Join(previous.Generation, "GeoLite2-City.mmdb")
			var err error
			switch name {
			case "corrupt_city":
				err = os.WriteFile(city, []byte("damaged synthetic database"), 0o640)
			case "missing_asn":
				err = os.Remove(filepath.Join(previous.Generation, "GeoLite2-ASN.mmdb"))
			case "missing_notice":
				err = os.Remove(filepath.Join(previous.Generation, "GeoLite2-City-LICENSE.txt"))
			case "unreadable_city":
				err = os.Chmod(city, 0)
			case "unsearchable_generation":
				err = os.Chmod(previous.Generation, 0o640)
				t.Cleanup(func() { _ = os.Chmod(previous.Generation, 0o750) })
			case "extra_notice":
				err = os.WriteFile(filepath.Join(previous.Generation, "extra.txt"), []byte("extra synthetic notice"), 0o640)
			case "symlink_city":
				if err = os.Remove(city); err == nil {
					err = os.Symlink(filepath.Join(previous.Generation, "GeoLite2-ASN.mmdb"), city)
				}
			case "hardlink_city":
				err = os.Link(city, filepath.Join(filepath.Dir(m.ConfigPath), "synthetic-city-link"))
			}
			if err != nil {
				t.Fatal(err)
			}
			result, err := m.Action(context.Background(), "geo_refresh", nil)
			current := readGeoState(t, m)
			if current.Result == "unchanged" || strings.Contains(result, "are unchanged") {
				t.Fatal("unsafe or incomplete active generation was accepted as unchanged")
			}
			if err == nil && (current.Result != "ok" || current.Generation == previous.Generation) {
				t.Fatal("successful repair did not activate a fresh generation")
			}
		})
	}
}

func TestGeoPendingRecoveryCannotTakeUnchangedPath(t *testing.T) {
	m, fake, _, _ := installedGeoFixture(t)
	previous := readGeoState(t, m)
	snapshot, err := m.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(m.journalPath(), []byte(`{"schema_version":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Action(context.Background(), "geo_refresh", nil); err == nil || !strings.Contains(err.Error(), "recover") {
		t.Fatal("pending journal bypassed recovery on an identical download")
	}
	current := readGeoState(t, m)
	after, err := m.Load(context.Background())
	if err != nil || after.Fingerprint != snapshot.Fingerprint || current.Result != "activation_failed" || current.Updated != previous.Updated || current.Generation != previous.Generation || len(fake.calls) != 0 {
		t.Fatal("blocked identical refresh changed active data or hid pending recovery")
	}
	if _, err := os.Stat(m.journalPath()); err != nil {
		t.Fatal("blocked refresh removed the pending recovery journal")
	}
}

func TestGeoUnchangedRejectsExternalConfigurationChange(t *testing.T) {
	m, fake, _, _ := installedGeoFixture(t)
	previous := readGeoState(t, m)
	snapshot, err := m.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Config.Hostname = "synthetic-external-change"
	changed, err := json.Marshal(snapshot.Config)
	if err != nil {
		t.Fatal(err)
	}
	transport := m.Assets.HTTP.Transport
	m.Assets.HTTP.Transport = fixtureTransport(func(request *http.Request) (*http.Response, error) {
		response, err := transport.RoundTrip(request)
		if strings.Contains(request.URL.Path, "GeoLite2-ASN") {
			if writeErr := os.WriteFile(m.ConfigPath, changed, 0o640); writeErr != nil {
				t.Fatal(writeErr)
			}
		}
		return response, err
	})
	if _, err := m.Action(context.Background(), "geo_refresh", nil); err == nil || !strings.Contains(err.Error(), "configuration changed") {
		t.Fatal("identical refresh bypassed concurrent configuration conflict")
	}
	after, err := os.ReadFile(m.ConfigPath)
	current := readGeoState(t, m)
	if err != nil || !bytes.Equal(after, changed) || current.Result != "activation_failed" || current.Generation != previous.Generation || current.Updated != previous.Updated || len(fake.calls) != 0 {
		t.Fatal("conflicting identical refresh changed configuration or activation state")
	}
}

func TestGeoUnchangedFailureStatesPreserveLastActivation(t *testing.T) {
	for _, failure := range []string{"download", "schedule"} {
		t.Run(failure, func(t *testing.T) {
			m, _, _, failASN := installedGeoFixture(t)
			if _, err := m.Action(context.Background(), "geo_refresh", nil); err != nil {
				t.Fatal(err)
			}
			previous := readGeoState(t, m)
			var err error
			want := "download_failed"
			if failure == "download" {
				*failASN = true
				_, err = m.Action(context.Background(), "geo_refresh", nil)
			} else {
				want = "unchanged"
				runner := m.runner
				m.runner = func(ctx context.Context, program string, args ...string) (string, error) {
					if args[0] == "enable" {
						return "", errors.New("synthetic scheduling failure")
					}
					return runner(ctx, program, args...)
				}
				_, err = m.Action(context.Background(), "geo_download", map[string]string{"account_id": "123", "license_key": "synthetic-private-key", "accepted_terms": "yes", "auto_update": "yes"})
			}
			current := readGeoState(t, m)
			if err == nil || current.Result != want || !current.Checked.After(previous.Checked) || current.Updated != previous.Updated || current.Generation != previous.Generation {
				t.Fatal("failed check or scheduling changed the recorded update outcome or last activation")
			}
			if failure == "schedule" {
				health := m.previousGeoHealth()
				if health.Result != "unchanged" || health.ConsecutiveFailures != 0 || health.Schedule == nil || health.Schedule.Result != "failed" || health.Schedule.ConsecutiveFailures != 1 {
					t.Fatal("schedule failure was lost or replaced successful verification")
				}
			}
		})
	}
}
