// SPDX-License-Identifier: MIT

package assets

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestGeoRequestDiagnosticClassifiesAndRedacts(t *testing.T) {
	const secret = "SYNTHETIC_PRIVATE_KEY_AND_SIGNED_URL"
	for _, tc := range []struct {
		name, reason string
		transport    error
		status       int
	}{
		{name: "dns", reason: "dns_lookup_failed", transport: &net.DNSError{Err: secret, Name: secret}},
		{name: "route", reason: "network_unreachable", transport: &net.OpError{Op: "dial", Err: syscall.ENETUNREACH}},
		{name: "refused", reason: "connection_refused", transport: syscall.ECONNREFUSED},
		{name: "timeout", reason: "timeout", transport: fmt.Errorf("%s: %w", secret, context.DeadlineExceeded)},
		{name: "cancelled", reason: "cancelled", transport: fmt.Errorf("%s: %w", secret, context.Canceled)},
		{name: "tls", reason: "tls_failed", transport: x509.HostnameError{Certificate: &x509.Certificate{}, Host: secret}},
		{name: "unknown", reason: "failed", transport: errors.New(secret)},
		{name: "unauthorized", reason: "http_status", status: 401},
		{name: "forbidden", reason: "http_status", status: 403},
		{name: "rate_limit", reason: "http_status", status: 429},
		{name: "server_error", reason: "http_status", status: 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := testClient(func(r *http.Request) (*http.Response, error) {
				if tc.transport != nil {
					return nil, &url.Error{Op: "Get", URL: "https://invalid.example/?signature=" + secret, Err: tc.transport}
				}
				return response(r, tc.status, []byte(secret)), nil
			})
			bundle, err := client.DownloadGeo(context.Background(), Credentials{AccountID: "123", LicenseKey: secret})
			if bundle != nil || err == nil {
				t.Fatal("failed update returned publishable data")
			}
			diagnostic, ok := GeoUpdateDiagnostic(err)
			if !ok || diagnostic.Stage != "download" || diagnostic.Reason != tc.reason || diagnostic.Edition != "City" || diagnostic.HTTPStatus != tc.status {
				t.Fatalf("incorrect failure diagnostic: %+v %v", diagnostic, err)
			}
			encoded, marshalErr := json.Marshal(diagnostic)
			if marshalErr != nil || strings.Contains(string(encoded)+err.Error()+diagnostic.Summary(), secret) {
				t.Fatal("public diagnostic exposed transport, credentials or response data")
			}
			if tc.reason == "timeout" && !errors.Is(err, context.DeadlineExceeded) || tc.reason == "cancelled" && !errors.Is(err, context.Canceled) {
				t.Fatal("sanitization lost cancellation semantics")
			}
		})
	}
}

func TestGeoTLSAlertUsesHandshakeBoundary(t *testing.T) {
	transport := &http.Transport{
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12, MaxVersion: tls.VersionTLS12},
		TLSHandshakeTimeout: time.Second,
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			client, server := net.Pipe()
			_ = server.SetDeadline(time.Now().Add(time.Second))
			go func() {
				defer server.Close()
				// This in-memory server rejects the client's protocol version.
				// Go returns an unexported TLS alert through net.OpError here.
				_ = tls.Server(server, &tls.Config{MinVersion: tls.VersionTLS13, MaxVersion: tls.VersionTLS13}).Handshake()
			}()
			return client, nil
		},
	}
	defer transport.CloseIdleConnections()
	client := &Client{HTTP: &http.Client{Transport: transport}}
	_, err := client.DownloadGeo(context.Background(), Credentials{AccountID: "123", LicenseKey: "synthetic-key"})
	diagnostic, ok := GeoUpdateDiagnostic(err)
	if !ok || diagnostic.Stage != "download" || diagnostic.Reason != "tls_failed" {
		t.Fatalf("remote TLS alert lost its handshake classification: %+v %v", diagnostic, err)
	}
}

type geoFailureReader struct{ err error }

func (r geoFailureReader) Read([]byte) (int, error) { return 0, r.err }

type geoCancelBody struct {
	reader *bytes.Reader
	cancel context.CancelFunc
	read   int
}

