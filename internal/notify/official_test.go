// SPDX-License-Identifier: MIT

package notify

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/store"
)

func officialFixture(t *testing.T, channel string) (*Official, store.OutboxMessage) {
	t.Helper()
	credential := config.OfficialCredential{Channel: channel}
	switch channel {
	case "qqbot":
		credential.AppID = "12345678"
		credential.AppSecret = strings.Repeat("a", 32)
		credential.TargetType = "user"
		credential.TargetID = "synthetic_target_aa"
	case "line":
		credential.ChannelAccessToken = strings.Repeat("b", 32)
		credential.TargetType = "user"
		credential.TargetID = "U" + strings.Repeat("0", 32)
	case "twilio_sms":
		credential.AccountSID = "AC" + strings.Repeat("a", 32)
		credential.AuthMode = "api_key"
		credential.APIKeySID = "SK" + strings.Repeat("b", 32)
		credential.APIKeySecret = strings.Repeat("c", 32)
		credential.From = "+15555550101"
		credential.To = "+15555550102"
	case "whatsapp_cloud":
		credential.PhoneNumberID = "12345678901"
		credential.AccessToken = strings.Repeat("d", 32)
		credential.Recipient = "15555550102"
		credential.GraphVersion = config.WhatsAppGraphVersion
		credential.Templates = &config.OfficialTemplates{Test: &config.OfficialTemplate{Name: "noderampart_test", Language: "en_US", Parameters: []string{"event_kind", "phase", "severity", "time", "bounded_summary", "local_reference"}}, Event: &config.OfficialTemplate{Name: "noderampart_event", Language: "en_US", Parameters: []string{"event_kind", "phase", "severity", "time", "bounded_summary", "local_reference"}}}
	}
	data, err := json.Marshal(credential)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "credential.json")
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultOfficialChannel(channel)
	cfg.CredentialFile = path
	cfg.Subscription = config.OfficialSubscription{ConfirmedAt: "2026-10-02T00:00:00Z", Purpose: "Synthetic contract test", NotificationTypes: []string{"event", "test"}, EvidenceRef: "synthetic-test", BasisID: strings.Repeat("0", 32), CostConfirmed: true}
	cfg.DailyEnabled = false
	cfg.Enabled = true
	sender, err := NewOfficial(channel, cfg)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	sender.now = func() time.Time { return now }
	body, semantic := FormatOfficialTest("test-host", "en")
	encoded, _ := json.Marshal(semantic)
	message := store.OutboxMessage{ID: "synthetic-message", Channel: channel, Destination: sender.Destination(), Body: body, Language: "en", LogicalKind: "test", SemanticPayload: string(encoded), DispatchState: "in_flight", DispatchAttempt: 1, FirstAttemptAt: now, ExpiresAt: now.Add(7 * 24 * time.Hour), RetryKey: "00000000-0000-4000-8000-000000000000"}
	if err := sender.PrepareMessage(&message); err != nil {
		t.Fatal(err)
	}
	return sender, message
}

