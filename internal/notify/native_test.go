// SPDX-License-Identifier: MIT

package notify

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/store"
)

var nativeFixtureURLs = map[string]string{
	"feishu":      "https://open.feishu.cn/open-apis/bot/v2/hook/synthetic-hook-123456",
	"wecom":       "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=synthetic-key-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	"discord":     "https://discord.com/api/webhooks/123456789012345678/synthetic-token-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb0",
	"slack":       "https://hooks.slack.com/services/T00000000/B00000000/SyntheticToken0000",
	"teams":       "https://defaultsynthetic.environment.api.powerplatform.com/powerautomate/automations/direct/cu/20/workflows/synthetic-workflow-123456/triggers/manual/paths/invoke?api-version=1&sp=%2Ftriggers%2Fmanual%2Frun&sv=1.0&sig=synthetic-signature-123456789",
	"google_chat": "https://chat.googleapis.com/v1/spaces/synthetic-space/messages?key=synthetic-key-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa&token=synthetic-token-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
}

type nativeRoundTrip func(*http.Request) (*http.Response, error)

func (f nativeRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func nativeFixture(t *testing.T, channel string) (*Native, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "protected.json")
	credential := config.NativeCredential{URL: nativeFixtureURLs[channel]}
	if channel == "feishu" {
		credential.Secret = "synthetic-signing-secret"
	}
	data, _ := json.Marshal(credential)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	n, err := NewNative(channel, config.NativeChannelConfig{CredentialFile: path, Timeout: config.Duration{Duration: time.Second}, Language: "en"})
	if err != nil {
		t.Fatal(err)
	}
	return n, path
}

func nativeSuccess(channel string) (int, string) {
	switch channel {
	case "feishu":
		return 200, `{"code":0,"msg":"success","data":{}}`
	case "wecom":
		return 200, `{"errcode":0,"errmsg":"ok"}`
	case "discord":
		return 200, `{"id":"123456789012345678","channel_id":"223456789012345678"}`
	case "slack":
		return 200, "ok\n"
	case "teams":
		return 202, ""
	default:
		return 200, `{"name":"spaces/synthetic-space/messages/synthetic-message","thread":{"name":"spaces/synthetic-space/threads/synthetic-thread"}}`
	}
}

func nativeMock(n *Native, status int, body, retry string) {
	n.client.Transport = nativeRoundTrip(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: status, Header: http.Header{"Retry-After": []string{retry}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})
}

