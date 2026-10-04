// SPDX-License-Identifier: MIT

package notify

import (
	"fmt"
	"html"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/littlesho/NodeRampart/internal/model"
)

// Native summaries are persisted before delivery. Small fixed field budgets
// leave room for metrics, coverage and a usable local command on every channel.
func FormatNativeEvent(hostname string, event model.Event, language string, location *time.Location) string {
	zh := language == "zh"
	if location == nil {
		location = time.UTC
	}
	title, supported := eventTitles[event.Kind]
	if !supported {
		title = eventText{"Unsupported event", "未支持的事件"}
	}
	severity := map[model.Severity]eventText{model.SeverityCritical: {"CRITICAL", "严重"}, model.SeverityHigh: {"HIGH", "高"}, model.SeverityMedium: {"MEDIUM", "中"}, model.SeverityLow: {"LOW", "低"}, model.SeverityInfo: {"INFO", "信息"}}[event.Severity]
	if severity.en == "" {
		severity = eventText{"UNCLASSIFIED", "未分类"}
	}
	line := func(en, cn, value string) string { return localText(zh, en, cn) + ": " + nativeField(value, 96) }
	lines := []string{"NodeRampart " + severity.local(zh) + " — " + title.local(zh),
		line("Time", "时间", eventTime(event.ObservedAt, location, zh)),
		line("Phase", "阶段", phaseText(event.Phase, zh)),
		line("Host", "主机", hostname),
		line("Event", "事件", event.ID),
		line("Type", "类型", safeCode(event.Kind, zh)),
		localText(zh, "Coverage: retained observations only; missing data is unknown.", "覆盖：仅保留观测；缺失数据为未知。")}
	if event.Evidence["coverage_complete"] == "false" || event.Evidence["coverage"] == "incomplete" {
		lines = append(lines, localText(zh, "WARNING: incomplete coverage.", "警告：覆盖不足。"))
	}
	lines = append(lines, localText(zh, "Evidence: ", "证据：")+nativeField(eventSummary(event, supported, zh), 420))
	for _, field := range healthDiagnostic(event, location, zh).fields {
		lines = append(lines, field.label.local(zh)+": "+nativeField(field.value, 256))
	}
	if reason, ok := reasonText[event.Evidence["reason"]]; ok {
		lines = append(lines, line("Reason", "原因", reason.local(zh)))
	}
	if event.SourceIP != "" {
		lines = append(lines, line("Source", "来源", event.SourceIP))
	} else if event.SourceRange != "" {
		lines = append(lines, line("Source range", "来源网段", event.SourceRange))
	}
	if event.Count > 0 {
		lines = append(lines, line("Count / integer rate", "计数或整数速率", strconv.FormatUint(event.Count, 10)))
	}
	if c := model.ProjectAlertContext(event.Kind, event.Evidence); c != nil {
		if c.ObservedBytes != nil {
			lines = append(lines, line("Guest TX bytes", "接口出站字节", strconv.FormatUint(*c.ObservedBytes, 10)))
		}
		if c.ThresholdBytes != nil {
			lines = append(lines, line("Threshold bytes", "阈值字节", strconv.FormatUint(*c.ThresholdBytes, 10)))
		}
		if c.ObservedCost != nil {
			lines = append(lines, line("Estimated cost", "估算费用", fmt.Sprintf("%g %s", *c.ObservedCost, c.Currency)))
		}
		if c.ThresholdCost != nil {
			lines = append(lines, line("Cost budget", "费用预算", fmt.Sprintf("%g %s", *c.ThresholdCost, c.Currency)))
		}
		if c.GrowthRatio != nil {
			lines = append(lines, line("Growth ratio", "增长倍数", strconv.FormatFloat(*c.GrowthRatio, 'g', -1, 64)))
		}
		if c.BaselineMeanBytes != nil {
			lines = append(lines, line("Baseline daily bytes", "基线日均字节", strconv.FormatFloat(*c.BaselineMeanBytes, 'g', -1, 64)))
		}
		if c.Coverage != "" {
			lines = append(lines, line("Recorded coverage", "记录覆盖", localText(zh, c.Coverage, map[string]string{"unknown": "未知", "incomplete": "不足", "adequate_recorded": "已记录为足够；不保证完整"}[c.Coverage])))
		}
	}
	command := "sudo noderampart events list --limit 20"
	if safeCode(event.ID, false) == event.ID && event.ID != "" {
		command = "sudo noderampart events show --id " + event.ID
	}
	// The footer is reserved even when optional detail is omitted.
	footer := localText(zh, "Observed only; no automatic blocking. Details: ", "仅观察；未自动封禁。详情：") + command
	base := nativeLines(lines, footer, language)
	return base
}

func FormatNativeTest(hostname, language string) string {
	zh := language == "zh"
	return nativeLines([]string{localText(zh, "NodeRampart notification test", "NodeRampart 通知测试"), localText(zh, "Host: ", "主机：") + nativeField(hostname, 96), localText(zh, "Synthetic test only. An interface acknowledgment does not confirm reading or end-to-end delivery.", "仅合成测试。接口回执不代表已读或端到端送达。")}, "sudo noderampart notify status", language)
}

func nativeField(value string, maximum int) string {
	var b strings.Builder
	for _, r := range html.UnescapeString(value) {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			r = ' '
		}
		if r == '@' {
			r = '＠'
		}
		piece := string(r)
		if b.Len()+len(piece)+3 > maximum {
			b.WriteString("…")
			break
		}
		b.WriteString(piece)
	}
	return b.String()
}

func nativeLines(lines []string, footer, language string) string {
	marker := localText(language == "zh", "… shortened; inspect locally.", "… 已缩略；请在本地查看。")
	var b strings.Builder
	for _, line := range lines {
		if b.Len()+len(line)+len(footer)+len(marker)+3 > 1800 {
			b.WriteString("\n" + marker)
			break
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(line)
	}
	b.WriteString("\n" + footer)
	return b.String()
}
