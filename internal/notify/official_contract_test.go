// SPDX-License-Identifier: MIT

package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/model"
)

func TestOfficialCredentialGenerationAndSelectionIdentity(t *testing.T) {
	for _, channel := range config.OfficialChannelNames() {
		t.Run(channel, func(t *testing.T) {
			sender, _ := officialFixture(t, channel)
			for _, change := range []func(*config.OfficialChannelConfig){func(c *config.OfficialChannelConfig) { c.Enabled = false }, func(c *config.OfficialChannelConfig) { c.Timeout = config.Duration{time.Second} }, func(c *config.OfficialChannelConfig) { c.DailyMessageLimit++ }, func(c *config.OfficialChannelConfig) {
				if channel == "twilio_sms" {
					c.DailySegmentLimit++
				}
			}, func(c *config.OfficialChannelConfig) { c.Subscription.NotificationTypes = []string{"test", "event"} }} {
				cfg := sender.cfg
				change(&cfg)
				other, err := NewOfficial(channel, cfg)
				if err != nil || other.Destination() != sender.Destination() {
					t.Fatal("same target pause/limit/order changed identity")
				}
			}
			if channel != "whatsapp_cloud" {
				cfg := sender.cfg
				cfg.Language = "zh"
				other, err := NewOfficial(channel, cfg)
				if err != nil || other.Destination() != sender.Destination() {
					t.Fatal("language changed target identity")
				}
			}
			for _, change := range []func(*config.OfficialChannelConfig){func(c *config.OfficialChannelConfig) { c.EventsEnabled = false }, func(c *config.OfficialChannelConfig) { c.MinSeverity = "critical" }, func(c *config.OfficialChannelConfig) { c.Subscription.BasisID = strings.Repeat("a", 32) }, func(c *config.OfficialChannelConfig) {
				c.Subscription.NotificationTypes = []string{"test"}
				c.EventsEnabled = false
			}} {
				cfg := sender.cfg
				change(&cfg)
				other, err := NewOfficial(channel, cfg)
				if err != nil || other.Destination() == sender.Destination() {
					t.Fatal("selection/consent change retained old delivery identity")
				}
			}
			if channel == "twilio_sms" {
				cfg := sender.cfg
				cfg.MaxSegments = 1
				other, err := NewOfficial(channel, cfg)
				if err != nil || other.Destination() == sender.Destination() {
					t.Fatal("segment policy did not isolate old queued requests")
				}
			}
			data, err := os.ReadFile(sender.cfg.CredentialFile)
			if err != nil {
				t.Fatal(err)
			}
			path := sender.cfg.CredentialFile
			replacement := filepath.Join(filepath.Dir(path), "replacement.json")
			if err := os.WriteFile(replacement, data, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(replacement, path); err != nil {
				t.Fatal(err)
			}
			other, err := NewOfficial(channel, sender.cfg)
			if err != nil || other.Destination() == sender.Destination() {
				t.Fatal("replacement generation resurrected old target")
			}
		})
	}
}

func TestOfficialProtectedCredentialsAndDirectTransport(t *testing.T) {
	sender, _ := officialFixture(t, "qqbot")
	transport, ok := sender.client.Transport.(*http.Transport)
	if !ok || transport.Proxy != nil || transport.MaxConnsPerHost != 1 || transport.MaxResponseHeaderBytes != 16<<10 || transport.TLSClientConfig != nil && transport.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("official HTTPS resource/security contract")
	}
	if err := sender.client.CheckRedirect(&http.Request{}, nil); err != http.ErrUseLastResponse {
		t.Fatal("redirect following allowed")
	}
	path := sender.cfg.CredentialFile
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if ValidateOfficialCredentialFile("qqbot", path) == nil {
		t.Fatal("world-readable credential accepted")
	}
	os.Chmod(path, 0600)
	link := filepath.Join(filepath.Dir(path), "symlink.json")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if ValidateOfficialCredentialFile("qqbot", link) == nil {
		t.Fatal("symlink accepted")
	}
	hard := filepath.Join(filepath.Dir(path), "hardlink.json")
	if err := os.Link(path, hard); err != nil {
		t.Fatal(err)
	}
	if ValidateOfficialCredentialFile("qqbot", path) == nil {
		t.Fatal("hard-linked secret accepted")
	}
	os.Remove(hard)
	for _, body := range []string{`{"channel":"qqbot","channel":"qqbot"}`, `{"channel":"qqbot","app_id":null}`, strings.Repeat("x", config.MaxOfficialCredentialBytes+1)} {
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if ValidateOfficialCredentialFile("qqbot", path) == nil {
			t.Fatal("invalid protected credential accepted")
		}
	}
}

