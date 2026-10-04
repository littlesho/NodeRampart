// SPDX-License-Identifier: MIT

package assets

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

const (
	awsHost = "pricing.us-east-1.amazonaws.com"
	ociHost = "apexapps.oracle.com"
	geoHost = "download.maxmind.com"
	r2Host  = "mm-prod-geoip-databases.a2649acb697e2c09b632799562c076f2.r2.cloudflarestorage.com"
)

func allowedURL(u *url.URL, geo bool) bool {
	if u == nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" || u.Opaque != "" {
		return false
	}
	if geo {
		return u.Host == geoHost || u.Host == r2Host
	}
	return u.Host == awsHost || u.Host == ociHost
}

func (c *Client) request(ctx context.Context, endpoint string, credentials *Credentials, maximum int64) ([]byte, error) {
	body, err := c.requestStream(ctx, endpoint, credentials, maximum)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	data, err := io.ReadAll(body)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// GeoIP consumes the response as a stream. Catalog callers retain the existing
// bounded []byte API; endpoints, credentials and redirect policy stay shared.
func (c *Client) requestStream(ctx context.Context, endpoint string, credentials *Credentials, maximum int64) (io.ReadCloser, error) {
	u, err := url.Parse(endpoint)
	geo := credentials != nil
	if err != nil || !allowedURL(u, geo) {
		return nil, errors.New("asset endpoint is not permitted")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	success := false
	defer func() {
		if !success {
			cancel()
		}
	}()
	var tlsFailed atomic.Bool
	ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{TLSHandshakeDone: func(_ tls.ConnectionState, err error) {
		if err != nil {
			tlsFailed.Store(true)
		}
	}})
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, errors.New("cannot create asset request")
	}
	req.Header.Set("Accept", "application/json, application/octet-stream")
	req.Header.Set("User-Agent", "NodeRampart-data-client")
	if geo {
		req.SetBasicAuth(credentials.AccountID, credentials.LicenseKey)
	}
	client := http.Client{Timeout: 30 * time.Second}
	if c != nil && c.HTTP != nil {
		client.Transport = c.HTTP.Transport
		if c.HTTP.Timeout > 0 && c.HTTP.Timeout < client.Timeout {
			client.Timeout = c.HTTP.Timeout
		}
	}
	client.CheckRedirect = func(next *http.Request, previous []*http.Request) error {
		next.Header.Del("Authorization")
		if len(previous) >= 3 || !allowedURL(next.URL, geo) || (!geo && next.URL.Host != u.Host) {
			return errAssetRedirectRejected
		}
		return nil
	}
	response, err := client.Do(req)
	if err != nil {
		// url.Error includes a full URL, potentially an R2 signature. Never wrap it.
		failure := classifyAssetRequestError(err)
		// Ordinary TLS alerts are unexported Go error types. Classify at the
		// actual handshake boundary, never by matching a remote error string.
		// Keep a more specific cancellation, timeout or network classification.
		if failure.reason == "failed" && tlsFailed.Load() {
			failure.reason = "tls_failed"
		}
		return nil, failure
	}
	if response.StatusCode != http.StatusOK {
		_ = response.Body.Close()
		if response.StatusCode < 100 || response.StatusCode > 599 {
			return nil, &assetRequestError{reason: "failed"}
		}
		return nil, &assetRequestError{reason: "http_status", status: response.StatusCode}
	}
	if response.ContentLength > maximum {
		_ = response.Body.Close()
		return nil, &assetRequestError{reason: "response_too_large"}
	}
	success = true
	return &assetStream{body: response.Body, cancel: cancel, remaining: maximum + 1}, nil
}

type assetStream struct {
	body      io.ReadCloser
	cancel    context.CancelFunc
	remaining int64
	failure   *assetRequestError
}

func (s *assetStream) Read(p []byte) (int, error) {
	if s.remaining <= 0 {
		s.failure = &assetRequestError{reason: "response_too_large"}
		return 0, s.failure
	}
	if int64(len(p)) > s.remaining {
		p = p[:s.remaining]
	}
	n, err := s.body.Read(p)
	s.remaining -= int64(n)
	if s.remaining <= 0 {
		s.failure = &assetRequestError{reason: "response_too_large"}
		return n, s.failure
	}
	if err != nil && !errors.Is(err, io.EOF) {
		s.failure = classifyAssetRequestError(err)
		if s.failure.reason == "failed" {
			s.failure.reason = "response_read_failed"
		}
		return n, s.failure
	}
	return n, err
}

func (s *assetStream) Close() error {
	s.cancel()
	return s.body.Close()
}

// checkJSON bounds nesting, strings and object identity before typed decoding.
// Providers may add unrelated fields, but ambiguous duplicate keys are rejected.
func checkJSON(raw []byte) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var visit func(int) error
	count := 0
	visit = func(depth int) error {
		count++
		if depth > 16 || count > 500000 {
			return errors.New("catalog JSON exceeds structure limits")
		}
		t, err := d.Token()
		if err != nil {
			return errors.New("catalog JSON is invalid")
		}
		if s, ok := t.(string); ok && len(s) > 8192 {
			return errors.New("catalog JSON string exceeds limit")
		}
		delim, ok := t.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := make(map[string]bool)
			for d.More() {
				token, err := d.Token()
				key, ok := token.(string)
				key = strings.ToLower(key)
				if err != nil || !ok || len(key) > 256 || seen[key] || len(seen) >= 30000 {
					return errors.New("catalog JSON object is invalid")
				}
				seen[key] = true
				if err := visit(depth + 1); err != nil {
					return err
				}
			}
		case '[':
			for d.More() {
				if err := visit(depth + 1); err != nil {
					return err
				}
			}
		default:
			return errors.New("catalog JSON delimiter is invalid")
		}
		_, err = d.Token()
		return err
	}
	if err := visit(0); err != nil {
		return err
	}
	if _, err := d.Token(); !errors.Is(err, io.EOF) {
		return errors.New("catalog JSON has trailing data")
	}
	return nil
}
