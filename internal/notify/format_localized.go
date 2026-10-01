// SPDX-License-Identifier: MIT

package notify

import (
	"fmt"
	"html"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/littlesho/NodeRampart/internal/model"
	"github.com/littlesho/NodeRampart/internal/timezones"
	"golang.org/x/text/language"
	"golang.org/x/text/language/display"
)

const eventBodyBytes = 4096 - 128 // The store may append its bounded coalescing suffix.

type eventText struct{ en, zh string }

func (t eventText) local(zh bool) string {
	if zh {
		return t.zh
	}
	return t.en
}

func localText(zh bool, en, chinese string) string { return (eventText{en, chinese}).local(zh) }

// These are renderer categories, not translated machine identifiers. New
// producer categories must get a deliberate template or the explicit fallback.
var eventTitles = map[string]eventText{
	"ssh_login_success":        {"SSH login success", "SSH 登录成功"},
	"ssh_brute_force":          {"SSH authentication failures", "SSH 认证失败告警"},
	"syn_flood":                {"SYN rate alert", "SYN 速率告警"},
	"udp_flood":                {"UDP rate alert", "UDP 速率告警"},
	"icmp_flood":               {"ICMP rate alert", "ICMP 速率告警"},
	"bandwidth_spike":          {"Bandwidth alert", "带宽告警"},
	"port_scan":                {"Port scan observation", "端口扫描观察"},
	"budget_month_bytes":       {"Cycle traffic allowance", "结算周期流量配额"},
	"budget_month_cost":        {"Cycle estimated cost budget", "结算周期费用估算预算"},
	"budget_day_bytes":         {"Daily traffic threshold", "日流量阈值"},
	"budget_day_growth":        {"Daily traffic growth", "日流量增长"},
	"health_sensor":            {"Sensor health", "采集器健康"},
	"health_interface_counter": {"Interface counter health", "接口计数器健康"},
	"health_ssh_journal":       {"SSH journal health", "SSH 日志健康"},
	"health_storage":           {"Storage health", "存储健康"},
	"health_geoip_update":      {"GeoIP update health", "地理数据库更新健康"},
}

var reasonText = map[string]eventText{
	"disabled":                      {"disabled", "已禁用"},
	"not_checked":                   {"not checked", "尚未检查"},
	"read_failed":                   {"read failed", "读取失败"},
	"state_invalid":                 {"recorded state invalid", "记录状态无效"},
	"clock_rollback":                {"clock moved backwards", "时钟回退"},
	"insufficient_coverage":         {"insufficient coverage", "覆盖不足"},
	"baseline_unavailable":          {"baseline unavailable", "基线不可用"},
	"zero_baseline":                 {"recorded baseline is zero", "已记录基线为零"},
	"billing_unavailable":           {"billing model unavailable", "计费模型不可用"},
	"calendar_unavailable":          {"billing calendar unavailable", "结算日历不可用"},
	"observed_estimate":             {"observed estimate", "已观测估算值"},
	"threshold_crossed":             {"configured threshold reached", "达到配置阈值"},
	"healthy":                       {"component reported healthy", "组件报告正常"},
	"component_unavailable":         {"component unavailable", "组件不可用"},
	"sensor_stale":                  {"sensor observation stale", "采集器观察过期"},
	"interface_stale":               {"interface counters stale", "接口计数过期"},
	"journal_unavailable":           {"SSH journal unavailable", "SSH 日志不可用"},
	"storage_write_failed":          {"storage write failed", "存储写入失败"},
	"storage_pressure":              {"storage budget under pressure", "存储预算承压"},
	"storage_busy":                  {"storage busy", "存储繁忙"},
	"storage_unconfigured":          {"storage budget not configured", "未配置存储预算"},
	"metadata_unavailable":          {"metadata unavailable", "元数据不可用"},
	"update_failed":                 {"GeoIP update failed", "地理数据库更新失败"},
	"update_stale":                  {"GeoIP update stale", "地理数据库更新过期"},
	"schedule_failed":               {"GeoIP update schedule failed", "地理数据库更新计划失败"},
	"startup_grace":                 {"startup observation grace", "启动观察宽限期"},
	"check_unknown":                 {"check result unknown", "检查结果未知"},
	"historical_policy_unavailable": {"historical billing policy unavailable", "历史计费规则不可用"},
	"period_close_pending":          {"period closing pending", "周期结算待完成"},
}

