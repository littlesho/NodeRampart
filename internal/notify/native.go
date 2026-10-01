// SPDX-License-Identifier: MIT

package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/store"
	"github.com/littlesho/NodeRampart/internal/version"
	"golang.org/x/sys/unix"
)

const NativeMaxBodyBytes = 1800

// Native keeps the inspected credential snapshot for the lifetime of this
// sender. A later file replacement must create a new sender and destination.
type Native struct {
	channel, endpoint, secret, destination, space, language string
	client                                                  *http.Client
	now                                                     func() time.Time
}

func NativeMinimumInterval(channel string) time.Duration {
	if channel == "wecom" {
		return 3 * time.Second
	}
	return time.Second
}

func NativeDestination(channel string, cfg config.NativeChannelConfig) (string, error) {
	n, err := NewNative(channel, cfg)
	if err != nil {
		return "", err
	}
	return n.Destination(), nil
}

func ValidateNativeCredential(channel, path string) error {
	credential, _, err := readNativeCredential(path)
	if err != nil {
		return err
	}
	return config.ValidateNativeCredential(channel, credential)
}

func NewNative(channel string, cfg config.NativeChannelConfig) (*Native, error) {
	credential, generation, err := readNativeCredential(cfg.CredentialFile)
	if err != nil {
		return nil, err
	}
	if err := config.ValidateNativeCredential(channel, credential); err != nil {
		return nil, err
	}
	u, _ := config.NativeHTTPSURL(channel, credential.URL)
	timeout := cfg.Timeout.Duration
	if timeout < time.Second || timeout > 30*time.Second {
		return nil, errors.New("native timeout must be 1s..30s")
	}
	// File generation isolates A -> B -> A and even uncertain same-URL
	// rotations. The SHA digest is never a URL, token, or human label.
	sum := sha256.Sum256([]byte(channel + "\x00" + u.String() + "\x00" + credential.Secret + "\x00" + cfg.CredentialFile + "\x00" + generation))
	transport := &http.Transport{
		// Secret-bearing vendor URLs are direct only. Existing generic Webhook
		// and heartbeat proxy behavior remains in their separate transport.
		Proxy:               nil,
		DialContext:         nativeDialContext(net.DefaultResolver, (&net.Dialer{Timeout: timeout}).DialContext),
		TLSHandshakeTimeout: timeout, ResponseHeaderTimeout: timeout,
		MaxResponseHeaderBytes: 16 << 10, DisableCompression: true,
		DisableKeepAlives: true, MaxIdleConns: 1, MaxIdleConnsPerHost: 1,
		MaxConnsPerHost: 1, ForceAttemptHTTP2: false,
	}
	n := &Native{channel: channel, endpoint: u.String(), secret: credential.Secret, destination: channel + ":" + hex.EncodeToString(sum[:]), language: config.NativeChannelLanguage(cfg), now: time.Now}
	if channel == "google_chat" {
		n.space = strings.Split(u.Path, "/")[3]
	}
	n.client = &http.Client{Transport: transport, Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return n, nil
}

func (n *Native) Destination() string            { return n.destination }
func (n *Native) MinimumInterval() time.Duration { return NativeMinimumInterval(n.channel) }
func (n *Native) Send(ctx context.Context, body string) error {
	return n.SendMessage(ctx, store.OutboxMessage{Body: body, Language: n.language})
}

func (n *Native) SendMessage(ctx context.Context, message store.OutboxMessage) error {
	if message.Destination != "" && message.Destination != n.destination {
		return errors.New("native message destination does not match credential snapshot")
	}
	if len(message.Body) == 0 || len(message.Body) > NativeMaxBodyBytes || !utf8.ValidString(message.Body) || strings.IndexFunc(message.Body, func(r rune) bool { return unicode.Is(unicode.Cf, r) || unicode.IsControl(r) && r != '\n' && r != '\t' }) >= 0 {
		return &DeliveryError{Channel: n.channel, Permanent: true, InvalidPayload: true}
	}
	data, err := n.payload(message.Body, message.Language)
	if err != nil {
		return &DeliveryError{Channel: n.channel, Permanent: true, InvalidPayload: true}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, n.endpoint, bytes.NewReader(data))
	if err != nil {
		return errors.New("native request could not be created")
	}
	request.Header.Set("Content-Type", "application/json; charset=UTF-8")
	request.Header.Set("User-Agent", "NodeRampart/"+version.Version)
	if n.channel == "discord" {
		request.Header.Set("User-Agent", "DiscordBot (https://github.com/littlesho/NodeRampart, "+version.Version+") NodeRampart")
	}
	response, err := n.client.Do(request)
	if err != nil {
		return errors.New("native HTTPS request failed")
	}
	defer response.Body.Close()
	data, err = io.ReadAll(io.LimitReader(response.Body, maxTelegramResponseBytes+1))
	delivery := &DeliveryError{Channel: n.channel, StatusCode: response.StatusCode, Permanent: response.StatusCode >= 300 && response.StatusCode < 500 && response.StatusCode != 408 && response.StatusCode != 429, RateLimited: response.StatusCode == 429}
	delivery.RetryAfter, delivery.SuspendDestination = parseRetryAfter(response.Header.Get("Retry-After"), n.now())
	if err != nil || len(data) > maxTelegramResponseBytes {
		delivery.InvalidResponse = true
		return nativeDeliveryError(delivery)
	}
	if n.confirm(response.StatusCode, data, delivery) {
		return nil
	}
	return nativeDeliveryError(delivery)
}

// Every channel encodes its own selected contract. The semantic body is frozen
// in the outbox; only Feishu's expiring timestamp/signature is regenerated.
func (n *Native) payload(body, language string) ([]byte, error) {
	var payload any
	switch n.channel {
	case "feishu":
		p := map[string]any{"msg_type": "text", "content": map[string]string{"text": neutralizeNativeMentions(body)}}
		if n.secret != "" {
			stamp := strconv.FormatInt(n.now().Unix(), 10)
			p["timestamp"], p["sign"] = stamp, feishuSign(stamp, n.secret)
		}
		payload = p
	case "wecom":
		payload = map[string]any{"msgtype": "text", "text": map[string]any{"content": neutralizeNativeMentions(body), "mentioned_list": []string{}, "mentioned_mobile_list": []string{}}}
	case "discord":
		payload = map[string]any{"content": discordPlainText(body), "allowed_mentions": map[string]any{"parse": []string{}, "users": []string{}, "roles": []string{}, "replied_user": false}, "flags": 4}
	case "slack":
		// Dynamic text exists only in plain_text. A fixed accessibility fallback
		// and mrkdwn=false avoid mentions in the top-level text as well.
		fallback := "NodeRampart notification"
		if language == "zh" {
			fallback = "NodeRampart 通知"
		}
		payload = map[string]any{"text": fallback, "mrkdwn": false, "unfurl_links": false, "unfurl_media": false, "blocks": []any{map[string]any{"type": "section", "text": map[string]any{"type": "plain_text", "text": body, "emoji": false}}}}
	case "teams":
		payload = map[string]any{"type": "message", "attachments": []any{map[string]any{"contentType": "application/vnd.microsoft.card.adaptive", "contentUrl": nil, "content": map[string]any{"type": "AdaptiveCard", "version": "1.2", "$schema": "http://adaptivecards.io/schemas/adaptive-card.json", "body": []any{map[string]any{"type": "TextBlock", "text": escapeNativeMarkdown(body), "wrap": true}}}}}}
	case "google_chat":
		payload = map[string]string{"text": escapeNativeMarkdown(body)}
	default:
		return nil, errors.New("native notification channel is unsupported")
	}
	data, err := json.Marshal(payload)
	if err != nil || len(data) > 16<<10 {
		return nil, errors.New("native encoded request exceeds its bound")
	}
	// Limits use final visible content after escaping, not the pre-escaped
	// semantic body's length. There is no automatic splitting or truncation.
	if n.channel == "wecom" && len(neutralizeNativeMentions(body)) > 2048 {
		return nil, errors.New("WeCom UTF-8 text exceeds 2048 bytes")
	}
	if n.channel == "discord" && len([]rune(discordPlainText(body))) > 2000 {
		return nil, errors.New("Discord escaped content exceeds 2000 characters")
	}
	return data, nil
}

func feishuSign(timestamp, secret string) string {
	mac := hmac.New(sha256.New, []byte(timestamp+"\n"+secret))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func neutralizeNativeMentions(body string) string {
	return strings.NewReplacer("<", "‹", ">", "›", "@", "＠").Replace(body)
}

func discordPlainText(body string) string {
	return "```\n" + strings.ReplaceAll(neutralizeNativeMentions(body), "`", "ˋ") + "\n```"
}

func escapeNativeMarkdown(body string) string {
	var b strings.Builder
	for _, r := range body {
		switch r {
		case '@':
			b.WriteRune('＠')
		case '<':
			b.WriteRune('‹')
		case '>':
			b.WriteRune('›')
		case '\\', '*', '_', '`', '~', '[', ']', '(', ')', '#', '|':
			b.WriteRune('\\')
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func nativeDeliveryError(delivery *DeliveryError) error {
	if delivery.RateLimited || delivery.RetryAfter > 0 || delivery.SuspendDestination {
		delivery.RateLimited, delivery.Permanent = true, false
	}
	return delivery
}

func (n *Native) confirm(status int, data []byte, delivery *DeliveryError) bool {
	if n.channel == "slack" {
		text := strings.TrimSpace(string(data))
		if status == 200 && text == "ok" {
			return true
		}
		switch text {
		case "action_prohibited", "channel_is_archived", "invalid_payload", "invalid_token", "no_active_hooks", "no_service", "no_service_id", "no_team", "no_text", "posting_to_general_channel_denied", "team_disabled", "user_not_found", "channel_not_found", "too_many_attachments":
			if status == 200 {
				delivery.Permanent = true
			}
		default:
			delivery.InvalidResponse = true
		}
		return false
	}
	if n.channel == "teams" {
		// This contract observes only acceptance by the asynchronous trigger.
		// Never poll Location or change destinations based on a response.
		if status == 202 && len(bytes.TrimSpace(data)) == 0 {
			return true
		}
		delivery.InvalidResponse = status >= 200 && status < 300
		return false
	}
	object, err := nativeJSONObject(data)
	if err != nil {
		delivery.InvalidResponse = true
		return false
	}
	integer := func(key string) (int, bool) {
		raw, ok := object[key]
		var value *int
		if !ok || json.Unmarshal(raw, &value) != nil || value == nil {
			return 0, false
		}
		return *value, true
	}
	str := func(key string) string { var value string; _ = json.Unmarshal(object[key], &value); return value }
	switch n.channel {
	case "feishu", "wecom":
		field := "code"
		if n.channel == "wecom" {
			field = "errcode"
		}
		code, valid := integer(field)
		if !valid {
			delivery.InvalidResponse = true
			return false
		}
		if status == 200 && code == 0 {
			return true
		}
		delivery.APIErrorCode = code
		// Business failures refine an otherwise successful HTTP response only.
		// A contradictory body cannot erase HTTP 429 or make 5xx/408 permanent.
		if status == 200 && code != 0 {
			delivery.Permanent = code != -1
			delivery.RateLimited = delivery.RateLimited || (n.channel == "feishu" && code == 11232) || (n.channel == "wecom" && code == 45009)
		}
		if delivery.RateLimited && delivery.RetryAfter == 0 {
			delivery.RetryAfter = time.Minute
		}
	case "discord":
		if status == 200 && object["code"] == nil && object["error"] == nil && nativeSnowflakeValid(str("id")) && nativeSnowflakeValid(str("channel_id")) {
			return true
		}
		if code, ok := integer("code"); ok {
			delivery.APIErrorCode = code
			if status == 200 {
				switch code {
				case 10015, 50001, 50013, 50035:
					delivery.Permanent = true
				case 20028, 20029:
					delivery.RateLimited = true
				}
			}
		} else {
			delivery.InvalidResponse = true
		}
		if raw, ok := object["retry_after"]; ok && status == 429 {
			delay, suspend, valid := nativeRetryFraction(raw)
			if !valid {
				delivery.InvalidResponse = true
			}
			if delay > delivery.RetryAfter {
				delivery.RetryAfter = delay
			}
			delivery.SuspendDestination = delivery.SuspendDestination || suspend
		}
	case "google_chat":
		name := str("name")
		prefix := "spaces/" + n.space + "/messages/"
		if status == 200 && object["error"] == nil && strings.HasPrefix(name, prefix) && nativeResourceID(strings.TrimPrefix(name, prefix)) {
			thread, threadErr := nativeJSONObject(object["thread"])
			var threadName string
			if threadErr == nil && json.Unmarshal(thread["name"], &threadName) == nil && strings.HasPrefix(threadName, "spaces/"+n.space+"/threads/") && nativeResourceID(strings.TrimPrefix(threadName, "spaces/"+n.space+"/threads/")) {
				return true
			}
		}
		googleErr, googleErrParse := nativeJSONObject(object["error"])
		var code *int
		if googleErrParse == nil && json.Unmarshal(googleErr["code"], &code) == nil && code != nil {
			delivery.APIErrorCode = *code
			code := *code
			if status == 200 && code >= 400 && code < 500 && code != 408 && code != 429 {
				delivery.Permanent = true
			}
			if status == 200 && code == 429 {
				delivery.RateLimited = true
			}
		} else {
			delivery.InvalidResponse = true
		}
	}
	return false
}

func nativeSnowflakeValid(value string) bool {
	n, err := strconv.ParseUint(value, 10, 64)
	return err == nil && n > 0 && strconv.FormatUint(n, 10) == value
}
func nativeResourceID(value string) bool {
	return len(value) > 0 && len(value) <= 256 && strings.IndexFunc(value, func(r rune) bool { return r < 0x21 || r > 0x7e || r == '/' || r == '\\' }) < 0
}

// Reject duplicate success fields and trailing JSON rather than accepting the
// decoder's last value as confirmation.
func nativeJSONObject(data []byte) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, errors.New("invalid response")
	}
	object := map[string]json.RawMessage{}
	for decoder.More() {
		token, err = decoder.Token()
		if err != nil {
			return nil, errors.New("invalid response")
		}
		key, ok := token.(string)
		if !ok {
			return nil, errors.New("invalid response")
		}
		if _, exists := object[key]; exists {
			return nil, errors.New("duplicate response field")
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return nil, errors.New("invalid response")
		}
		object[key] = raw
	}
	if _, err := decoder.Token(); err != nil {
		return nil, errors.New("invalid response")
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, errors.New("trailing response")
	}
	return object, nil
}

func nativeRetryFraction(raw json.RawMessage) (time.Duration, bool, bool) {
	var value json.Number
	if len(raw) == 0 || raw[0] == '"' || json.Unmarshal(raw, &value) != nil || string(raw) == "null" {
		return 0, false, false
	}
	seconds, err := strconv.ParseFloat(string(value), 64)
	if err != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0 {
		return 0, false, false
	}
	if seconds > maxTelegramRetryAfter.Seconds() {
		return 0, true, true
	}
	return time.Duration(math.Ceil(seconds * float64(time.Second))), false, true
}

func readNativeCredential(path string) (config.NativeCredential, string, error) {
	var credential config.NativeCredential
	fd, err := openNativeSecret(path)
	if err != nil {
		return credential, "", errors.New("native protected credentials unavailable or unsafe")
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	var before, after unix.Stat_t
	if unix.Fstat(fd, &before) != nil || before.Mode&unix.S_IFMT != unix.S_IFREG || before.Nlink != 1 || before.Size < 20 || before.Size > 8192 || !nativeCredentialPermission(before) {
		return credential, "", errors.New("native protected credential file ownership, permissions, type or size is unsafe")
	}
	data, err := io.ReadAll(io.LimitReader(file, 8193))
	if err != nil || len(data) > 8192 || unix.Fstat(fd, &after) != nil || before.Dev != after.Dev || before.Ino != after.Ino || before.Nlink != after.Nlink || before.Mode != after.Mode || before.Uid != after.Uid || before.Gid != after.Gid || before.Size != after.Size || before.Mtim != after.Mtim || before.Ctim != after.Ctim {
		return credential, "", errors.New("native protected credentials changed or could not be read")
	}
	object, err := nativeJSONObject(data)
	if err != nil || len(object) < 1 || len(object) > 2 || object["url"] == nil {
		return credential, "", errors.New("native protected credentials require a valid JSON object")
	}
	for key := range object {
		if key != "url" && key != "secret" {
			return credential, "", errors.New("native protected credential field is unsupported")
		}
		var value *string
		if json.Unmarshal(object[key], &value) != nil || value == nil {
			return credential, "", errors.New("native protected credential fields must be strings")
		}
	}
	if json.Unmarshal(data, &credential) != nil {
		return credential, "", errors.New("native protected credential fields are invalid")
	}
	generation := fmt.Sprintf("%d:%d:%d:%d:%d:%d", before.Dev, before.Ino, before.Ctim.Sec, before.Ctim.Nsec, before.Mtim.Sec, before.Mtim.Nsec)
	return credential, generation, nil
}

func nativeServiceIDs() (uint32, uint32, bool) {
	account, err := user.Lookup("noderampart")
	if err != nil {
		return 0, 0, false
	}
	uid, uerr := strconv.ParseUint(account.Uid, 10, 32)
	gid, gerr := strconv.ParseUint(account.Gid, 10, 32)
	return uint32(uid), uint32(gid), uerr == nil && gerr == nil
}

func nativeCredentialPermission(stat unix.Stat_t) bool {
	uid, gid, service := nativeServiceIDs()
	if stat.Uid != 0 && stat.Uid != uint32(os.Geteuid()) && (!service || stat.Uid != uid) {
		return false
	}
	if stat.Mode&0o7777 == 0o600 {
		return true
	}
	return service && stat.Gid == gid && (stat.Uid == 0 || stat.Uid == uid) && stat.Mode&0o7777 == 0o640
}

func openNativeSecret(path string) (int, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return -1, errors.New("unsafe credential path")
	}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	parent, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, err
	}
	uid, _, service := nativeServiceIDs()
	for _, part := range parts[:len(parts)-1] {
		next, err := unix.Openat(parent, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		unix.Close(parent)
		if err != nil {
			return -1, err
		}
		var stat unix.Stat_t
		if unix.Fstat(next, &stat) != nil || (stat.Uid != 0 && stat.Uid != uint32(os.Geteuid()) && (!service || stat.Uid != uid)) || (stat.Mode&0o022 != 0 && !(stat.Uid == 0 && stat.Mode&unix.S_ISVTX != 0)) {
			unix.Close(next)
			return -1, errors.New("unsafe credential directory")
		}
		parent = next
	}
	defer unix.Close(parent)
	return unix.Openat(parent, parts[len(parts)-1], unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
}

type nativeResolver interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}
type nativeDialer func(context.Context, string, string) (net.Conn, error)

func nativeDialContext(resolver nativeResolver, dial nativeDialer) nativeDialer {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil || port != "443" {
			return nil, errors.New("native HTTPS dial refused")
		}
		addresses, err := resolver.LookupIPAddr(ctx, host)
		if err != nil || len(addresses) == 0 || len(addresses) > 32 {
			return nil, errors.New("native DNS resolution failed")
		}
		for _, address := range addresses {
			ip, ok := netip.AddrFromSlice(address.IP)
			if !ok || address.Zone != "" || !nativePublicIP(ip) {
				return nil, errors.New("native DNS returned a prohibited address")
			}
		}
		for _, address := range addresses {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			conn, err := dial(ctx, network, net.JoinHostPort(address.IP.String(), port))
			if err != nil {
				continue
			}
			peer, err := netip.ParseAddrPort(conn.RemoteAddr().String())
			resolved, valid := netip.AddrFromSlice(address.IP)
			if err != nil || !valid || !nativePublicIP(peer.Addr()) || peer.Addr().Unmap() != resolved.Unmap() {
				conn.Close()
				return nil, errors.New("native connected peer address refused")
			}
			return conn, nil
		}
		return nil, errors.New("native HTTPS connection failed")
	}
}

var nativeDeniedNetworks = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001::/32"), netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("2002::/16"),
}

func nativePublicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.Zone() != "" {
		return false
	}
	if ip.Is6() && !netip.MustParsePrefix("2000::/3").Contains(ip) {
		return false
	}
	for _, prefix := range nativeDeniedNetworks {
		if prefix.Contains(ip) {
			return false
		}
	}
	return true
}
