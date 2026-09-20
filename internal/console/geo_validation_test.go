// SPDX-License-Identifier: MIT

package console

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/littlesho/NodeRampart/internal/assets"
)

type geoDiagnosticTransport func(*http.Request) (*http.Response, error)

func (f geoDiagnosticTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func geoDiagnosticErrorFixture(t *testing.T, edition string, budget bool) error {
	t.Helper()
	data := []byte("synthetic invalid database")
	if budget {
		// A bounded metadata map containing 32 arrays of 512 empty strings
		// exceeds the per-record value budget before any reader allocation.
		data = []byte{0xab, 0xcd, 0xef, 'M', 'a', 'x', 'M', 'i', 'n', 'd', '.', 'c', 'o', 'm', 0xe1, 0x41, 'k', 0x1d, 4, 3}
		for range 32 {
			data = append(data, 0x1e, 4, 0, 227)
			for range 512 {
				data = append(data, 0x40)
			}
		}
	}
	var raw bytes.Buffer
	zip := gzip.NewWriter(&raw)
	archive := tar.NewWriter(zip)
	if err := archive.WriteHeader(&tar.Header{Name: edition + "_20260101/" + edition + ".mmdb", Typeflag: tar.TypeReg, Mode: 0o600, Size: int64(len(data))}); err != nil {
		t.Fatal(err)
	}
	if _, err := archive.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zip.Close(); err != nil {
		t.Fatal(err)
	}
	client := assets.Client{HTTP: &http.Client{Transport: geoDiagnosticTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(raw.Bytes())), ContentLength: int64(raw.Len()), Request: r}, nil
	})}}
	bundle, err := client.DownloadGeo(context.Background(), assets.Credentials{AccountID: "123", LicenseKey: "synthetic-key"})
	if bundle != nil {
		_ = bundle.Close()
		t.Fatal("invalid synthetic MMDB returned a usable bundle")
	}
	if err == nil {
		t.Fatal("invalid synthetic MMDB was accepted")
	}
	return err
}

func TestGeoValidationMessagesAreScopedAndBilingual(t *testing.T) {
	for _, budget := range []bool{false, true} {
		err := geoDiagnosticErrorFixture(t, "GeoLite2-City", budget)
		wrapped := fmt.Errorf("SYNTHETIC_SIGNED_URL_BODY_SECRET: %w", err)
		for _, action := range []string{"geo_download", "geo_refresh"} {
			for _, lang := range []string{"en", "zh"} {
				message, ok := geoValidationFailure(action, wrapped, lang)
				if !ok || !strings.Contains(message, "City") || !strings.Contains(message, "MMDB") || strings.Contains(message, "SECRET") {
					t.Fatalf("unsafe or missing diagnostic: %q", message)
				}
				if budget && (lang == "en" && !strings.Contains(message, "resource budget exceeded") || lang == "zh" && !strings.Contains(message, "资源预算已耗尽")) {
					t.Fatalf("budget misclassified: %q", message)
				}
			}
		}
		if _, ok := geoValidationFailure("telegram_setup", wrapped, "en"); ok {
			t.Fatal("GeoIP diagnostic escaped its action scope")
		}
	}
	for _, err := range []error{errors.New("SYNTHETIC_SECRET"), context.Canceled, context.DeadlineExceeded} {
		if message, ok := geoValidationFailure("geo_refresh", err, "en"); ok || message != "" {
			t.Fatal("unknown sensitive error acquired a diagnostic")
		}
	}
}

func TestSimulationGeoRefreshShowsOnlySafeDiagnostic(t *testing.T) {
	for _, known := range []bool{false, true} {
		t.Run(fmt.Sprint(known), func(t *testing.T) {
			err := errors.New("SYNTHETIC_SIGNED_URL_BODY_SECRET")
			want := "Secret values are not shown"
			if known {
				err = fmt.Errorf("SYNTHETIC_SIGNED_URL_BODY_SECRET: %w", geoDiagnosticErrorFixture(t, "GeoLite2-City", true))
				want = "resource budget exceeded"
			}
			b := newBackend()
			b.action = func(context.Context, string, map[string]string) (string, error) {
				return "SYNTHETIC_BODY_SECRET", err
			}
			s, _, _ := launch(t, false, b)
			awaitFrame(t, s, "Main menu")
			selectIndex(s, 6)
			awaitFrame(t, s, "Local GeoIP")
			selectIndex(s, 2)
			awaitFrame(t, s, "Confirm action")
			key(s, tcell.KeyRight)
			key(s, tcell.KeyEnter)
			frame := awaitFrame(t, s, want)
			if strings.Contains(frame, "SYNTHETIC") || strings.Contains(frame, "SECRET") {
				t.Fatal("GeoIP error or result body reached the screen")
			}
		})
	}
}