func TestOfficialQQTokenContractMatrix(t *testing.T) {
	for _, body := range []string{`{}`, `{"access_token":"token"}`, `{"access_token":null,"expires_in":7200}`, `{"access_token":"token","expires_in":null}`, `{"access_token":"token","expires_in":0}`, `{"access_token":"token","expires_in":7201}`, `{"access_token":"token","expires_in":7200.5}`, `{"access_token":"token","expires_in":"07200"}`, `{"access_token":"token","expires_in":7200,"expires_in":1}`, `{"access_token":"bad\r\ntoken","expires_in":7200}`, `{"code":"0","access_token":"token","expires_in":7200}`} {
		sender, message := officialFixture(t, "qqbot")
		var posts int
		sender.client.Transport = nativeRoundTrip(func(request *http.Request) (*http.Response, error) {
			if request.URL.Path != "/app/getAppAccessToken" {
				posts++
			}
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
		})
		out, _ := sender.Dispatch(context.Background(), message)
		if out.State == DispatchAccepted || posts != 0 {
			t.Fatal("invalid token receipt submitted message")
		}
	}
	for _, test := range []struct{ body, state string }{{`{"code":100001}`, DispatchNotAccepted}, {`{"code":100007}`, DispatchRejected}, {`{"code":100016}`, DispatchRejected}, {`{"code":10004}`, DispatchRejected}} {
		sender, message := officialFixture(t, "qqbot")
		sender.client.Transport = nativeRoundTrip(func(request *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(test.body)), Request: request}, nil
		})
		out, _ := sender.Dispatch(context.Background(), message)
		if out.State != test.state {
			t.Fatal("QQ token business code classification")
		}
	}
}

func TestOfficialReceiptCannotEchoProtectedData(t *testing.T) {
	for _, channel := range []string{"qqbot", "line", "whatsapp_cloud"} {
		t.Run(channel, func(t *testing.T) {
			sender, message := officialFixture(t, channel)
			var body string
			switch channel {
			case "qqbot":
				body = `{"id":"` + sender.credential.AppSecret + `","timestamp":"2026-10-02T12:00:00Z"}`
			case "line":
				sender.credential.ChannelAccessToken = "12345678901234567890"
				body = `{"sentMessages":[{"id":"12345678901234567890"}]}`
			case "whatsapp_cloud":
				body = `{"messaging_product":"whatsapp","messages":[{"id":"wamid.` + sender.credential.AccessToken + `"}]}`
			}
			officialMock(sender, 200, body, "")
			out, _ := sender.Dispatch(context.Background(), message)
			if out.State != DispatchUnknown || out.ProviderID != "" {
				t.Fatal("response secret entered persisted receipt")
			}
		})
	}
}

func TestOfficialWhatsAppBusinessAndFrozenTemplate(t *testing.T) {
	sender, message := officialFixture(t, "whatsapp_cloud")
	for _, test := range []struct {
		code    int
		state   string
		suspend bool
	}{{0, DispatchRejected, true}, {3, DispatchRejected, true}, {10, DispatchRejected, true}, {190, DispatchRejected, true}, {200, DispatchRejected, true}, {4, DispatchNotAccepted, false}, {80007, DispatchNotAccepted, false}, {130429, DispatchNotAccepted, false}, {131056, DispatchNotAccepted, false}, {131048, DispatchNotAccepted, true}, {131064, DispatchNotAccepted, true}, {132000, DispatchRejected, false}, {132001, DispatchRejected, true}, {132005, DispatchRejected, false}, {132012, DispatchRejected, false}, {132015, DispatchRejected, true}, {132016, DispatchRejected, true}, {1, DispatchUnknown, false}, {2, DispatchUnknown, false}, {131000, DispatchUnknown, false}, {131016, DispatchUnknown, false}} {
		body, _ := json.Marshal(map[string]any{"error": map[string]any{"code": test.code, "message": "synthetic untrusted private detail"}})
		officialMock(sender, 400, string(body), "3600")
		out, _ := sender.Dispatch(context.Background(), message)
		if out.State != test.state || out.Suspend != test.suspend || out.APIErrorCode != test.code || out.RetryAfterDuration != time.Hour {
			t.Fatalf("Meta code %d classification: %s", test.code, out.State)
		}
	}
	for _, body := range []string{`{"error":{}}`, `{"error":{"code":null}}`, `{"error":{"code":"0"}}`, `{"error":{"code":190,"code":0}}`, `{"messaging_product":"whatsapp","messages":[{"id":"wamid.valid","message_status":"delivered"}]}`} {
		officialMock(sender, 200, body, "")
		out, _ := sender.Dispatch(context.Background(), message)
		if out.State == DispatchAccepted {
			t.Fatal("missing/malformed business field accepted")
		}
	}
	old := message.FrozenPayload
	sender.cfg.Language = "zh"
	message.SemanticPayload = `{"bad":"new translation"}`
	officialMock(sender, 200, `{"messaging_product":"whatsapp","messages":[{"id":"wamid.synthetic_ack"}]}`, "")
	out, err := sender.Dispatch(context.Background(), message)
	if err != nil || out.State != DispatchAccepted || message.FrozenPayload != old {
		t.Fatal("old template was rerendered on retry")
	}
	var frozen officialFrozen
	json.Unmarshal([]byte(old), &frozen)
	frozen.Template.Name = "unapproved_replacement"
	encoded, _ := json.Marshal(frozen)
	message.FrozenPayload = string(encoded)
	out, err = sender.Dispatch(context.Background(), message)
	if err == nil || out.State != DispatchRejected {
		t.Fatal("template mapping bypass")
	}
}

