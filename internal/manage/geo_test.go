// SPDX-License-Identifier: MIT

package manage

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/assets"
)

type fixtureTransport func(*http.Request) (*http.Response, error)

func (f fixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func syntheticGeoClient(t *testing.T, failASN *bool) *assets.Client {
	return syntheticGeoClientWithFixture(t, failASN, newManagedGeoFixture())
}

type managedGeoFixture struct {
	cityBuild, asnBuild     uint64
	cityLicense, asnLicense string
	requests                int
}

func newManagedGeoFixture() *managedGeoFixture {
	epoch := uint64(time.Now().Add(-24 * time.Hour).Unix())
	return &managedGeoFixture{cityBuild: epoch, asnBuild: epoch, cityLicense: "Synthetic test fixture license", asnLicense: "Synthetic test fixture license"}
}

func syntheticGeoClientWithFixture(t *testing.T, failASN *bool, fixture *managedGeoFixture) *assets.Client {
	t.Helper()
	return &assets.Client{HTTP: &http.Client{Transport: fixtureTransport(func(r *http.Request) (*http.Response, error) {
		fixture.requests++
		if r.URL.Host != "download.maxmind.com" {
			t.Fatal("unexpected GeoIP endpoint")
		}
		edition := "GeoLite2-City"
		build, license := fixture.cityBuild, fixture.cityLicense
		if strings.Contains(r.URL.Path, "GeoLite2-ASN") {
			edition = "GeoLite2-ASN"
			build, license = fixture.asnBuild, fixture.asnLicense
			if *failASN {
				return nil, errors.New("synthetic-private-key transport failure")
			}
		}
		var out bytes.Buffer
		z := gzip.NewWriter(&out)
		tarfile := tar.NewWriter(z)
		for name, data := range map[string][]byte{edition + ".mmdb": managedSyntheticMMDB(edition, build), "LICENSE.txt": []byte(license), "COPYRIGHT.txt": []byte("Synthetic test fixture authors")} {
			if err := tarfile.WriteHeader(&tar.Header{Name: edition + "_20260101/" + name, Mode: 0o644, Size: int64(len(data))}); err != nil {
				t.Fatal(err)
			}
			if _, err := tarfile.Write(data); err != nil {
				t.Fatal(err)
			}
		}
		if err := tarfile.Close(); err != nil {
			t.Fatal(err)
		}
		if err := z.Close(); err != nil {
			t.Fatal(err)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(out.Bytes())), ContentLength: int64(out.Len()), Request: r}, nil
	})}}
}

func TestGeoActivationIsPairedAndFailedRefreshPreservesIt(t *testing.T) {
	m, _ := fixtureManager(t)
	fail := false
	m.Assets = syntheticGeoClient(t, &fail)
	input := map[string]string{"account_id": "123", "license_key": "synthetic-private-key", "accepted_terms": "yes", "auto_update": "no"}
	for i := 0; i < 2; i++ {
		result, err := m.Action(context.Background(), "geo_download", input)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(result, input["license_key"]) {
			t.Fatal("credential returned")
		}
	}
	snapshot, err := m.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(snapshot.Config.Geo.CityMMDB) != filepath.Dir(snapshot.Config.Geo.ASNMMDB) {
		t.Fatal("unpaired active databases")
	}
	entries, err := os.ReadDir(m.localPath("geoip"))
	if err != nil || len(entries) != 1 {
		t.Fatal("old managed GeoIP generations retained")
	}
	if _, err := readFile(m.localPath("maxmind.credentials.json"), 4096, true, -1); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(m.ConfigPath)
	fail = true
	result, err := m.Action(context.Background(), "geo_refresh", nil)
	if err == nil || strings.Contains(result+err.Error(), input["license_key"]) {
		t.Fatal("missing or secret-bearing download failure")
	}
	after, _ := os.ReadFile(m.ConfigPath)
	if !bytes.Equal(before, after) {
		t.Fatal("failed second edition changed configuration")
	}
	if _, err := os.Stat(snapshot.Config.Geo.CityMMDB); err != nil {
		t.Fatal("failed refresh removed valid old data")
	}
	status, err := m.geoStatus(context.Background())
	if err != nil || !strings.Contains(status, "download_failed") {
		t.Fatal("failed refresh not visible")
	}
}

func TestGeoTimerIsDataOnlyAndExplicit(t *testing.T) {
	m, fake := fixtureManager(t)
	if _, err := m.scheduleGeo(context.Background(), true); err == nil {
		t.Fatal("timer enabled without credentials")
	}
	if err := m.writeJSON(m.localPath("maxmind.credentials.json"), assets.Credentials{AccountID: "123", LicenseKey: "synthetic-only"}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := m.scheduleGeo(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(m.unitDir, "noderampart-geoip-update.service"))
	if err != nil {
		t.Fatal(err)
	}
	unit := string(data)
	if !strings.Contains(unit, "ExecStart=/usr/bin/noderampart assets update") || strings.Contains(unit, "CAP_SYS_ADMIN") || strings.Contains(unit, "synthetic-only") {
		t.Fatal("unsafe data updater unit")
	}
	if len(fake.calls) == 0 || fake.calls[len(fake.calls)-1] != "enable --now noderampart-geoip-update.timer" {
		t.Fatal("timer not activated explicitly")
	}
	rule, err := readFile(filepath.Join(m.tmpfilesDir, "noderampart-management.conf"), 4096, false, -1)
	if err != nil || !strings.Contains(string(rule), "f /run/noderampart-management.lock 0600 root root - -") || !strings.Contains(unit, "systemd-tmpfiles-setup.service") {
		t.Fatal("updater lacks a safe boot-time lock creator")
	}
}

// Empty test MMDB generated from metadata; no licensed database records.
func managedSyntheticMMDB(edition string, epoch uint64) []byte {
	text := func(s string) []byte {
		if len(s) < 29 {
			return append([]byte{0x40 | byte(len(s))}, []byte(s)...)
		}
		return append([]byte{0x5d, byte(len(s) - 29)}, []byte(s)...)
	}
	integer := func(value uint64, kind byte) []byte {
		data := []byte{}
		for value > 0 {
			data = append([]byte{byte(value)}, data...)
			value >>= 8
		}
		if kind <= 7 {
			return append([]byte{kind<<5 | byte(len(data))}, data...)
		}
		return append([]byte{byte(len(data)), kind - 7}, data...)
	}
	metadata := []byte{0xe9}
	add := func(key string, value []byte) {
		metadata = append(metadata, text(key)...)
		metadata = append(metadata, value...)
	}
	add("binary_format_major_version", integer(2, 5))
	add("binary_format_minor_version", integer(0, 5))
	add("build_epoch", integer(epoch, 9))
	add("database_type", text(edition))
	description := append([]byte{0xe1}, text("en")...)
	description = append(description, text("Synthetic empty fixture")...)
	add("description", description)
	add("ip_version", integer(4, 5))
	add("node_count", integer(1, 6))
	add("record_size", integer(24, 5))
	add("languages", append([]byte{1, 4}, text("en")...))
	data := append([]byte{0, 0, 1, 0, 0, 1}, make([]byte, 16)...)
	data = append(data, []byte{0xab, 0xcd, 0xef, 'M', 'a', 'x', 'M', 'i', 'n', 'd', '.', 'c', 'o', 'm'}...)
	return append(data, metadata...)
}
