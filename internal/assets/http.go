// SPDX-License-Identifier: MIT

package assets

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
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
	u, err := url.Parse(endpoint)
	geo := credentials != nil
	if err != nil || !allowedURL(u, geo) {
		return nil, errors.New("asset endpoint is not permitted")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
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
			return errors.New("asset redirect is not permitted")
		}
		return nil
	}
	response, err := client.Do(req)
	if err != nil {
		// url.Error includes a full URL, potentially an R2 signature. Never wrap it.
		return nil, errors.New("asset request failed or timed out")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("asset download returned HTTP %d", response.StatusCode)
	}
	if response.ContentLength > maximum {
		return nil, errors.New("asset response exceeds size limit")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maximum+1))
	if err != nil {
		return nil, errors.New("asset response could not be read")
	}
	if int64(len(body)) > maximum {
		return nil, errors.New("asset response exceeds size limit")
	}
	return body, nil
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
