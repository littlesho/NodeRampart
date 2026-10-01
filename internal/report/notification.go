// SPDX-License-Identifier: MIT

package report

import (
	"errors"
	"fmt"
	"html"
	"math"
	"strings"
	"time"

	"github.com/littlesho/NodeRampart/internal/store"
	"github.com/littlesho/NodeRampart/internal/timezones"
)

// NotificationBody keeps the English delivery contract used by Webhook.
func NotificationBody(snapshot store.ReportSnapshot) (string, error) {
	return NotificationBodyLocalized(snapshot, "en")
}

func notificationLanguage(language string) (string, error) {
	if language == "" {
		language = "en"
	}
	if language != "en" && language != "zh" {
		return "", errors.New("unsupported report notification language")
	}
	return language, nil
}

// NotificationBodyLocalized renders retained facts, without copying original
// prose or source identifiers into a new delivery. It never rewrites history.
func NotificationBodyLocalized(snapshot store.ReportSnapshot, language string) (string, error) {
	language, err := notificationLanguage(language)
	if err != nil {
		return "", err
	}
	tr := func(en, zh string) string {
		if language == "zh" {
			return zh
		}
		return en
	}
	date := html.EscapeString(strings.ToValidUTF8(snapshot.Date, "�"))
	lines := []string{tr("🛡 <b>NodeRampart — daily report ", "🛡 <b>NodeRampart — 日报 ") + date + "</b>", tr("Local archive: ", "本地归档：") + date}
	if len(snapshot.Document) == 0 {
		lines = append(lines, tr("Legacy original full content unavailable; open the preserved local report. Statistics and the original report timezone are unknown; missing history is not zero.", "旧快照缺少原始完整内容，请打开保留的本地报告。完整统计及原报告时区未知；缺失历史不等于零。"), tr("Source identifiers are omitted from this delivery.", "本条通知隐去来源标识。"))
		return notificationLines(lines, language), nil
	}
	doc, err := DecodeDocument(snapshot.Document)
	if err != nil {
		return "", err
	}
	location, knownZone, err := notificationLocation(doc.Timezone)
	if err != nil {
		return "", err
	}
	if !doc.PeriodStart.Equal(snapshot.PeriodStart) || !doc.PeriodEnd.Equal(snapshot.PeriodEnd) || doc.Date != "" && doc.Date != snapshot.Date {
		return "", errors.New("report notification period conflicts with archived document")
	}
	period := func(start, end time.Time, zone *time.Location) string {
		return notificationTime(start.In(zone)) + " — " + notificationTime(end.In(zone))
	}
	lines = append(lines, tr("Host: ", "主机：")+html.EscapeString(doc.Hostname), tr("Period (end exclusive): ", "报告区间（不含结束时刻）：")+period(doc.PeriodStart, doc.PeriodEnd, location))
	if knownZone {
		lines = append(lines, tr("Snapshot timezone: ", "快照时区：")+html.EscapeString(doc.Timezone))
	} else {
		lines = append(lines, tr("Original timezone was not fixed in this snapshot; times above are shown in UTC.", "此快照未固定原始时区；以上时刻以 UTC 显示。"))
	}
	var events, auth uint64
	saturated := false
	add := func(total *uint64, count uint64) {
		if math.MaxUint64-*total < count {
			*total, saturated = math.MaxUint64, true
		} else {
			*total += count
		}
	}
	for _, item := range doc.Summary.Events {
		add(&events, item.Count)
	}
	for _, item := range doc.Summary.Auth {
		add(&auth, item.Count)
	}
	summary := doc.Summary
	lines = append(lines,
		tr("Counts reflect retained observations; missing observations are unknown; zero does not prove no activity.", "以下计数反映保留观测；缺失观测仍未知；零不证明没有活动。"),
		fmt.Sprintf(tr("Retained security events: %d; SSH observations: %d", "保留的安全事件：%d；SSH 观察：%d"), events, auth),
		fmt.Sprintf(tr("Interface RX %s / TX %s", "网卡接收 %s / 发送 %s"), formatBytes(summary.Interface.RXBytes), formatBytes(summary.Interface.TXBytes)),
		fmt.Sprintf(tr("Sensor batches: %d; parse errors: %d", "传感批次：%d；解析错误：%d"), summary.Batches, summary.ParseErrors),
		fmt.Sprintf(tr("Kernel capture: %d packets; drops: %d; statistics errors: %d", "内核捕获：%d 个包；丢包：%d；统计错误：%d"), summary.KernelPackets, summary.KernelDrops, summary.KernelStatsErrors),
		fmt.Sprintf(tr("Flow overflow: %d packets / %s", "流量记录溢出：%d 个包 / %s"), summary.OverflowPackets, formatBytes(summary.OverflowBytes)),
		fmt.Sprintf(tr("IPC send loss estimate: %d batches; %d packets / %s", "IPC 发送丢失估计：%d 批次；%d 个包 / %s"), summary.IPCDroppedBatches, summary.IPCDroppedPackets, formatBytes(summary.IPCDroppedBytes)),
		fmt.Sprintf(tr("Unattributed/reconciliation: RX %s / TX %s", "未归因/计数对账：接收 %s / 发送 %s"), formatBytes(positiveDifference(summary.Interface.RXBytes, summary.AttributedRXBytes)), formatBytes(positiveDifference(summary.Interface.TXBytes, summary.AttributedTXBytes))),
		tr("Older sensors do not report IPC loss; recovered counters may include earlier periods.", "旧传感器不报告 IPC 丢失；恢复后的计数可能包含更早周期。"))
	if summary.Batches == 0 {
		lines = append(lines, tr("Detailed sensor coverage unavailable; regional attribution may be missing.", "详细传感覆盖不可用；区域归因可能缺失。"))
	}
	if summary.HealthCounterSaturations > 0 || saturated {
		lines = append(lines, tr("Counters reached their reporting limit; affected totals are lower bounds.", "计数达到报告上限；受影响的总数仅为下界。"))
	}
	lines = append(lines, tr("<b>Recorded coverage</b>", "<b>已记录覆盖</b>"))
	for _, name := range []string{"interface_counter", "sensor_feed", "ssh_journal"} {
		state := notificationCoverage(doc, name)
		label := name
		if language == "zh" {
			label = map[string]string{"interface_counter": "网卡计数", "sensor_feed": "报文采集", "ssh_journal": "SSH 日志"}[name]
			state = map[string]string{"complete": "完整运行记录", "partial": "部分", "unknown": "未知", "disabled": "已停用"}[state]
		}
		lines = append(lines, "• "+label+": "+state)
	}
	if len(doc.Integrity.Gaps) > 0 {
		lines = append(lines, fmt.Sprintf(tr("Retained coverage gaps: %d; a zero gap count means unknown loss, not zero loss.", "保留的覆盖缺口：%d；缺口计数为零表示损失未知，不表示没有损失。"), len(doc.Integrity.Gaps)))
	}
	lines = append(lines, tr("Unrecorded/conflicting time is unknown. Running coverage is not proof of complete or lossless data.", "未记录或冲突的时段为未知。运行覆盖不证明数据完整或没有丢失。"))
	if doc.Integrity.HistoryTruncated || doc.Integrity.GapsTruncated || doc.Integrity.LossHoursTruncated || doc.Integrity.More {
		lines = append(lines, tr("Coverage history, gaps or loss detail is bounded/truncated; inspect the local archive.", "覆盖历史、缺口或丢失明细有边界或截断；请检查本地归档。"))
	}
	retention := doc.Integrity.Retention
	if retention == nil {
		lines = append(lines, tr("Retention ledger unavailable; removed history cannot be inferred from remaining records.", "裁剪台账不可用；不能由残存记录推断已删除的历史。"))
	} else {
		if retention.TrackingStarted.IsZero() || retention.TrackingStarted.After(doc.PeriodStart) || retention.EvictedEntries > 0 {
			lines = append(lines, tr("Earlier pruning history is unknown or no longer retained.", "更早的裁剪历史未知或已不再保留。"))
		}
		if len(retention.Entries) > 0 || retention.More {
			lines = append(lines, tr("Pruning overlaps this report; affected rows are not a count of lost packets.", "本报告区间存在裁剪记录；受影响的来源行数不等于丢失数据包数。"))
		} else {
			lines = append(lines, tr("No retained pruning entry overlaps this period; this does not prove complete data.", "本区间无保留的裁剪记录；这不证明数据完整。"))
		}
	}
	if doc.Billing != nil {
		pricing := doc.Billing
		if err := pricing.Validate(); err != nil {
			return "", errors.New("invalid archived report billing snapshot")
		}
		billingLocation, known, err := notificationLocation(pricing.Timezone)
		if err != nil {
			return "", errors.New("invalid archived report billing timezone")
		}
		unit := "GB"
		if pricing.UnitBytes == 1<<30 {
			unit = "GiB"
		}
		lines = append(lines,
			tr("<b>Billing estimate</b>", "<b>费用估算</b>"),
			tr("Accounting period (end exclusive): ", "计费区间（不含结束时刻）：")+period(pricing.PeriodStart, pricing.PeriodEnd, billingLocation),
			tr("Whole-cycle data completeness is unknown; retained counters may understate usage or cost.", "整个结算周期的数据完整性未知；保留计数可能低估用量或费用。"),
			fmt.Sprintf(tr("Cycle guest TX: %s; free allowance %.2f %s; billable estimate %.2f %s", "当期客户机发送量：%s；免费额度 %.2f %s；估算计费用量 %.2f %s"), formatBytes(pricing.OutboundBytes), pricing.Profile.FreeGB, unit, pricing.Estimate.BillableGB, unit),
			fmt.Sprintf(tr("Estimated cost: %.2f %s; tariff saved at generation: %s", "估算费用：%.2f %s；使用生成时保存的价格：%s"), pricing.Estimate.Cost, html.EscapeString(pricing.Estimate.Currency), html.EscapeString(pricing.Profile.Name)),
			tr("Guest TX is not the provider billing meter or invoice. Estimates exclude fees outside the saved tariff.", "客户机发送量不等于服务商计费流量或账单；估算不包含保存的价格模型之外的费用。"))
		if known {
			lines = append(lines, tr("Billing snapshot timezone: ", "费用快照时区：")+html.EscapeString(pricing.Timezone))
		} else {
			lines = append(lines, tr("Original billing timezone was not fixed; accounting times are shown in UTC.", "原费用时区未固定；计费时刻以 UTC 显示。"))
		}
	} else {
		lines = append(lines, tr("Billing estimate was not configured for this snapshot.", "此快照未配置费用估算。"))
	}
	lines = append(lines,
		tr("Retained statistics include overlapping UTC hours; missing observations are not reconstructed. Recent or late records can change during bounded generation.", "保留统计包含与报告区间重叠的 UTC 整点小时；不重建缺失观测。有界生成期间，最近或晚到记录可能变化。"),
		tr("Source identifiers are omitted from this delivery; full retained detail stays in the local archive.", "本条通知隐去来源标识；完整的保留明细仍在本地归档。"),
		tr("<b>Retained security detail</b>", "<b>保留的安全统计明细</b>"))
	for _, item := range summary.Auth {
		kind := item.Kind
		if language == "zh" {
			kind = map[string]string{"success": "登录成功", "failure": "认证失败", "invalid_user": "无效用户", "pam_failure": "PAM 认证失败"}[item.Kind]
			if kind == "" {
				kind = "其他观察（" + item.Kind + "）"
			}
		}
		lines = append(lines, fmt.Sprintf("• SSH %s: %d", html.EscapeString(kind), item.Count))
	}
	for _, item := range summary.Events {
		lines = append(lines, fmt.Sprintf("• %s / %s: %d", html.EscapeString(notificationEventKind(item.Kind, language)), html.EscapeString(notificationSeverity(string(item.Severity), language)), item.Count))
	}
	return notificationLines(lines, language), nil
}

