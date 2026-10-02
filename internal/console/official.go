// SPDX-License-Identifier: MIT

package console

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/littlesho/NodeRampart/internal/config"
)

var officialBrands = map[string]string{"qqbot": "QQ Bot", "line": "LINE", "twilio_sms": "Twilio SMS", "whatsapp_cloud": "WhatsApp Cloud"}

func officialUIActionChannel(id string) (string, string) {
	for _, suffix := range []string{"_setup", "_credentials", "_subscription"} {
		if strings.HasSuffix(id, suffix) {
			name := strings.TrimSuffix(id, suffix)
			if config.IsOfficialChannel(name) {
				return name, strings.TrimPrefix(suffix, "_")
			}
		}
	}
	return "", ""
}

func officialSubscriptionField(path string) bool {
	for _, channel := range config.OfficialChannelNames() {
		if strings.HasPrefix(path, "notifications."+channel+".subscription.") {
			return true
		}
	}
	return false
}

var officialFieldLabels = map[string][2]string{
	"app_id": {"App ID", "应用 ID"}, "app_secret": {"App secret", "应用密钥"}, "target_type": {"Target type (QQ: user/group; LINE: user/group/room)", "目标类型（QQ：user/group；LINE：user/group/room）"}, "target_id": {"App-scoped target ID", "应用范围内目标 ID"},
	"channel_access_token": {"Channel access token", "频道访问 Token"}, "account_sid": {"Account SID", "账户 SID"}, "auth_mode": {"Authentication mode (api_key/auth_token)", "鉴权模式（api_key/auth_token）"}, "api_key_sid": {"API key SID", "API 密钥 SID"}, "api_key_secret": {"API key secret", "API 密钥正文"}, "auth_token": {"Account auth token", "账户鉴权 Token"}, "from": {"From number (E.164)", "发送号码（E.164）"}, "messaging_service_sid": {"Messaging Service SID (alternative to From)", "Messaging Service SID（与 From 二选一）"}, "to": {"Recipient number (E.164)", "接收号码（E.164）"},
	"phone_number_id": {"Business phone number ID", "企业电话号码 ID"}, "access_token": {"System-user access token", "系统用户访问 Token"}, "recipient": {"Recipient international digits (without +)", "接收号码国际数字（不含 +）"}, "graph_version": {"Graph version (v26.0)", "Graph 版本（v26.0）"},
}

