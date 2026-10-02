// SPDX-License-Identifier: MIT

package notify

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/store"
	"golang.org/x/sys/unix"
)

const (
	DispatchAccepted    = "accepted"
	DispatchNotAccepted = "not_accepted"
	DispatchUnknown     = "unknown"
	DispatchRejected    = "rejected"
	DispatchOptedOut    = "opted_out"
)

// DispatchOutcome contains bounded, non-secret receipt data. Accepted means
// acceptance by an API, never that a recipient read the notification.
type DispatchOutcome struct {
	State, ProviderID, ProviderState, Reason string
	StatusCode, APIErrorCode                 int
	RetryAfterDuration, PollAfterDuration    time.Duration
	PlatformSegments                         *int
	Price                                    *string
	PriceUnit                                string
	Suspend                                  bool
}

// OfficialSemantic is produced locally before admission. It contains no
// provider recipient, credential, raw log, remote URL or diagnostic package.
type OfficialSemantic struct {
	HostAlias      string `json:"host_alias"`
	EventKind      string `json:"event_kind"`
	Phase          string `json:"phase"`
	Severity       string `json:"severity"`
	Time           string `json:"time"`
	BoundedSummary string `json:"bounded_summary"`
	LocalReference string `json:"local_reference"`
	Coverage       string `json:"coverage"`
}

// Official retains the inspected credential generation. Token refresh only
// refreshes QQ access for that same immutable application and recipient.
type Official struct {
	channel, destination, fingerprint string
	cfg                               config.OfficialChannelConfig
	credential                        config.OfficialCredential
	client                            *http.Client
	now                               func() time.Time
	qqTokenGate                       chan struct{}
	qqToken                           string
	qqTokenUntil                      time.Time
}

