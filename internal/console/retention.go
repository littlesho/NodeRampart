// SPDX-License-Identifier: MIT

package console

var retentionLabels = map[string][2]string{
	"":                                        {"All", "全部"},
	"events":                                  {"Event detail", "事件明细"},
	"traffic_hourly":                          {"Traffic attribution", "流量归属明细"},
	"auth_hourly":                             {"SSH source totals", "SSH 来源汇总"},
	"interface_hourly":                        {"Interface traffic totals", "网卡流量总量"},
	"interface_detail_hourly":                 {"Per-interface traffic", "逐网卡流量"},
	"collector_health_hourly":                 {"Sensor loss counters", "采集损失计数"},
	"report_snapshots":                        {"Saved reports", "已保存报告"},
	"coverage_intervals":                      {"Component coverage", "组件覆盖区间"},
	"coverage_gaps":                           {"Recorded data gaps", "已记录数据缺口"},
	"notification_outbox":                     {"Notification history", "通知历史"},
	"notification_silences":                   {"Silence history", "静默历史"},
	"time_expiry":                             {"Retention period ended", "保留期结束"},
	"storage_pressure":                        {"Storage capacity pressure", "存储容量不足"},
	"cardinality_compaction":                  {"Detail merged at the entry limit", "达到条目上限，合并明细"},
	"capacity_eviction":                       {"History entry limit reached", "达到历史条目上限"},
	"silence_body_discard":                    {"Silenced notification body cleared", "清除已静默通知正文"},
	"totals_preserved":                        {"Totals retained when detail was merged", "合并明细时保留总量"},
	"notification_outcome_preserved":          {"Notification outcome retained at this operation", "本次操作保留通知结果"},
	"event_decisions_have_separate_retention": {"Event notification decisions have separate retention", "事件通知决策有独立保留期"},
	"none_guaranteed":                         {"Inspect remaining data; no equivalent detail is guaranteed", "请检查剩余数据，不保证存在等价明细"},
}

func retentionLabel(key, value, language string) string {
	if key != "dataset" && key != "reason" && key != "aggregate_survives" {
		return value
	}
	if names, ok := retentionLabels[value]; ok {
		if language == "zh" {
			return names[1]
		}
		return names[0]
	}
	return value
}