func TestNativeRequestContracts(t *testing.T) {
	for _, channel := range config.NativeChannelNames() {
		t.Run(channel, func(t *testing.T) {
			n, _ := nativeFixture(t, channel)
			n.now = func() time.Time { return time.Unix(1599360473, 0) }
			calls := 0
			n.client.Transport = nativeRoundTrip(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method != "POST" || r.Header.Get("Content-Type") != "application/json; charset=UTF-8" || r.Header.Get("Authorization") != "" {
					t.Fatal("wrong native request envelope")
				}
				data, err := io.ReadAll(r.Body)
				if err != nil || len(data) > 16<<10 {
					t.Fatal("unbounded request")
				}
				var p map[string]any
				if json.Unmarshal(data, &p) != nil {
					t.Fatal("invalid payload")
				}
				switch channel {
				case "feishu":
					if p["msg_type"] != "text" || p["timestamp"] != "1599360473" || p["sign"] != feishuSign("1599360473", "synthetic-signing-secret") {
						t.Fatal("Feishu contract")
					}
				case "wecom":
					if p["msgtype"] != "text" || len(p["text"].(map[string]any)["mentioned_list"].([]any)) != 0 {
						t.Fatal("WeCom contract")
					}
				case "discord":
					if !strings.HasPrefix(r.URL.Path, "/api/v10/webhooks/") || !strings.HasPrefix(r.Header.Get("User-Agent"), "DiscordBot (https://github.com/littlesho/NodeRampart, ") {
						t.Fatal("Discord version or identified User-Agent missing")
					}
					if r.URL.Query().Get("wait") != "true" || len(p["allowed_mentions"].(map[string]any)["parse"].([]any)) != 0 || strings.Contains(p["content"].(string), "@everyone") || strings.Contains(p["content"].(string), "<@") {
						t.Fatal("Discord mention/confirmation contract")
					}
					if !strings.HasPrefix(p["content"].(string), "```\n") || strings.Count(p["content"].(string), "```") != 2 {
						t.Fatal("event broke literal formatting")
					}
				case "slack":
					text := p["blocks"].([]any)[0].(map[string]any)["text"].(map[string]any)
					if text["type"] != "plain_text" || p["text"] != "NodeRampart notification" || p["mrkdwn"] != false || p["channel"] != nil {
						t.Fatal("Slack plain text contract")
					}
				case "teams":
					if p["type"] != "message" {
						t.Fatal("Teams trigger contract")
					}
					a := p["attachments"].([]any)[0].(map[string]any)
					if a["contentUrl"] != nil || a["contentType"] != "application/vnd.microsoft.card.adaptive" || a["content"].(map[string]any)["version"] != "1.2" || p["@type"] != nil {
						t.Fatal("Teams AdaptiveCard contract")
					}
				case "google_chat":
					if r.URL.Query().Get("key") != "synthetic-key-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" || r.URL.Query().Get("token") != "synthetic-token-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" || p["text"] == nil {
						t.Fatal("Google protected query contract")
					}
				}
				status, body := nativeSuccess(channel)
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})
			if err := n.Send(context.Background(), "NodeRampart HIGH\n@everyone @here <@user> <!channel> *fake* ``` bad"); err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatal("unexpected splitting")
			}
			if err := n.SendMessage(context.Background(), store.OutboxMessage{Body: "safe", Destination: "other:identity"}); err == nil || calls != 1 {
				t.Fatal("destination snapshot mismatch sent")
			}
		})
	}
}

func TestNativeResponseMatrix(t *testing.T) {
	for _, channel := range config.NativeChannelNames() {
		t.Run(channel, func(t *testing.T) {
			n, _ := nativeFixture(t, channel)
			status, body := nativeSuccess(channel)
			nativeMock(n, status, body, "")
			if err := n.Send(context.Background(), "safe summary"); err != nil {
				t.Fatal(err)
			}
			for _, body := range []string{"", `{}`, `null`, `[]`, `{"code":null,"errcode":null}`, `{"code":"0","errcode":"0"}`, `{"code":0,"code":1,"errcode":0,"errcode":1}`, `{"code":0,"errcode":0} trailing`, "secret-synthetic-response-token", strings.Repeat("x", maxTelegramResponseBytes+1)} {
				if channel == "teams" && body == "" {
					continue
				}
				nativeMock(n, status, body, "")
				err := n.Send(context.Background(), "safe summary")
				if err == nil || strings.Contains(err.Error(), "secret-synthetic-response-token") {
					t.Fatal("unknown response succeeded or leaked")
				}
			}
			for _, code := range []int{301, 302, 307, 400, 401, 403, 404, 408, 410, 422, 429, 500, 503} {
				nativeMock(n, code, body, "90")
				err := n.Send(context.Background(), "safe summary")
				var d *DeliveryError
				if !errors.As(err, &d) || d.StatusCode != code || d.RetryAfter != 90*time.Second || !d.RateLimited || d.Permanent {
					t.Fatalf("HTTP %d incorrect category: %v", code, err)
				}
				nativeMock(n, code, "{}", "")
				err = n.Send(context.Background(), "safe summary")
				if !errors.As(err, &d) || (code >= 300 && code < 500 && code != 408 && code != 429) != d.Permanent {
					t.Fatalf("HTTP %d incorrect permanence: %v", code, err)
				}
			}
		})
	}
}

