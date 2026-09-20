// SPDX-License-Identifier: MIT

package console

import (
	"context"
	"strings"

	"github.com/littlesho/NodeRampart/internal/assets"
	"github.com/rivo/tview"
)

type parameter struct {
	key, en, zh, value string
	choices            []string
	secret, multiline  bool
}
type action struct {
	id, en, zh, helpEN, helpZH string
	params                     []parameter
	confirmEN, confirmZH       string
	mutation                   bool
}

func p(key, en, zh string) parameter      { return parameter{key: key, en: en, zh: zh} }
func secret(key, en, zh string) parameter { v := p(key, en, zh); v.secret = true; return v }
func choice(key, en, zh string, values ...string) parameter {
	v := p(key, en, zh)
	v.choices = values
	v.value = values[0]
	return v
}

var actions = []action{
	{id: "status", en: "Current status", zh: "当前状态"},
	{id: "health", en: "Last 24 hours: coverage and traffic", zh: "最近 24 小时：完整性与流量"},
	{id: "doctor", en: "Local diagnosis", zh: "本机诊断"},
	{id: "alerts_status", en: "Budget and health alerts", zh: "预算与健康告警", helpEN: "Current checks, durable milestones and pending transitions. Configure alerts in Configuration; Telegram delivery needs notifications enabled.", helpZH: "查看当前检查、已记录阈值和待保存状态；在功能配置中启用告警，推送需启用 Telegram。"},
	{id: "retention", en: "Data retention and pruning", zh: "数据保留与裁剪", helpEN: "See affected periods, removal reasons and remaining data. Earlier history before tracking began remains unknown.", helpZH: "查看受影响时间、裁剪原因及剩余数据；台账启用前的裁剪历史仍为未知。"},
	{id: "evidence_export", en: "Export local diagnostic evidence", zh: "导出本地诊断证据", helpEN: "Creates a new file in a private directory. ZIP includes offline HTML and JSON. No upload or message is sent; UTC timing and counts can remain linkable. Blank incident exports diagnostic history.", helpZH: "在私有目录创建新文件；ZIP 包含离线 HTML 和 JSON。不会上传或发消息，UTC 时间和数量仍可能被关联。Incident 留空导出诊断记录。", params: []parameter{p("output", "New output file", "新输出文件"), choice("format", "File format", "文件格式", "zip", "html", "json"), p("incident", "Incident ID (optional)", "Incident ID（可选）"), p("since", "From RFC3339 time (optional)", "开始时间 RFC3339（可选）"), p("until", "Until RFC3339 time (optional)", "结束时间 RFC3339（可选）")}},
	{id: "report_now", en: "Current report", zh: "当前报告", helpEN: "A local report for the last 24 hours. Does not send Telegram.", helpZH: "生成最近 24 小时的本地报告；不发送 Telegram。"},
	{id: "report_list", en: "Saved daily reports", zh: "已保存的日报"},
	{id: "report_show", en: "Read a saved report", zh: "查看指定日报", helpEN: "Use a completed date in YYYY-MM-DD format.", helpZH: "使用 YYYY-MM-DD 格式的日期。", params: []parameter{p("date", "Report date (YYYY-MM-DD)", "报告日期（YYYY-MM-DD）")}},
	{id: "report_backfill", en: "Fill missing daily reports", zh: "补齐缺失日报", helpEN: "At most 31 completed dates. Historical reports stay local. Existing archives are not overwritten.", helpZH: "最多 31 个已结束日期；补报保存在本地，不覆盖已有存档。", params: []parameter{p("from", "First date (YYYY-MM-DD)", "开始日期（YYYY-MM-DD）"), p("through", "Last date (YYYY-MM-DD)", "结束日期（YYYY-MM-DD）")}, confirmEN: "Create the missing local reports for these dates?", confirmZH: "为这些日期生成缺失的本地日报？", mutation: true},
	{id: "incident_list", en: "Recent incidents", zh: "最近的 Incident"},
	{id: "incident_show", en: "Incident details", zh: "Incident 详情", params: []parameter{p("id", "Incident ID", "Incident ID")}},
	{id: "timeline", en: "Event timeline", zh: "事件时间线"},
	{id: "notify_status", en: "Notification delivery status", zh: "通知投递状态"},
	{id: "notify_list", en: "Notification messages", zh: "通知消息列表"},
	{id: "notify_test", en: "Send a test notification", zh: "发送测试通知", confirmEN: "Queue a real Telegram test message to your configured chat?", confirmZH: "向已配置会话发送一条真实 Telegram 测试通知？", mutation: true},
	{id: "notify_retry", en: "Retry a notification", zh: "重试通知", params: []parameter{p("id", "Message ID", "消息 ID")}, confirmEN: "Retry this notification? It may be delivered again.", confirmZH: "重试这条通知？可能再次投递。", mutation: true},
	{id: "notify_quarantine", en: "Quarantine a notification", zh: "隔离通知", params: []parameter{p("id", "Message ID", "消息 ID")}, confirmEN: "Stop retries for this notification?", confirmZH: "停止这条通知的重试？", mutation: true},
	{id: "notify_resume", en: "Resume a destination", zh: "恢复通知目标", params: []parameter{{key: "destination", en: "Destination", zh: "通知目标", value: "telegram"}}, confirmEN: "Resume delivery attempts to this destination?", confirmZH: "恢复向此目标投递通知？", mutation: true},
	{id: "silence_list", en: "Active and retained silences", zh: "当前与保留的静默规则"},
	{id: "silence_add", en: "Add an expiring silence", zh: "添加到期静默", helpEN: "Choose an incident, event kind, or both. Expiry must be within 7 days; use RFC3339, for example 2026-09-13T12:00:00+08:00.", helpZH: "选择 Incident、事件类型或两者；到期须在 7 天内，使用 RFC3339 格式，例如 2026-09-13T12:00:00+08:00。", params: []parameter{p("incident", "Incident ID (optional)", "Incident ID（可选）"), p("kind", "Event kind (optional)", "事件类型（可选）"), p("until", "Expiry with timezone", "到期时间（含时区）"), p("reason", "Reason (optional)", "原因（可选）")}, confirmEN: "Silence matching event notifications until this expiry? Stored events remain available.", confirmZH: "在到期前静默匹配的事件通知？事件记录仍会保留。", mutation: true},
	{id: "silence_remove", en: "Revoke a silence", zh: "撤销静默", params: []parameter{p("id", "Silence ID", "静默 ID")}, confirmEN: "Revoke this silence? Future notifications become eligible; old notifications are not replayed.", confirmZH: "撤销此静默？未来通知恢复正常；不会补发旧通知。", mutation: true},
	{id: "backup_create", en: "Create a database backup", zh: "创建数据库备份", helpEN: "Choose a new file under /var/lib/noderampart/backups. The daemon creates a consistent snapshot.", helpZH: "在 /var/lib/noderampart/backups 下指定新文件；守护进程创建一致快照。", params: []parameter{p("output", "New backup file", "新备份文件")}, confirmEN: "Create this local database backup?", confirmZH: "创建此本地数据库备份？", mutation: true},
	{id: "backup_verify", en: "Verify a backup", zh: "验证备份", params: []parameter{p("input", "Backup file", "备份文件")}},
	{id: "replay_anonymize", en: "Anonymize replay metadata", zh: "脱敏回放元数据", helpEN: "Offline metadata only. Output must be a new file in a private directory.", helpZH: "仅处理离线元数据；输出须为私有目录中的新文件。", params: []parameter{p("input", "Metadata input file", "元数据输入文件"), p("output", "New anonymized output", "新的脱敏输出文件")}, confirmEN: "Create the anonymized metadata file? Timing and traffic patterns can remain linkable.", confirmZH: "生成脱敏元数据文件？时序与流量行为仍可能可关联。", mutation: true},
	{id: "replay_compare", en: "Compare detection thresholds", zh: "比较检测阈值", helpEN: "Use anonymized metadata and two configuration files. No traffic or notifications are generated.", helpZH: "使用脱敏元数据和两份配置；不会生成网络流量或通知。", params: []parameter{p("input", "Anonymized metadata", "脱敏元数据"), p("baseline", "Baseline configuration", "基线配置"), p("candidate", "Candidate configuration", "候选配置")}},
	{id: "service_start", en: "Start configured services", zh: "启动已配置服务", confirmEN: "Enable and start the configured NodeRampart services?", confirmZH: "启用并启动已配置的 NodeRampart 服务？", mutation: true},
	{id: "service_stop", en: "Stop services", zh: "停止服务", confirmEN: "Stop observation now? The stopped interval will have no collected data.", confirmZH: "现在停止观察？停止期间不会采集数据。", mutation: true},
	{id: "service_restart", en: "Restart services", zh: "重启服务", confirmEN: "Restart the NodeRampart services? Observation may briefly pause.", confirmZH: "重启 NodeRampart 服务？观察可能短暂停顿。", mutation: true},
	{id: "geo_status", en: "Local GeoIP status", zh: "本地 GeoIP 状态"},
	{id: "geo_download", en: "Set up local GeoIP downloads", zh: "设置本地 GeoIP 下载", helpEN: "Optional. Enroll with your own account: https://www.maxmind.com/en/geolite2/signup\nReview: https://www.maxmind.com/en/geolite/eula\nAccount ID and License Key are hidden. Choose Cancel / Skip to continue without GeoIP.", helpZH: "可选。使用自己的账户注册：https://www.maxmind.com/en/geolite2/signup\n请阅读条款：https://www.maxmind.com/en/geolite/eula\nAccount ID 与 License Key 均隐藏输入；可取消/跳过并继续使用。", params: []parameter{secret("account_id", "MaxMind Account ID", "MaxMind Account ID"), secret("license_key", "MaxMind License Key", "MaxMind License Key"), choice("accepted_terms", "I have accepted the GeoLite terms", "我已接受 GeoLite 条款", "no", "yes"), choice("auto_update", "Enable daily database updates", "启用每日数据库更新", "no", "yes")}, confirmEN: "Download GeoLite2 City and ASN using your account and apply the validated local databases?", confirmZH: "使用您的账户下载 GeoLite2 City 与 ASN，并应用经过验证的本地数据库？", mutation: true},
	{id: "geo_refresh", en: "Refresh local GeoIP databases", zh: "更新本地 GeoIP 数据库", confirmEN: "Contact MaxMind using your stored credentials and refresh the local databases?", confirmZH: "使用已保存的凭据连接 MaxMind 并更新本地数据库？", mutation: true},
	{id: "geo_schedule", en: "Daily GeoIP updates", zh: "GeoIP 每日更新", params: []parameter{choice("enabled", "Daily update schedule", "每日更新计划", "no", "yes")}, confirmEN: "Apply this daily database update schedule?", confirmZH: "应用此每日数据库更新计划？", mutation: true},
	{id: "telegram_setup", en: "Set up Telegram", zh: "设置 Telegram", helpEN: "Create a bot using @BotFather, then start a private chat or add it to your group. Enter the chat ID yourself. Blank token keeps an existing configured token. Setup does not send messages; use Send test separately.", helpZH: "使用 @BotFather 创建 Bot，然后发起私人会话或加入群组，自行填写 Chat ID。Token 留空保留已配置的 Token。设置不会发送消息；测试通知需单独选择。", params: []parameter{secret("token", "Bot token (hidden)", "Bot Token（隐藏）"), p("chat_id", "Chat ID", "Chat ID"), choice("enabled", "Enable Telegram notifications", "启用 Telegram 通知", "yes", "no")}, confirmEN: "Save the Telegram settings? No test message will be sent.", confirmZH: "保存 Telegram 设置？不会发送测试消息。", mutation: true},
	{id: "privacy_key_generate", en: "Generate a privacy hash key", zh: "生成隐私哈希密钥", confirmEN: "Generate and configure a local privacy key? A new key changes future hashed identifiers; existing history is not rewritten.", confirmZH: "生成并配置本地隐私密钥？新密钥改变未来的哈希标识；不会改写历史记录。", mutation: true},
	{id: "prices_regions", en: "Available cloud regions", zh: "可用云区域", helpEN: "Public pricing regions and OCI geographic groups; no account discovery.", helpZH: "公开价格区域与 OCI 地理分组；不会查询账户资源。"},
	{id: "prices_fetch", en: "Fetch official egress tariffs", zh: "获取官方出站价格", helpEN: "Enter the region shown in Available cloud regions. Assign this host's free allowance for the whole month after accounting for other hosts/services. Do not subtract this host's already observed usage again. The byte unit is a calculation assumption, not a verified provider meter. Requires Internet access.", helpZH: "填写“可用云区域”显示的区域。免费额度是扣除其他主机/服务分配后，分配给本机整个本月的份额；不要再次扣除本机已观测用量。字节单位仅为计算假设，不代表已验证的厂商计费口径。需要联网。", params: []parameter{choice("provider", "Cloud provider", "云厂商", "aws", "oci"), p("region", "AWS region / OCI group", "AWS 区域 / OCI 分组"), {key: "free_gb", en: "Monthly free allowance assigned to this host", zh: "分配给本机本月的免费额度", value: "0"}, choice("unit_bytes", "Bytes per tariff GB (assumption)", "每价格 GB 的字节数（假设）", "1073741824", "1000000000")}, confirmEN: "Fetch public tariffs and configure the selected egress estimate? No cloud credentials are used.", confirmZH: "获取公开价格并配置所选出站费用估算？不使用云账户凭据。", mutation: true},
	{id: "prices_show", en: "Current month usage and cost estimate", zh: "本月流量与费用估算", helpEN: "Observed guest traffic and cached public tariffs. Not a cloud invoice.", helpZH: "已观测的来宾流量与缓存公开价格；不代表云账单。"},
	{id: "billing_profile_save", en: "Custom billing profile (advanced)", zh: "自定义费用配置（高级）", helpEN: "Advanced JSON profile: schema_version, name, provider, source_region, currency, effective_date, source_url, free_gb, optional unit_bytes, internet_egress tiers. Save requires complete validation; use official tariff setup for ordinary use.", helpZH: "高级 JSON 配置：schema_version、name、provider、source_region、currency、effective_date、source_url、free_gb、可选 unit_bytes、internet_egress 阶梯。保存需完整验证；日常使用建议通过官方价格设置。", params: []parameter{{key: "profile_json", en: "Billing profile JSON", zh: "费用配置 JSON", multiline: true}}, confirmEN: "Validate and apply this custom billing profile? You are responsible for its source and tariff values.", confirmZH: "验证并应用此自定义费用配置？请确认来源与费率正确。", mutation: true},
	{id: "uninstall", en: "Uninstall, keep configuration and data", zh: "卸载并保留配置与数据", helpEN: "Stops observation and removes NodeRampart. Configuration, databases and credentials are retained. Type REMOVE to continue.", helpZH: "停止观察并卸载 NodeRampart，保留配置、数据库与凭据。请输入 REMOVE 继续。", params: []parameter{p("confirm", "Type REMOVE", "输入 REMOVE")}, confirmEN: "Remove NodeRampart while keeping your configuration and data?", confirmZH: "卸载 NodeRampart 并保留配置和数据？", mutation: true},
	{id: "purge", en: "Uninstall and delete all managed data", zh: "卸载并删除所有托管数据", helpEN: "Permanently deletes managed configuration, databases, credentials and service accounts. Back up needed records first. Type PURGE to continue.", helpZH: "永久删除托管配置、数据库、凭据和服务账户。请先备份所需记录。输入 PURGE 继续。", params: []parameter{p("confirm", "Type PURGE", "输入 PURGE")}, confirmEN: "Permanently delete NodeRampart's managed configuration, credentials and collected data?", confirmZH: "永久删除 NodeRampart 的托管配置、凭据和已采集数据？", mutation: true},
	{id: "recover_config", en: "Recover previous configuration", zh: "恢复之前的配置", helpEN: "Restores the retained configuration when safe. Does not downgrade or replace a database. Type RESTORE.", helpZH: "在安全条件下恢复保留配置；不会降级或替换数据库。请输入 RESTORE。", params: []parameter{p("confirm", "Type RESTORE", "输入 RESTORE")}, confirmEN: "Recover the previous configuration and its service state?", confirmZH: "恢复之前的配置与对应服务状态？", mutation: true},
}

