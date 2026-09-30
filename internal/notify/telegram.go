// SPDX-License-Identifier: MIT

package notify

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
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

// Destination derives identity only from the public bot number and numeric chat
// ID. The credential suffix is never included, stored or logged. Numeric IDs
// remain stable across a credential rotation; mutable @usernames cannot prove
// that an old body belongs to the same recipient and are rejected.
func (t *Telegram) Destination() string {
	identity, _ := telegramDestination(t.token, t.chatID)
	return identity
}

func TelegramDestination(tokenFile, chatID string) (string, error) {
	token, err := readSecret(tokenFile)
	if err != nil {
		return "", err
	}
	return telegramDestination(token, chatID)
}

func telegramDestination(token, chatID string) (string, error) {
	if !tokenPattern.MatchString(token) {
		return "", errors.New("Telegram token file has an invalid format")
	}
	chat, err := strconv.ParseInt(chatID, 10, 64)
	if err != nil || chat == 0 || strings.TrimSpace(chatID) != chatID {
		return "", errors.New("Telegram target requires a nonzero numeric chat ID")
	}
	bot, err := strconv.ParseUint(strings.SplitN(token, ":", 2)[0], 10, 64)
	if err != nil || bot == 0 {
		return "", errors.New("Telegram bot identity is invalid")
	}
	digest := sha256.Sum256([]byte(strconv.FormatUint(bot, 10) + ":" + strconv.FormatInt(chat, 10)))
	return "telegram:" + hex.EncodeToString(digest[:]), nil
}

type DeliveryError struct {
	Channel         string
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
	channel := "Telegram"
	if e.Channel == "webhook" {
		channel = "Webhook"
	}
	if e.SuspendDestination {
		return fmt.Sprintf("%s retry interval requires destination resume (HTTP %d, API %d)", channel, e.StatusCode, e.APIErrorCode)
	}
	if e.InvalidResponse {
		return fmt.Sprintf("%s response invalid (HTTP %d)", channel, e.StatusCode)
	}
	return fmt.Sprintf("%s delivery failed (HTTP %d, API %d)", channel, e.StatusCode, e.APIErrorCode)
}

func NewTelegram(tokenFile, chatID string, timeout time.Duration) (*Telegram, error) {
	token, err := readSecret(tokenFile)
	if err != nil {
		return nil, err
	}
	if !tokenPattern.MatchString(token) {
		return nil, errors.New("Telegram token file has an invalid format")
	}
	if _, err := telegramDestination(token, chatID); err != nil {
		return nil, err
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
	secret, _, err := readSecretGeneration(path)
	return secret, err
}

// Walk each directory by descriptor so a parent symlink cannot redirect a
// credential read. The returned file remains tied to this inspected inode.
func openSecret(path string) (int, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return -1, err
	}
	parts := strings.Split(strings.TrimPrefix(filepath.Clean(absolute), "/"), "/")
	parent, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, err
	}
	for _, part := range parts[:len(parts)-1] {
		next, err := unix.Openat(parent, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		unix.Close(parent)
		if err != nil {
			return -1, err
		}
		parent = next
	}
	defer unix.Close(parent)
	return unix.Openat(parent, parts[len(parts)-1], unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
}

func readSecretGeneration(path string) (string, string, error) {
	fd, err := openSecret(path)
	if err != nil {
		return "", "", errors.New("protected token could not be opened")
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	var before, after unix.Stat_t
	if unix.Fstat(fd, &before) != nil {
		return "", "", errors.New("protected token could not be inspected")
	}
	if before.Mode&unix.S_IFMT != unix.S_IFREG || before.Nlink != 1 {
		return "", "", errors.New("protected token must be a regular file, not a symlink")
	}
	if before.Mode&0o077 != 0 {
		return "", "", errors.New("protected token file must not be accessible by group or others")
	}
	if before.Size < 20 || before.Size > 512 {
		return "", "", errors.New("protected token file has an invalid size")
	}
	data, err := io.ReadAll(io.LimitReader(file, 513))
	if err != nil {
		return "", "", errors.New("protected token could not be read")
	}
	if len(data) > 512 {
		return "", "", errors.New("protected token file has an invalid size")
	}
	if unix.Fstat(fd, &after) != nil || before.Dev != after.Dev || before.Ino != after.Ino || before.Mode != after.Mode || before.Uid != after.Uid || before.Gid != after.Gid || before.Size != after.Size || before.Mtim != after.Mtim || before.Ctim != after.Ctim {
		return "", "", errors.New("protected token changed while reading")
	}
	generation := fmt.Sprintf("%d:%d:%d:%d:%d:%d", before.Dev, before.Ino, before.Ctim.Sec, before.Ctim.Nsec, before.Mtim.Sec, before.Mtim.Nsec)
	return strings.TrimSpace(string(data)), generation, nil
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