func init() {
	actions = append(actions, action{id: "notify_preview", en: "Preview a notification without sending", zh: "预览通知而不发送", params: []parameter{choice("channel", "Channel", "渠道", "qqbot", "line", "twilio_sms", "whatsapp_cloud")}, helpEN: "Reads the configured protected snapshot locally and displays a bounded body or approved template with actual parameters. No request is sent. Preview does not bypass consent, limits or paid-test confirmation.", helpZH: "在本地读取已配置的受保护快照，显示有界正文或已批准模板及实际参数。不会发送请求；预览不能绕过同意、限额或付费测试确认。"})

	for i := range actions {
		if actions[i].id == "notify_test" || actions[i].id == "notify_discard_isolated" {
			actions[i].params[0].choices = append(actions[i].params[0].choices, config.OfficialChannelNames()...)
		}
	}
	actions = append(actions, action{id: "notify_reconcile_paid", en: "Reconcile paid notifications after restore", zh: "恢复后核对付费通知", params: []parameter{choice("channel", "Paid channel", "付费渠道", "twilio_sms", "whatsapp_cloud"), p("evidence_ref", "External receipt and budget reconciliation evidence ID", "外部回执与额度核对依据编号"), p("confirm", "Type RECONCILE after manual reconciliation", "人工核对后输入 RECONCILE")}, mutation: true, confirmEN: "Resume new paid notifications after manually reconciling external receipts and local budgets? Unknown old attempts remain held; this cannot clear recipient opt-out locks.", confirmZH: "人工核对外部回执与本地额度后恢复新的付费通知？旧未知尝试继续保留；此操作不能解除接收者退订锁。", helpEN: "A database restore can omit later charged requests. Reconcile actual external receipts and budgets before confirming. This operation does not resend old unknown messages or erase reservations, opt-outs or historical uncertainty.", helpZH: "数据库恢复可能缺少之后已收费的请求。确认前须人工核对真实外部回执与额度；此操作不会重发旧未知消息，也不会清除预留、退订锁或历史不确定性。"})
	for _, channel := range config.OfficialChannelNames() {
		brand := officialBrands[channel]
		params := []parameter{choice("enabled", "Enable this channel", "启用本渠道", "no", "yes"), choice("language", "Notification language (independent of UI)", "通知语言（与界面独立）", "en", "zh"), choice("events_enabled", "Event notifications", "事件通知", "yes", "no"), choice("daily_enabled", "Daily summary notifications", "每日摘要通知", "yes", "no"), choice("min_severity", "Minimum event severity", "最低事件严重程度", "medium", "high", "critical"), {key: "daily_message_limit", en: "Messages per UTC calendar day (0 forbids sends)", zh: "每 UTC 日消息额度（0 禁止发送）", value: "20"}}
		if channel == "twilio_sms" {
			params = append(params, parameter{key: "daily_segment_limit", en: "Estimated SMS segments per UTC calendar day", zh: "每 UTC 日估算 SMS 分段额度", value: "40"}, choice("max_segments", "Maximum SMS segments per message", "每条 SMS 最大分段数", "2", "1"))
		}
		actions = append(actions, action{id: channel + "_setup", en: "Configure " + brand, zh: "配置 " + brand, params: params, mutation: true, confirmEN: "Apply this local notification policy? No test or platform request is sent.", confirmZH: "应用本地通知策略？不会发送测试或访问平台。", helpEN: "Configure protected credentials and record actual recipient consent before enabling. Zero is a sending prohibition, not an unlimited allowance. Paid daily summaries are off by default and tests share subscription and daily budgets. A policy change isolates pending bodies. Recording consent is not platform approval.", helpZH: "启用前先配置受保护凭据并记录接收者的真实同意。零表示禁止发送，不是无限额度。付费日报默认关闭；测试共用订阅和每日额度。修改策略会隔离未发正文。记录同意不代表平台审批。"})
		credentialParams := []parameter{choice("credential_action", "Credential snapshot action", "凭据快照操作", "keep", "replace", "clear")}
		for _, key := range config.OfficialCredentialFieldNames(channel) {
			label, ok := officialFieldLabels[key]
			if !ok {
				parts := strings.SplitN(key, "_", 2)
				kind := map[string][2]string{"event": {"Event", "事件"}, "daily": {"Daily summary", "日报摘要"}, "test": {"Test", "测试"}}[parts[0]]
				suffix := map[string][2]string{"name": {"approved template name", "已批准模板名称"}, "language": {"approved language code", "已批准语言代码"}, "parameters": {"ordered required BODY fields (comma-separated)", "必需 BODY 字段顺序（逗号分隔）"}}[parts[1]]
				label = [2]string{kind[0] + " " + suffix[0], kind[1] + suffix[1]}
			}
			credentialParams = append(credentialParams, secret(key, label[0]+" (hidden; blank retains)", label[1]+"（隐藏；留空保留）"))
		}
		credentialParams = append(credentialParams, p("clear_fields", "Explicitly clear field names (comma-separated)", "明确清空字段名（逗号分隔）"))
		actions = append(actions, action{id: channel + "_credentials", en: "Protected credentials: " + brand, zh: "受保护凭据：" + brand, params: credentialParams, mutation: true, confirmEN: "Write the reviewed protected snapshot? Unmodified fields are retained. Changed credentials isolate old pending bodies; no message is sent.", confirmZH: "写入已检查的受保护快照？未修改字段保持不变。修改凭据会隔离旧正文；不会发送消息。", helpEN: "Choose Replace to edit individual hidden fields; blank retains an existing field. Explicit clearing uses exact field names and cannot conflict with a replacement. Clear removes the entire snapshot reference only when disabled. Twilio uses api_key or auth_token and exactly one From/Messaging Service SID. WhatsApp uses v26.0, approved template languages and exactly one of each required BODY parameter: event_kind,phase,severity,time,bounded_summary,local_reference; host_alias is optional. Put them in the approved positional order. No empty mapping or free text fallback. Setup never sends a request.", helpZH: "选择替换后可逐项修改隐藏字段；留空保留已有字段。明确清空须填写准确字段名，不能同时填写替换值。整份清空仅允许在停用后移除快照引用。Twilio 使用 api_key 或 auth_token，From 与 Messaging Service SID 二选一。WhatsApp 使用 v26.0、已批准模板语言与各出现一次的必需 BODY 字段：event_kind,phase,severity,time,bounded_summary,local_reference；host_alias 可选，须按已批准模板的位置顺序排列，不允许空映射或自由文本回退。设置不会访问平台。"})
		subscriptionParams := []parameter{choice("consent_action", "Recipient consent action", "接收者同意操作", "keep", "record", "revoke"), p("purpose", "Explicit notification purpose (non-secret)", "明确通知用途（不含秘密）"), p("notification_types", "Consented types: event,daily,test", "已同意类型：event,daily,test"), p("evidence_ref", "Local consent evidence ID (no file content)", "本地同意依据编号（不读取文件正文）"), choice("cost_confirmed", "I confirm this subscription may incur charges", "确认此订阅可能产生费用", "no", "yes")}
		if channel == "twilio_sms" {
			subscriptionParams = append(subscriptionParams, choice("platform_recovery_confirmed", "Recipient completed the official opt-out recovery path", "接收者已完成官方退订恢复流程", "no", "yes"))
		}
		actions = append(actions, action{id: channel + "_subscription", en: "Recipient subscription: " + brand, zh: "接收者订阅：" + brand, params: subscriptionParams, mutation: true, confirmEN: "Record this explicit consent basis or revoke the subscription? This local declaration cannot replace actual recipient consent or platform authorization.", confirmZH: "记录明确同意依据或撤销订阅？本地声明不能替代真实接收者同意或平台授权。", helpEN: "Record requires a specific purpose, consented notification types and a bounded evidence ID. The local consent timestamp and immutable basis ID are created only by an explicit new record action. Keep preserves them; Revoke suppresses pending notifications. Paid tests need separate preview and charge confirmation. Twilio 21610 remains locked across credential changes and ordinary resume; only new recipient consent after the recipient completes the official recovery path can qualify for restoration. NodeRampart never sends START on the recipient's behalf.", helpZH: "记录新同意须填写具体用途、获同意的通知类型与有界依据编号；仅明确记录新同意时生成本地时间与不可变依据 ID。保留不会改变它们；撤销会抑制未发通知。付费测试另须预览并确认费用。Twilio 21610 锁不会因修改凭据或普通恢复而解除；仅接收者完成官方恢复后获得的新同意才可满足恢复条件。NodeRampart 不会代接收者发送 START。"})
		prefix := "notifications." + channel + "."
		labels := map[string][2]string{"enabled": {"Enabled", "启用"}, "credential_file": {"Protected credential file", "受保护凭据文件"}, "language": {"Notification language", "通知语言"}, "timeout": {"Request timeout", "请求超时"}, "events_enabled": {"Event notifications", "事件通知"}, "min_severity": {"Minimum event severity", "最低事件严重程度"}, "daily_enabled": {"Daily summaries", "每日摘要"}, "daily_message_limit": {"Daily message budget", "每日消息额度"}, "daily_segment_limit": {"Daily SMS segment budget", "每日 SMS 分段额度"}, "max_segments": {"Maximum SMS segments", "最大 SMS 分段数"}, "subscription.confirmed_at": {"Recorded consent time", "已记录同意时间"}, "subscription.purpose": {"Consent purpose", "同意用途"}, "subscription.notification_types": {"Consented notification types", "已同意通知类型"}, "subscription.evidence_ref": {"Consent evidence ID", "同意依据编号"}, "subscription.basis_id": {"Consent basis ID", "同意依据 ID"}, "subscription.revoked": {"Subscription revoked", "订阅已撤销"}, "subscription.cost_confirmed": {"Subscription cost confirmed", "已确认订阅费用"}, "subscription.platform_recovery_confirmed": {"Recipient official restoration declaration", "接收者官方恢复声明"}}
		for _, key := range []string{"enabled", "credential_file", "language", "timeout", "events_enabled", "min_severity", "daily_enabled", "daily_message_limit", "daily_segment_limit", "max_segments", "subscription.confirmed_at", "subscription.purpose", "subscription.notification_types", "subscription.evidence_ref", "subscription.basis_id", "subscription.revoked", "subscription.cost_confirmed", "subscription.platform_recovery_confirmed"} {
			label := labels[key]
			helpEN, helpZH := "Use channel setup and protected credential input. Zero forbids sending. Save never sends a test.", "请使用渠道设置与受保护凭据输入。零禁止发送；保存不会发送测试。"
			if strings.HasPrefix(key, "subscription.") {
				helpEN, helpZH = "Read-only here. Record or revoke actual consent through Recipient subscription; ordinary configuration editing cannot change the consent basis.", "此处只读。请通过接收者订阅记录或撤销真实同意；普通配置编辑不能改变同意依据。"
			}
			var choices []string
			if key == "language" {
				choices = []string{"en", "zh"}
			} else if key == "min_severity" {
				choices = []string{"medium", "high", "critical"}
			} else if key == "enabled" || key == "events_enabled" || key == "daily_enabled" {
				choices = yesNo
			}
			fields = append(fields, field{prefix + key, "notifications", brand + " " + label[0], brand + " " + label[1], helpEN, helpZH, choices})
		}
	}
}

