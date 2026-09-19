// SPDX-License-Identifier: MIT

package notify

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func syntheticTelegram(status int, body io.Reader, retryAfter string) *Telegram {
	return &Telegram{
		token: fakeToken(), chatID: "synthetic-chat", endpoint: "https://telegram.invalid",
		client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			header := make(http.Header)
			if retryAfter != "" {
				header.Set("Retry-After", retryAfter)
			}
			return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(body)}, nil
		})},
	}
}

func TestTelegramRequiresSuccessEnvelope(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		body    string
		success bool
	}{
		{"success", 200, `{"ok":true,"result":{"message_id":1}}`, true},
		{"unknown fields", 200, `{"ok":true,"result":{},"future_field":true}`, true},
		{"empty body", 200, ``, false},
		{"missing confirmation", 200, `{"result":{}}`, false},
		{"false confirmation", 200, `{"ok":false,"error_code":500}`, false},
		{"wrong confirmation type", 200, `{"ok":"true"}`, false},
		{"null confirmation", 200, `{"ok":null}`, false},
		{"null envelope", 200, `null`, false},
		{"array envelope", 200, `[{"ok":true}]`, false},
		{"truncated envelope", 200, `{"ok":true`, false},
		{"trailing document", 200, `{"ok":true} {"ok":false}`, false},
		{"trailing garbage", 200, `{"ok":true}untrusted`, false},
		{"non-success HTTP", 500, `{"ok":true}`, false},
		{"oversized success prefix", 200, `{"ok":true}` + strings.Repeat(" ", maxTelegramResponseBytes), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tg := syntheticTelegram(tc.status, strings.NewReader(tc.body), "")
			err := tg.Send(context.Background(), "synthetic message")
			if (err == nil) != tc.success {
				t.Fatalf("success = %v, want %v", err == nil, tc.success)
			}
		})
	}
}

func TestTelegramErrorClassificationAndRetryHints(t *testing.T) {
	for _, tc := range []struct {
		name        string
		status      int
		body        string
		header      string
		wait        time.Duration
		permanent   bool
		rateLimited bool
	}{
		{"JSON flood wait", 429, `{"ok":false,"error_code":429,"parameters":{"retry_after":3600}}`, "", time.Hour, false, true},
		{"longer header", 429, `{"ok":false,"parameters":{"retry_after":3600}}`, "7200", 2 * time.Hour, false, true},
		{"longer JSON", 429, `{"ok":false,"parameters":{"retry_after":7200}}`, "3600", 2 * time.Hour, false, true},
		{"API rate limit with HTTP200", 200, `{"ok":false,"error_code":429,"parameters":{"retry_after":60}}`, "", time.Minute, false, true},
		{"invalid body keeps header", 429, `untrusted`, "120", 2 * time.Minute, false, true},
		{"service unavailable wait", 503, `{"ok":false,"error_code":503}`, "120", 2 * time.Minute, false, true},
		{"negative JSON ignored", 429, `{"ok":false,"parameters":{"retry_after":-1}}`, "", 0, false, true},
		{"fractional JSON ignored", 429, `{"ok":false,"parameters":{"retry_after":1.5}}`, "", 0, false, true},
		{"string JSON ignored", 429, `{"ok":false,"parameters":{"retry_after":"60"}}`, "", 0, false, true},
		{"null JSON ignored", 429, `{"ok":false,"parameters":{"retry_after":null}}`, "", 0, false, true},
		{"malformed header ignored", 429, `{"ok":false}`, "-100", 0, false, true},
		{"HTTP bad request", 400, `{"ok":false,"error_code":400}`, "", 0, true, false},
		{"HTTP unauthorized", 401, `untrusted`, "", 0, true, false},
		{"HTTP forbidden", 403, `{"ok":false}`, "", 0, true, false},
		{"API permanent with HTTP200", 200, `{"ok":false,"error_code":400}`, "", 0, true, false},
		{"server failure", 500, `{"ok":false,"error_code":500}`, "", 0, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tg := syntheticTelegram(tc.status, strings.NewReader(tc.body), tc.header)
			err := tg.Send(context.Background(), "synthetic message")
			var delivery *DeliveryError
			if !errors.As(err, &delivery) {
				t.Fatal("expected categorized delivery error")
			}
			if delivery.RetryAfter != tc.wait || delivery.Permanent != tc.permanent || delivery.RateLimited != tc.rateLimited {
				t.Fatalf("wait/permanent/rate-limited = %v/%v/%v, want %v/%v/%v", delivery.RetryAfter, delivery.Permanent, delivery.RateLimited, tc.wait, tc.permanent, tc.rateLimited)
			}
		})
	}
}

