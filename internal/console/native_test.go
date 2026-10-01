// SPDX-License-Identifier: MIT

package console

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/notify"
)

func openNativeSettings(t *testing.T, s *recordedScreen, channel string) string {
	t.Helper()
	selectIndex(s, 5)
	awaitFrame(t, s, "Notification channels")
	for i, name := range config.NativeChannelNames() {
		if name == channel {
			selectIndex(s, 11+i)
			return awaitFrame(t, s, "Replacement webhook URL (hidden)")
		}
	}
	t.Fatal("unknown test channel")
	return ""
}

func TestNativeSetupCatalogAndEightChannelActions(t *testing.T) {
	for _, channel := range config.NativeChannelNames() {
		a, ok := findAction(channel + "_setup")
		if !ok || !a.mutation {
			t.Fatal("native settings action missing")
		}
		hasURL, hasLanguage := false, false
		for _, param := range a.params {
			if param.key == "url" {
				hasURL = param.secret
			}
			if param.key == "language" {
				hasLanguage = len(param.choices) == 2 && param.choices[0] == "en" && param.choices[1] == "zh"
			}
		}
		if !hasURL || !hasLanguage || a.confirmEN == "" || a.helpEN == "" || a.helpZH == "" {
			t.Fatal("native action incomplete", channel)
		}
	}
	for _, id := range []string{"notify_test", "notify_discard_isolated"} {
		a, _ := findAction(id)
		if len(a.params) != 1 || len(a.params[0].choices) != 8 {
			t.Fatal("channel operation omits targets")
		}
		for _, name := range a.params[0].choices {
			if !config.IsNotificationChannel(name) {
				t.Fatal("unknown channel")
			}
		}
	}
}

func TestSimulationNativeHiddenInputCancellationAndIndependentLanguage(t *testing.T) {
	for _, channel := range config.NativeChannelNames() {
		t.Run(channel, func(t *testing.T) {
			b := newBackend()
			n := b.cfg.Notifications.NativeChannels()[channel]
			n.Language = "zh"
			n.Enabled = true
			_ = b.cfg.Notifications.SetNativeChannel(channel, n)
			s, _, _ := launch(t, false, b)
			awaitFrame(t, s, "Main menu")
			frame := openNativeSettings(t, s, channel)
			if !strings.Contains(frame, "简体中文") {
				t.Fatal("UI language overwrote native message language")
			}
			for range 3 {
				key(s, tcell.KeyTab)
			}
			value := "SYNTHETIC_PRIVATE_WEBHOOK_INPUT"
			textKeys(s, value)
			frame = awaitFrame(t, s, strings.Repeat("•", len(value)))
			if strings.Contains(frame, value) {
				t.Fatal("native URL rendered in clear text")
			}
			key(s, tcell.KeyEscape)
			awaitFrame(t, s, "Notification channels")
			if len(b.calls) != 0 || len(b.saves) != 0 {
				t.Fatal("settings browsing/cancel sent or saved")
			}
		})
	}
}