func officialParameterValue(c config.OfficialChannelConfig, key, fallback string) string {
	booleans := map[string]bool{"enabled": c.Enabled, "events_enabled": c.EventsEnabled, "daily_enabled": c.DailyEnabled}
	if v, ok := booleans[key]; ok {
		if v {
			return "yes"
		}
		return "no"
	}
	switch key {
	case "language":
		return config.OfficialChannelLanguage(c)
	case "min_severity":
		return c.MinSeverity
	case "daily_message_limit":
		return strconv.Itoa(c.DailyMessageLimit)
	case "daily_segment_limit":
		return strconv.Itoa(c.DailySegmentLimit)
	case "max_segments":
		return strconv.Itoa(c.MaxSegments)
	}
	return fallback
}

func officialChoiceLabel(key, value, language string) string {
	labels := map[string][2]string{"yes": {"Yes", "是"}, "no": {"No", "否"}, "false": {"No", "否"}, "true": {"Yes", "是"}, "keep": {"Keep unchanged", "保留不变"}, "replace": {"Replace edited fields", "替换修改字段"}, "clear": {"Clear snapshot", "清空快照"}, "record": {"Record new consent", "记录新同意"}, "revoke": {"Revoke consent", "撤销同意"}, "medium": {"Medium", "中"}, "high": {"High", "高"}, "critical": {"Critical", "严重"}}
	if key == "enabled" {
		labels["yes"], labels["no"] = [2]string{"Enabled", "启用"}, [2]string{"Disabled", "停用"}
	}
	if label, ok := labels[value]; ok {
		return notificationText(label, language)
	}
	return value
}