func findAction(id string) (action, bool) {
	for _, a := range actions {
		if a.id == id {
			switch id {
			case "report_list", "notify_list":
				cursor := p("before", "Earlier than date (optional)", "早于日期（可选）")
				if id == "notify_list" {
					cursor = p("before", "Before message ID (optional)", "早于消息 ID（可选）")
				}
				a.params = []parameter{cursor, {key: "limit", en: "Records (1–100)", zh: "条数（1–100）", value: "20"}}
			case "health":
				a.en, a.zh = "Coverage and traffic for a period", "指定时间段的完整性与流量"
				a.helpEN = "Leave dates blank for the last 24 hours, or enter RFC3339 timestamps up to 400 days apart. Retain the original time range when continuing coverage segments by offset."
				a.helpZH = "日期留空查询最近 24 小时；也可填写相隔最多 400 天的 RFC3339 时间。使用 offset 继续查询覆盖分段时，须保留原时间范围。"
				a.params = []parameter{p("since", "From time (optional)", "开始时间（可选）"), p("until", "Until time (optional)", "结束时间（可选）"), {key: "limit", en: "Segments (1–100)", zh: "分段数（1–100）", value: "100"}, {key: "offset", en: "Segment offset", zh: "分段偏移量", value: "0"}}
			case "retention":
				a.params = []parameter{p("since", "Affected data from (optional)", "受影响数据开始时间（可选）"), p("until", "Affected data until (optional)", "受影响数据结束时间（可选）"),
					choice("dataset", "Dataset (blank means all)", "数据类型（留空表示全部）", "", "events", "traffic_hourly", "auth_hourly", "interface_hourly", "interface_detail_hourly", "collector_health_hourly", "report_snapshots", "coverage_intervals", "coverage_gaps", "notification_outbox", "notification_silences"),
					choice("reason", "Reason (blank means all)", "原因（留空表示全部）", "", "time_expiry", "storage_pressure", "cardinality_compaction", "capacity_eviction", "silence_body_discard"),
					{key: "limit", en: "Entries (1–100)", zh: "条数（1–100）", value: "20"}, p("before_id", "Earlier cursor (optional)", "更早游标（可选）")}
			case "incident_list", "timeline", "incident_show":
				a.helpEN = "Leave dates blank for the last 7 days. Select entries for details; page navigation keeps this time range. Optional cursors can resume an earlier query."
				a.helpZH = "日期留空查询最近 7 天；选择条目查看详情，翻页会保持时间范围。可选游标用于恢复之前的查询。"
				a.params = []parameter{p("since", "From time (optional)", "开始时间（可选）"), p("until", "Until time (optional)", "结束时间（可选）")}
				if id == "incident_list" {
					a.params = append(a.params, p("before", "Before cursor time", "之前游标时间"), p("before_id", "Before cursor ID", "之前游标 ID"))
				} else {
					a.params = append(a.params, p("after", "After cursor time", "之后游标时间"), p("after_id", "After cursor ID", "之后游标 ID"))
				}
				if id == "incident_show" {
					a.params = append([]parameter{p("id", "Incident ID", "Incident ID")}, a.params...)
				}
				a.params = append(a.params, parameter{key: "limit", en: "Records (1–100)", zh: "条数（1–100）", value: "20"})
			}
			return a, true
		}
	}
	return action{}, false
}

