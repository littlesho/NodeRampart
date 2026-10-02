// SPDX-License-Identifier: MIT

package console

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/littlesho/NodeRampart/internal/config"
)

var nativeBrands = map[string]string{"feishu": "Feishu", "wecom": "WeCom", "discord": "Discord", "slack": "Slack", "teams": "Microsoft Teams Workflows", "google_chat": "Google Chat"}

var deliveryFailurePattern = regexp.MustCompile(`^(Telegram|Webhook|Feishu|WeCom|Discord|Slack|Teams workflow|Google Chat|QQ Bot|LINE|Twilio SMS|WhatsApp Cloud) (delivery failed|retry interval requires destination resume) \(HTTP ([0-9]{1,3}), API (-?[0-9]{1,10})\)$`)
var deliveryResponsePattern = regexp.MustCompile(`^(Telegram|Webhook|Feishu|WeCom|Discord|Slack|Teams workflow|Google Chat|QQ Bot|LINE|Twilio SMS|WhatsApp Cloud) response invalid \(HTTP ([0-9]{1,3})\)$`)
var deliveryPayloadPattern = regexp.MustCompile(`^(Telegram|Webhook|Feishu|WeCom|Discord|Slack|Teams workflow|Google Chat|QQ Bot|LINE|Twilio SMS|WhatsApp Cloud) payload invalid$`)

var notificationTexts = map[string][2]string{
	"selected notification sender is disabled or unavailable":              {"The selected channel is disabled or unavailable. Inspect its protected credentials and delivery status.", "所选渠道已停用或不可用。请检查受保护凭据与投递状态。"},
	"could not queue test notification":                                    {"The test notification could not enter the outbox. Inspect storage health and queue limits.", "测试通知无法入队。请检查存储健康与队列限额。"},
	"isolated notification bodies could not be discarded":                  {"Isolated notification bodies could not be discarded. Inspect storage health and retry the selected channel.", "无法丢弃隔离通知正文。请检查存储健康，再重试所选渠道。"},
	"notification delivery failed":                                         {"Notification delivery failed; inspect channel status and protected credentials.", "通知投递失败；请检查渠道状态与受保护凭据。"},
	"notification target or privacy policy changed; retained in isolation": {"Notification target or privacy policy changed; retained in isolation", "通知目标或隐私策略已更改；消息保留隔离"},
	"isolated notification explicitly discarded":                           {"Isolated notification body explicitly discarded", "已明确丢弃隔离通知正文"},
	"queued": {"Queued locally; remote acceptance is not confirmed", "已在本地入队；尚未确认远端接受"},
	"isolated bodies discarded; active queue preserved":                                                  {"Isolated bodies discarded; active queue preserved", "已丢弃隔离正文；当前队列保持不变"},
	"An enabled optional notification channel is unavailable; base monitoring continues.":                {"An enabled optional notification channel is unavailable; base monitoring continues.", "已启用的可选通知渠道不可用；基础监控继续运行。"},
	"Correct its protected credential file, validate configuration, then restart.":                       {"Correct its protected credential file, validate configuration, then restart.", "修正受保护凭据文件，验证配置后重启。"},
	"An optional component reports degraded operation.":                                                  {"An optional component reports degraded operation.", "可选组件报告运行异常。"},
	"Inspect the component state and its fixed local failure reason; retry or restart after correction.": {"Inspect the component state and its fixed local failure reason; retry or restart after correction.", "查看组件状态与固定的本地故障原因；修正后重试或重启。"},
	"Retained notifications include rejected, quarantined or isolated work.":                             {"Retained notifications include rejected, quarantined or isolated work.", "保留的通知包含被拒绝、重试隔离或目标隔离的消息。"},
	"Inspect notify status/list and explicitly handle isolated messages.":                                {"Inspect notify status/list and explicitly handle isolated messages.", "查看通知状态与消息列表，并明确处理隔离消息。"},
	"optional_component_degraded":                                                                        {"Optional component degraded", "可选组件运行异常"},
	"notification_attention":                                                                             {"Notification queue needs attention", "通知队列需要处理"},
}

