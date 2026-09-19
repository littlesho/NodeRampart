// SPDX-License-Identifier: MIT

package console

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

var resultLabels = map[string][2]string{
	"checks": {"Check execution in this process", "本进程的检查执行情况"}, "group": {"Check group", "检查分组"},
	"last_attempt_at_utc":            {"Latest check started (UTC)", "最近开始检查（UTC）"},
	"last_completed_at_utc":          {"Latest check completed (UTC)", "最近完成检查（UTC）"},
	"last_successful_check_utc":      {"Latest known evaluation (UTC)", "最近获得明确判断（UTC）"},
	"duration_milliseconds":          {"Check duration (ms)", "检查耗时（毫秒）"},
	"delay_milliseconds":             {"Delay beyond the one-minute cadence (ms)", "超出一分钟检查间隔的延迟（毫秒）"},
	"in_progress":                    {"Check in progress", "检查进行中"},
	"last_persisted_observation_utc": {"Last confirmed saved observation (UTC)", "最近确认保存的观察（UTC）"},
	"pending_since_utc":              {"Waiting to persist since (UTC)", "从此时间起等待保存（UTC）"},
	"pending_age_milliseconds":       {"Time waiting to persist (ms)", "等待保存时长（毫秒）"},
	"previous_period":                {"Previous recorded month closing", "上个已记录月份的结算结果"},
	"evaluated":                      {"Historical inputs evaluated", "已使用历史依据结算"},
	"interfaces":                     {"Observed interfaces", "观察网卡"}, "sensor_interfaces": {"Latest sensor data by interface", "各网卡最新采集时间"},
	"storage_budget": {"Storage budget", "存储预算"}, "auth_detection": {"SSH detection", "SSH 检测"},
	"coverage_gaps": {"Coverage gaps", "覆盖缺口"}, "version": {"Version", "版本"}, "started_at_utc": {"Started at (UTC)", "启动时间（UTC）"},
	"uptime": {"Uptime", "运行时长"}, "sensor_required": {"Sensor required", "采集器是否必要"}, "last_sensor_batch_utc": {"Latest sensor data (UTC)", "最新采集数据（UTC）"},
	"telegram_enabled": {"Telegram enabled", "Telegram 已启用"}, "geo_enabled": {"GeoIP enabled", "GeoIP 已启用"},
	"components": {"Components", "组件"}, "storage": {"Storage health", "存储健康"}, "queue": {"Notification queue", "通知队列"}, "journal": {"SSH journal", "SSH 日志"},
	"event_ingest": {"Event persistence", "事件持久化"}, "detection": {"Network detection", "网络检测"}, "history": {"Historical coverage", "历史覆盖"},
	"current": {"Current status", "当前状态"}, "interface_traffic": {"Interface traffic", "网卡流量"}, "rx_bytes": {"Received bytes", "接收字节数"},
	"tx_bytes": {"Sent bytes", "发送字节数"}, "rx_packets": {"Received packets", "接收包数"}, "tx_packets": {"Sent packets", "发送包数"},
	"interface_identity_unavailable": {"Traffic without an available interface identity", "缺少网卡标识的流量"}, "totals_consistent": {"Totals reconcile", "总量一致"},
	"interfaces_truncated": {"Interface list truncated", "网卡列表已截断"}, "notes": {"Notes", "说明"}, "report": {"Report", "报告"}, "reports": {"Saved reports", "已保存日报"},
	"body": {"Content", "内容"}, "date": {"Date", "日期"}, "events": {"Events", "事件"}, "incidents": {"Incidents", "Incident 列表"}, "notifications": {"Notifications", "通知"},
	"state": {"State", "状态"}, "name": {"Name", "名称"}, "id": {"ID", "ID"}, "kind": {"Event kind", "事件类型"}, "severity": {"Severity", "严重程度"},
	"summary": {"Summary", "概要"}, "observed_at_utc": {"Observed at (UTC)", "观察时间（UTC）"}, "incident_id": {"Incident ID", "Incident ID"},
	"next_before": {"Next page: before", "下一页：before"}, "next_before_id": {"Next page: before ID", "下一页：before_id"},
	"next_until_utc": {"Next page: until (UTC)", "下一页：until（UTC）"}, "next_after_id": {"Next page: after ID", "下一页：after_id"},
	"next_date": {"Resume from date", "继续补齐的日期"}, "status": {"Status", "状态"}, "reason": {"Reason", "原因"}, "error": {"Error", "错误"},
	"pending": {"Pending", "待处理"}, "sent": {"Sent", "已发送"}, "failed": {"Failed", "失败"}, "suppressed": {"Silenced", "已静默"}, "expired": {"Expired", "已到期"},
	"monitoring": {"Budget and health alerts", "预算与健康告警"}, "rules": {"Rules", "规则"}, "available": {"Evidence available", "依据可用"},
	"alert": {"Alert inputs saved with this event", "此事件保存的告警依据"}, "availability": {"Recorded context availability", "事件依据完整程度"},
	"metric": {"Alert metric", "告警指标"}, "period": {"Accounting period", "统计周期"}, "period_start_utc": {"Period starts (UTC)", "周期起点（UTC）"}, "period_end_utc": {"Period ends (UTC)", "周期终点（UTC）"},
	"milestone": {"Durable milestone (%)", "已记录阈值（%）"}, "coverage": {"Coverage qualification", "覆盖情况"}, "basis": {"Calculation basis", "计算依据"},
	"observed_bytes": {"Observed outgoing bytes", "已观测出站字节数"}, "threshold_bytes": {"Traffic threshold (bytes)", "流量阈值（字节）"},
	"observed_cost": {"Estimated cost", "估算费用"}, "threshold_cost": {"Cost threshold", "费用阈值"}, "currency": {"Currency", "币种"},
	"baseline_mean_bytes": {"Baseline daily mean (bytes)", "基线日均流量（字节）"}, "baseline_days": {"Covered baseline days", "有覆盖的参考天数"},
	"growth_ratio": {"Daily growth multiple", "日增长倍数"}, "condition_since_utc": {"Condition observed since (UTC)", "异常起始时间（UTC）"},
	"retention": {"Retention and pruning evidence", "保留与裁剪记录"}, "entries": {"Ledger entries", "台账条目"}, "dataset": {"Dataset", "数据类型"},
	"affected_rows": {"Affected source rows", "受影响源记录数"}, "operations": {"Recorded operations", "已记录操作次数"},
	"data_start_utc": {"Affected data starts (UTC)", "受影响数据起点（UTC）"}, "data_end_utc": {"Affected data ends (UTC)", "受影响数据终点（UTC）"},
	"tracking_started_utc": {"Ledger tracking began (UTC)", "台账开始记录时间（UTC）"}, "evicted_entries": {"Detailed ledger entries retired", "已淘汰台账明细条数"},
	"aggregate_survives": {"What was retained at removal", "处理时仍保留的内容"}, "retained": {"Data currently retained in this period", "此时间段当前保留的数据"},
	"totals": {"Lifetime ledger totals (all periods)", "台账累计（所有时间段）"}, "detail_limit": {"Detailed ledger entry limit", "台账明细条数上限"},
}