func TestTelegramResponseReadBound(t *testing.T) {
	body := &countingReader{reader: strings.NewReader(`{"ok":true}` + strings.Repeat(" ", 4*maxTelegramResponseBytes))}
	err := syntheticTelegram(200, body, "").Send(context.Background(), "synthetic message")
	if err == nil {
		t.Fatal("oversized body was accepted")
	}
	if body.read != maxTelegramResponseBytes+1 {
		t.Fatalf("read %d bytes, want %d", body.read, maxTelegramResponseBytes+1)
	}
}

type countingReader struct {
	reader io.Reader
	read   int
}

func (r *countingReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.read += n
	return n, err
}

type brokenResponseReader struct{}

func (brokenResponseReader) Read(p []byte) (int, error) {
	return copy(p, `{"ok":true}`), errors.New("synthetic private upstream failure")
}

func TestTelegramReadAndTransportErrorsAreRedacted(t *testing.T) {
	tg := syntheticTelegram(200, brokenResponseReader{}, "")
	if err := tg.Send(context.Background(), "synthetic message"); err == nil || strings.Contains(err.Error(), "private") {
		t.Fatal("response read failure must be redacted and rejected")
	}
	tg.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("synthetic private upstream failure " + fakeToken())
	})
	if err := tg.Send(context.Background(), "synthetic message"); err == nil || strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), fakeToken()) {
		t.Fatal("transport failure must be redacted and rejected")
	}
}

func TestRetryAfterHTTPDate(t *testing.T) {
	now := time.Date(2026, 9, 11, 1, 2, 3, 100, time.UTC)
	if got, suspend := parseRetryAfter(now.Add(31*time.Second).Format(http.TimeFormat), now); got != 31*time.Second || suspend {
		t.Fatalf("date wait = %v, want 31s", got)
	}
	if got, suspend := parseRetryAfter(now.Add(-time.Hour).Format(http.TimeFormat), now); got != 0 || suspend {
		t.Fatalf("past date wait = %v", got)
	}
}

func TestTelegramExcessiveWaitSuspendsDestination(t *testing.T) {
	for _, tc := range []struct {
		name   string
		body   string
		header string
	}{
		{"JSON beyond policy", `{"ok":false,"parameters":{"retry_after":2592001}}`, ""},
		{"JSON integer overflow", `{"ok":false,"parameters":{"retry_after":18446744073709551616}}`, ""},
		{"header beyond policy", `{"ok":false}`, "2592001"},
		{"header integer overflow", `{"ok":false}`, "18446744073709551616"},
		{"HTTP date beyond policy", `{"ok":false}`, time.Now().Add(31 * 24 * time.Hour).Format(http.TimeFormat)},
		{"header excessive with normal JSON", `{"ok":false,"parameters":{"retry_after":60}}`, "2592001"},
		{"JSON excessive with normal header", `{"ok":false,"parameters":{"retry_after":2592001}}`, "60"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := syntheticTelegram(429, strings.NewReader(tc.body), tc.header).Send(context.Background(), "synthetic message")
			var delivery *DeliveryError
			if !errors.As(err, &delivery) || !delivery.SuspendDestination || !delivery.RateLimited {
				t.Fatal("excessive wait must require explicit destination resume")
			}
		})
	}
	if delay, suspend := parseRetrySeconds("2592000"); delay != 30*24*time.Hour || suspend {
		t.Fatal("maximum supported retry must be honored exactly")
	}
}