// FormatEventLocalized renders only fixed event templates and recorded scalar
// evidence. Summary and arbitrary evidence strings can contain log-derived
// prose, so neither is guessed, translated, or used as a template fallback.
// It runs at admission; durable outbox bodies are not translated on retry.
func FormatEventLocalized(hostname string, event model.Event, languageName string, location *time.Location) string {
	zh := languageName == "zh"
	if location == nil {
		location = time.UTC
	}
	title, supported := eventTitles[event.Kind]
	if !supported {
		title = eventText{"Unsupported event", "未支持的事件"}
	}
	severities := map[model.Severity]eventText{
		model.SeverityCritical: {"CRITICAL", "严重"}, model.SeverityHigh: {"HIGH", "高"},
		model.SeverityMedium: {"MEDIUM", "中"}, model.SeverityLow: {"LOW", "低"}, model.SeverityInfo: {"INFO", "信息"},
	}
	severity, ok := severities[event.Severity]
	if !ok {
		severity = eventText{"UNCLASSIFIED (" + safeCode(string(event.Severity), false) + ")", "未分类（" + safeCode(string(event.Severity), true) + "）"}
	}
	icon := map[model.Severity]string{model.SeverityCritical: "🔴", model.SeverityHigh: "🔴", model.SeverityMedium: "🟠", model.SeverityLow: "🟡", model.SeverityInfo: "🔵"}[event.Severity]
	if icon == "" {
		icon = "🔵"
	}
	line := func(en, chinese, value string, maximum int) string {
		return localText(zh, en, chinese) + ": " + escapedField(value, maximum)
	}
	// Keep identity, time, phase, primary evidence and action before optional
	// details. Each external field has its own escaped-byte bound.
	lines := []string{
		fmt.Sprintf("%s <b>NodeRampart %s — %s</b>", icon, severity.local(zh), title.local(zh)),
		line("Host", "主机", hostname, 192),
		line("Time", "时间", eventTime(event.ObservedAt, location, zh), 192),
		line("Phase", "阶段", phaseText(event.Phase, zh), 160),
		line("Event type code", "事件类型代码", safeCode(event.Kind, zh), 96),
		line("Event ID", "事件标识", nonempty(event.ID, localText(zh, "not recorded", "未记录")), 192),
		line("Incident", "事件关联", nonempty(event.IncidentID, localText(zh, "not recorded", "未记录")), 192),
	}
	source := event.SourceIP
	if source == "" {
		source = event.SourceRange
	}
	if source != "" {
		lines = append(lines, line("Source", "来源", source, 256))
	}
	lines = append(lines, line("Evidence", "证据", eventSummary(event, supported, zh), 768))
	if event.Count > 0 {
		if floodKind(event.Kind) {
			lines = append(lines, line("Recorded integer rate (rounded down)", "已记录整数速率（向下取整）", strconv.FormatUint(event.Count, 10), 32))
		} else {
			lines = append(lines, line("Count", "计数", strconv.FormatUint(event.Count, 10), 32))
		}
	}
	if event.Target != "" {
		if (event.Kind == "ssh_login_success" || event.Kind == "ssh_brute_force") && strings.HasPrefix(event.Target, "ssh user=") {
			lines = append(lines, line("SSH user", "SSH 用户", strings.TrimPrefix(event.Target, "ssh user="), 192))
		} else {
			lines = append(lines, line("Target (recorded value)", "目标（记录原值）", event.Target, 192))
		}
	}
	lines = append(lines, localText(zh, "Action: observed only; no automatic blocking", "动作：仅观察；未自动封禁"))
	lines = append(lines, eventEvidence(event, location, zh)...)
	lines = append(lines, geoLines(event.Geo, zh)...)
	return joinWithinMarker(lines, eventBodyBytes, localText(zh, "… truncated; more details are available locally", "… 内容已截短；更多详情请在本地查看"))
}