func officialSuccess(sender *Official, message store.OutboxMessage) (int, string) {
	switch sender.channel {
	case "qqbot":
		return 200, `{"id":"ROBOT1.0_synthetic","timestamp":"2026-10-02T12:00:00Z"}`
	case "line":
		return 200, `{"sentMessages":[{"id":"12345678901234567890"}]}`
	case "twilio_sms":
		data, _ := json.Marshal(map[string]any{"sid": "SM" + strings.Repeat("a", 32), "account_sid": sender.credential.AccountSID, "to": sender.credential.To, "body": message.Body, "status": "queued", "num_segments": "1", "price": nil, "price_unit": nil, "error_code": nil})
		return 201, string(data)
	case "whatsapp_cloud":
		return 200, `{"messaging_product":"whatsapp","messages":[{"id":"wamid.synthetic_ack"}]}`
	}
	return 0, ""
}
func officialMock(sender *Official, status int, body string, retry string) {
	sender.client.Transport = nativeRoundTrip(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/app/getAppAccessToken" {
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"access_token":"synthetic-qq-token","expires_in":"7200"}`)), Request: request}, nil
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Retry-After": []string{retry}}, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})
}

func TestOfficialRequestContracts(t *testing.T) {
	for _, channel := range config.OfficialChannelNames() {
		t.Run(channel, func(t *testing.T) {
			sender, message := officialFixture(t, channel)
			var requests int
			sender.client.Transport = nativeRoundTrip(func(request *http.Request) (*http.Response, error) {
				if request.URL.Scheme != "https" || request.URL.User != nil || request.URL.RawQuery != "" {
					t.Fatal("fixed HTTPS target contract")
				}
				if request.URL.Path == "/app/getAppAccessToken" {
					var token map[string]string
					if json.NewDecoder(request.Body).Decode(&token) != nil || token["appId"] != sender.credential.AppID || token["clientSecret"] != sender.credential.AppSecret {
						t.Fatal("QQ token contract")
					}
					return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"access_token":"synthetic-qq-token","expires_in":7200}`)), Request: request}, nil
				}
				requests++
				switch channel {
				case "qqbot":
					var p map[string]any
					json.NewDecoder(request.Body).Decode(&p)
					if request.URL.Host != "api.bot.qq.com" || request.URL.Path != "/v2/users/"+sender.credential.TargetID+"/messages" || request.Header.Get("Authorization") != "QQBot synthetic-qq-token" || request.Header.Get("X-Union-Appid") != sender.credential.AppID || p["msg_type"] != float64(0) || p["content"] != message.Body || len(p) != 2 {
						t.Fatal("QQ proactive text contract")
					}
				case "line":
					var p map[string]any
					json.NewDecoder(request.Body).Decode(&p)
					if request.URL.Host != "api.line.me" || request.URL.Path != "/v2/bot/message/push" || request.Header.Get("X-Line-Retry-Key") != message.RetryKey || request.Header.Get("Authorization") != "Bearer "+sender.credential.ChannelAccessToken || p["to"] != sender.credential.TargetID || len(p["messages"].([]any)) != 1 {
						t.Fatal("LINE push contract")
					}
				case "twilio_sms":
					request.ParseForm()
					username, password, ok := request.BasicAuth()
					if request.URL.Host != "api.twilio.com" || !ok || username != sender.credential.APIKeySID || password != sender.credential.APIKeySecret || request.Form.Get("To") != sender.credential.To || request.Form.Get("Body") != message.Body || request.Form.Get("SmartEncoded") != "false" || request.Form.Get("SendAsMms") != "false" || request.Form.Get("StatusCallback") != "" || request.Form.Get("From") != sender.credential.From || request.Form.Get("MessagingServiceSid") != "" {
						t.Fatal("Twilio bounded SMS contract")
					}
				case "whatsapp_cloud":
					var p map[string]any
					json.NewDecoder(request.Body).Decode(&p)
					template := p["template"].(map[string]any)
					component := template["components"].([]any)[0].(map[string]any)
					if request.URL.Host != "graph.facebook.com" || request.URL.Path != "/v26.0/"+sender.credential.PhoneNumberID+"/messages" || request.Header.Get("Authorization") != "Bearer "+sender.credential.AccessToken || p["messaging_product"] != "whatsapp" || p["type"] != "template" || p["to"] != "+"+sender.credential.Recipient || template["name"] != "noderampart_test" || component["type"] != "body" || len(component["parameters"].([]any)) != 6 || p["text"] != nil {
						t.Fatal("approved BODY text template contract")
					}
				}
				status, body := officialSuccess(sender, message)
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
			})
			out, err := sender.Dispatch(context.Background(), message)
			if err != nil || out.State != DispatchAccepted || out.ProviderID == "" || requests != 1 {
				t.Fatalf("explicit acceptance failed: %s %v", out.State, err)
			}
		})
	}
}