// Human-readable structure for existing JSON results. Unknown fields remain
// visible, exact integers are preserved, and rendering work has explicit bounds.
func humanResult(text, language string) string {
	if len(text) > maxOutputBytes {
		return text
	}
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	var value, extra any
	if decoder.Decode(&value) != nil || decoder.Decode(&extra) != io.EOF {
		return text
	}
	var out strings.Builder
	nodes := 0
	var walk func(any, int)
	label := func(key string) string {
		if names, ok := resultLabels[key]; ok {
			if language == "zh" {
				return names[1]
			}
			return names[0]
		}
		return strings.ReplaceAll(key, "_", " ")
	}
	walk = func(v any, depth int) {
		if out.Len() > maxOutputBytes || nodes >= 4096 {
			return
		}
		nodes++
		indent := strings.Repeat("  ", depth)
		if depth > 12 {
			out.WriteString(indent + "…\n")
			return
		}
		switch item := v.(type) {
		case map[string]any:
			keys := make([]string, 0, len(item))
			for key := range item {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			if len(keys) == 0 {
				out.WriteString(indent + "—\n")
			}
			for _, key := range keys {
				if out.Len() > maxOutputBytes || nodes >= 4096 {
					break
				}
				switch child := item[key].(type) {
				case map[string]any, []any:
					out.WriteString(indent + label(key) + ":\n")
					walk(child, depth+1)
				default:
					out.WriteString(indent + label(key) + ": ")
					if text, ok := child.(string); ok {
						child = monitorExecutionLabel(key, alertContextLabel(key, retentionLabel(key, text, language), language), language)
					}
					walk(child, 0)
				}
			}
		case []any:
			if len(item) == 0 {
				out.WriteString(indent + "—\n")
			}
			for i, child := range item {
				if out.Len() > maxOutputBytes || nodes >= 4096 {
					break
				}
				out.WriteString(fmt.Sprintf("%s%d.\n", indent, i+1))
				walk(child, depth+1)
			}
		case string:
			out.WriteString(indent + item + "\n")
		case nil:
			out.WriteString(indent + "—\n")
		case bool:
			word := "No"
			if item {
				word = "Yes"
			}
			if language == "zh" {
				word = "否"
				if item {
					word = "是"
				}
			}
			out.WriteString(indent + word + "\n")
		default:
			fmt.Fprintf(&out, "%s%v\n", indent, item)
		}
	}
	walk(value, 0)
	if out.Len() > maxOutputBytes || nodes >= 4096 {
		out.WriteString("\nResult display was truncated / 结果显示已截断\n")
	}
	return out.String()
}

func monitorExecutionLabel(key, value, language string) string {
	var label [2]string
	if key == "group" {
		switch value {
		case "health":
			label = [2]string{"Health", "采集与存储健康"}
		case "budget":
			label = [2]string{"Budget", "流量与费用预算"}
		}
	} else if key == "state" {
		switch value {
		case "completed":
			label = [2]string{"Completed with known observations", "完成，已获得明确判断"}
		case "partial":
			label = [2]string{"Completed with unavailable checks or pending writes", "完成，部分依据不可用或等待保存"}
		case "timeout":
			label = [2]string{"Check interrupted or timed out", "检查中断或超时"}
		case "not_checked":
			label = [2]string{"Not checked in this process", "本进程尚未检查"}
		}
	} else if key == "reason" {
		switch value {
		case "period_close_pending":
			label = [2]string{"Previous recorded month is waiting to close", "等待结算上个已记录月份"}
		case "historical_policy_unavailable":
			label = [2]string{"Historical threshold or tariff inputs unavailable", "缺少历史阈值或计价依据"}
		case "schedule_failed":
			label = [2]string{"GeoIP update scheduling failed", "GeoIP 自动更新调度失败"}
		}
	}
	if label[0] == "" {
		return value
	}
	if language == "zh" {
		return label[1]
	}
	return label[0]
}

func alertContextLabel(key, value, language string) string {
	var label [2]string
	switch key {
	case "availability":
		switch value {
		case "recorded":
			label = [2]string{"Complete inputs saved with the event", "已保存完整事件依据"}
		case "partial":
			label = [2]string{"Partial saved inputs; omitted values are unavailable", "仅有部分事件依据；省略的值不可用"}
		case "unavailable":
			label = [2]string{"No usable inputs saved with the event", "没有可用的事件依据"}
		}
	case "basis":
		if value == "selected_interface_guest_tx" {
			label = [2]string{"Observed selected-interface guest TX estimate", "所选网卡已观测出站流量估算"}
		}
	case "metric":
		label = map[string][2]string{
			"budget_month_bytes": {"Monthly outgoing traffic", "月出站流量"}, "budget_month_cost": {"Monthly estimated cost", "月估算费用"},
			"budget_day_bytes": {"Completed-day outgoing traffic", "完整日出站流量"}, "budget_day_growth": {"Completed-day growth", "完整日流量增长"},
			"health_sensor": {"Packet sensor health", "数据包采集器健康"}, "health_interface_counter": {"Interface counter health", "网卡计数健康"},
			"health_ssh_journal": {"SSH journal health", "SSH 日志健康"}, "health_storage": {"Storage health", "存储健康"},
			"health_geoip_update": {"GeoIP update health", "GeoIP 更新健康"},
		}[value]
	}
	if label[0] == "" {
		return value
	}
	if language == "zh" {
		return label[1]
	}
	return label[0]
}
