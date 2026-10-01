// SPDX-License-Identifier: MIT

package report

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"

	"github.com/littlesho/NodeRampart/internal/api"
	"github.com/littlesho/NodeRampart/internal/store"
)

// NativeNotificationBody projects retained scalars into one short message. It
// never copies the full archive, source identities, logs or billing provider URL.
func NativeNotificationBody(snapshot store.ReportSnapshot, language string) (string, error) {
	if !api.ValidDate(snapshot.Date) {
		return "", errors.New("invalid native report date")
	}
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
	lines := []string{tr("NodeRampart — daily report ", "NodeRampart — 日报 ") + snapshot.Date}
	footer := "sudo noderampart report show --date " + snapshot.Date + " --format json"
	if len(snapshot.Document) == 0 {
		lines = append(lines, tr("Legacy snapshot: original timezone/statistics/coverage unavailable; missing history is not zero.", "旧快照：原始时区、统计及覆盖未知；缺失历史不等于零。"))
		return nativeReportSummary(lines, footer, language), nil
	}
	doc, err := DecodeDocument(snapshot.Document)
	if err != nil {
		return "", err
	}
	if doc.Date != snapshot.Date || !doc.PeriodStart.Equal(snapshot.PeriodStart) || !doc.PeriodEnd.Equal(snapshot.PeriodEnd) {
		return "", errors.New("native report period conflicts with archived document")
	}
	location, known, err := notificationLocation(doc.Timezone)
	if err != nil {
		return "", err
	}
	lines = append(lines, tr("Period (end exclusive): ", "区间（不含结束时刻）：")+notificationTime(doc.PeriodStart.In(location))+" — "+notificationTime(doc.PeriodEnd.In(location)), tr("Timezone: ", "时区：")+doc.Timezone)
	if !known {
		lines = append(lines, tr("Original timezone unknown; times shown in UTC.", "原始时区未知；时刻以 UTC 显示。"))
	}
	// Coverage comes before optional facts so truncation cannot imply healthy data.
	for _, name := range []string{"interface_counter", "sensor_feed", "ssh_journal"} {
		state := notificationCoverage(doc, name)
		label := name
		if language == "zh" {
			label = map[string]string{"interface_counter": "接口计数", "sensor_feed": "报文采集", "ssh_journal": "SSH 日志"}[name]
			state = map[string]string{"complete": "完整运行记录", "partial": "部分", "unknown": "未知", "disabled": "停用"}[state]
		}
		lines = append(lines, tr("Coverage ", "覆盖 ")+label+": "+state)
	}
	lines = append(lines, tr("Missing observations are unknown; coverage does not prove lossless data. Guest TX is an estimate, not a bill.", "缺失观测为未知；覆盖不证明无丢失。接口出站为估算，不等于账单。"))
	if len(doc.Integrity.Gaps) > 0 || doc.Integrity.HistoryTruncated || doc.Integrity.GapsTruncated || doc.Integrity.More || doc.Summary.HealthCounterSaturations > 0 {
		lines = append(lines, tr("WARNING: retained gaps/bounded history/lower-bound counters; inspect locally.", "警告：覆盖缺口、历史边界或计数下界；请在本地查看。"))
	}
	s := doc.Summary
	var eventCount, authCount uint64
	for _, e := range s.Events {
		if math.MaxUint64-eventCount < e.Count {
			eventCount = math.MaxUint64
		} else {
			eventCount += e.Count
		}
	}
	for _, a := range s.Auth {
		if math.MaxUint64-authCount < a.Count {
			authCount = math.MaxUint64
		} else {
			authCount += a.Count
		}
	}
	lines = append(lines, fmt.Sprintf(tr("Retained events %d; SSH observations %d (counts may be lower bounds)", "保留事件 %d；SSH 观测 %d（计数可能为下界）"), eventCount, authCount))
	lines = append(lines, fmt.Sprintf(tr("Interface RX %s / TX %s; sensor batches %d", "接口接收 %s / 发送 %s；采集批次 %d"), formatBytes(s.Interface.RXBytes), formatBytes(s.Interface.TXBytes), s.Batches), fmt.Sprintf(tr("Capture drops %d; parse/statistics errors %d/%d; overflow %d packets", "捕获丢包 %d；解析/统计错误 %d/%d；溢出 %d 个包"), s.KernelDrops, s.ParseErrors, s.KernelStatsErrors, s.OverflowPackets), fmt.Sprintf(tr("IPC loss estimate %d batches / %d packets", "IPC 丢失估计 %d 批次 / %d 个包"), s.IPCDroppedBatches, s.IPCDroppedPackets))
	if doc.Billing != nil {
		b := doc.Billing
		if b.Validate() != nil {
			return "", errors.New("invalid native billing snapshot")
		}
		lines = append(lines, tr("Estimated cycle cost: ", "周期费用估算：")+strconv.FormatFloat(b.Estimate.Cost, 'f', 2, 64)+" "+b.Profile.Currency, tr("Whole-cycle coverage unknown; private/duplicate paths and excluded costs limit this estimate.", "全周期覆盖未知；私网、重复路径及模型外费用限制此估算。"))
	}
	if len(doc.Integrity.Gaps) > 0 || doc.Integrity.HistoryTruncated || doc.Integrity.GapsTruncated || doc.Integrity.More || s.HealthCounterSaturations > 0 {
		lines = append(lines, tr("WARNING: retained coverage gaps, bounded history or lower-bound counters; inspect locally.", "警告：覆盖缺口、历史边界或计数下界；请在本地查看。"))
	}
	if doc.Integrity.Retention == nil || len(doc.Integrity.Retention.Entries) > 0 {
		lines = append(lines, tr("Retention history is unavailable or pruning overlaps the report.", "裁剪历史未知或本区间有裁剪。"))
	}
	return nativeReportSummary(lines, footer, language), nil
}

// Reserve the footer and visible shortening marker. Product text is fixed;
// control/format characters in archive labels cannot manufacture new fields.
func nativeReportSummary(lines []string, footer, language string) string {
	marker := "… shortened; inspect locally."
	if language == "zh" {
		marker = "… 已缩略；请在本地查看。"
	}
	var output strings.Builder
	for _, line := range lines {
		var safe strings.Builder
		for _, r := range line {
			if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
				r = ' '
			}
			if r == '@' {
				r = '＠'
			}
			if safe.Len()+len(string(r))+3 > 700 {
				safe.WriteString("…")
				break
			}
			safe.WriteRune(r)
		}
		if output.Len()+safe.Len()+len(footer)+len(marker)+3 > 1800 {
			output.WriteString("\n" + marker)
			break
		}
		if output.Len() > 0 {
			output.WriteByte('\n')
		}
		output.WriteString(safe.String())
	}
	output.WriteString("\n" + footer)
	return output.String()
}