func FormatTest(hostname, languageName string) string {
	zh := languageName == "zh"
	return joinWithinMarker([]string{
		"✅ <b>NodeRampart " + localText(zh, "notification test", "通知测试") + "</b>",
		localText(zh, "Host", "主机") + ": " + escapedField(hostname, 256),
	}, eventBodyBytes, localText(zh, "… truncated", "… 内容已截短"))
}

func phaseText(phase string, zh bool) string {
	phases := map[string]eventText{"start": {"start", "开始"}, "update": {"update", "更新"}, "recovery": {"recovery", "恢复"}, "observed": {"observed", "已观察"}}
	if value, ok := phases[phase]; ok {
		return value.local(zh)
	}
	return localText(zh, "unknown phase", "未知阶段") + " (" + safeCode(phase, zh) + ")"
}

func eventTime(at time.Time, location *time.Location, zh bool) string {
	if at.IsZero() {
		return localText(zh, "unknown", "未知")
	}
	local := at.In(location)
	return local.Format("2006-01-02 15:04:05") + " " + location.String() + " (" + timezones.Offset(local) + ")"
}

func floodKind(kind string) bool {
	return kind == "syn_flood" || kind == "udp_flood" || kind == "icmp_flood" || kind == "bandwidth_spike"
}

func eventSummary(event model.Event, supported, zh bool) string {
	if !supported {
		return localText(zh, "This event type has no supported notification template; raw details remain local.", "此事件类型尚无通知模板；原始详情仅保留在本地。")
	}
	if floodKind(event.Kind) {
		rate, rateOK := number(event.Evidence["observed_rate"])
		threshold, thresholdOK := number(event.Evidence["threshold_rate"])
		if !rateOK || !thresholdOK {
			return localText(zh, "A network threshold event was recorded; original rate parameters are unavailable.", "已记录网络阈值事件；原始速率参数未保留。")
		}
		unit := localText(zh, "packets/s", "包/秒")
		if event.Kind == "bandwidth_spike" {
			unit = localText(zh, "bytes/s", "字节/秒")
		}
		if event.Phase == "recovery" {
			return fmt.Sprintf(localText(zh, "Recorded recovery: rate %s %s; configured threshold %s %s.", "已记录恢复：速率 %s %s；配置阈值 %s %s。"), rate, unit, threshold, unit)
		}
		return fmt.Sprintf(localText(zh, "Observed rate %s %s; configured threshold %s %s.", "已观测速率 %s %s；配置阈值 %s %s。"), rate, unit, threshold, unit)
	}
	switch event.Kind {
	case "port_scan":
		if event.Count > 0 {
			return fmt.Sprintf(localText(zh, "At least %d distinct local ports were probed.", "至少记录了 %d 个不同本地端口被探测。"), event.Count)
		}
		return localText(zh, "A port scan observation was recorded; retained count is unavailable.", "已记录端口扫描观察；保留计数不可用。")
	case "ssh_login_success":
		return localText(zh, "A successful SSH authentication was observed.", "已观察到一次 SSH 认证成功。")
	case "ssh_brute_force":
		window, ok := milliseconds(event.Evidence["window_millis"], zh)
		if event.Count > 0 && ok {
			if zh {
				return fmt.Sprintf("在 %s内记录了 %d 次 SSH 认证失败。", window, event.Count)
			}
			return fmt.Sprintf("%d SSH authentication failures were recorded in %s.", event.Count, window)
		}
		if event.Count > 0 {
			return fmt.Sprintf(localText(zh, "%d SSH authentication failures were recorded; window parameters are unavailable.", "已记录 %d 次 SSH 认证失败；检测窗口参数未保留。"), event.Count)
		}
		return localText(zh, "An SSH failure threshold event was recorded; retained evidence is incomplete.", "已记录 SSH 认证失败阈值事件；保留证据不足。")
	}
	if strings.HasPrefix(event.Kind, "health_") {
		if event.Phase == "recovery" {
			return localText(zh, "A component health recovery was recorded.", "已记录组件健康恢复。")
		}
		return localText(zh, "The recorded component health condition requires attention.", "已记录的组件健康状态需关注。")
	}
	context := model.ProjectAlertContext(event.Kind, event.Evidence)
	if context == nil || context.Milestone == nil || (*context.Milestone != 80 && *context.Milestone != 100) {
		return localText(zh, "A usage/budget threshold event was recorded; comparison parameters are incomplete.", "已记录用量或预算阈值事件；比较参数不足。")
	}
	if event.Kind == "budget_month_cost" {
		return fmt.Sprintf(localText(zh, "Recorded estimated cost reached %d%% of the configured cost budget.", "已记录估算费用达到配置费用预算的 %d%%。"), *context.Milestone)
	}
	if event.Kind == "budget_day_growth" {
		return localText(zh, "Observed daily guest TX reached the recorded growth threshold (estimate).", "已观测日出站流量达到已记录的增长阈值（估算）。")
	}
	return fmt.Sprintf(localText(zh, "Observed guest TX estimate reached %d%% of the configured threshold.", "所选接口出站估算已达到配置阈值的 %d%%。"), *context.Milestone)
}

