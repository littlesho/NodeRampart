// SPDX-License-Identifier: MIT

package console

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/notify"
	"github.com/littlesho/NodeRampart/internal/store"
)

func TestOfficialActionsOfferIndividualHiddenInputsAndBilingualHelp(t *testing.T) {
	for _, channel := range config.OfficialChannelNames() {
		for _, operation := range []string{"setup", "credentials", "subscription"} {
			a, ok := findAction(channel + "_" + operation)
			if !ok || !a.mutation || a.helpEN == "" || a.helpZH == "" || a.confirmEN == "" || a.confirmZH == "" || len(a.params) > 16 {
				t.Fatal("incomplete action", channel, operation)
			}
			if operation == "credentials" {
				for _, name := range config.OfficialCredentialFieldNames(channel) {
					found := false
					for _, p := range a.params {
						if p.key == name {
							found = p.secret && p.en != "" && p.zh != ""
						}
					}
					if !found {
						t.Fatal("missing hidden field", channel, name)
					}
				}
			}
		}
	}
	c := config.Defaults()
	path := "notifications.twilio_sms.subscription.basis_id"
	if setField(&c, path, "ordinary-config-override") == nil {
		t.Fatal("ordinary field edit changed consent identity")
	}
	if setField(&c, path, fieldText(&c, path)) != nil {
		t.Fatal("read-only round trip rejected")
	}
}

func TestPaidPreviewRequiresPositiveProtocolAndBoundedFields(t *testing.T) {
	base := map[string]any{"channel": "twilio_sms", "body": "Synthetic alert. Local reference: nr-test", "language": "en", "estimated_segments": 1, "encoding": "gsm7", "cost": "unknown", "daily_message_limit": 20, "daily_segment_limit": 40, "preview_id": strings.Repeat("0", 64), "network_sent": false, "budget": officialPreviewBudget("twilio_sms"), "next_reset_utc": "2026-10-03T00:00:00Z", "target": "+1******0102", "frozen_request": map[string]string{"channel": "twilio_sms", "kind": "test", "fingerprint": strings.Repeat("0", 64), "body": "Synthetic alert. Local reference: nr-test"}}
	data, _ := json.Marshal(base)
	for _, lang := range []string{"en", "zh"} {
		text, id, err := parsePaidPreview(string(data), "twilio_sms", lang)
		if err != nil || id != base["preview_id"] || !strings.Contains(text, base["body"].(string)) {
			t.Fatal("valid preview discarded", err)
		}
		if !strings.Contains(text, "40") || !strings.Contains(text, "17") || !strings.Contains(text, "36") || !strings.Contains(text, "2026-10-03T00:00:00Z") || !strings.Contains(text, "GSM-7") || lang == "zh" && !strings.Contains(text, "发送可能收费") {
			t.Fatal("paid preview incomplete localization")
		}
	}
	for _, key := range []string{"channel", "preview_id", "network_sent", "daily_message_limit", "estimated_segments", "daily_segment_limit", "target", "budget", "next_reset_utc"} {
		bad := map[string]any{}
		for k, v := range base {
			bad[k] = v
		}
		delete(bad, key)
		data, _ := json.Marshal(bad)
		if _, _, err := parsePaidPreview(string(data), "twilio_sms", "en"); err == nil {
			t.Fatal("missing required preview field", key)
		}
	}
	for key, value := range map[string]any{"network_sent": true, "estimated_segments": 3, "daily_message_limit": 1001, "preview_id": "malformed", "body": strings.Repeat("x", 8193), "encoding": "unknown", "next_reset_utc": "2026-10-03T01:00:00Z"} {
		bad := map[string]any{}
		for k, v := range base {
			bad[k] = v
		}
		bad[key] = value
		data, _ := json.Marshal(bad)
		if _, _, err := parsePaidPreview(string(data), "twilio_sms", "en"); err == nil {
			t.Fatal("invalid preview accepted", key)
		}
	}
	for _, key := range []string{"channel", "logical_messages_reserved", "estimated_segments_reserved", "restore_reconciliation_required", "opted_out"} {
		budget := officialPreviewBudget("twilio_sms")
		delete(budget, key)
		bad := map[string]any{}
		for k, v := range base {
			bad[k] = v
		}
		bad["budget"] = budget
		data, _ := json.Marshal(bad)
		if _, _, err := parsePaidPreview(string(data), "twilio_sms", "en"); err == nil {
			t.Fatal("missing budget observation mistaken for zero", key)
		}
	}
}