func TestOfficialResponseMatrix(t *testing.T) {
	for _, channel := range config.OfficialChannelNames() {
		t.Run(channel, func(t *testing.T) {
			sender, message := officialFixture(t, channel)
			successStatus, successBody := officialSuccess(sender, message)
			for _, body := range []string{"", `{}`, `null`, `[]`, `{"id":null}`, `{"id":"first","id":"second"}`, `{"messages":null,"sentMessages":null}`, "synthetic-secret-response", strings.Repeat("x", maxTelegramResponseBytes+1), successBody + " trailing"} {
				officialMock(sender, successStatus, body, "")
				out, err := sender.Dispatch(context.Background(), message)
				if out.State == DispatchAccepted || err != nil && strings.Contains(err.Error(), "synthetic-secret-response") {
					t.Fatal("unconfirmed response accepted or leaked")
				}
			}
			for _, status := range []int{301, 302, 307, 400, 401, 403, 404, 408, 429, 500, 503} {
				officialMock(sender, status, successBody, "3600")
				out, _ := sender.Dispatch(context.Background(), message)
				if out.State == DispatchAccepted {
					t.Fatal("wrong HTTP status accepted")
				}
				if status == 429 && (out.State != DispatchNotAccepted || out.RetryAfterDuration != time.Hour) {
					t.Fatal("rate limit classification or wait lost")
				}
				if status >= 500 && out.State != DispatchUnknown {
					t.Fatal("POST ambiguity must be preserved")
				}
			}
			officialMock(sender, 429, `{"code":21610,"error":{"code":10},"err_code":40054013}`, "999999999999999999999999")
			out, _ := sender.Dispatch(context.Background(), message)
			if !out.Suspend {
				t.Fatal("unrepresentable cooldown must suspend")
			}
		})
	}
}

func TestOfficialDurableAndSnapshotGuards(t *testing.T) {
	for _, channel := range config.OfficialChannelNames() {
		t.Run(channel, func(t *testing.T) {
			sender, message := officialFixture(t, channel)
			var calls int
			sender.client.Transport = nativeRoundTrip(func(*http.Request) (*http.Response, error) {
				calls++
				return nil, errors.New("synthetic transport failure")
			})
			if sender.Send(context.Background(), message.Body) == nil || sender.SendMessage(context.Background(), message) == nil {
				t.Fatal("direct sender bypass")
			}
			for _, change := range []func(*store.OutboxMessage){func(m *store.OutboxMessage) { m.DispatchState = "prepared" }, func(m *store.OutboxMessage) { m.DispatchAttempt = 0 }, func(m *store.OutboxMessage) { m.FirstAttemptAt = time.Time{} }, func(m *store.OutboxMessage) { m.ID = "" }, func(m *store.OutboxMessage) { m.Destination = "other:identity" }, func(m *store.OutboxMessage) { m.FrozenPayload = `{}` }, func(m *store.OutboxMessage) { m.LogicalKind = "daily" }, func(m *store.OutboxMessage) { m.Body = "changed body" }} {
				if channel == "whatsapp_cloud" { // Its API payload is the frozen template, not the display body.
					copy := message
					change(&copy)
					if copy.Body != message.Body {
						continue
					}
				}
				copy := message
				change(&copy)
				out, err := sender.Dispatch(context.Background(), copy)
				if err == nil || out.State != DispatchRejected || calls != 0 {
					t.Fatal("invalid durable request reached transport")
				}
			}
			for _, secret := range []string{sender.credential.AppSecret, sender.credential.ChannelAccessToken, sender.credential.APIKeySecret, sender.credential.AccessToken, sender.credential.TargetID, sender.credential.To, sender.credential.Recipient} {
				if secret != "" && strings.Contains(message.FrozenPayload, secret) {
					t.Fatal("credential or recipient persisted")
				}
			}
			if strings.Contains(sender.RecipientPreview(), sender.credential.To) && sender.credential.To != "" {
				t.Fatal("preview exposed phone")
			}
		})
	}
}