func eventEvidence(event model.Event, location *time.Location, zh bool) []string {
	var lines []string
	add := func(en, chinese, value string) {
		lines = append(lines, localText(zh, en, chinese)+": "+escapedField(value, 512))
	}
	fields := event.Evidence
	if fields["interface"] != "" {
		add("Interface (recorded name)", "接口（记录名称）", fields["interface"])
	}
	if event.Kind == "ssh_login_success" || event.Kind == "ssh_brute_force" {
		methods := map[string]eventText{"password": {"password", "密码"}, "publickey": {"public key", "公钥"}, "keyboard-interactive": {"keyboard interactive", "键盘交互"}, "keyboard-interactive/pam": {"keyboard interactive (PAM)", "键盘交互（PAM）"}, "hostbased": {"host based", "主机认证"}, "gssapi-with-mic": {"GSSAPI", "GSSAPI 认证"}, "pam": {"PAM", "PAM 认证"}}
		method, ok := methods[fields["method"]]
		if !ok {
			method = eventText{"unknown (" + safeCode(fields["method"], false) + ")", "未知（" + safeCode(fields["method"], true) + "）"}
		}
		add("Authentication method", "认证方式", method.local(zh))
		if fields["count_basis"] == "openssh_final_failure" {
			add("Count basis", "计数依据", localText(zh, "OpenSSH final failure records; auxiliary logs are not counted twice", "OpenSSH 最终认证失败记录；辅助日志不重复累计"))
		} else if fields["count_basis"] != "" {
			add("Count basis", "计数依据", unknownEnum(fields["count_basis"], zh))
		}
		if value, ok := unsigned(fields["preceding_source_failures"]); ok {
			add("Preceding source failures", "此来源先前失败次数", strconv.FormatUint(value, 10))
		}
		if value, ok := authenticationWindow(fields, zh); ok {
			add("Detection window", "检测窗口", value)
		}
		for _, check := range []struct{ key, en, chinese string }{{"invalid_user", "Invalid user", "无效账户"}, {"preceding_source_failures_complete", "Preceding failure evidence complete", "先前失败证据完整"}, {"detection_window_complete", "Detection window complete", "检测窗口完整"}} {
			if value, ok := boolean(fields[check.key], zh); ok {
				add(check.en, check.chinese, value)
			}
		}
		history := map[string]eventText{"observing": {"observation period; insufficient history for an unusualness decision", "历史观察期；证据不足，不判罕见"}, "history_unavailable": {"history unavailable; no unusualness decision", "历史不可用；未作罕见判断"}, "history_incomplete": {"history coverage incomplete; no unusualness decision", "历史覆盖不完整；未作罕见判断"}, "available": {"bounded historical hint available; not an intrusion finding", "可提供有界历史提示；并非入侵结论"}}
		if state := fields["history_hint_state"]; state != "" {
			value, ok := history[state]
			if !ok {
				value = eventText{"unknown historical hint state (" + safeCode(state, false) + ")", "未知历史提示状态（" + safeCode(state, true) + "）"}
			}
			add("SSH history", "SSH 历史", value.local(zh))
		}
		if fields["history_basis"] == "current_process_retained_successes_7d" {
			add("History basis", "历史依据", localText(zh, "successes retained by this process over 7 days; cold starts/privacy changes/pruning can limit history", "仅本进程保留的近 7 天成功记录；冷启动、隐私变化或裁剪可限制历史"))
		} else if fields["history_basis"] != "" {
			add("History basis", "历史依据", unknownEnum(fields["history_basis"], zh))
		}
		switch fields["history_source_hint"] {
		case "first_observed_source":
			add("Source history hint", "来源历史提示", localText(zh, "first observation of this transformed source in retained history", "保留历史中首次观察到此隐私转换后的来源"))
		case "first_observed_prefix":
			add("Source history hint", "来源历史提示", localText(zh, "first observation of this shared prefix; a prefix is not an exact identity", "保留历史中首次观察到此共享前缀；前缀不代表精确身份"))
		default:
			if fields["history_source_hint"] != "" {
				add("Source history hint", "来源历史提示", unknownEnum(fields["history_source_hint"], zh))
			}
		}
		if fields["history_time_hint"] == "unseen_local_hour" {
			add("Time history hint", "时段历史提示", localText(zh, "the current local hour was not seen in retained history; only a departure from known history", "保留历史中未见当前本地小时；仅表示偏离已知历史"))
		} else if fields["history_time_hint"] != "" {
			add("Time history hint", "时段历史提示", unknownEnum(fields["history_time_hint"], zh))
		}
	}
	if floodKind(event.Kind) || event.Kind == "port_scan" {
		if floodKind(event.Kind) {
			unit := localText(zh, "packets/s", "包/秒")
			if event.Kind == "bandwidth_spike" {
				unit = localText(zh, "bytes/s", "字节/秒")
			}
			if value, ok := recordedThreshold(event.Kind, fields); ok {
				add("Configured rate threshold", "配置速率阈值", value+" "+unit)
			}
		}
		for _, check := range []struct{ key, en, chinese string }{{"coverage_complete", "Recorded coverage complete", "记录覆盖完整"}} {
			if value, ok := boolean(fields[check.key], zh); ok {
				add(check.en, check.chinese, value)
			}
		}
		for _, check := range []struct{ key, en, chinese string }{{"window_millis", "Actual sample interval", "实际采样间隔"}, {"incident_duration_millis", "Incident duration", "事件持续时间"}, {"elapsed_millis", "Observed scan duration", "扫描观察历时"}} {
			if value, ok := milliseconds(fields[check.key], zh); ok {
				add(check.en, check.chinese, value)
			}
		}
		for _, check := range []struct{ key, en, chinese string }{{"packets", "Observed packets", "已观测包数"}, {"top_source_packets", "Top retained source packets", "保留主要来源包数"}, {"top_source_bytes", "Top retained source bytes", "保留主要来源字节数"}, {"threshold", "Unique-port threshold", "不同端口阈值"}} {
			if event.Kind != "port_scan" && check.key == "threshold" {
				continue
			}
			if value, ok := unsigned(fields[check.key]); ok {
				add(check.en, check.chinese, strconv.FormatUint(value, 10))
			}
		}
		if value, ok := number(fields["window_seconds"]); ok {
			add("Configured scan window", "配置扫描窗口", value+localText(zh, " seconds", " 秒"))
		}
		if fields["rule"] == "inbound_syn_or_unsolicited_udp" {
			add("Probe basis", "探测依据", localText(zh, "inbound SYN or unsolicited UDP", "入站 SYN 或未请求的 UDP"))
		} else if fields["rule"] != "" {
			add("Probe basis", "探测依据", unknownEnum(fields["rule"], zh))
		}
		if fields["source_scope"] == "retained_flows" {
			add("Source scope", "来源范围", localText(zh, "top source among retained flows; not a complete source census", "已保留流量中的主要来源；非完整来源统计"))
		} else if fields["source_scope"] != "" {
			add("Source scope", "来源范围", unknownEnum(fields["source_scope"], zh))
		}
	}
	if context := model.ProjectAlertContext(event.Kind, fields); context != nil {
		availability := map[string]eventText{"recorded": {"recorded fields complete; not proof of complete coverage", "记录字段齐备；不代表覆盖完整"}, "partial": {"recorded fields partial; missing values are not zero", "仅部分字段有记录；缺失不等于零"}, "unavailable": {"recorded context unavailable", "记录上下文不可用"}}
		add("Recorded context", "记录上下文", availability[context.Availability].local(zh))
		if fields["reason"] != "" {
			value, ok := reasonText[fields["reason"]]
			if !ok {
				value = eventText{"unknown reason (" + safeCode(fields["reason"], false) + "); original details remain local", "未知原因（" + safeCode(fields["reason"], true) + "）；原始详情仅保留本地"}
			}
			add("Reason", "原因", value.local(zh))
		}
		if context.ConditionSince != nil {
			add("Condition since", "状态起始", eventTime(*context.ConditionSince, location, zh))
		}
		if context.Period != "" {
			add("Recorded period label", "记录周期标签", context.Period)
		}
		if context.PeriodStart != nil {
			add("Period start", "周期开始", eventTime(*context.PeriodStart, location, zh))
			add("Period end (exclusive)", "周期结束（不含）", eventTime(*context.PeriodEnd, location, zh))
		}
		for _, item := range []struct {
			value       *uint64
			en, chinese string
		}{{context.ObservedBytes, "Observed guest TX", "已观测接口出站"}, {context.ThresholdBytes, "Traffic threshold", "流量阈值"}} {
			if item.value != nil {
				add(item.en, item.chinese, strconv.FormatUint(*item.value, 10)+localText(zh, " bytes", " 字节"))
			}
		}
		for _, item := range []struct {
			value       *float64
			en, chinese string
		}{{context.ObservedCost, "Estimated cost", "估算费用"}, {context.ThresholdCost, "Cost budget", "费用预算"}, {context.BaselineMeanBytes, "Baseline daily mean (bytes)", "基线日均字节数"}, {context.GrowthRatio, "Configured growth ratio", "配置增长倍数"}} {
			if item.value != nil {
				text := strconv.FormatFloat(*item.value, 'g', -1, 64)
				if item.value == context.ObservedCost || item.value == context.ThresholdCost {
					text += " " + context.Currency
				}
				add(item.en, item.chinese, text)
			}
		}
		if context.BaselineDays != nil {
			add("Baseline complete days", "基线完整日数", strconv.Itoa(*context.BaselineDays))
		}
		if context.Milestone != nil {
			add("Threshold milestone", "阈值进度", strconv.Itoa(*context.Milestone)+"%")
		}
		if context.Coverage != "" {
			coverage := map[string]eventText{"unknown": {"unknown", "未知"}, "incomplete": {"incomplete", "不完整"}, "adequate_recorded": {"adequate coverage recorded; not a completeness guarantee", "已记录为足够覆盖；并非完整保证"}}
			add("Coverage", "覆盖", coverage[context.Coverage].local(zh))
		} else if fields["coverage"] != "" {
			add("Coverage", "覆盖", unknownEnum(fields["coverage"], zh))
		}
		if context.Basis != "" {
			add("Estimate basis", "估算依据", localText(zh, "selected-interface guest TX; private/duplicate paths may be included; overlapping UTC-hour estimate, not a bill, excluding costs outside the model", "所选接口虚拟机出站；可能含私网及重复路径，按重叠 UTC 小时估算；不等于账单，不含模型之外费用"))
		} else if fields["basis"] != "" {
			add("Estimate basis", "估算依据", unknownEnum(fields["basis"], zh))
		}
		if strings.HasPrefix(event.Kind, "health_") {
			add("Health observation scope", "健康观察范围", localText(zh, "recorded component health; does not guarantee complete collection or notification delivery", "组件健康记录；不保证采集完整或通知已投递"))
		}
	}
	return lines
}