func TestSimulationOfficialHiddenFieldCancellationDoesNotSaveOrSend(t *testing.T) {
	b := newBackend()
	s, _, _ := launch(t, false, b)
	awaitFrame(t, s, "Main menu")
	selectIndex(s, 5)
	awaitFrame(t, s, "NodeRampart — Notification channels")
	selectIndex(s, notificationActionIndex(t, "qqbot_credentials"))
	awaitFrame(t, s, "App ID (hidden; blank retains)")
	key(s, tcell.KeyTab)
	textKeys(s, "SYNTHETIC_PRIVATE_APP_ID")
	key(s, tcell.KeyTab)
	textKeys(s, "SYNTHETIC_PRIVATE_SECRET")
	frame := awaitFrame(t, s, "App secret (hidden; blank retains)")
	if strings.Contains(frame, "SYNTHETIC_PRIVATE_APP_ID") || strings.Contains(frame, "SYNTHETIC_PRIVATE_SECRET") {
		t.Fatal("hidden field visible")
	}
	key(s, tcell.KeyEscape)
	awaitFrame(t, s, "NodeRampart — Notification channels")
	if len(b.calls) != 0 || len(b.saves) != 0 {
		t.Fatal("cancel triggered action")
	}
}

func paidPreviewFixture() string {
	body := "Synthetic alert; noderampart report now"
	data, _ := json.Marshal(map[string]any{"channel": "twilio_sms", "body": body, "language": "en", "estimated_segments": 1, "encoding": "gsm7", "cost": "unknown", "daily_message_limit": 20, "daily_segment_limit": 40, "preview_id": strings.Repeat("0", 64), "network_sent": false, "budget": officialPreviewBudget("twilio_sms"), "next_reset_utc": "2026-10-03T00:00:00Z", "target": "+1******0102", "frozen_request": map[string]string{"channel": "twilio_sms", "kind": "test", "fingerprint": strings.Repeat("0", 64), "body": body}})
	return string(data)
}

func openTwilioTest(t *testing.T, s *recordedScreen) {
	t.Helper()
	awaitFrame(t, s, "Main menu")
	selectIndex(s, 5)
	awaitFrame(t, s, "NodeRampart — Notification channels")
	selectIndex(s, notificationActionIndex(t, "notify_test"))
	awaitFrame(t, s, "Send a test notification")
	key(s, tcell.KeyEnter)
	for range 10 {
		key(s, tcell.KeyDown)
	}
	key(s, tcell.KeyEnter)
	key(s, tcell.KeyTab)
	key(s, tcell.KeyEnter)
}

func TestSimulationPaidTestPreviewCancelAndSeparateChargeConfirmation(t *testing.T) {
	for _, confirm := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "confirm"}[confirm], func(t *testing.T) {
			b := newBackend()
			b.action = func(_ context.Context, id string, args map[string]string) (string, error) {
				if id == "notify_preview" {
					if args["channel"] != "twilio_sms" {
						t.Error("wrong preview target")
					}
					return paidPreviewFixture(), nil
				}
				if id != "notify_test" || args["channel"] != "twilio_sms" || args["confirm_paid"] != "yes" || args["preview_id"] != strings.Repeat("0", 64) {
					t.Error("test lost explicit charge or preview binding")
				}
				return `{"state":"queued"}`, nil
			}
			s, _, _ := launch(t, false, b)
			openTwilioTest(t, s)
			frame := awaitFrame(t, s, "Actual notification body")
			if !strings.Contains(frame, "may incur charges") {
				t.Fatal("missing cost preview")
			}
			if !confirm {
				key(s, tcell.KeyEscape)
				awaitFrame(t, s, "NodeRampart — Notification channels")
			} else {
				key(s, tcell.KeyRight)
				key(s, tcell.KeyEnter)
				awaitFrame(t, s, "Queued locally")
			}
			select {
			case call := <-b.calls:
				if call.id != "notify_preview" {
					t.Fatal("send before preview")
				}
			case <-time.After(time.Second):
				t.Fatal("no preview")
			}
			if confirm {
				select {
				case call := <-b.calls:
					if call.id != "notify_test" {
						t.Fatal("not selected test")
					}
				case <-time.After(time.Second):
					t.Fatal("missing test")
				}
			}
			if len(b.calls) != 0 || len(b.saves) != 0 {
				t.Fatal("cancel, preview or confirmation caused extra work")
			}
		})
	}
}

