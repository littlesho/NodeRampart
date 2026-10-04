// SPDX-License-Identifier: MIT

package notify

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/littlesho/NodeRampart/internal/model"
)

func officialField(value string, limit int) string {
	var b strings.Builder
	for _, r := range value {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			r = ' '
		}
		if r == '@' {
			r = ' '
		}
		if b.Len()+len(string(r))+1 > limit {
			b.WriteByte('~')
			break
		}
		b.WriteRune(r)
	}
	value = strings.TrimSpace(b.String())
	if value == "" {
		return "-"
	}
	return value
}

func FormatOfficialEvent(hostname string, event model.Event, language string, location *time.Location) (string, OfficialSemantic) {
	zh := language == "zh"
	if location == nil {
		location = time.UTC
	}
	title, supported := eventTitles[event.Kind]
	if !supported {
		title = eventText{"Unknown event", "未知事件"}
	}
	severity := map[model.Severity]eventText{model.SeverityCritical: {"CRITICAL", "严重"}, model.SeverityHigh: {"HIGH", "高"}, model.SeverityMedium: {"MEDIUM", "中"}, model.SeverityLow: {"LOW", "低"}, model.SeverityInfo: {"INFO", "信息"}}[event.Severity]
	if severity.en == "" {
		severity = eventText{"UNKNOWN", "未知"}
	}
	observed := localText(zh, "unknown", "未知")
	if !event.ObservedAt.IsZero() {
		observed = event.ObservedAt.In(location).Format("2006-01-02 15:04-0700")
	}
	coverage := localText(zh, "Coverage unknown", "覆盖未知")
	if event.Evidence["coverage_complete"] == "false" || event.Evidence["coverage"] == "incomplete" {
		coverage = localText(zh, "Coverage incomplete", "覆盖不足")
	}
	semantic := OfficialSemantic{HostAlias: officialField(hostname, 80), EventKind: title.local(zh), Phase: phaseText(event.Phase, zh), Severity: severity.local(zh), Time: observed, BoundedSummary: officialEventSummary(event, supported, zh), Coverage: coverage, LocalReference: "sudo noderampart notify list"}
	return FormatOfficialSummary(semantic, language), semantic
}

func FormatOfficialTest(hostname, language string) (string, OfficialSemantic) {
	zh := language == "zh"
	semantic := OfficialSemantic{HostAlias: officialField(hostname, 80), EventKind: localText(zh, "Test", "测试"), Phase: localText(zh, "synthetic", "合成"), Severity: localText(zh, "TEST", "测试"), Time: localText(zh, "synthetic", "合成"), BoundedSummary: localText(zh, "Synthetic notification", "合成通知"), Coverage: localText(zh, "Coverage unknown", "覆盖未知"), LocalReference: "sudo noderampart notify list"}
	return FormatOfficialSummary(semantic, language), semantic
}

func FormatOfficialSummary(s OfficialSemantic, language string) string {
	return nativeLines([]string{"NodeRampart " + s.Severity + " " + s.Time, s.HostAlias + " " + s.EventKind + " " + s.Phase, s.BoundedSummary, s.Coverage}, s.LocalReference, language)
}

func officialEventSummary(event model.Event, supported, zh bool) string {
	if !supported {
		return localText(zh, "Unsupported kind; details local", "未支持的类别；详情在本地")
	}
	if floodKind(event.Kind) {
		rate, ok := number(event.Evidence["observed_rate"])
		threshold, limitOK := number(event.Evidence["threshold_rate"])
		if !ok || !limitOK {
			return localText(zh, "Rate/threshold unknown", "速率或阈值未知")
		}
		unit := localText(zh, "pkt/s", "包/秒")
		if event.Kind == "bandwidth_spike" {
			unit = localText(zh, "B/s", "字节/秒")
		}
		return fmt.Sprintf(localText(zh, "Rate %s/%s %s", "速率 %s/阈值 %s %s"), rate, threshold, unit)
	}
	switch event.Kind {
	case "ssh_login_success":
		return localText(zh, "SSH success", "SSH 认证成功")
	case "ssh_brute_force":
		if event.Count > 0 {
			return fmt.Sprintf(localText(zh, "SSH failures=%d", "SSH 失败=%d"), event.Count)
		}
		return localText(zh, "SSH failures unknown", "SSH 失败计数未知")
	case "port_scan":
		if event.Count > 0 {
			return fmt.Sprintf(localText(zh, "Ports=%d", "端口数=%d"), event.Count)
		}
		return localText(zh, "Port count unknown", "端口计数未知")
	}
	if strings.HasPrefix(event.Kind, "health_") {
		diagnostic := healthDiagnostic(event, time.UTC, zh).compact
		if reason, ok := reasonText[event.Evidence["reason"]]; ok {
			if diagnostic != "" {
				return reason.local(zh) + "; " + diagnostic
			}
			return reason.local(zh)
		}
		if diagnostic != "" {
			return diagnostic
		}
		return localText(zh, "Health observation; details local", "健康观察；详情在本地")
	}
	c := model.ProjectAlertContext(event.Kind, event.Evidence)
	if c == nil {
		return localText(zh, "Budget comparison unknown", "预算比较参数未知")
	}
	var values []string
	if c.ObservedBytes != nil {
		values = append(values, localText(zh, "TX=", "出站=")+strconv.FormatUint(*c.ObservedBytes, 10)+localText(zh, "B", "字节"))
	}
	if c.ThresholdBytes != nil {
		values = append(values, localText(zh, "limit=", "阈值=")+strconv.FormatUint(*c.ThresholdBytes, 10)+localText(zh, "B", "字节"))
	}
	if c.ObservedCost != nil {
		values = append(values, localText(zh, "estimate=", "估算=")+strconv.FormatFloat(*c.ObservedCost, 'g', -1, 64)+" "+officialField(c.Currency, 16))
	}
	if c.ThresholdCost != nil {
		values = append(values, localText(zh, "budget=", "预算=")+strconv.FormatFloat(*c.ThresholdCost, 'g', -1, 64)+" "+officialField(c.Currency, 16))
	}
	if c.GrowthRatio != nil {
		values = append(values, localText(zh, "growth=", "增长=")+strconv.FormatFloat(*c.GrowthRatio, 'g', -1, 64))
	}
	if c.BaselineMeanBytes != nil {
		values = append(values, localText(zh, "baseline=", "基线=")+strconv.FormatFloat(*c.BaselineMeanBytes, 'g', -1, 64)+localText(zh, "B", "字节"))
	}
	if len(values) == 0 {
		return localText(zh, "Budget comparison unknown", "预算比较参数未知")
	}
	return strings.Join(values, " ")
}

// RecipientPreview never exposes a raw account or recipient identifier.
func (s *Official) RecipientPreview() string {
	switch s.channel {
	case "qqbot", "line":
		return s.credential.TargetType + " ***" + s.credential.TargetID[len(s.credential.TargetID)-4:]
	case "twilio_sms":
		return "***" + maskedPhoneSuffix(s.credential.To)
	case "whatsapp_cloud":
		return "***" + maskedPhoneSuffix(s.credential.Recipient)
	}
	return "[protected recipient]"
}

func maskedPhoneSuffix(value string) string {
	if len(value) > 2 {
		return value[len(value)-2:]
	}
	return "**"
}