func (u *ui) actionMenu(en, zh string, ids []string, back func()) {
	items := []menuItem{}
	for _, id := range ids {
		a, ok := findAction(id)
		if !ok {
			continue
		}
		items = append(items, menuItem{a.en, a.zh, a.helpEN, a.helpZH, func() { u.openAction(a.id, func() { u.actionMenu(en, zh, ids, back) }) }})
	}
	u.menu(en, zh, items, back)
}
func (u *ui) health() {
	u.actionMenu("Health and diagnosis", "完整性与诊断", []string{"health", "doctor", "alerts_status", "retention", "evidence_export"}, u.home)
}
func (u *ui) reports() {
	u.actionMenu("Reports", "报告", []string{"report_now", "report_list", "report_show", "report_backfill"}, u.home)
}
func (u *ui) incidents() {
	u.actionMenu("Events and incidents", "事件与 Incident", []string{"incident_list", "incident_show", "timeline"}, u.home)
}
func (u *ui) notifications() {
	u.actionMenu("Telegram and notifications", "Telegram 与通知", []string{"telegram_setup", "notify_status", "notify_list", "notify_test", "notify_retry", "notify_quarantine", "notify_resume", "silence_list", "silence_add", "silence_remove"}, u.home)
}
func (u *ui) geo() {
	u.actionMenu("Local GeoIP", "本地 GeoIP", []string{"geo_status", "geo_download", "geo_refresh", "geo_schedule"}, u.home)
}
func (u *ui) prices() {
	items := []menuItem{}
	for _, id := range []string{"prices_regions", "prices_fetch", "prices_show"} {
		a, _ := findAction(id)
		items = append(items, menuItem{a.en, a.zh, a.helpEN, a.helpZH, func() { u.openAction(a.id, u.prices) }})
	}
	items = append(items, menuItem{"Create a custom tariff", "创建自定义价格", "Edit ordinary fields and price tiers without JSON.", "通过普通字段与价格档位设置，无需编写 JSON。", u.newCustomProfile},
		menuItem{"Import a custom tariff (advanced JSON)", "导入自定义价格（高级 JSON）", "Paste a complete existing profile.", "粘贴完整的已有费用配置。", func() { u.openAction("billing_profile_save", u.prices) }})
	u.menu("Cloud egress estimates", "云出站费用估算", items, u.home)
}
func (u *ui) tools() {
	u.actionMenu("Backup, replay and privacy", "备份、回放与隐私", []string{"backup_create", "backup_verify", "replay_anonymize", "replay_compare", "privacy_key_generate", "evidence_export"}, u.home)
}
func (u *ui) services() {
	u.actionMenu("Services and uninstall", "服务与卸载", []string{"service_start", "service_stop", "service_restart", "recover_config", "uninstall", "purge"}, u.home)
}