func notificationLocation(name string) (*time.Location, bool, error) {
	// Historical "Local" does not identify the zone used on another host.
	if name == "" || name == "Local" {
		return time.UTC, false, nil
	}
	location, err := time.LoadLocation(name)
	if err != nil {
		return nil, false, errors.New("invalid archived report timezone")
	}
	return location, true, nil
}

func notificationTime(at time.Time) string {
	return at.Format("2006-01-02 15:04") + " " + timezones.Offset(at)
}

func notificationCoverage(doc *Document, name string) string {
	duration := doc.PeriodEnd.Sub(doc.PeriodStart).Milliseconds()
	for _, component := range doc.Integrity.Components {
		if component.Name != name {
			continue
		}
		if component.RunningMS == 0 && component.DegradedMS == 0 && component.DisabledMS == 0 {
			return "unknown"
		}
		bounded := doc.Integrity.HistoryTruncated || doc.Integrity.GapsTruncated
		for _, gap := range doc.Integrity.Gaps {
			bounded = bounded || gap.Name == name
		}
		if !bounded && duration > 0 && component.UnknownMS == 0 && component.ConflictMS == 0 && component.DegradedMS == 0 {
			if component.RunningMS >= duration && component.DisabledMS == 0 {
				return "complete"
			}
			if component.DisabledMS >= duration && component.RunningMS == 0 {
				return "disabled"
			}
		}
		return "partial"
	}
	return "unknown"
}