func (r *geoCancelBody) Read(buffer []byte) (int, error) {
	n, err := r.reader.Read(buffer[:min(len(buffer), 256)])
	r.read += n
	if r.read >= 4096 {
		r.cancel()
	}
	return n, err
}

func TestGeoCancellationDuringMMDBStagingKeepsCause(t *testing.T) {
	data := make([]byte, 64<<10)
	_, _ = rand.New(rand.NewSource(1)).Read(data) // Incompressible, synthetic bytes.
	raw := geoArchive(t, "GeoLite2-City", func(_ *[]*tar.Header, members *[][]byte) { (*members)[0] = data })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	body := &geoCancelBody{reader: bytes.NewReader(raw), cancel: cancel}
	client := testClient(func(request *http.Request) (*http.Response, error) {
		v := response(request, 200, nil)
		v.Body, v.ContentLength = io.NopCloser(body), int64(len(raw))
		return v, nil
	})
	_, err := client.DownloadGeo(ctx, Credentials{AccountID: "123", LicenseKey: "synthetic-key"})
	diagnostic, ok := GeoUpdateDiagnostic(err)
	if body.read < 4096 || body.read >= len(raw) || !errors.Is(err, context.Canceled) || !ok || diagnostic.Stage != "download" || diagnostic.Reason != "cancelled" {
		t.Fatalf("mid-stream cancellation lost its cause: bytes=%d/%d diagnostic=%+v err=%v", body.read, len(raw), diagnostic, err)
	}
}

func TestGeoMalformedArchiveContentIsNotStorageFailure(t *testing.T) {
	var truncatedTar bytes.Buffer
	archive := tar.NewWriter(&truncatedTar)
	if err := archive.WriteHeader(&tar.Header{Name: "GeoLite2-City.mmdb", Size: 32, Mode: 0o644}); err != nil {
		t.Fatal(err)
	}
	_, _ = archive.Write([]byte("short"))
	var truncatedGzip bytes.Buffer
	compressed := gzip.NewWriter(&truncatedGzip)
	_, _ = compressed.Write(truncatedTar.Bytes())
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	for name, raw := range map[string][]byte{
		"notice_nul":    geoArchive(t, "GeoLite2-City", func(_ *[]*tar.Header, members *[][]byte) { (*members)[1] = []byte("invalid\x00notice") }),
		"notice_utf8":   geoArchive(t, "GeoLite2-City", func(_ *[]*tar.Header, members *[][]byte) { (*members)[1] = []byte{0xff} }),
		"tar_truncated": truncatedGzip.Bytes(),
	} {
		t.Run(name, func(t *testing.T) {
			client := testClient(func(request *http.Request) (*http.Response, error) { return response(request, 200, raw), nil })
			_, err := client.DownloadGeo(context.Background(), Credentials{AccountID: "123", LicenseKey: "synthetic-key"})
			diagnostic, ok := GeoUpdateDiagnostic(err)
			if !ok || diagnostic.Stage != "archive" || diagnostic.Reason != "invalid" || diagnostic.Edition != "City" {
				t.Fatalf("remote content problem was reported as local storage failure: %+v %v", diagnostic, err)
			}
		})
	}
}

func TestGeoStreamDiagnosticSurvivesArchiveBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, reason string
		setup        func(*http.Request) *http.Response
	}{
		{"body_timeout", "timeout", func(r *http.Request) *http.Response {
			v := response(r, 200, nil)
			v.ContentLength, v.Body = -1, io.NopCloser(geoFailureReader{context.DeadlineExceeded})
			return v
		}},
		{"body_truncated", "response_read_failed", func(r *http.Request) *http.Response {
			v := response(r, 200, nil)
			v.ContentLength, v.Body = -1, io.NopCloser(geoFailureReader{io.ErrUnexpectedEOF})
			return v
		}},
		{"content_length", "response_too_large", func(r *http.Request) *http.Response {
			v := response(r, 200, nil)
			v.ContentLength = maxCompressed + 1
			return v
		}},
		{"redirect", "redirect_rejected", func(r *http.Request) *http.Response {
			v := response(r, 302, nil)
			v.Header.Set("Location", "https://unapproved.invalid/?key=SYNTHETIC_PRIVATE_KEY")
			return v
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := testClient(func(r *http.Request) (*http.Response, error) { return tc.setup(r), nil })
			_, err := client.DownloadGeo(context.Background(), Credentials{AccountID: "123", LicenseKey: "synthetic-key"})
			diagnostic, ok := GeoUpdateDiagnostic(err)
			if !ok || diagnostic.Stage != "download" || diagnostic.Reason != tc.reason || diagnostic.Edition != "City" || strings.Contains(err.Error(), "SYNTHETIC_PRIVATE_KEY") {
				t.Fatalf("body failure lost its safe cause: %+v %v", diagnostic, err)
			}
		})
	}
}