func notificationText(label [2]string, language string) string {
	if language == "zh" {
		return label[1]
	}
	return label[0]
}

// Map only fixed product categories and tightly bounded numeric protocol codes.
// Unknown errors never serve as an English-to-Chinese translation source.
func notificationResultLabel(key, value, language string) string {
	if label, ok := officialNotificationTexts[value]; ok {
		return notificationText(label, language)
	}
	if key == "dispatch_state" || key == "provider_state" || key == "provider_delivery_status" {
		if label, ok := officialDispatchLabels[value]; ok {
			return notificationText(label, language)
		}
	}
	if label, ok := notificationTexts[value]; ok {
		return notificationText(label, language)
	}
	if key == "channel" {
		if brand, ok := nativeBrands[value]; ok {
			return brand
		}
		if brand, ok := officialBrands[value]; ok {
			return brand
		}
	}
	for channel, brand := range nativeBrands {
		if value == channel+"_credentials_unavailable" {
			return notificationText([2]string{brand + " protected credentials unavailable or unsafe", brand + " 受保护凭据不可用或不安全"}, language)
		}
		if key == "name" && value == channel+"_worker" {
			return notificationText([2]string{brand + " delivery worker", brand + " 投递工作线程"}, language)
		}
	}
	for channel, brand := range officialBrands {
		if value == channel+"_credentials_unavailable" {
			return notificationText([2]string{brand + " protected credentials unavailable or unsafe", brand + " 受保护凭据不可用或不安全"}, language)
		}
		if key == "name" && value == channel+"_worker" {
			return notificationText([2]string{brand + " delivery worker", brand + " 投递工作线程"}, language)
		}
	}
	if key == "state" {
		if label, ok := map[string][2]string{
			"disabled": {"Disabled", "已停用"}, "running": {"Running", "运行中"}, "failed": {"Failed", "失败"},
			"degraded": {"Degraded", "运行异常"}, "unknown": {"Unknown", "未知"}, "healthy": {"Healthy", "正常"},
			"silenced": {"Silenced", "已静默"}, "not_applicable": {"Not applicable", "不适用"},
		}[value]; ok {
			return notificationText(label, language)
		}
	}
	if key != "last_error" {
		return value
	}
	if value == "" {
		return value
	}
	if match := deliveryFailurePattern.FindStringSubmatch(value); match != nil {
		if language != "zh" {
			return value
		}
		category := "投递失败"
		if match[2] == "retry interval requires destination resume" {
			category = "重试等待时间要求手动恢复目标"
		}
		return fmt.Sprintf("%s %s（HTTP %s，API %s）", match[1], category, match[3], match[4])
	}
	if match := deliveryResponsePattern.FindStringSubmatch(value); match != nil {
		if language == "zh" {
			return fmt.Sprintf("%s 响应无效（HTTP %s）", match[1], match[2])
		}
		return value
	}
	if match := deliveryPayloadPattern.FindStringSubmatch(value); match != nil {
		if language == "zh" {
			return match[1] + " 请求正文无效"
		}
		return value
	}
	return notificationText([2]string{"Unrecognized delivery failure category; inspect local channel status.", "未识别的投递错误类别；请查看本地渠道状态。"}, language)
}

func nativeActionFailure(err error, language string) string {
	if label, ok := notificationTexts[err.Error()]; ok {
		return notificationText(label, language)
	}
	if err.Error() == "context canceled" || err.Error() == "context deadline exceeded" {
		return notificationText([2]string{"The notification operation was cancelled or timed out. Inspect local status before retrying.", "通知操作已取消或超时。重试前请检查本地状态。"}, language)
	}
	return notificationText([2]string{"The notification operation failed. Inspect local channel status and protected credentials; no raw error is displayed.", "通知操作失败。请检查本地渠道状态与受保护凭据；不显示原始错误。"}, language)
}