func notificationEventKind(kind, language string) string {
	if language != "zh" {
		return kind
	}
	translated := map[string]string{
		"bandwidth_spike": "带宽突增", "syn_flood": "SYN 洪泛", "udp_flood": "UDP 洪泛", "icmp_flood": "ICMP 洪泛", "connection_flood": "连接洪泛", "port_scan": "端口扫描", "tcp_port_scan": "TCP 端口扫描",
		"ssh_brute_force": "SSH 暴力认证", "ssh_login_success": "SSH 登录成功", "ssh_first_source": "首次观察的 SSH 来源", "ssh_rare_hour": "SSH 登录时段偏离历史",
		"budget_month_bytes": "当期流量预算", "budget_month_cost": "当期费用预算", "budget_day_bytes": "每日流量预算", "budget_day_growth": "每日流量增长",
		"health_sensor": "报文采集健康", "health_interface_counter": "网卡计数健康", "health_ssh_journal": "SSH 日志健康", "health_storage": "存储健康", "health_geoip_update": "GeoIP 更新健康",
	}[kind]
	if translated == "" {
		return "其他事件（" + kind + "）"
	}
	return translated
}

func notificationSeverity(severity, language string) string {
	if language == "zh" {
		if translated := map[string]string{"critical": "严重", "high": "高", "medium": "中", "low": "低", "info": "信息"}[severity]; translated != "" {
			return translated
		}
	}
	return severity
}

func notificationLines(lines []string, language string) string {
	marker := "… truncated; open the full local archive."
	notice := "Missing history is not zero; retained coverage may be incomplete. Any cost shown is not the provider billing meter or invoice; estimates exclude fees outside the saved tariff."
	if language == "zh" {
		marker = "… 已截断，请查看完整本地归档。"
		notice = "缺失历史不等于零；保留覆盖可能不完整。若显示费用，它不等于服务商计费流量或账单；估算不包含保存的价格模型之外的费用。"
	}
	if len(strings.Join(lines, "\n")) <= 4096 {
		return strings.Join(lines, "\n")
	}
	// Dynamic HTML-escaped fields can fill the body before the detailed limits.
	// Keep their meaning with the marker even when those full lines do not fit.
	marker = notice + "\n" + marker
	var kept []string
	length := 0
	for _, line := range lines {
		if length+len(line)+1+len(marker) > 4096 {
			break
		}
		kept, length = append(kept, line), length+len(line)+1
	}
	return strings.Join(append(kept, marker), "\n")
}