func TestOfficialLINEKeyAndReceipt(t *testing.T) {
	sender, message := officialFixture(t, "line")
	for _, elapsed := range []time.Duration{-time.Second, 24 * time.Hour, 48 * time.Hour} {
		copy := message
		copy.FirstAttemptAt = sender.now().Add(-elapsed)
		var calls int
		sender.client.Transport = nativeRoundTrip(func(*http.Request) (*http.Response, error) { calls++; return nil, errors.New("must not call") })
		out, err := sender.Dispatch(context.Background(), copy)
		reason := "retry_window_expired"
		if elapsed < 0 {
			reason = "clock_rollback"
		}
		if err == nil || out.Reason != reason || calls != 0 || elapsed < 0 && !out.Suspend {
			t.Fatal("LINE retry window bypass")
		}
	}
	message.RetryKey = "invalid"
	out, err := sender.Dispatch(context.Background(), message)
	if err == nil || out.State != DispatchRejected {
		t.Fatal("invalid retry key")
	}
	message.RetryKey = "00000000-0000-4000-8000-000000000000"
	for _, header := range []string{"", "bad\nheader", "00000000-0000-4000-8000-000000000000"} {
		sender.client.Transport = nativeRoundTrip(func(request *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 409, Header: http.Header{"X-Line-Accepted-Request-Id": []string{header}}, Body: io.NopCloser(strings.NewReader(`{"message":"The retry key is already accepted"}`)), Request: request}, nil
		})
		out, _ := sender.Dispatch(context.Background(), message)
		if (out.State == DispatchAccepted) != (header == "00000000-0000-4000-8000-000000000000") {
			t.Fatal("409 needs explicit prior acceptance receipt")
		}
	}
	for _, body := range []string{`{"sentMessages":[{"id":12345678901234567890}]}`, `{"sentMessages":[{"id":"12345678901234567890"}]}`} {
		officialMock(sender, 200, body, "")
		out, _ := sender.Dispatch(context.Background(), message)
		if out.State != DispatchAccepted || out.ProviderID != "12345678901234567890" {
			t.Fatal("LINE message ID must not lose precision")
		}
	}
}

func TestOfficialQQTokenSingleFlightAndBusinessCodes(t *testing.T) {
	sender, message := officialFixture(t, "qqbot")
	var tokens atomic.Int32
	sender.client.Transport = nativeRoundTrip(func(request *http.Request) (*http.Response, error) {
		body := `{"id":"synthetic_ack","timestamp":"2026-10-02T12:00:00Z"}`
		if request.URL.Path == "/app/getAppAccessToken" {
			tokens.Add(1)
			body = `{"access_token":"synthetic-qq-token","expires_in":7200}`
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})
	var wait sync.WaitGroup
	for range 24 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			out, err := sender.Dispatch(context.Background(), message)
			if err != nil || out.State != DispatchAccepted {
				t.Errorf("parallel token dispatch: %s %v", out.State, err)
			}
		}()
	}
	wait.Wait()
	if tokens.Load() != 1 {
		t.Fatal("QQ token refresh was not single-flight")
	}
	sender.now = func() time.Time { return message.FirstAttemptAt.Add(119 * time.Minute) }
	out, err := sender.Dispatch(context.Background(), message)
	if err != nil || out.State != DispatchAccepted || tokens.Load() != 2 {
		t.Fatal("QQ expiry refresh did not use new request")
	}
	for _, test := range []struct{ body, state string }{{`{"err_code":40034100}`, DispatchNotAccepted}, {`{"err_code":40054013}`, DispatchOptedOut}, {`{"err_code":40034105}`, DispatchRejected}, {`{"code":50055001}`, DispatchUnknown}, {`{"err_code":null}`, DispatchUnknown}, {`{"err_code":0,"code":1}`, DispatchUnknown}} {
		officialMock(sender, 200, test.body, "")
		out, _ := sender.Dispatch(context.Background(), message)
		if out.State != test.state {
			t.Fatalf("QQ business state got %s want %s", out.State, test.state)
		}
	}
}