func NewOfficial(channel string, cfg config.OfficialChannelConfig) (*Official, error) {
	if err := config.ValidateOfficialChannel(channel, cfg); err != nil {
		return nil, err
	}
	credential, generation, err := readOfficialCredential(channel, cfg.CredentialFile)
	if err != nil {
		return nil, err
	}
	if err := config.ValidateOfficialCredentialPolicy(channel, cfg, credential); err != nil {
		return nil, err
	}
	if cfg.Timeout.Duration < time.Second || cfg.Timeout.Duration > 30*time.Second {
		return nil, errors.New("official channel timeout must be 1s..30s")
	}
	cfg.Subscription.NotificationTypes = append([]string(nil), cfg.Subscription.NotificationTypes...)
	canonical, _ := json.Marshal(credential)
	selectionTypes := append([]string(nil), cfg.Subscription.NotificationTypes...)
	sort.Strings(selectionTypes)
	selection, _ := json.Marshal(struct {
		Events      bool     `json:"events"`
		Severity    string   `json:"severity"`
		Daily       bool     `json:"daily"`
		MaxSegments int      `json:"max_segments"`
		Types       []string `json:"types"`
	}{cfg.EventsEnabled, cfg.MinSeverity, cfg.DailyEnabled, cfg.MaxSegments, selectionTypes})
	fingerprint := sha256.Sum256([]byte(string(canonical) + "\x00" + string(selection)))
	identity := sha256.Sum256([]byte(channel + "\x00" + cfg.CredentialFile + "\x00" + generation + "\x00" + cfg.Subscription.BasisID + "\x00" + string(canonical) + "\x00" + string(selection)))
	timeout := cfg.Timeout.Duration
	transport := &http.Transport{Proxy: nil, DialContext: nativeDialContext(net.DefaultResolver, (&net.Dialer{Timeout: timeout}).DialContext), TLSHandshakeTimeout: timeout, ResponseHeaderTimeout: timeout, MaxResponseHeaderBytes: 16 << 10, DisableCompression: true, DisableKeepAlives: true, MaxIdleConns: 1, MaxIdleConnsPerHost: 1, MaxConnsPerHost: 1, ForceAttemptHTTP2: false}
	return &Official{channel: channel, destination: channel + ":" + hex.EncodeToString(identity[:]), fingerprint: hex.EncodeToString(fingerprint[:]), cfg: cfg, credential: credential, now: time.Now, qqTokenGate: make(chan struct{}, 1), client: &http.Client{Transport: transport, Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func OfficialDestination(channel string, cfg config.OfficialChannelConfig) (string, error) {
	sender, err := NewOfficial(channel, cfg)
	if err != nil {
		return "", err
	}
	return sender.Destination(), nil
}

func (s *Official) Destination() string { return s.destination }
func (s *Official) MinimumInterval() time.Duration {
	if s.channel == "qqbot" {
		return 3 * time.Second
	}
	if s.channel == "whatsapp_cloud" {
		return 6 * time.Second
	}
	return time.Second
}

// Direct sends cannot spend money or evade durable dispatch intent/budgets.
func (s *Official) Send(context.Context, string) error {
	return errors.New("official notifications require a persisted outbox dispatch")
}
func (s *Official) SendMessage(context.Context, store.OutboxMessage) error {
	return errors.New("official notifications require a persisted outbox dispatch")
}

func ValidateOfficialCredentialFile(channel, path string) error {
	_, _, err := readOfficialCredential(channel, path)
	return err
}

func readOfficialCredential(channel, path string) (config.OfficialCredential, string, error) {
	var credential config.OfficialCredential
	if !config.IsOfficialChannel(channel) {
		return credential, "", errors.New("unsupported official notification channel")
	}
	fd, err := openNativeSecret(path)
	if err != nil {
		return credential, "", errors.New("official protected credentials unavailable or unsafe")
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	var before, after unix.Stat_t
	if unix.Fstat(fd, &before) != nil || before.Mode&unix.S_IFMT != unix.S_IFREG || before.Nlink != 1 || before.Size < 20 || before.Size > config.MaxOfficialCredentialBytes || !nativeCredentialPermission(before) {
		return credential, "", errors.New("official protected credential ownership, permissions, type or size is unsafe")
	}
	data, err := io.ReadAll(io.LimitReader(file, config.MaxOfficialCredentialBytes+1))
	if err != nil || len(data) > config.MaxOfficialCredentialBytes || unix.Fstat(fd, &after) != nil || before.Dev != after.Dev || before.Ino != after.Ino || before.Nlink != after.Nlink || before.Mode != after.Mode || before.Uid != after.Uid || before.Gid != after.Gid || before.Size != after.Size || before.Mtim != after.Mtim || before.Ctim != after.Ctim {
		return credential, "", errors.New("official protected credentials changed or could not be read")
	}
	credential, err = config.DecodeOfficialCredential(channel, data)
	if err != nil {
		return config.OfficialCredential{}, "", err
	}
	generation := fmt.Sprintf("%d:%d:%d:%d:%d:%d", before.Dev, before.Ino, before.Ctim.Sec, before.Ctim.Nsec, before.Mtim.Sec, before.Mtim.Nsec)
	return credential, generation, nil
}

func (s *Official) request(ctx context.Context, request *http.Request) (int, http.Header, []byte, error) {
	request = request.WithContext(ctx)
	response, err := s.client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return 0, nil, nil, ctx.Err()
		}
		return 0, nil, nil, errors.New("official HTTPS request failed")
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxTelegramResponseBytes+1))
	if err != nil || len(data) > maxTelegramResponseBytes {
		if ctx.Err() != nil {
			return response.StatusCode, response.Header, nil, ctx.Err()
		}
		return response.StatusCode, response.Header, nil, errors.New("official response unavailable or exceeds its bound")
	}
	return response.StatusCode, response.Header, data, nil
}

func officialHTTPFailure(status int, headers http.Header, now time.Time) DispatchOutcome {
	out := DispatchOutcome{State: DispatchUnknown, StatusCode: status, Reason: "unconfirmed_response"}
	if status >= 400 && status < 500 && status != 408 {
		out.State = DispatchRejected
		out.Reason = "request_rejected"
	}
	if status == 429 {
		out.State = DispatchNotAccepted
		out.Reason = "rate_limited"
	}
	if status == 401 || status == 403 {
		out.Reason = "credential_or_permission_rejected"
		out.Suspend = true
	}
	var overlong bool
	out.RetryAfterDuration, overlong = parseRetryAfter(headers.Get("Retry-After"), now)
	out.Suspend = out.Suspend || overlong
	return out
}

func officialReceiptID(value string) bool {
	return len(value) > 0 && len(value) <= 160 && !strings.Contains(value, "://") && strings.IndexFunc(value, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_.:+/=", r))
	}) < 0
}