func geoLines(geo model.Geo, zh bool) []string {
	var lines []string
	add := func(en, chinese, value string) {
		lines = append(lines, localText(zh, en, chinese)+": "+escapedField(value, 256))
	}
	if geo.CountryCode != "" || geo.Country != "" {
		country := countryName(geo.CountryCode, zh)
		if country == "" && geo.Country == "Private or local network" {
			country = countryName("PRIVATE", zh)
		}
		if country == "" {
			country = localText(zh, "unknown country", "国家未知")
		}
		add("Country (GeoIP estimate)", "国家（地理估算）", country)
		if countryName(geo.CountryCode, zh) == "" && geo.Country != "" && geo.Country != "Private or local network" {
			add("Country (database original name)", "国家（数据库原名）", geo.Country)
		}
	}
	if geo.Region != "" {
		add("Region (database original name)", "地区（数据库原名）", geo.Region)
	}
	if geo.City != "" {
		add("City (database original name)", "城市（数据库原名）", geo.City)
	}
	if geo.ASN != 0 {
		add("ASN", "自治系统编号", fmt.Sprintf("AS%d", geo.ASN))
	}
	if geo.ASNOrg != "" {
		add("ASN organization (original name)", "自治系统组织（原名）", geo.ASNOrg)
	}
	if geo.ASNNetwork != "" {
		add("ASN range", "自治系统网段", geo.ASNNetwork)
	}
	if geo.DatabaseAge != "" {
		value := localText(zh, "unknown", "未知")
		if strings.HasSuffix(geo.DatabaseAge, "d") {
			if days, ok := unsigned(strings.TrimSuffix(geo.DatabaseAge, "d")); ok {
				value = strconv.FormatUint(days, 10) + localText(zh, " days", " 天")
			}
		}
		add("GeoIP database age", "地理数据库库龄", value)
	}
	if geo.CountryCode != "PRIVATE" && (geo.CountryCode != "" || geo.Region != "" || geo.City != "" || geo.ASN != 0) {
		add("GeoIP scope", "地理估算范围", localText(zh, "database estimate; not a verified physical location or intrusion finding", "数据库估算；并非核实的物理位置或入侵结论"))
	}
	return lines
}