func TestNativeHTTPFailuresTakePrecedenceOverContradictoryBusinessBodies(t *testing.T) {
	// Valid vendor failures can contradict their transport status. Preserve the
	// transport classification and server hold instead of quarantining HTTP 429,
	// treating a 5xx as a permanent payload problem, or retrying HTTP 401.
	bodies := map[string][]string{
		"feishu":      {`{"code":19021}`, `{"code":11232}`, `{"code":-1}`, `{"code":0}`},
		"wecom":       {`{"errcode":40058}`, `{"errcode":45009}`, `{"errcode":-1}`, `{"errcode":0}`},
		"discord":     {`{"code":50035}`, `{"code":20028}`, `{"code":-1}`, `{"id":"123456789012345678","channel_id":"223456789012345678"}`},
		"slack":       {"invalid_payload", "action_prohibited", "invalid_token", "ok"},
		"teams":       {`{"error":{"code":400}}`, "", "ok"},
		"google_chat": {`{"error":{"code":403}}`, `{"error":{"code":429}}`, `{"error":{"code":503}}`, `{"name":"spaces/synthetic-space/messages/message","thread":{"name":"spaces/synthetic-space/threads/thread"}}`},
	}
	for _, channel := range config.NativeChannelNames() {
		t.Run(channel, func(t *testing.T) {
			n, _ := nativeFixture(t, channel)
			for _, status := range []int{400, 401, 403, 404, 408, 429, 500, 503} {
				for _, body := range bodies[channel] {
					nativeMock(n, status, body, "")
					err := n.Send(context.Background(), "safe summary")
					var d *DeliveryError
					permanent := status >= 400 && status < 500 && status != 408 && status != 429
					if !errors.As(err, &d) || d.StatusCode != status || d.Permanent != permanent || d.RateLimited != (status == 429) {
						t.Fatalf("HTTP %d changed classification with a contradictory business response: %v", status, err)
					}
					if status == 429 {
						nativeMock(n, status, body, "91")
						err = n.Send(context.Background(), "safe summary")
						if !errors.As(err, &d) || !d.RateLimited || d.Permanent || d.RetryAfter != 91*time.Second {
							t.Fatalf("HTTP 429 business response lost the server hold: %v", err)
						}
					}
				}
			}
		})
	}
}

func TestNativeBusinessFailures(t *testing.T) {
	for _, test := range []struct {
		channel, body   string
		permanent, rate bool
		code            int
	}{
		{"feishu", `{"code":19021,"msg":"secret-synthetic-url"}`, true, false, 19021},
		{"feishu", `{"code":11232}`, false, true, 11232},
		{"wecom", `{"errcode":40058}`, true, false, 40058},
		{"wecom", `{"errcode":45009}`, false, true, 45009},
		{"wecom", `{"errcode":-1}`, false, false, -1},
		{"discord", `{"code":50035}`, true, false, 50035},
		{"discord", `{"code":10015}`, true, false, 10015},
		{"discord", `{"code":20028}`, false, true, 20028},
		{"slack", "action_prohibited", true, false, 0},
		{"slack", "channel_is_archived", true, false, 0},
		{"slack", "invalid_payload", true, false, 0},
		{"slack", "invalid_token", true, false, 0},
		{"google_chat", `{"error":{"code":401,"message":"secret-synthetic-token"}}`, true, false, 401},
		{"google_chat", `{"error":{"code":403}}`, true, false, 403},
		{"google_chat", `{"error":{"code":429}}`, false, true, 429},
		{"google_chat", `{"error":{"code":503}}`, false, false, 503},
	} {
		t.Run(test.channel+"_"+test.body[:min(8, len(test.body))], func(t *testing.T) {
			n, _ := nativeFixture(t, test.channel)
			nativeMock(n, 200, test.body, "")
			err := n.Send(context.Background(), "safe")
			var d *DeliveryError
			if !errors.As(err, &d) || d.Permanent != test.permanent || d.RateLimited != test.rate || d.APIErrorCode != test.code || strings.Contains(err.Error(), "secret-synthetic") {
				t.Fatalf("business category: %v", err)
			}
		})
	}
	for _, channel := range []string{"discord", "google_chat", "teams"} {
		n, _ := nativeFixture(t, channel)
		for _, body := range []string{`{"id":"123","channel_id":12}`, `{"id":"123"}`, `{"id":"123456789012345678","channel_id":"223456789012345678","code":50035}`, `{"name":"spaces/another-space/messages/message","thread":{"name":"spaces/another-space/threads/thread"}}`, `{"name":"spaces/synthetic-space/messages/message"}`, `{"name":"spaces/synthetic-space/messages/message","thread":{"name":"spaces/synthetic-space/threads/thread"},"error":{"code":403}}`, `{"name":"spaces/synthetic-space/messages/message","thread":{"name":"spaces/other/threads/thread","name":"spaces/synthetic-space/threads/thread"}}`, `{"error":{"code":null}}`, `{"error":{"code":"0"}}`, `{"error":{"code":503,"code":429}}`, `{"ok":true}`} {
			nativeMock(n, 200, body, "")
			if n.Send(context.Background(), "safe") == nil {
				t.Fatal("incomplete acknowledgment succeeded")
			}
		}
	}
}