var officialPreviewID = regexp.MustCompile(`^[0-9a-f]{64}$`)

func parsePaidPreview(text, channel, language string) (string, string, error) {
	invalid := errors.New("invalid paid notification preview")
	if len(text) > 32768 {
		return "", "", invalid
	}
	var v struct {
		Channel           string          `json:"channel"`
		Body              string          `json:"body"`
		Language          string          `json:"language"`
		FrozenRequest     json.RawMessage `json:"frozen_request"`
		EstimatedSegments *int            `json:"estimated_segments"`
		Encoding          string          `json:"encoding"`
		Cost              string          `json:"cost"`
		LocalDailyLimit   *int            `json:"daily_message_limit"`
		SegmentsLimit     *int            `json:"daily_segment_limit"`
		PreviewID         string          `json:"preview_id"`
		NetworkSent       *bool           `json:"network_sent"`
		Target            string          `json:"target"`
		NextReset         string          `json:"next_reset_utc"`
		Budget            *struct {
			Channel          string `json:"channel"`
			MessagesReserved *int   `json:"logical_messages_reserved"`
			SegmentsReserved *int   `json:"estimated_segments_reserved"`
			RestoreHold      *bool  `json:"restore_reconciliation_required"`
			OptedOut         *bool  `json:"opted_out"`
		} `json:"budget"`
	}
	if json.Unmarshal([]byte(text), &v) != nil || v.Channel != channel || !officialPreviewID.MatchString(v.PreviewID) || v.NetworkSent == nil || *v.NetworkSent || v.Language != "en" && v.Language != "zh" || len(v.Body) == 0 || len(v.Body) > 8192 || v.Cost != "unknown" || v.LocalDailyLimit == nil || *v.LocalDailyLimit < 0 || *v.LocalDailyLimit > 1000 || len(v.Target) == 0 || len(v.Target) > 128 {
		return "", "", invalid
	}
	reset, resetErr := time.Parse(time.RFC3339, v.NextReset)
	if resetErr != nil || !strings.HasSuffix(v.NextReset, "Z") || reset.Hour() != 0 || reset.Minute() != 0 || reset.Second() != 0 || v.Budget == nil || v.Budget.Channel != channel || v.Budget.MessagesReserved == nil || *v.Budget.MessagesReserved < 0 || *v.Budget.MessagesReserved > 1000 || v.Budget.SegmentsReserved == nil || *v.Budget.SegmentsReserved < 0 || *v.Budget.SegmentsReserved > 2000 || v.Budget.RestoreHold == nil || v.Budget.OptedOut == nil {
		return "", "", invalid
	}
	segments := "0"
	if v.EstimatedSegments != nil {
		if *v.EstimatedSegments < 0 || *v.EstimatedSegments > 2 {
			return "", "", invalid
		}
		segments = strconv.Itoa(*v.EstimatedSegments)
	}
	if channel == "twilio_sms" && (v.EstimatedSegments == nil || *v.EstimatedSegments < 1 || v.SegmentsLimit == nil || *v.SegmentsLimit < 0 || *v.SegmentsLimit > 2000 || v.Encoding != "gsm7" && v.Encoding != "ucs2") {
		return "", "", invalid
	}
	if len(v.FrozenRequest) == 0 || len(v.FrozenRequest) > 8192 {
		return "", "", invalid
	}
	var frozen struct {
		Channel     string          `json:"channel"`
		Kind        string          `json:"kind"`
		Fingerprint string          `json:"fingerprint"`
		Body        string          `json:"body"`
		Template    json.RawMessage `json:"template"`
	}
	if json.Unmarshal(v.FrozenRequest, &frozen) != nil || frozen.Channel != channel || frozen.Kind != "test" || !officialPreviewID.MatchString(frozen.Fingerprint) || channel == "twilio_sms" && frozen.Body != v.Body {
		return "", "", invalid
	}
	var templateText string
	if channel == "whatsapp_cloud" {
		var template struct {
			Name     string   `json:"name"`
			Language string   `json:"language"`
			Slots    []string `json:"slots"`
			Values   []string `json:"values"`
		}
		if json.Unmarshal(frozen.Template, &template) != nil || len(template.Name) == 0 || len(template.Name) > 128 || len(template.Language) > 8 || len(template.Slots) > 7 || len(template.Slots) != len(template.Values) {
			return "", "", invalid
		}
		if config.ValidateOfficialTemplate(&config.OfficialTemplate{Name: template.Name, Language: template.Language, Parameters: template.Slots}) != nil {
			return "", "", invalid
		}
		for _, value := range template.Values {
			if len(value) > 1024 || cleanText(value) != value {
				return "", "", invalid
			}
		}
		encoded, _ := json.Marshal(template)
		templateText = string(encoded)
	}
	smsEN, smsZH := "", ""
	if channel == "twilio_sms" {
		encoding := map[string]string{"gsm7": "GSM-7", "ucs2": "UCS-2"}[v.Encoding]
		smsEN = fmt.Sprintf("Estimated SMS segments: %s (%s)\nEstimated SMS segments per UTC calendar day: %d; reserved: %d; remaining: %d\n", segments, encoding, *v.SegmentsLimit, *v.Budget.SegmentsReserved, max(0, *v.SegmentsLimit-*v.Budget.SegmentsReserved))
		smsZH = fmt.Sprintf("估算 SMS 分段：%s（%s）\n每 UTC 日估算 SMS 分段额度：%d；已预留：%d；剩余：%d\n", segments, encoding, *v.SegmentsLimit, *v.Budget.SegmentsReserved, max(0, *v.SegmentsLimit-*v.Budget.SegmentsReserved))
	}
	costEN, costZH := "Platform charges are not measured locally. Local limits do not replace platform quotas or plans.", "本地不测算平台费用。本地限额不能替代平台额度或套餐。"
	if config.IsPaidOfficialChannel(channel) {
		costEN, costZH = "Actual charge: unknown; this request may incur charges.", "实际费用：未知；发送可能收费。"
	}
	if language == "zh" {
		return fmt.Sprintf("目标：%s\n通知语言：%s\n通知正文或本地摘要：\n%s\n已批准模板及实际参数：%s\n%s每 UTC 日消息额度：%d（0 禁止发送）；已预留：%d；剩余：%d\n下次 UTC 日重置：%s\n付费恢复核对暂停：%s；接收者退订锁：%s\n%s 此测试共用订阅、额度与有效期；预览没有发送网络请求。", v.Target, v.Language, v.Body, templateText, smsZH, *v.LocalDailyLimit, *v.Budget.MessagesReserved, max(0, *v.LocalDailyLimit-*v.Budget.MessagesReserved), v.NextReset, officialChoiceLabel("", strconv.FormatBool(*v.Budget.RestoreHold), language), officialChoiceLabel("", strconv.FormatBool(*v.Budget.OptedOut), language), costZH), v.PreviewID, nil
	}
	return fmt.Sprintf("Target: %s\nNotification language: %s\nActual notification body or local summary:\n%s\nApproved template and actual parameters: %s\n%sMessages per UTC calendar day: %d (0 forbids sends); reserved: %d; remaining: %d\nNext UTC-day reset: %s\nPaid restore reconciliation hold: %t; recipient opt-out lock: %t\n%s The test shares consent, budgets and expiry. Preview made no network request.", v.Target, v.Language, v.Body, templateText, smsEN, *v.LocalDailyLimit, *v.Budget.MessagesReserved, max(0, *v.LocalDailyLimit-*v.Budget.MessagesReserved), v.NextReset, *v.Budget.RestoreHold, *v.Budget.OptedOut, costEN), v.PreviewID, nil
}