// CLDR names come from the already-pinned x/text dependency. No runtime fetch
// and no incomplete hand-maintained list of a few countries is involved.
func countryName(code string, zh bool) string {
	if code == "PRIVATE" {
		return localText(zh, "Private or local network", "私有或本地网络")
	}
	if len(code) != 2 {
		return ""
	}
	region, err := language.ParseRegion(code)
	if err != nil || !region.IsCountry() {
		return ""
	}
	if zh {
		return display.Chinese.Regions().Name(region)
	}
	return display.English.Regions().Name(region)
}

// Safe machine codes remain visible in an unknown-category notification. Prose,
// path-like values and arbitrary logs never become an English fallback.
func unknownEnum(value string, zh bool) string {
	return localText(zh, "unrecognized recorded code", "未识别的记录代码") + " (" + safeCode(value, zh) + ")"
}

func safeCode(value string, zh bool) string {
	if len(value) == 0 || len(value) > 96 || strings.IndexFunc(value, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' && r != '-' && r != '.'
	}) >= 0 {
		return localText(zh, "unavailable", "不可用")
	}
	return value
}

func authenticationWindow(fields map[string]string, zh bool) (string, bool) {
	if value, ok := milliseconds(fields["window_millis"], zh); ok {
		return value, true
	}
	// This exact legacy field is a Go duration emitted by the auth producer;
	// accept only a bounded duration grammar, never a Summary or an English log.
	if value := fields["window"]; len(value) > 0 && len(value) <= 32 {
		if duration, err := time.ParseDuration(value); err == nil && duration >= 0 && duration <= 24*time.Hour && duration%time.Millisecond == 0 {
			return milliseconds(strconv.FormatInt(duration.Milliseconds(), 10), zh)
		}
	}
	return "", false
}