func TestFeishuFixedVectorAndRetryTimestamp(t *testing.T) {
	// Independently generated with Python hmac.new(b"1599360473\nsynthetic-signing-secret", b"", hashlib.sha256).
	if got := feishuSign("1599360473", "synthetic-signing-secret"); got != "VqvSL8MoOhBIhiOJFWtWMTHcMpwIWVIP/dPXOKcYJCc=" {
		t.Fatalf("signature vector: %s", got)
	}
	n, _ := nativeFixture(t, "feishu")
	stamp := int64(1599360473)
	n.now = func() time.Time { return time.Unix(stamp, 0) }
	var stamps []string
	var signatures []string
	n.client.Transport = nativeRoundTrip(func(r *http.Request) (*http.Response, error) {
		data, _ := io.ReadAll(r.Body)
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(data, &fields)
		var ts, sign string
		_ = json.Unmarshal(fields["timestamp"], &ts)
		_ = json.Unmarshal(fields["sign"], &sign)
		stamps = append(stamps, ts)
		signatures = append(signatures, sign)
		return &http.Response{StatusCode: 503, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"code":-1}`)), Request: r}, nil
	})
	_ = n.Send(context.Background(), "unchanged body")
	stamp += 3601
	_ = n.Send(context.Background(), "unchanged body")
	if len(stamps) != 2 || stamps[0] == stamps[1] || signatures[0] == signatures[1] {
		t.Fatal("retry persisted expiring signature")
	}
}

func TestNativeRetryBoundaries(t *testing.T) {
	n, _ := nativeFixture(t, "discord")
	for _, test := range []struct {
		value            string
		delay            time.Duration
		suspend, invalid bool
	}{
		{"0.001", time.Millisecond, false, false}, {"1.25", 1250 * time.Millisecond, false, false}, {"2592000", 30 * 24 * time.Hour, false, false}, {"2592001", 0, true, false}, {"1e99", 0, true, false}, {"-1", 0, false, true}, {`"1"`, 0, false, true}, {"null", 0, false, true}, {"{}", 0, false, true},
	} {
		nativeMock(n, 429, `{"code":429,"retry_after":`+test.value+`}`, "")
		err := n.Send(context.Background(), "safe")
		var d *DeliveryError
		if !errors.As(err, &d) || d.RetryAfter != test.delay || d.SuspendDestination != test.suspend || d.InvalidResponse != test.invalid || !d.RateLimited || d.Permanent {
			t.Fatalf("retry %s: %v", test.value, err)
		}
	}
	for _, value := range []string{"2592001", "99999999999999999999999999999999999999"} {
		nativeMock(n, 503, `{"code":0}`, value)
		var d *DeliveryError
		err := n.Send(context.Background(), "safe")
		if !errors.As(err, &d) || !d.SuspendDestination || !d.RateLimited {
			t.Fatal("excessive wait shortened")
		}
	}
}

func TestNativeCredentialSnapshotAndFileSafety(t *testing.T) {
	n, path := nativeFixture(t, "feishu")
	old := n.Destination()
	original := n.endpoint
	data, _ := os.ReadFile(path)
	replacement := strings.Replace(string(data), "synthetic-hook-123456", "another-hook-123456789", 1)
	// Management stages immutable files and uses atomic replacement. In-place
	// writes can share an inode timestamp at filesystem clock granularity;
	// store isolation also prevents reviving rows if a manual edit returns an
	// identical snapshot. Exercise the real managed generation here.
	if err := os.WriteFile(path+".next", []byte(replacement), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path+".next", path); err != nil {
		t.Fatal(err)
	}
	current, err := NewNative("feishu", config.NativeChannelConfig{CredentialFile: path, Timeout: config.Duration{Duration: time.Second}})
	if err != nil || current.Destination() == old || n.endpoint != original {
		t.Fatal("replacement rebound sender snapshot")
	}
	if err := os.WriteFile(path+".next", data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path+".next", path); err != nil {
		t.Fatal(err)
	}
	back, err := NewNative("feishu", config.NativeChannelConfig{CredentialFile: path, Timeout: config.Duration{Duration: time.Second}})
	if err != nil || back.Destination() == old || back.Destination() == current.Destination() {
		t.Fatal("A -> B -> A identity revived")
	}
	for _, mode := range []os.FileMode{0o644, 0o660, 0o604, 0o700} {
		if os.Chmod(path, mode) != nil {
			t.Fatal("chmod")
		}
		if ValidateNativeCredential("feishu", path) == nil {
			t.Fatal("unsafe mode accepted")
		}
	}
	_ = os.Chmod(path, 0o600)
	symlink := filepath.Join(filepath.Dir(path), "link")
	if err := os.Symlink(path, symlink); err != nil {
		t.Fatal(err)
	}
	if ValidateNativeCredential("feishu", symlink) == nil {
		t.Fatal("symlink credential accepted")
	}
	hardlink := filepath.Join(filepath.Dir(path), "hardlink")
	if err := os.Link(path, hardlink); err != nil {
		t.Fatal(err)
	}
	if ValidateNativeCredential("feishu", path) == nil {
		t.Fatal("hardlink credential accepted")
	}
	_ = os.Remove(hardlink)
	for _, body := range []string{`{"url":"` + original + `","unexpected":"secret-synthetic"}`, `{"url":"` + original + `","url":"` + original + `"}`, strings.Repeat("x", 8193), `{"url":null}`, `{"url":"` + original + `","secret":null}`} {
		_ = os.WriteFile(path, []byte(body), 0o600)
		if err := ValidateNativeCredential("feishu", path); err == nil || strings.Contains(err.Error(), "secret-synthetic") {
			t.Fatal("bad credential accepted or leaked")
		}
	}
	_ = os.WriteFile(path, data, 0o600)
	_ = os.Chmod(filepath.Dir(path), 0o777)
	if ValidateNativeCredential("feishu", path) == nil {
		t.Fatal("writable parent accepted")
	}
	_ = os.Chmod(filepath.Dir(path), 0o700)
}

type nativeFakeResolver struct {
	addresses []net.IPAddr
	err       error
}

func (r nativeFakeResolver) LookupIPAddr(context.Context, string) ([]net.IPAddr, error) {
	return r.addresses, r.err
}

type nativeFakeConn struct {
	net.Conn
	peer   net.Addr
	closed bool
}

func (c *nativeFakeConn) RemoteAddr() net.Addr { return c.peer }
func (c *nativeFakeConn) Close() error         { c.closed = true; return nil }

func TestNativeDNSAndActualPeerChecks(t *testing.T) {
	for _, value := range []string{"127.0.0.1", "10.0.0.1", "192.168.0.1", "169.254.169.254", "100.100.100.200", "0.0.0.0", "198.18.0.1", "192.0.2.1", "::1", "::ffff:127.0.0.1", "fd00::1", "fe80::1", "2001:db8::1", "2002:7f00:1::", "64:ff9b::7f00:1"} {
		ip := netip.MustParseAddr(value)
		if nativePublicIP(ip) {
			t.Fatalf("private/reserved address allowed: %s", value)
		}
		calls := 0
		dial := nativeDialContext(nativeFakeResolver{addresses: []net.IPAddr{{IP: net.ParseIP(value)}}}, func(context.Context, string, string) (net.Conn, error) {
			calls++
			return nil, errors.New("must not dial")
		})
		if _, err := dial(context.Background(), "tcp", "open.feishu.cn:443"); err == nil || calls != 0 {
			t.Fatal("prohibited address dialed")
		}
	}
	for _, peer := range []string{"127.0.0.1:443", "8.8.4.4:443"} {
		addr, _ := net.ResolveTCPAddr("tcp", peer)
		conn := &nativeFakeConn{peer: addr}
		dial := nativeDialContext(nativeFakeResolver{addresses: []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}}, func(context.Context, string, string) (net.Conn, error) { return conn, nil })
		if _, err := dial(context.Background(), "tcp", "open.feishu.cn:443"); err == nil || !conn.closed {
			t.Fatal("connected peer was not pinned")
		}
	}
	for _, value := range []string{"8.8.8.8", "::ffff:8.8.8.8", "2606:4700:4700::1111"} {
		addr := &net.TCPAddr{IP: net.ParseIP(value), Port: 443}
		conn := &nativeFakeConn{peer: addr}
		calls := 0
		dial := nativeDialContext(nativeFakeResolver{addresses: []net.IPAddr{{IP: net.ParseIP(value)}}}, func(_ context.Context, _ string, address string) (net.Conn, error) {
			calls++
			if strings.Contains(address, "open.feishu.cn") {
				t.Fatal("dial repeated hostname DNS")
			}
			return conn, nil
		})
		if _, err := dial(context.Background(), "tcp", "open.feishu.cn:443"); err != nil || calls != 1 {
			t.Fatalf("public pinned peer rejected: %v", err)
		}
	}
	dial := nativeDialContext(nativeFakeResolver{addresses: []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}, {IP: net.ParseIP("127.0.0.1")}}}, func(context.Context, string, string) (net.Conn, error) { t.Fatal("mixed DNS dialed"); return nil, nil })
	if _, err := dial(context.Background(), "tcp", "open.feishu.cn:443"); err == nil {
		t.Fatal("mixed public/private DNS accepted")
	}
}

func TestNativeTransportCancellationTLSAndNoRedirect(t *testing.T) {
	for _, channel := range config.NativeChannelNames() {
		n, _ := nativeFixture(t, channel)
		transport := n.client.Transport.(*http.Transport)
		if transport.Proxy != nil || !transport.DisableKeepAlives || transport.MaxConnsPerHost != 1 || transport.MaxResponseHeaderBytes != 16<<10 || transport.TLSClientConfig != nil {
			t.Fatal("unsafe production transport defaults")
		}
		n.client.Transport = nativeRoundTrip(func(r *http.Request) (*http.Response, error) {
			return nil, errors.New("POST " + r.URL.String() + " synthetic-secret-network-error")
		})
		if err := n.Send(context.Background(), "safe"); err == nil || strings.Contains(err.Error(), "synthetic") || strings.Contains(err.Error(), "https:") {
			t.Fatal("net/http leaked URL")
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		n.client.Transport = nativeRoundTrip(func(r *http.Request) (*http.Response, error) { return nil, r.Context().Err() })
		if n.Send(ctx, "safe") == nil {
			t.Fatal("cancel succeeded")
		}
		calls := 0
		n.client.Transport = nativeRoundTrip(func(r *http.Request) (*http.Response, error) {
			calls++
			return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{"https://other-secret.invalid/capture"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
		})
		if n.Send(context.Background(), "safe") == nil || calls != 1 {
			t.Fatal("redirect followed")
		}
	}
	// Controlled loopback TLS fixture exists only in tests; no product switch
	// can exempt DNS/IP validation or disable certificate verification.
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"code":0}`)) }))
	defer server.Close()
	n, _ := nativeFixture(t, "feishu")
	target := server.URL
	bridge := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}}
	n.client.Transport = nativeRoundTrip(func(r *http.Request) (*http.Response, error) {
		copy := r.Clone(r.Context())
		copy.URL.Scheme = "https"
		copy.URL.Host = strings.TrimPrefix(target, "https://")
		return bridge.RoundTrip(copy)
	})
	if n.Send(context.Background(), "safe") == nil {
		t.Fatal("untrusted test certificate accepted")
	}
	trusted := server.Client().Transport
	n.client.Transport = nativeRoundTrip(func(r *http.Request) (*http.Response, error) {
		copy := r.Clone(r.Context())
		copy.URL.Scheme = "https"
		copy.URL.Host = strings.TrimPrefix(target, "https://")
		return trusted.RoundTrip(copy)
	})
	if err := n.Send(context.Background(), "safe"); err != nil {
		t.Fatal("dedicated test certificate failed", err)
	}
}