func (u *ui) previewPaidTest(a action, args map[string]string, back func()) {
	channel, language := args["channel"], u.lang
	u.background(u.tr("Preview paid test", "预览付费测试"), func(ctx context.Context) func() {
		text, err := u.backend.Action(ctx, "notify_preview", map[string]string{"channel": channel})
		preview, id, parseErr := parsePaidPreview(text, channel, language)
		return func() {
			if err != nil || parseErr != nil {
				clearArguments(args)
				u.output(u.tr("Preview unavailable", "预览不可用"), u.tr("The paid test was not sent. Check subscription, credentials and limits; no raw error is shown.", "未发送付费测试。请检查订阅、凭据与限额；不显示原始错误。"), back)
				return
			}
			u.confirm(html.EscapeString(preview)+u.tr("\nConfirm this paid test request?", "\n确认发送此付费测试请求？"), func() { args["confirm_paid"] = "yes"; args["preview_id"] = id; u.executeAction(a, args, back) }, func() { clearArguments(args); back() })
		}
	}, func() { clearArguments(args); back() })
}

func (u *ui) showOfficialPreview(a action, args map[string]string, back func()) {
	channel, language := args["channel"], u.lang
	u.background(u.tr(a.en, a.zh), func(ctx context.Context) func() {
		text, err := u.backend.Action(ctx, "notify_preview", args)
		clearArguments(args)
		preview, _, parseErr := parsePaidPreview(text, channel, language)
		return func() {
			if err != nil || parseErr != nil {
				u.output(u.tr("Preview unavailable", "预览不可用"), u.tr("No notification was sent. Inspect local subscription, credentials and budgets; no raw error is shown.", "未发送通知。请检查本地订阅、凭据与额度；不显示原始错误。"), back)
				return
			}
			u.output(u.tr(a.en, a.zh), html.EscapeString(preview), back)
		}
	}, func() { clearArguments(args); back() })
}