func TestOfficialTwilioTTLAndMalformedAccounting(t *testing.T) {
	sender, message := officialFixture(t, "twilio_sms")
	_, body := officialSuccess(sender, message)
	for _, remaining := range []time.Duration{time.Second, 119 * time.Second, 999 * time.Millisecond} {
		copy := message
		copy.ExpiresAt = sender.now().Add(remaining)
		var calls int
		sender.client.Transport = nativeRoundTrip(func(request *http.Request) (*http.Response, error) {
			calls++
			request.ParseForm()
			if request.Form.Get("ValidityPeriod") != strconv.FormatInt(int64(remaining/time.Second), 10) {
				t.Fatal("provider validity exceeds local lifetime")
			}
			return &http.Response{StatusCode: 201, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
		})
		out, err := sender.Dispatch(context.Background(), copy)
		if remaining < time.Second {
			if err == nil || out.State != DispatchRejected || calls != 0 {
				t.Fatal("subsecond-expiry paid request")
			}
		} else if err != nil || out.State != DispatchAccepted || calls != 1 {
			t.Fatal("bounded validity rejected")
		}
	}
	for _, change := range []func(map[string]any){func(p map[string]any) { p["num_segments"] = 1 }, func(p map[string]any) { p["num_segments"] = "-1" }, func(p map[string]any) { p["num_segments"] = "101" }, func(p map[string]any) { p["price"] = 0.0075 }, func(p map[string]any) { p["price"] = "-0.0075"; p["price_unit"] = nil }, func(p map[string]any) { p["error_code"] = "21610" }, func(p map[string]any) { p["account_sid"] = "AC" + strings.Repeat("b", 32) }, func(p map[string]any) { p["to"] = "+15555559999" }, func(p map[string]any) { p["status"] = "received" }} {
		var object map[string]any
		json.Unmarshal([]byte(body), &object)
		change(object)
		encoded, _ := json.Marshal(object)
		officialMock(sender, 201, string(encoded), "")
		out, _ := sender.Dispatch(context.Background(), message)
		if out.State != DispatchUnknown {
			t.Fatal("malformed accounting or foreign receipt accepted")
		}
	}
}

func TestOfficialEventLanguageCategoriesAndTime(t *testing.T) {
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	for kind, title := range eventTitles {
		for _, language := range []string{"en", "zh"} {
			event := model.Event{Kind: kind, Severity: model.SeverityCritical, Phase: "recovery", ObservedAt: time.Date(2026, 11, 1, 6, 30, 0, 0, time.UTC), Count: 7, Evidence: map[string]string{"observed_rate": "150", "threshold_rate": "100", "coverage": "incomplete", "reason": "sensor_stale"}}
			body, semantic := FormatOfficialEvent("test\x1bhost@everyone", event, language, location)
			if !strings.Contains(body, title.local(language == "zh")) || !strings.Contains(body, "2026-11-01 01:30-0500") || !strings.Contains(body, localText(language == "zh", "recovery", "恢复")) || semantic.Coverage != localText(language == "zh", "Coverage incomplete", "覆盖不足") || strings.ContainsAny(body, "\x1b") || strings.Contains(body, "@everyone") {
				t.Fatal("localized category/time/core coverage missing")
			}
			if _, err := decodeOfficialSemantic(mustOfficialJSON(t, semantic)); err != nil {
				t.Fatal("renderer generated invalid semantics", err)
			}
		}
	}
}

func mustOfficialJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestOfficialLINEUTF16AndTemplateLiteralCoverage(t *testing.T) {
	if officialUTF16Units(strings.Repeat("😀", 2500)) != 5000 || officialUTF16Units(strings.Repeat("😀", 2501)) != 5002 || officialUTF16Units("a中😀") != 4 {
		t.Fatal("LINE text limits must count UTF-16 units")
	}
	sender, message := officialFixture(t, "whatsapp_cloud")
	_, semantic := FormatOfficialTest("test-host", "en")
	semantic.BoundedSummary = "core=*fake* _spoof_ ~del~ `code`"
	semantic.Coverage = "Coverage incomplete"
	message.SemanticPayload = mustOfficialJSON(t, semantic)
	if err := sender.PrepareMessage(&message); err != nil {
		t.Fatal(err)
	}
	var frozen officialFrozen
	if json.Unmarshal([]byte(message.FrozenPayload), &frozen) != nil {
		t.Fatal("frozen template missing")
	}
	for i, slot := range frozen.Template.Slots {
		if slot == "bounded_summary" {
			if !strings.Contains(frozen.Template.Values[i], "Coverage incomplete") || strings.ContainsAny(frozen.Template.Values[i], "*_~`") {
				t.Fatal("template dropped coverage or allowed dynamic formatting")
			}
		}
	}
}

func TestOfficialKnownAuthenticationSuspendsOnlyTarget(t *testing.T) {
	for _, channel := range config.OfficialChannelNames() {
		sender, message := officialFixture(t, channel)
		for _, status := range []int{401, 403} {
			officialMock(sender, status, `{"error":{"code":2},"err_code":50055001}`, "")
			out, _ := sender.Dispatch(context.Background(), message)
			if out.State != DispatchRejected || !out.Suspend || out.Reason != "credential_or_permission_rejected" {
				t.Fatal("HTTP authentication failure was retried as an unknown payload")
			}
		}
	}
}

func TestOfficialWhatsAppPairCooldownAndBound(t *testing.T) {
	sender, message := officialFixture(t, "whatsapp_cloud")
	if sender.MinimumInterval() != 6*time.Second {
		t.Fatal("official same-recipient minimum interval")
	}
	for _, attempt := range []int{1, 4, 5, 8, 10, 1000000000} {
		copy := message
		copy.DispatchAttempt = attempt
		officialMock(sender, 400, `{"error":{"code":131056}}`, "")
		out, _ := sender.Dispatch(context.Background(), copy)
		delay, suspend := officialWhatsAppPairDelay(attempt)
		if out.Suspend != suspend || !suspend && (out.RetryAfterDuration < time.Minute || out.RetryAfterDuration < delay) {
			t.Fatal("pair backoff was shortened or not bounded")
		}
	}
}

func TestOfficialPreparationRejectsSecretsAndUnsafeSemantics(t *testing.T) {
	for _, channel := range config.OfficialChannelNames() {
		sender, message := officialFixture(t, channel)
		copy := message
		secret := ""
		switch channel {
		case "qqbot":
			secret = sender.credential.AppSecret
		case "line":
			secret = sender.credential.ChannelAccessToken
		case "twilio_sms":
			secret = sender.credential.APIKeySecret
		case "whatsapp_cloud":
			secret = sender.credential.AccessToken
		}
		if channel == "qqbot" || channel == "line" {
			copy.Body = "unsafe " + secret
		} else {
			_, semantic := FormatOfficialTest("test-host", "en")
			semantic.BoundedSummary = "unsafe " + secret
			copy.SemanticPayload = mustOfficialJSON(t, semantic)
		}
		if sender.PrepareMessage(&copy) == nil {
			t.Fatal("known credential entered a queued body")
		}
	}
	sender, message := officialFixture(t, "twilio_sms")
	for _, value := range []string{`{}`, `{"host_alias":"ok","host_alias":"evil"}`, strings.Repeat("x", 4097)} {
		copy := message
		copy.SemanticPayload = value
		if sender.PrepareMessage(&copy) == nil {
			t.Fatal("invalid semantic object accepted")
		}
	}
	_, semantic := FormatOfficialTest("test-host", "en")
	semantic.BoundedSummary = "unsafe\u202econtrol"
	message.SemanticPayload = mustOfficialJSON(t, semantic)
	if sender.PrepareMessage(&message) == nil {
		t.Fatal("bidirectional control accepted")
	}
}