func TestOfficialControlledStatusAndUnknownFailuresAreLocalized(t *testing.T) {
	for reason := range officialNotificationTexts {
		data, _ := json.Marshal(map[string]string{"last_error": reason})
		text := humanResult(string(data), "zh")
		if strings.Contains(text, reason) {
			t.Fatal("controlled reason not localized", reason)
		}
	}
	for _, channel := range config.OfficialChannelNames() {
		raw := channel + "_credentials_unavailable"
		data, _ := json.Marshal(map[string]string{"channel": channel, "name": channel + "_worker", "last_error": raw, "dispatch_state": "delivery_unknown", "provider_state": "accepted"})
		text := humanResult(string(data), "zh")
		if strings.Contains(text, "_worker") || strings.Contains(text, "_credentials_unavailable") || !strings.Contains(text, "尚未确认送达") || !strings.Contains(text, "远端结果未知") {
			t.Fatal("official state/error incomplete localization", text)
		}
	}
	secret := "SYNTHETIC_PRIVATE_RESPONSE"
	data, _ := json.Marshal(map[string]string{"last_error": secret})
	if strings.Contains(humanResult(string(data), "zh"), secret) || strings.Contains(nativeActionFailure(errors.New(secret), "zh"), secret) {
		t.Fatal("raw error exposed")
	}
}

func TestOfficialAcceptedStateDoesNotUseTeamsWorkflowLabel(t *testing.T) {
	for _, channel := range config.OfficialChannelNames() {
		data, _ := json.Marshal(map[string]string{"channel": channel, "state": "accepted"})
		text := humanResult(string(data), "zh")
		if !strings.Contains(text, "平台已接受请求；尚未确认送达") || strings.Contains(text, "工作流") {
			t.Fatal("accepted state overstates or misidentifies confirmation", text)
		}
	}
	data, _ := json.Marshal(map[string]string{"channel": "teams", "state": "accepted"})
	if !strings.Contains(humanResult(string(data), "zh"), "工作流请求已接受") {
		t.Fatal("Teams workflow observability changed")
	}
}

func TestOfficialUnknownObservationsAndCanceledProviderStatus(t *testing.T) {
	for _, language := range []string{"en", "zh"} {
		data, _ := json.Marshal(map[string]any{"channel": "twilio_sms", "platform_segments": nil, "platform_price": nil, "platform_charges": nil, "provider_delivery_status": "canceled", "http_status": 200, "api_error_code": 21610})
		text := humanResult(string(data), language)
		for _, key := range []string{"platform_segments", "platform_price", "platform_charges"} {
			label := notificationText(resultLabels[key], language)
			unknown := notificationText([2]string{"Unknown", "未知"}, language)
			if !strings.Contains(text, label+": "+unknown+"\n") {
				t.Fatal("unavailable platform observation is not explicitly unknown", language, key, text)
			}
		}
		if !strings.Contains(text, notificationText(officialDispatchLabels["canceled"], language)) || language == "zh" && strings.Contains(text, "provider delivery status:") || !strings.Contains(text, "21610") {
			t.Fatal("provider status or numeric diagnostics not localized", language, text)
		}
		data, _ = json.Marshal(map[string]any{"platform_segments": 0, "platform_price": "0", "ordinary_absent": nil})
		text = humanResult(string(data), language)
		if !strings.Contains(text, notificationText(resultLabels["platform_segments"], language)+": 0\n") || !strings.Contains(text, "ordinary absent: —\n") {
			t.Fatal("known zero or unrelated absent value changed", language, text)
		}
	}
}