func TestOfficialTwilioPollAndAccounting(t *testing.T) {
	sender, message := officialFixture(t, "twilio_sms")
	status, body := officialSuccess(sender, message)
	officialMock(sender, status, body, "")
	out, err := sender.Dispatch(context.Background(), message)
	if err != nil || out.PlatformSegments == nil || *out.PlatformSegments != 1 || out.Price != nil || out.PollAfterDuration <= 0 {
		t.Fatal("Twilio queue acceptance or unknown price lost")
	}
	message.DispatchState = "accepted"
	message.ProviderID = out.ProviderID
	message.PollAttempts = 1
	message.PollDeadline = sender.now().Add(24 * time.Hour)
	for _, state := range []string{"accepted", "queued", "sending", "sent", "delivered", "failed", "undelivered", "canceled"} {
		var response map[string]any
		json.Unmarshal([]byte(body), &response)
		response["status"] = state
		response["price"] = "-0.0075"
		response["price_unit"] = "usd"
		response["num_segments"] = "0"
		encoded, _ := json.Marshal(response)
		var called bool
		sender.client.Transport = nativeRoundTrip(func(request *http.Request) (*http.Response, error) {
			called = true
			if request.Method != http.MethodGet || request.URL.Path != "/2010-04-01/Accounts/"+sender.credential.AccountSID+"/Messages/"+message.ProviderID+".json" {
				t.Fatal("poll must GET only the known receipt")
			}
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(encoded))), Request: request}, nil
		})
		out, err := sender.Poll(context.Background(), message)
		if err != nil || !called || out.ProviderState != state || out.Price == nil || *out.Price != "-0.0075" || out.PriceUnit != "USD" || out.PlatformSegments == nil || *out.PlatformSegments != 0 {
			t.Fatal("poll state or typed accounting lost")
		}
	}
	for _, change := range []func(*store.OutboxMessage){func(m *store.OutboxMessage) { m.ProviderID = "https://private.invalid/" }, func(m *store.OutboxMessage) { m.PollAttempts = 9 }, func(m *store.OutboxMessage) { m.PollDeadline = sender.now() }, func(m *store.OutboxMessage) { m.DispatchState = "prepared" }} {
		copy := message
		change(&copy)
		out, err := sender.Poll(context.Background(), copy)
		if err == nil || out.State != DispatchRejected {
			t.Fatal("invalid poll intent")
		}
	}
	officialMock(sender, 400, `{"code":21610,"message":"synthetic private recipient"}`, "")
	message.DispatchState = "in_flight"
	out, _ = sender.Dispatch(context.Background(), message)
	if out.State != DispatchOptedOut || out.APIErrorCode != 21610 {
		t.Fatal("Twilio opt out must lock subsequent admission")
	}
}

func TestOfficialSMSCountsAndMandatoryRejection(t *testing.T) {
	for _, test := range []struct {
		body, encoding string
		segments       int
	}{{strings.Repeat("a", 160), "gsm7", 1}, {strings.Repeat("a", 161), "gsm7", 2}, {strings.Repeat("^", 80), "gsm7", 1}, {strings.Repeat("^", 81), "gsm7", 2}, {strings.Repeat("中", 70), "ucs2", 1}, {strings.Repeat("中", 71), "ucs2", 2}, {strings.Repeat("😀", 35), "ucs2", 1}, {strings.Repeat("😀", 36), "ucs2", 2}, {strings.Repeat("a", 305), "gsm7", 3}, {strings.Repeat("中", 133), "ucs2", 3}} {
		encoding, n, err := EstimateSMS(test.body)
		if err != nil || encoding != test.encoding || n != test.segments {
			t.Fatalf("encoding/count %s %d %v", encoding, n, err)
		}
	}
	_, semantic := FormatOfficialTest(strings.Repeat("x", 80), "en")
	body, _, n, err := RenderOfficialSMS(semantic, "en", 1)
	if err != nil || n != 1 || !strings.Contains(body, "~") || !strings.Contains(body, "Reply STOP") || !strings.Contains(body, "Coverage unknown") || !strings.Contains(body, "sudo noderampart notify list") {
		t.Fatal("only host may shorten; mandatory fields must survive")
	}
	semantic.BoundedSummary = strings.Repeat("core=123 ", 80)
	if _, _, _, err := RenderOfficialSMS(semantic, "en", 2); err == nil {
		t.Fatal("overlong core metrics silently truncated")
	}
}

func TestOfficialCancelAndSecretFailures(t *testing.T) {
	for _, channel := range config.OfficialChannelNames() {
		t.Run(channel, func(t *testing.T) {
			sender, message := officialFixture(t, channel)
			sender.client.Transport = nativeRoundTrip(func(request *http.Request) (*http.Response, error) {
				<-request.Context().Done()
				return nil, errors.New("synthetic-secret-token https://private.invalid/")
			})
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			out, err := sender.Dispatch(ctx, message)
			if err == nil || out.State == DispatchAccepted || strings.Contains(err.Error(), "synthetic-secret-token") || strings.Contains(err.Error(), "private.invalid") {
				t.Fatal("context cancellation or safe error boundary")
			}
		})
	}
}