func TestNativeUnicodeAndEncodedBounds(t *testing.T) {
	for _, channel := range config.NativeChannelNames() {
		n, _ := nativeFixture(t, channel)
		status, body := nativeSuccess(channel)
		nativeMock(n, status, body, "")
		for _, text := range []string{strings.Repeat("a", NativeMaxBodyBytes), strings.Repeat("界", NativeMaxBodyBytes/3), strings.Repeat("😀", NativeMaxBodyBytes/4), strings.Repeat("_", NativeMaxBodyBytes)} {
			if err := n.Send(context.Background(), text); err != nil {
				t.Fatalf("%s UTF-8 bound: %v", channel, err)
			}
		}
		for _, text := range []string{"", strings.Repeat("a", NativeMaxBodyBytes+1), "bad\x1b[31m", "bad\rhidden", "bad\u202ehidden", string([]byte{0xff})} {
			err := n.Send(context.Background(), text)
			var d *DeliveryError
			if !errors.As(err, &d) || !d.Permanent || !d.InvalidPayload {
				t.Fatalf("invalid payload category: %v", err)
			}
		}
	}
}

func TestSlackFallbackUsesFrozenMessageLanguage(t *testing.T) {
	n, _ := nativeFixture(t, "slack")
	n.language = "zh"
	var fallback []string
	n.client.Transport = nativeRoundTrip(func(r *http.Request) (*http.Response, error) {
		var payload map[string]json.RawMessage
		data, _ := io.ReadAll(r.Body)
		if json.Unmarshal(data, &payload) != nil {
			t.Fatal("invalid Slack payload")
		}
		var text string
		if json.Unmarshal(payload["text"], &text) != nil {
			t.Fatal("invalid Slack fallback")
		}
		fallback = append(fallback, text)
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok")), Request: r}, nil
	})
	if err := n.SendMessage(context.Background(), store.OutboxMessage{Body: "frozen old English body", Language: "en"}); err != nil {
		t.Fatal(err)
	}
	if err := n.Send(context.Background(), "新的中文正文"); err != nil {
		t.Fatal(err)
	}
	if len(fallback) != 2 || fallback[0] != "NodeRampart notification" || fallback[1] != "NodeRampart 通知" {
		t.Fatal("configuration language changed frozen presentation or left Chinese fallback untranslated")
	}
}