func recordedThreshold(kind string, fields map[string]string) (string, bool) {
	if value, ok := number(fields["threshold_rate"]); ok {
		return value, true
	}
	// The old producer retained a scalar plus one fixed unit. Only that fixed
	// grammar can be projected; no sentence translation or rate reconstruction.
	unit := " pps"
	if kind == "bandwidth_spike" {
		unit = " bytes/s"
	}
	value := fields["threshold"]
	if len(value) <= 140 && strings.HasSuffix(value, unit) {
		return number(strings.TrimSuffix(value, unit))
	}
	return "", false
}

func unsigned(value string) (uint64, bool) {
	if value == "" || len(value) > 20 || strings.IndexFunc(value, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
		return 0, false
	}
	n, err := strconv.ParseUint(value, 10, 64)
	return n, err == nil
}
func number(value string) (string, bool) {
	// Integer evidence may span the full uint64 range. Preserve its exact
	// value before the bounded float branch; binary64 loses digits above 2^53.
	if integer, ok := unsigned(value); ok {
		return strconv.FormatUint(integer, 10), true
	}
	if len(value) == 0 || len(value) > 128 || strings.IndexFunc(value, func(r rune) bool {
		return (r < '0' || r > '9') && r != '.' && r != 'e' && r != 'E' && r != '+' && r != '-'
	}) >= 0 {
		return "", false
	}
	n, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > 1e120 {
		return "", false
	}
	return strconv.FormatFloat(n, 'g', -1, 64), true
}
func boolean(value string, zh bool) (string, bool) {
	if value == "true" {
		return localText(zh, "yes", "是"), true
	}
	if value == "false" {
		return localText(zh, "no", "否"), true
	}
	return "", false
}
func milliseconds(value string, zh bool) (string, bool) {
	n, ok := unsigned(value)
	if !ok {
		return "", false
	}
	if n%1000 == 0 {
		return strconv.FormatUint(n/1000, 10) + localText(zh, " seconds", " 秒"), true
	}
	return strconv.FormatUint(n, 10) + localText(zh, " milliseconds", " 毫秒"), true
}
func nonempty(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// Bound before escaping rather than copying an unbounded field. One rune and
// its complete HTML entity are appended together; control characters cannot
// manufacture extra labelled lines. Invalid UTF-8 becomes a replacement rune.
func escapedField(value string, maximum int) string {
	var output strings.Builder
	for _, r := range value {
		if unicode.IsControl(r) {
			r = ' '
		}
		piece := html.EscapeString(string(r))
		if output.Len()+len(piece)+len("…") > maximum {
			output.WriteString("…")
			break
		}
		output.WriteString(piece)
	}
	return output.String()
}