func (u *ui) openAction(id string, back func()) {
	a, ok := findAction(id)
	if !ok {
		return
	}
	if a.mutation && u.dirty {
		u.output(u.tr("Save or discard the configuration draft first", "请先保存或放弃配置草稿"), u.tr("This action can change settings or services. Return to Configuration and save or discard the unsaved draft first.", "此操作可能更改配置或服务。请先返回功能配置，保存或放弃未保存草稿。"), u.configuration)
		return
	}
	if id == "prices_fetch" {
		u.priceProvider(back)
		return
	}
	u.actionForm(a, back)
}

func (u *ui) actionForm(a action, back func()) {
	if len(a.params) == 0 {
		u.prepareAction(a, map[string]string{}, back)
		return
	}
	form := tview.NewForm()
	readers := map[string]func() string{}
	clearers := []func(){}
	for _, definition := range a.params {
		param := definition
		label := u.tr(param.en, param.zh)
		if len(param.choices) > 0 {
			selected := 0
			for i, value := range param.choices {
				if value == param.value {
					selected = i
					break
				}
			}
			labels := append([]string(nil), param.choices...)
			if a.id == "retention" {
				for i, value := range labels {
					labels[i] = retentionLabel(param.key, value, u.lang)
				}
			}
			form.AddDropDown(label, labels, selected, func(_ string, index int) { selected = index })
			readers[param.key] = func() string { return param.choices[selected] }
		} else if param.multiline {
			area := tview.NewTextArea().SetPlaceholder(u.tr("Paste a complete custom profile here", "在此粘贴完整自定义配置")).SetSize(8, 50).SetMaxLength(256 << 10)
			area.SetLabel(label)
			form.AddFormItem(area)
			readers[param.key] = area.GetText
			clearers = append(clearers, func() { area.SetText("", false) })
		} else {
			input := tview.NewInputField().SetLabel(label).SetFieldWidth(40)
			if param.secret {
				input.SetMaskCharacter('•')
			} else {
				input.SetText(param.value)
			}
			limit := 4096
			if param.secret {
				limit = 512
			}
			input.SetAcceptanceFunc(func(text string, _ rune) bool { return len(text) <= limit })
			form.AddFormItem(input)
			readers[param.key] = input.GetText
			clearers = append(clearers, func() { input.SetText("") })
		}
	}
	clear := func() {
		for _, reset := range clearers {
			reset()
		}
	}
	cancel := func() { clear(); back() }
	form.AddButton(u.tr("Continue", "继续"), func() {
		args := map[string]string{}
		for key, read := range readers {
			args[key] = read()
		}
		clear()
		u.prepareAction(a, args, back)
	})
	cancelLabel := u.tr("Cancel", "取消")
	if a.id == "geo_download" {
		cancelLabel = u.tr("Cancel / Skip", "取消 / 跳过")
	}
	form.AddButton(cancelLabel, cancel)
	if a.helpEN != "" {
		form.AddButton(u.tr("Read full help", "查看完整说明"), func() {
			clear()
			u.output(u.tr(a.en, a.zh), u.tr(a.helpEN, a.helpZH), func() { u.actionForm(a, back) })
		})
	}
	help := tview.NewTextView().SetDynamicColors(false).SetText(u.tr(a.helpEN, a.helpZH))
	flex := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(help, 7, 0, false).AddItem(form, 0, 1, true)
	u.root(u.tr(a.en, a.zh), flex, cancel)
}

