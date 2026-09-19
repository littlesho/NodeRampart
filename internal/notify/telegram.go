// SPDX-License-Identifier: MIT

package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	telegramEndpoint         = "https://api.telegram.org"
	maxTelegramResponseBytes = 64 << 10
	maxTelegramRetryAfter    = 30 * 24 * time.Hour
)

var tokenPattern = regexp.MustCompile(`^[0-9]{6,16}:[A-Za-z0-9_-]{30,128}$`)

type Sender interface {
	Send(context.Context, string) error
}

type Telegram struct {
	token    string
	chatID   string
	endpoint string
	client   *http.Client
}

type DeliveryError struct {
	StatusCode      int
	APIErrorCode    int
	RetryAfter      time.Duration
	Permanent       bool
	RateLimited     bool
	InvalidResponse bool
	// An unrepresentable or excessive server wait requires explicit operator
	// resume; automatically shortening the server's minimum could flood it.
	SuspendDestination bool
}

func (e *DeliveryError) Error() string {
	if e.SuspendDestination {
		return fmt.Sprintf("Telegram retry interval requires destination resume (HTTP %d, API %d)", e.StatusCode, e.APIErrorCode)
	}
	if e.InvalidResponse {
		return fmt.Sprintf("Telegram response invalid (HTTP %d)", e.StatusCode)
	}
	return fmt.Sprintf("Telegram delivery failed (HTTP %d, API %d)", e.StatusCode, e.APIErrorCode)
}

func NewTelegram(tokenFile, chatID string, timeout time.Duration) (*Telegram, error) {
	token, err := readSecret(tokenFile)
	if err != nil {
		return nil, err
	}
	if !tokenPattern.MatchString(token) {
		return nil, errors.New("Telegram token file has an invalid format")
	}
	if timeout < time.Second || timeout > time.Minute {
		timeout = 10 * time.Second
	}
	client := &http.Client{
		Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("Telegram redirect refused")
		},
	}
	return &Telegram{token: token, chatID: chatID, endpoint: telegramEndpoint, client: client}, nil
}

func readSecret(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("read Telegram token: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", errors.New("Telegram token must be a regular file, not a symlink")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return "", errors.New("Telegram token file must not be accessible by group or others")
	}
	if info.Size() < 20 || info.Size() > 512 {
		return "", errors.New("Telegram token file has an invalid size")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read Telegram token: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

func (t *Telegram) Send(ctx context.Context, body string) error {
	if body == "" || len(body) > 4096 {
		return errors.New("Telegram message must be 1..4096 bytes")
	}
	payload, err := json.Marshal(map[string]any{"chat_id": t.chatID, "text": body, "parse_mode": "HTML", "disable_web_page_preview": true})
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, t.endpoint+"/bot"+t.token+"/sendMessage", bytes.NewReader(payload))
	if err != nil {
		return errors.New("create Telegram request")
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := t.client.Do(request)
	if err != nil {
		return errors.New("Telegram request failed")
	}
	defer response.Body.Close()
	// Read one extra byte so a response with a valid JSON prefix cannot bypass
	// the limit. Do not drain an oversized or unbounded response body.
	data, readErr := io.ReadAll(io.LimitReader(response.Body, maxTelegramResponseBytes+1))
	var envelope struct {
		OK         *bool `json:"ok"`
		ErrorCode  int   `json:"error_code"`
		Parameters struct {
			RetryAfter json.RawMessage `json:"retry_after"`
		} `json:"parameters"`
	}
	valid := readErr == nil && len(data) <= maxTelegramResponseBytes &&
		json.Unmarshal(data, &envelope) == nil && envelope.OK != nil
	if response.StatusCode >= 200 && response.StatusCode < 300 && valid && *envelope.OK {
		return nil
	}
	delivery := &DeliveryError{StatusCode: response.StatusCode, InvalidResponse: !valid}
	if valid && !*envelope.OK {
		delivery.APIErrorCode = envelope.ErrorCode
	}
	delivery.Permanent = permanentTelegramCode(delivery.StatusCode) || permanentTelegramCode(delivery.APIErrorCode)
	delivery.RateLimited = delivery.StatusCode == http.StatusTooManyRequests || delivery.APIErrorCode == http.StatusTooManyRequests
	delivery.RetryAfter, delivery.SuspendDestination = parseRetryAfter(response.Header.Get("Retry-After"), time.Now())
	if valid && !*envelope.OK {
		delay, suspend := parseRetrySeconds(string(envelope.Parameters.RetryAfter))
		if delay > delivery.RetryAfter {
			delivery.RetryAfter = delay
		}
		delivery.SuspendDestination = delivery.SuspendDestination || suspend
	}
	// A server-specified wait also applies to temporary service failures.
	if delivery.RetryAfter > 0 || delivery.SuspendDestination {
		delivery.RateLimited = true
	}
	if delivery.RateLimited {
		delivery.Permanent = false
	}
	return delivery
}

func permanentTelegramCode(code int) bool {
	return code == http.StatusBadRequest || code == http.StatusUnauthorized || code == http.StatusForbidden
}

func parseRetryAfter(value string, now time.Time) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if delay, suspend := parseRetrySeconds(value); delay > 0 || suspend {
		return delay, suspend
	}
	if at, err := http.ParseTime(value); err == nil {
		delay := at.Sub(now)
		if delay > maxTelegramRetryAfter {
			return 0, true
		}
		if delay > 0 {
			// Round up so a subsecond interval is never retried early.
			return ((delay + time.Second - 1) / time.Second) * time.Second, false
		}
	}
	return 0, false
}

func parseRetrySeconds(value string) (time.Duration, bool) {
	if value == "" {
		return 0, false
	}
	// Check digits before parsing so negative/fractional/string values cannot
	// become durations. Excessive integers suspend rather than wrap or shorten
	// the server's requested minimum wait.
	for _, digit := range value {
		if digit < '0' || digit > '9' {
			return 0, false
		}
	}
	seconds, err := strconv.ParseUint(value, 10, 64)
	if err != nil || seconds > uint64(maxTelegramRetryAfter/time.Second) {
		return 0, true
	}
	return time.Duration(seconds) * time.Second, false
}