func TestSimulationNativeReviewHidesCredentialsAndRequiresExplicitSave(t *testing.T) {
	const url = "https://hooks.slack.com/services/TSYNTHETIC1/BSYNTHETIC2/SyntheticToken0000"
	b := newBackend()
	b.action = func(_ context.Context, id string, args map[string]string) (string, error) {
		if id != "slack_setup" || args["url"] != url || args["credential_action"] != "replace" || args["language"] != "en" || args["enabled"] != "no" {
			t.Error("wrong native setup payload")
		}
		return "", errors.New("unexpected response " + url)
	}
	s, _, _ := launch(t, false, b)
	awaitFrame(t, s, "Main menu")
	openNativeSettings(t, s, "slack")
	key(s, tcell.KeyTab)
	key(s, tcell.KeyTab)
	key(s, tcell.KeyEnter)
	key(s, tcell.KeyDown)
	key(s, tcell.KeyEnter)
	key(s, tcell.KeyTab)
	textKeys(s, url)
	key(s, tcell.KeyTab)
	key(s, tcell.KeyEnter)
	frame := awaitFrame(t, s, "Review notification settings")
	if strings.Contains(frame, url) || !strings.Contains(frame, "replace with hidden input") || len(b.calls) != 0 || len(b.saves) != 0 {
		t.Fatal("review leaked credentials or applied early")
	}
	key(s, tcell.KeyTab)
	key(s, tcell.KeyEnter)
	frame = awaitFrame(t, s, "Confirm action")
	if strings.Contains(frame, url) || len(b.calls) != 0 {
		t.Fatal("confirmation leaked or applied early")
	}
	key(s, tcell.KeyRight)
	key(s, tcell.KeyEnter)
	frame = awaitFrame(t, s, "Secret values are not shown")
	if strings.Contains(frame, url) {
		t.Fatal("backend error leaked native URL")
	}
	select {
	case call := <-b.calls:
		if call.id != "slack_setup" {
			t.Fatal("setup automatically sent test")
		}
	case <-time.After(time.Second):
		t.Fatal("missing explicit setup")
	}
	if len(b.calls) != 0 || len(b.saves) != 0 {
		t.Fatal("additional side effect")
	}
}

func TestSimulationNativeReviewCancelDoesNotApply(t *testing.T) {
	b := newBackend()
	s, _, _ := launch(t, false, b)
	awaitFrame(t, s, "Main menu")
	openNativeSettings(t, s, "google_chat")
	for range 4 {
		key(s, tcell.KeyTab)
	}
	key(s, tcell.KeyEnter)
	awaitFrame(t, s, "Review notification settings")
	key(s, tcell.KeyEscape)
	awaitFrame(t, s, "Notification channels")
	if len(b.calls) != 0 || len(b.saves) != 0 {
		t.Fatal("cancelled review applied settings")
	}
}

func TestSimulationNotificationHelpIsReadOnlyAndExplainsTeamsBoundary(t *testing.T) {
	b := newBackend()
	s, _, _ := launch(t, false, b)
	awaitFrame(t, s, "Main menu")
	selectIndex(s, 5)
	awaitFrame(t, s, "Notification channels")
	selectIndex(s, 17)
	frame := awaitFrame(t, s, "OAuth is unsupported")
	if !strings.Contains(frame, "does not confirm final Teams display") || len(b.calls) != 0 || len(b.saves) != 0 {
		t.Fatal("help omitted delivery boundary or contacted backend")
	}
}

func TestTeamsAcceptanceAndOtherInterfaceAcknowledgmentsAreLocalized(t *testing.T) {
	input := `{"notifications":[{"channel":"teams","state":"accepted"},{"channel":"slack","state":"sent"}]}`
	for _, language := range []string{"en", "zh"} {
		result := humanResult(input, language)
		accepted, ack := "Workflow request accepted", "Interface acknowledged"
		if language == "zh" {
			accepted, ack = "工作流请求已接受", "接口已确认"
		}
		if !strings.Contains(result, accepted) || !strings.Contains(result, ack) || strings.Contains(result, "read") || strings.Contains(result, "已读") {
			t.Fatal("delivery observability boundary incorrectly rendered")
		}
	}
}

