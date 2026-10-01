// SPDX-License-Identifier: MIT

package config

import (
	"strings"
	"testing"
)

func TestNativeOfficialURLContracts(t *testing.T) {
	valid := map[string][]string{
		"feishu":      {"https://open.feishu.cn/open-apis/bot/v2/hook/synthetic-hook-123456"},
		"wecom":       {"https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=synthetic-key-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		"discord":     {"https://discord.com/api/webhooks/123456789012345678/synthetic-token-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb0", "https://discord.com/api/v10/webhooks/123456789012345678/synthetic-token-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb0?wait=true"},
		"slack":       {"https://hooks.slack.com/services/T00000000/B00000000/SyntheticToken0000"},
		"teams":       {"https://defaultsynthetic.environment.api.powerplatform.com/powerautomate/automations/direct/workflows/synthetic-workflow-123456/triggers/manual/paths/invoke?api-version=1&sp=%2Ftriggers%2Fmanual%2Frun&sv=1.0&sig=synthetic-signature-123456789", "https://defaultsynthetic.b5.environment.api.powerplatform.com/powerautomate/automations/direct/cu/20/workflows/synthetic-workflow-123456/triggers/manual/paths/invoke?api-version=1&sp=%2Ftriggers%2Fmanual%2Frun&sv=1.0&sig=synthetic-signature-123456789", "https://prod-26.westeurope.logic.azure.com:443/workflows/synthetic-workflow-123456/triggers/manual/paths/invoke?api-version=2016-06-01&sp=%2Ftriggers%2Fmanual%2Frun&sv=1.0&sig=synthetic-signature-123456789"},
		"google_chat": {"https://chat.googleapis.com/v1/spaces/synthetic-space/messages?key=synthetic-key-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa&token=synthetic-token-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},
	}
	for channel, values := range valid {
		for _, value := range values {
			u, err := NativeHTTPSURL(channel, value)
			if err != nil {
				t.Fatalf("%s fixture: %v", channel, err)
			}
			if channel == "discord" && u.Query().Get("wait") != "true" {
				t.Fatal("Discord must confirm message")
			}
			for _, bad := range []string{strings.Replace(value, "https:", "http:", 1), value + "#fragment", strings.Replace(value, "://", "://userinfo@", 1), strings.Replace(value, "://", "://evil.", 1), strings.Replace(value, ".com/", ".com.evil.invalid/", 1), strings.Replace(value, ".cn/", ".cn.evil.invalid/", 1), value + "\n", strings.Replace(value, "/messages", "/%6dessages", 1)} {
				if bad == value {
					continue
				}
				if _, err := NativeHTTPSURL(channel, bad); err == nil {
					t.Fatalf("%s accepted malformed official URL", channel)
				}
			}
		}
	}
	for _, test := range []struct{ channel, url string }{
		{"wecom", "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?%6bey=synthetic-key-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		{"wecom", "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=synthetic-key-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa&key=synthetic-key-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		{"wecom", "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=synthetic-key-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa&extra=x"},
		{"discord", "https://discord.com/api/webhooks/123456789012345678/synthetic-token-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb0?wait=false"},
		{"discord", "https://discord.com/api/webhooks/123456789012345678/synthetic-token-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb0?thread_id=123456789"},
		{"teams", "https://tenant.azurewebsites.net/powerautomate/automations/direct/workflows/synthetic-workflow-123456/triggers/manual/paths/invoke?api-version=1&sp=%2Ftriggers%2Fmanual%2Frun&sv=1.0&sig=synthetic-signature-123456789"},
		{"teams", "https://outlook.office.com/webhook/synthetic-secret"},
		{"google_chat", "https://chat.googleapis.com/v1/spaces/synthetic-space/messages?key=synthetic-key-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa&token=synthetic-token-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb&alt=json"},
		{"feishu", "https://127.0.0.1/open-apis/bot/v2/hook/synthetic-hook-123456"},
		{"feishu", "https://open.feishu.cn:444/open-apis/bot/v2/hook/synthetic-hook-123456"},
	} {
		if _, err := NativeHTTPSURL(test.channel, test.url); err == nil {
			t.Fatalf("%s accepted bypass", test.channel)
		}
	}
}

func TestNativeCredentialDoesNotExposeSecretErrors(t *testing.T) {
	for _, c := range []NativeCredential{{URL: "https://secret-synthetic.invalid/secret-synthetic-token"}, {URL: "https://open.feishu.cn/open-apis/bot/v2/hook/synthetic-hook-123456", Secret: "secret-synthetic\ninvalid"}} {
		if err := ValidateNativeCredential("feishu", c); err == nil || strings.Contains(err.Error(), "secret-synthetic") {
			t.Fatal("invalid credential accepted or leaked")
		}
	}
}