func nativeSetupChannel(id string) string {
	name := strings.TrimSuffix(id, "_setup")
	if strings.HasSuffix(id, "_setup") && config.IsNativeChannel(name) {
		return name
	}
	return ""
}

func init() {
	for _, channel := range config.NativeChannelNames() {
		brand := nativeBrands[channel]
		params := []parameter{
			choice("enabled", "Enable this channel", "启用本渠道", "no", "yes"),
			choice("language", "Message language (independent of UI)", "消息语言（与界面独立）", "en", "zh"),
			choice("credential_action", "Webhook URL action", "Webhook URL 操作", "keep", "replace", "clear"),
			secret("url", "Replacement webhook URL (hidden)", "替换 Webhook URL（隐藏）"),
		}
		if channel == "feishu" {
			params = append(params, choice("secret_action", "Signing secret action", "签名密钥操作", "keep", "replace", "clear"), secret("secret", "Replacement signing secret (hidden)", "替换签名密钥（隐藏）"))
		}
		actions = append(actions, action{id: channel + "_setup", en: "Set up " + brand, zh: "设置 " + brand,
			helpEN: "Use your administrator-authorized incoming webhook. Keep leaves the protected credential unchanged; Replace requires hidden input; Clear requires this channel disabled. Each channel has one independent target and language. Setup sends no message. A changed credential isolates old backlog; disabling the unchanged target pauses it. Test separately from Notifications. Read docs/NOTIFICATION_CHANNELS.md for creation, permissions and platform limits.",
			helpZH: "使用自己获得管理员授权的入站 Webhook。保留维持原受保护凭据；替换须填写隐藏输入；清空前须停用本渠道。每个渠道拥有独立目标与语言。设置不会发送消息；修改凭据会隔离旧积压，停用未变更目标仅暂停投递。请从通知菜单单独测试。创建、权限及平台限制详见 docs/NOTIFICATION_CHANNELS.zh-CN.md。",
			params: params, mutation: true, confirmEN: "Save the reviewed settings? Running services may briefly restart. No test message is sent.", confirmZH: "保存已检查的设置？运行中服务可能短暂重启；不会发送测试消息。"})
		prefix := "notifications." + channel + "."
		fields = append(fields,
			field{prefix + "enabled", "notifications", "Enable " + brand, "启用 " + brand, "Configure its protected URL with channel setup before enabling.", "启用前请在通知渠道设置中配置受保护 URL。", yesNo},
			field{prefix + "credential_file", "notifications", brand + " credential file", brand + " 凭据文件", "Protected managed credential reference only. Never enter the webhook URL here.", "仅引用受保护的托管凭据文件；切勿在此填写 Webhook URL。", nil},
			field{prefix + "language", "notifications", brand + " message language", brand + " 消息语言", "English or Simplified Chinese, independent of UI. Existing messages retain their text.", "英语或简体中文，与界面独立；已入队消息保留原文。", []string{"en", "zh"}},
			field{prefix + "timeout", "notifications", brand + " timeout", brand + " 超时", "1s–30s; default 10s. Retries use the persistent outbox.", "1s–30s；默认 10s；重试使用持久发件队列。", nil})
	}
	// Help remains a read-only action rendered locally by openAction.
	actions = append(actions, action{id: "notification_help", en: "Notification channel help", zh: "通知渠道使用帮助"})
}