func TestOfficialPreviewShowsApprovedWhatsAppParametersWithoutSending(t *testing.T) {
	a, ok := findAction("notify_preview")
	if !ok || a.mutation || a.helpEN == "" || a.helpZH == "" || len(a.params) != 1 || len(a.params[0].choices) != 4 {
		t.Fatal("standalone dry-run preview action incomplete")
	}
	slots := []string{"local_reference", "event_kind", "phase", "severity", "time", "bounded_summary"}
	values := []string{"noderampart report now", "test", "test", "high", "2026-10-02T08:00:00Z", "Synthetic CPU alert; coverage: unavailable"}
	frozen := map[string]any{"channel": "whatsapp_cloud", "kind": "test", "fingerprint": strings.Repeat("0", 64), "template": map[string]any{"name": "synthetic_notice", "language": "zh_CN", "slots": slots, "values": values}}
	v := map[string]any{"channel": "whatsapp_cloud", "body": "合成监控摘要", "language": "zh", "cost": "unknown", "daily_message_limit": 20, "preview_id": strings.Repeat("0", 64), "network_sent": false, "budget": officialPreviewBudget("whatsapp_cloud"), "next_reset_utc": "2026-10-03T00:00:00Z", "target": "********0102", "frozen_request": frozen}
	data, _ := json.Marshal(v)
	text, _, err := parsePaidPreview(string(data), "whatsapp_cloud", "zh")
	if err != nil || !strings.Contains(text, "synthetic_notice") || !strings.Contains(text, "zh_CN") || !strings.Contains(text, "noderampart report now") || !strings.Contains(text, "实际费用：未知") {
		t.Fatal("template preview missing actual mapping", err)
	}
	frozen["template"].(map[string]any)["slots"] = []string{"host_alias"}
	frozen["template"].(map[string]any)["values"] = []string{"synthetic host"}
	data, _ = json.Marshal(v)
	if _, _, err := parsePaidPreview(string(data), "whatsapp_cloud", "zh"); err == nil {
		t.Fatal("preview hid required notification fields")
	}
}

func TestSMSPreviewConsumesActualPreparedEncodingContract(t *testing.T) {
	for _, language := range []string{"en", "zh"} {
		c := config.DefaultOfficialChannel("twilio_sms")
		c.Language = language
		c.Subscription = config.OfficialSubscription{ConfirmedAt: "2026-10-02T08:00:00Z", Purpose: "Synthetic alert", NotificationTypes: []string{"test"}, EvidenceRef: "synthetic-test-consent", BasisID: strings.Repeat("0", 32), CostConfirmed: true}
		credential := config.OfficialCredential{Channel: "twilio_sms", AccountSID: "AC" + strings.Repeat("0", 32), AuthMode: "auth_token", AuthToken: "SYNTHETIC-TOKEN-0000", From: "+12025550101", To: "+12025550102"}
		data, _ := json.Marshal(credential)
		c.CredentialFile = filepath.Join(t.TempDir(), "credential.json")
		if err := os.WriteFile(c.CredentialFile, data, 0o600); err != nil {
			t.Fatal(err)
		}
		sender, err := notify.NewOfficial("twilio_sms", c)
		if err != nil {
			t.Fatal(err)
		}
		body, semantic := notify.FormatOfficialTest("synthetic", language)
		payload, _ := json.Marshal(semantic)
		m := store.OutboxMessage{Channel: "twilio_sms", Destination: sender.Destination(), LogicalKind: "test", Language: language, Body: body, SemanticPayload: string(payload)}
		if err := sender.PrepareMessage(&m); err != nil {
			t.Fatal(err)
		}
		data, _ = json.Marshal(map[string]any{"channel": m.Channel, "language": m.Language, "body": m.Body, "frozen_request": json.RawMessage(m.FrozenPayload), "encoding": m.Encoding, "estimated_segments": m.EstimatedSegments, "cost": "unknown", "daily_message_limit": c.DailyMessageLimit, "daily_segment_limit": c.DailySegmentLimit, "preview_id": strings.Repeat("0", 64), "network_sent": false, "budget": officialPreviewBudget(m.Channel), "next_reset_utc": "2026-10-03T00:00:00Z", "target": sender.RecipientPreview()})
		text, _, err := parsePaidPreview(string(data), m.Channel, language)
		if err != nil || !strings.Contains(text, m.Body) || !strings.Contains(text, map[string]string{"gsm7": "GSM-7", "ucs2": "UCS-2"}[m.Encoding]) {
			t.Fatal("actual preparation and TUI preview diverged", err)
		}
	}
}

func officialPreviewBudget(channel string) map[string]any {
	return map[string]any{"channel": channel, "logical_messages_reserved": 3, "estimated_segments_reserved": 4, "restore_reconciliation_required": false, "opted_out": false}
}