func clearArguments(args map[string]string) {
	for key := range args {
		args[key] = ""
		delete(args, key)
	}
}

func (u *ui) prepareAction(a action, args map[string]string, back func()) {
	if a.id == "geo_download" && args["accepted_terms"] != "yes" {
		clearArguments(args)
		u.output(u.tr(a.en, a.zh), u.tr("Accept the official GeoLite terms yourself before downloading, or skip this optional feature.", "下载前请自行接受官方 GeoLite 条款，或跳过此可选功能。"), back)
		return
	}
	for id, word := range map[string]string{"uninstall": "REMOVE", "purge": "PURGE", "recover_config": "RESTORE"} {
		if a.id == id && args["confirm"] != word {
			clearArguments(args)
			u.output(u.tr(a.en, a.zh), u.tr("The confirmation word did not match. No action was taken.", "确认文字不匹配，未执行操作。"), back)
			return
		}
	}
	proceed := func() { u.executeAction(a, args, back) }
	if a.confirmEN != "" {
		u.confirm(u.tr(a.confirmEN, a.confirmZH), proceed, func() { clearArguments(args); back() })
	} else {
		proceed()
	}
}

func (u *ui) executeAction(a action, args map[string]string, back func()) {
	if u.busy {
		clearArguments(args)
		u.output(u.tr(a.en, a.zh), u.tr("An operation is still finishing. Please wait.", "仍有操作正在结束，请稍候。"), back)
		return
	}
	if browsable(a.id) {
		u.startBrowser(a, args, back)
		return
	}
	secrets := []string{}
	sensitive := false
	for _, p := range a.params {
		if p.secret {
			sensitive = true
		}
		if p.secret && args[p.key] != "" {
			secrets = append(secrets, args[p.key])
		}
	}
	// Refresh also uses credentials, although it has no secret input fields.
	protectedFailure := sensitive || a.id == "geo_download" || a.id == "geo_refresh"
	language := u.lang
	secretFailure := u.tr("The operation failed or was cancelled. Check the entered credentials, connectivity and local permissions. Secret values are not shown.", "操作失败或已取消。请检查凭据、网络连接与本机权限；不会显示凭据内容。")
	secretSuccess := u.tr("Settings were applied successfully. Use the status menu to inspect the result. No secret values are displayed.", "设置已成功应用。可在状态菜单查看结果；不会显示凭据内容。")
	u.background(u.tr(a.en, a.zh), func(ctx context.Context) func() {
		text, err := u.backend.Action(ctx, a.id, args)
		clearArguments(args)
		if err != nil {
			if protectedFailure {
				text = secretFailure
			}
			if diagnostic, ok := geoValidationFailure(a.id, err, language); ok {
				text = diagnostic
			}
		} else if sensitive {
			text = secretSuccess
		}
		for _, value := range secrets {
			text = strings.ReplaceAll(text, value, "[redacted]")
		}
		clear(secrets)
		return func() {
			if a.mutation {
				u.loaded = false
			}
			title := u.tr(a.en, a.zh)
			if err != nil {
				title += u.tr(" — not completed", " — 未完成")
				if !protectedFailure {
					if text != "" {
						text = err.Error() + "\n\n" + humanResult(text, u.lang)
					} else {
						text = err.Error()
					}
				}
			}
			if err == nil && !sensitive {
				text = humanResult(text, u.lang)
			}
			u.output(title, text, back)
		}
	}, back)
}

// Only the typed assets boundary may supply these allowlisted fields. Never
// display a transport, wrapped URL, response body or arbitrary error chain here.
func geoValidationFailure(actionID string, err error, language string) (string, bool) {
	if actionID != "geo_download" && actionID != "geo_refresh" {
		return "", false
	}
	edition, reason, ok := assets.GeoValidationDiagnostic(err)
	if !ok {
		return "", false
	}
	if language == "zh" {
		if reason == "resource_budget" {
			return edition + "：MMDB 校验失败，校验资源预算已耗尽。此结果不是凭据认证失败。", true
		}
		return edition + "：MMDB 校验未通过，未应用下载的数据库。", true
	}
	if reason == "resource_budget" {
		return edition + ": MMDB validation failed: validation resource budget exceeded. This is not a credential authentication failure.", true
	}
	return edition + ": MMDB validation rejected the database. The downloaded databases were not applied.", true
}