func (u *ui) reviewNativeAction(a action, args map[string]string, back func()) {
	channel := nativeSetupChannel(a.id)
	invalid := ""
	if args["credential_action"] == "replace" {
		credential := config.NativeCredential{URL: args["url"]}
		if args["secret_action"] == "replace" {
			credential.Secret = args["secret"]
		}
		if err := config.ValidateNativeCredential(channel, credential); err != nil {
			invalid = u.tr("The official webhook URL or signing secret is invalid.", "官方 Webhook URL 或签名密钥无效。")
		}
	}
	if args["credential_action"] != "replace" && args["url"] != "" || args["secret_action"] != "replace" && args["secret"] != "" {
		invalid = u.tr("Choose Replace before entering a replacement credential.", "输入新凭据前请选择替换。")
	}
	if args["credential_action"] == "clear" && args["enabled"] != "no" {
		invalid = u.tr("Disable this channel before clearing credentials.", "清空凭据前请停用本渠道。")
	}
	if invalid != "" {
		clearArguments(args)
		u.output(u.tr("Settings need correction", "设置需要修正"), invalid, func() { u.actionForm(a, back) })
		return
	}
	label := func(value string) string {
		switch value {
		case "keep":
			return u.tr("keep unchanged", "保留不变")
		case "replace":
			return u.tr("replace with hidden input", "替换为隐藏输入")
		case "clear":
			return u.tr("clear", "清空")
		}
		return value
	}
	enabled := u.tr("disabled", "停用")
	if args["enabled"] == "yes" {
		enabled = u.tr("enabled", "启用")
	}
	text := nativeBrands[channel] + "\n" + u.tr("State: ", "状态：") + enabled + "\n" + u.tr("Message language: ", "消息语言：") + args["language"] + "\n" + u.tr("Webhook URL: ", "Webhook URL：") + label(args["credential_action"])
	if channel == "feishu" {
		text += "\n" + u.tr("Signing secret: ", "签名密钥：") + label(args["secret_action"])
	}
	text += "\n\n" + u.tr("Credential contents remain hidden. Changed credentials isolate old messages; language applies to new bodies only. Already in-flight requests may complete at their original target. No test is sent.", "凭据正文保持隐藏。修改凭据会隔离旧消息；语言仅影响新正文。已发送中的请求可能在原目标完成。不会发送测试消息。")
	pages, truncated := outputPages(text)
	u.outputPageAction(u.tr("Review notification settings", "检查通知设置"), pages, 0, truncated, u.tr("Save and apply", "保存并应用"), func() {
		u.confirm(u.tr(a.confirmEN, a.confirmZH), func() { u.executeAction(a, args, back) }, func() { clearArguments(args); back() })
	}, func() { clearArguments(args); back() })
}

func (u *ui) notificationHelp(back func()) {
	u.output(u.tr("Notification channel help", "通知渠道使用帮助"), u.tr("Configure one authorized target per channel. Webhook URLs and Feishu signing secrets are entered hidden and saved in daemon-readable protected files. Saving never sends a message. Select a channel explicitly in Send a test notification. Delivery status and Notification messages show queue, retries and isolated bodies. Discard isolated bodies affects only the selected channel. Teams uses the anonymous-secret-URL Workflows Adaptive Card contract; a tenant requiring Entra/OAuth is unsupported. Workflows acceptance does not confirm final Teams display. See docs/NOTIFICATION_CHANNELS.md for official creation steps, owner/co-owner handling, rotation, revocation, privacy and service terms. NodeRampart does not register accounts or accept platform terms.", "每个渠道配置一个已授权目标。Webhook URL 与飞书签名密钥使用隐藏输入，保存到守护进程可读的受保护文件。保存不会发送消息；请在发送测试通知中明确选择渠道。投递状态及通知消息列表显示队列、重试与隔离正文；丢弃隔离正文仅影响所选渠道。Teams 使用受保护秘密 URL 的 Workflows Adaptive Card 契约；要求 Entra/OAuth 的租户不支持。工作流接受请求不代表 Teams 最终展示成功。官方创建步骤、所有者与共同所有者、轮换撤销、隐私及服务条款详见 docs/NOTIFICATION_CHANNELS.zh-CN.md。NodeRampart 不代注册账号或接受平台条款。"), back)
}