func TestNativeDiagnosisAndQueueErrorsUseFixedChineseCategories(t *testing.T) {
	for _, channel := range config.NativeChannelNames() {
		failures := []notify.DeliveryError{
			{Channel: channel, StatusCode: 429, APIErrorCode: 9499},
			{Channel: channel, StatusCode: 503, APIErrorCode: -1, InvalidResponse: true},
			{Channel: channel, InvalidPayload: true},
			{Channel: channel, StatusCode: 429, APIErrorCode: 9499, SuspendDestination: true},
		}
		for _, failure := range failures {
			value := map[string]any{
				"channel": channel, "last_error": failure.Error(), "attempts": 1,
				"components":        []map[string]string{{"name": channel + "_worker", "state": "degraded"}},
				"optional_failures": map[string]string{channel: channel + "_credentials_unavailable"},
				"diagnosis": map[string]any{"schema_version": 1, "state": "degraded", "checks": []map[string]string{{
					"reason_code": channel + "_credentials_unavailable", "state": "degraded",
					"impact":    "An enabled optional notification channel is unavailable; base monitoring continues.",
					"next_step": "Correct its protected credential file, validate configuration, then restart.",
				}}},
			}
			data, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			result := humanResult(string(data), "zh")
			for _, expected := range []string{"通知渠道", "最近投递错误", "投递工作线程", "受保护凭据不可用或不安全", "基础监控继续运行", "验证配置后重启", "运行异常"} {
				if !strings.Contains(result, expected) {
					t.Fatal("Chinese notification diagnostic omitted a controlled label", channel, expected)
				}
			}
			for _, raw := range []string{"credentials_unavailable", "delivery failed", "response invalid", "payload invalid", "requires destination resume", "An enabled optional", "Correct its", "last error", "next step", "reason code"} {
				if strings.Contains(result, raw) {
					t.Fatal("Chinese notification diagnostic retained an English controlled label", channel, raw)
				}
			}
			if !failure.InvalidPayload && !strings.Contains(result, "HTTP") {
				t.Fatal("localized error discarded the protocol status")
			}
		}
	}
}

func TestNativeUnknownErrorAndMalformedCategoryNeverExposeResponse(t *testing.T) {
	const private = "SYNTHETIC_PRIVATE_RESPONSE"
	for _, raw := range []string{private, "Slack delivery failed (HTTP 429, API 9499) " + private, "Slack response invalid (HTTP 200)\n" + private} {
		data, err := json.Marshal(map[string]string{"last_error": raw})
		if err != nil {
			t.Fatal(err)
		}
		for _, language := range []string{"en", "zh"} {
			result := humanResult(string(data), language)
			if strings.Contains(result, private) || strings.Contains(nativeActionFailure(errors.New(raw), language), private) {
				t.Fatal("unrecognized error category exposed response contents")
			}
		}
	}
}

func TestSimulationNativeTestUnavailableDisplaysChineseFixedError(t *testing.T) {
	b := newBackend()
	b.action = func(_ context.Context, id string, args map[string]string) (string, error) {
		if id != "notify_test" || args["channel"] != "feishu" {
			t.Error("test targeted the wrong channel")
		}
		return "", errors.New("selected notification sender is disabled or unavailable")
	}
	s, _, _ := launch(t, false, b)
	awaitFrame(t, s, "Main menu")
	selectIndex(s, 10)
	awaitFrame(t, s, "主菜单")
	selectIndex(s, 5)
	awaitFrame(t, s, "通知渠道")
	selectIndex(s, 3)
	awaitFrame(t, s, "发送测试通知")
	key(s, tcell.KeyEnter)
	key(s, tcell.KeyDown)
	key(s, tcell.KeyDown)
	key(s, tcell.KeyEnter)
	key(s, tcell.KeyTab)
	key(s, tcell.KeyEnter)
	awaitFrame(t, s, "确认操作")
	key(s, tcell.KeyRight)
	key(s, tcell.KeyEnter)
	frame := awaitFrame(t, s, "所选渠道已停用或不可用")
	if !strings.Contains(frame, "受保护凭据与投递状态") || strings.Contains(frame, "sender is disabled") {
		t.Fatal("native test failure was not localized")
	}
	select {
	case call := <-b.calls:
		if call.id != "notify_test" || call.args["channel"] != "feishu" {
			t.Fatal("explicit test targeted the wrong channel")
		}
	case <-time.After(time.Second):
		t.Fatal("missing explicit selected-channel test")
	}
	if len(b.calls) != 0 || len(b.saves) != 0 {
		t.Fatal("failure display retried or altered configuration")
	}
}