func TestGeoDiagnosticRejectsUntrustedFields(t *testing.T) {
	for _, diagnostic := range []GeoDiagnostic{
		{Stage: "SECRET", Reason: "failed"},
		{Stage: "download", Reason: "SECRET"},
		{Stage: "download", Reason: "failed", Edition: "SECRET"},
		{Stage: "download", Reason: "http_status", HTTPStatus: 99},
		{Stage: "download", Reason: "http_status", HTTPStatus: 200},
		{Stage: "download", Reason: "http_status", HTTPStatus: 600},
		{Stage: "validation", Reason: "http_status", HTTPStatus: 403},
		{Stage: "download", Reason: "failed", HTTPStatus: 403},
	} {
		if diagnostic.Validate() == nil || strings.Contains(diagnostic.Summary(), "SECRET") {
			t.Fatal("untrusted diagnostic was accepted or rendered")
		}
		if got, ok := GeoUpdateDiagnostic(&geoDiagnosticError{diagnostic: diagnostic}); ok || got != (GeoDiagnostic{}) {
			t.Fatal("invalid typed diagnostic crossed the boundary")
		}
	}
	if _, ok := GeoUpdateDiagnostic(errors.New("validator_privilege_drop_failed")); ok {
		t.Fatal("untyped error text was trusted as a diagnostic")
	}
	for _, diagnostic := range []GeoDiagnostic{
		{Stage: "validation", Reason: "validator_privilege_drop_failed", Edition: "City"},
		{Stage: "download", Reason: "http_status", HTTPStatus: 429, Edition: "ASN"},
		{Stage: "staging", Reason: "storage_full"},
	} {
		health := Health{SchemaVersion: 1, CheckedAt: time.Now().UTC(), Result: "download_failed", ConsecutiveFailures: 1, Diagnostic: &diagnostic}
		encoded, err := json.Marshal(health)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "health.json")
		if err := os.WriteFile(path, encoded, 0o640); err != nil {
			t.Fatal(err)
		}
		got, err := readHealth(path, uint32(os.Geteuid()))
		if err != nil || got.Diagnostic == nil || *got.Diagnostic != diagnostic {
			t.Fatal("sanitized health did not retain its typed diagnostic")
		}
		health.Result, health.LastSuccessAt, health.ConsecutiveFailures = "ok", health.CheckedAt, 0
		if health.Validate() == nil {
			t.Fatal("success retained an old failure diagnostic")
		}
	}
}

func TestGeoValidatorSetupDiagnosticIsFixedAndBounded(t *testing.T) {
	for _, reason := range []string{"validator_privilege_drop_failed", "validator_setup_failed"} {
		path := filepath.Join(t.TempDir(), "fixture.mmdb")
		if err := os.WriteFile(path, []byte("noderampart-validator-failure\n"+`{"reason":"`+reason+`"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		_, err = verifyMMDBFile(context.Background(), file, "GeoLite2-ASN")
		_ = file.Close()
		diagnostic, ok := GeoUpdateDiagnostic(err)
		if !ok || diagnostic.Stage != "validation" || diagnostic.Reason != reason || diagnostic.Edition != "ASN" {
			t.Fatalf("validator setup failure lost: %+v %v", diagnostic, err)
		}
		if _, _, ok := GeoValidationDiagnostic(err); ok {
			t.Fatal("sandbox setup failure was presented as rejected MMDB data")
		}
	}
}
