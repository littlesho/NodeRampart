// SPDX-License-Identifier: MIT

package notify

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/store"
)

type httpsJSON struct {
	endpoint, bearer, generation string
	client                       *http.Client
}

func newHTTPSJSON(endpoint, credentialFile string, timeout time.Duration) (*httpsJSON, error) {
	u, err := config.HTTPSURL(endpoint)
	if err != nil {
		return nil, err
	}
	bearer, generation, err := readSecretGeneration(credentialFile)
	if err != nil {
		return nil, errors.New("HTTPS credentials unavailable or unsafe")
	}
	if len(bearer) < 20 || strings.IndexFunc(bearer, func(r rune) bool { return r < 0x21 || r > 0x7e }) >= 0 {
		return nil, errors.New("HTTPS bearer must contain 20..512 visible ASCII bytes")
	}
	if timeout < time.Second || timeout > 30*time.Second {
		return nil, errors.New("HTTPS timeout must be 1s..30s")
	}
	transport := &http.Transport{Proxy: http.ProxyFromEnvironment, DialContext: (&net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}).DialContext, TLSHandshakeTimeout: timeout, ResponseHeaderTimeout: timeout, MaxResponseHeaderBytes: 32 << 10, DisableCompression: true, MaxIdleConns: 1, MaxIdleConnsPerHost: 1, MaxConnsPerHost: 1, IdleConnTimeout: 30 * time.Second, ForceAttemptHTTP2: true}
	client := &http.Client{Transport: transport, Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return &httpsJSON{endpoint: u.String(), bearer: bearer, generation: generation, client: client}, nil
}

func (s *httpsJSON) send(ctx context.Context, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil || len(data) > 16<<10 {
		return errors.New("HTTPS JSON payload exceeds its bound")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, bytes.NewReader(data))
	if err != nil {
		return errors.New("HTTPS request could not be created")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+s.bearer)
	response, err := s.client.Do(request)
	if err != nil {
		return errors.New("HTTPS request failed")
	}
	defer response.Body.Close()
	delivery := &DeliveryError{Channel: "webhook", StatusCode: response.StatusCode, Permanent: response.StatusCode >= 300 && response.StatusCode < 500 && response.StatusCode != http.StatusRequestTimeout && response.StatusCode != http.StatusTooManyRequests, RateLimited: response.StatusCode == http.StatusTooManyRequests}
	delivery.RetryAfter, delivery.SuspendDestination = parseRetryAfter(response.Header.Get("Retry-After"), time.Now())
	if delivery.RateLimited || delivery.RetryAfter > 0 || delivery.SuspendDestination {
		delivery.RateLimited, delivery.Permanent = true, false
	}
	data, err = io.ReadAll(io.LimitReader(response.Body, maxTelegramResponseBytes+1))
	if err != nil || len(data) > maxTelegramResponseBytes {
		delivery.InvalidResponse = true
		return delivery
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return nil
	}
	return delivery
}

type Webhook struct {
	http        *httpsJSON
	destination string
}

func NewWebhook(endpoint, receiverID, credentialFile string, timeout time.Duration) (*Webhook, error) {
	if len(receiverID) == 0 || len(receiverID) > 64 || strings.IndexFunc(receiverID, func(r rune) bool { return r <= 0x20 || r == 0x7f }) >= 0 {
		return nil, errors.New("webhook receiver identity is invalid")
	}
	sender, err := newHTTPSJSON(endpoint, credentialFile, timeout)
	if err != nil {
		return nil, err
	}
	// Credentials may select another tenant at the same URL. File generation is
	// non-secret and conservatively isolates every uncertain credential change.
	sum := sha256.Sum256([]byte(sender.endpoint + "\x00" + receiverID + "\x00" + sender.generation))
	return &Webhook{http: sender, destination: "webhook:" + hex.EncodeToString(sum[:])}, nil
}

func (w *Webhook) Destination() string { return w.destination }
func (w *Webhook) Send(ctx context.Context, body string) error {
	return w.SendMessage(ctx, store.OutboxMessage{Body: body})
}
func (w *Webhook) SendMessage(ctx context.Context, message store.OutboxMessage) error {
	if len(message.Body) == 0 || len(message.Body) > 4096 {
		return errors.New("webhook message must contain 1..4096 bytes")
	}
	eventID := ""
	if parts := strings.Split(message.DedupeKey, ":"); len(parts) == 3 && parts[0] == "event" {
		eventID = parts[1]
	}
	return w.http.send(ctx, struct {
		Version   int    `json:"schema_version"`
		MessageID string `json:"message_id"`
		EventID   string `json:"event_id,omitempty"`
		Body      string `json:"body"`
	}{1, message.ID, eventID, message.Body})
}

type HeartbeatPayload struct {
	InstanceID string    `json:"instance_id"`
	At         time.Time `json:"at_utc"`
	Version    string    `json:"version"`
	Alive      bool      `json:"process_alive"`
	Health     string    `json:"functional_health"`
	Reasons    []string  `json:"reason_codes"`
}
type HeartbeatSender interface {
	Send(context.Context, HeartbeatPayload) error
}

type Heartbeat struct{ http *httpsJSON }

func NewHeartbeat(endpoint, credentialFile string, timeout time.Duration) (*Heartbeat, error) {
	s, err := newHTTPSJSON(endpoint, credentialFile, timeout)
	if err != nil {
		return nil, err
	}
	return &Heartbeat{http: s}, nil
}
func (h *Heartbeat) Send(ctx context.Context, payload HeartbeatPayload) error {
	if len(payload.InstanceID) == 0 || len(payload.InstanceID) > 64 || payload.At.IsZero() || len(payload.Version) == 0 || len(payload.Version) > 128 || !payload.Alive || (payload.Health != "healthy" && payload.Health != "degraded" && payload.Health != "unknown") || len(payload.Reasons) > 16 {
		return errors.New("heartbeat summary is invalid")
	}
	for _, reason := range payload.Reasons {
		if len(reason) == 0 || len(reason) > 64 || strings.IndexFunc(reason, func(r rune) bool { return r != '_' && (r < 'a' || r > 'z') }) >= 0 {
			return errors.New("heartbeat reason code is invalid")
		}
	}
	return h.http.send(ctx, payload)
}
