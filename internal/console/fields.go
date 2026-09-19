// SPDX-License-Identifier: MIT

package console

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/littlesho/NodeRampart/internal/config"
)

type field struct {
	path, group, en, zh, helpEN, helpZH string
	choices                             []string
}

// Each editable config leaf has exactly one entry. Tests compare this catalog
// with Config's JSON fields so future settings cannot silently disappear.
var fields = []field{
	{"hostname", "general", "Host display name", "主机显示名称", "1–255 bytes; used in reports and notifications.", "1–255 字节；用于报告和通知。", nil},
	{"sensor.enabled", "sensor", "Observe network traffic", "观察网络流量", "Enable the packet metadata sensor.", "启用报文元数据采集。", yesNo},
	{"sensor.required", "sensor", "Require packet sensor", "要求采集器可用", "Treat an unavailable sensor as a required component failure.", "将采集器不可用视为必要组件故障。", yesNo},
	{"sensor.interface", "sensor", "Legacy single interface", "兼容单网卡设置", "Blank for automatic selection. Cannot be combined with the interface list.", "留空自动选择；不可与网卡列表同时填写。", nil},
	{"sensor.interfaces", "sensor", "Interface list", "网卡列表", "Up to 8 comma-separated names. Blank selects default IPv4/IPv6 routes.", "最多 8 个网卡名，以逗号分隔；留空按 IPv4/IPv6 默认路由选择。", nil},
	{"sensor.batch_interval", "sensor", "Sampling interval", "采样间隔", "100ms–1m; default 1s.", "100ms–1m；默认 1s。", nil},
	{"sensor.max_tracked_flows", "sensor", "Maximum tracked flows", "最大跟踪流数量", "64–4096, shared across selected interfaces.", "64–4096，由所选网卡共享。", nil},
	{"sensor.receive_buffer_bytes", "sensor", "Receive buffer (bytes)", "接收缓冲区（字节）", "65536–268435456 bytes; default 4194304.", "65536–268435456 字节；默认 4194304。", nil},
	{"auth.enabled", "auth", "Monitor SSH authentication", "监控 SSH 认证", "Reads local journald authentication metadata.", "读取本机 journald 认证元数据。", yesNo},
	{"auth.journalctl", "auth", "journalctl program", "journalctl 程序", "Only the fixed system journalctl paths are supported.", "仅支持固定系统 journalctl 路径。", []string{"/usr/bin/journalctl", "/bin/journalctl"}},
	{"auth.threshold", "auth", "Failed login threshold", "认证失败次数阈值", "2–4096 failures within the window; default 8.", "窗口内 2–4096 次失败；默认 8。", nil},
	{"auth.window", "auth", "Authentication window", "认证统计窗口", "1s–24h; default 5m.", "1s–24h；默认 5m。", nil},
	{"auth.cooldown", "auth", "Authentication cooldown", "认证告警冷却时间", "0s–168h; default 15m.", "0s–168h；默认 15m。", nil},
	{"detection.syn_packets_per_second", "detection", "SYN packets per second", "每秒 SYN 包数", "Unsigned integer; default 5000.", "非负整数；默认 5000。", nil},
	{"detection.udp_packets_per_second", "detection", "UDP packets per second", "每秒 UDP 包数", "Unsigned integer; default 10000.", "非负整数；默认 10000。", nil},
	{"detection.icmp_packets_per_second", "detection", "ICMP packets per second", "每秒 ICMP 包数", "Unsigned integer; default 2000.", "非负整数；默认 2000。", nil},
	{"detection.bytes_per_second", "detection", "Bytes per second", "每秒字节数", "Unsigned integer; default 104857600 bytes/s.", "非负整数；默认 104857600 字节/秒。", nil},
	{"detection.recovery_ratio", "detection", "Recovery threshold ratio", "恢复阈值比例", "Greater than 0 and less than 1; default 0.5.", "大于 0 且小于 1；默认 0.5。", nil},
	{"detection.recovery_windows", "detection", "Recovery windows", "恢复确认窗口数", "1–60 consecutive windows; default 3.", "连续 1–60 个窗口；默认 3。", nil},
	{"detection.update_interval", "detection", "Ongoing event update interval", "持续事件更新间隔", "1s–24h; default 5m.", "1s–24h；默认 5m。", nil},
	{"detection.scan_unique_ports", "detection", "Distinct ports for scan detection", "端口扫描不同端口数", "2–65535; default 20.", "2–65535；默认 20。", nil},
	{"detection.scan_window", "detection", "Port scan window", "端口扫描统计窗口", "1s–1h; default 1m.", "1s–1h；默认 1m。", nil},
	{"geo.city_mmdb", "geo", "Local City database path", "本地 City 数据库路径", "Blank disables City lookup. Use GeoIP setup for managed downloads.", "留空关闭 City 查询；托管下载请使用 GeoIP 设置。", nil},
	{"geo.asn_mmdb", "geo", "Local ASN database path", "本地 ASN 数据库路径", "Blank disables ASN lookup. Database files must be readable by the daemon.", "留空关闭 ASN 查询；数据库文件须对守护进程可读。", nil},
	{"notifications.merge_window", "notifications", "Merge ongoing alerts", "合并持续事件告警", "0s–24h; 0s disables merging, default 10m.", "0s–24h；0s 关闭合并，默认 10m。", nil},
	{"notifications.telegram.enabled", "notifications", "Enable Telegram", "启用 Telegram", "Configure credentials using Telegram setup before enabling.", "启用前请通过 Telegram 设置填写凭据。", yesNo},
	{"notifications.telegram.token_file", "notifications", "Telegram token file path", "Telegram Token 文件路径", "Advanced: daemon-readable 0600 file. The token is never displayed.", "高级：守护进程可读的 0600 文件；不会显示 Token。", nil},
	{"notifications.telegram.chat_id", "notifications", "Telegram chat ID", "Telegram 会话 ID", "Your private chat or group ID; at most 128 bytes.", "私人会话或群组 ID；最多 128 字节。", nil},
	{"notifications.telegram.timeout", "notifications", "Telegram request timeout", "Telegram 请求超时", "1s–1m; default 10s.", "1s–1m；默认 10s。", nil},
	{"reports.enabled", "reports", "Create daily reports", "生成日报", "Daily reports are stored locally; delivery needs Telegram enabled.", "日报保存在本地；推送需启用 Telegram。", yesNo},
	{"reports.daily_at", "reports", "Daily report time", "日报时间", "24-hour HH:MM in the report timezone; default 09:00.", "报告时区的 24 小时制 HH:MM；默认 09:00。", nil},
	{"reports.timezone", "reports", "Report timezone", "报告时区", "Local, UTC or an IANA name such as Asia/Shanghai. Old reports are immutable.", "Local、UTC 或 Asia/Shanghai 等 IANA 时区名；旧日报保持不变。", nil},
	{"reports.top_n", "reports", "Top entries in reports", "报告排名条数", "1–50; default 10.", "1–50；默认 10。", nil},
	{"reports.backfill_days", "reports", "Automatic backfill days", "自动补齐天数", "0–31; 0 disables. Historical reports remain local.", "0–31；0 关闭；历史补报仅保存在本地。", nil},
	{"privacy.notification_ip", "privacy", "IP addresses in notifications", "通知中的 IP 显示", "prefix masks the host; hash is stable with your key; full reveals the address.", "prefix 隐去主机部分；hash 使用密钥生成稳定标识；full 显示完整地址。", []string{"prefix", "hash", "full"}},
	{"privacy.store_ip", "privacy", "Stored IP addresses", "存储的 IP 显示", "Changes affect future records only. Existing records are not rewritten.", "仅影响未来记录；不会改写已有记录。", []string{"prefix", "hash", "full"}},
	{"privacy.hash_key_file", "privacy", "Privacy key file path", "隐私密钥文件路径", "Required for hash mode. Generate a key from the privacy menu first.", "hash 模式必填；可先在隐私菜单生成密钥。", nil},
	{"billing.enabled", "billing", "Estimate public Internet egress", "估算公网出站流量费用", "Guest traffic estimate only; excludes instances, storage, NAT and taxes.", "仅按来宾流量估算；不含实例、存储、NAT 或税费。", yesNo},
	{"billing.profile_path", "billing", "Billing profile path", "费用配置文件路径", "Use cloud pricing setup or a validated custom profile.", "使用云价格设置或经验证的自定义费用配置。", nil},
	{"alerts.budget.enabled", "budget_alerts", "Enable budget alerts", "启用预算告警", "Configure at least one limit. Records stay local unless Telegram is enabled. Selected-interface TX is an estimate, including private traffic and possible duplicate paths.", "至少设置一项额度；启用 Telegram 后才推送。按所选网卡出站量估算，可能含内网流量及重复路径。", yesNo},
	{"alerts.budget.monthly_bytes", "budget_alerts", "Monthly traffic budget (bytes)", "月度流量额度（字节）", "0 disables this metric; otherwise 1–9223372036854775807. Remind once at 80% and 100% each month in the report timezone.", "0 关闭本项；否则 1–9223372036854775807。按报告时区每月达到 80% 和 100% 各提醒一次。", nil},
	{"alerts.budget.monthly_cost", "budget_alerts", "Monthly estimated-cost budget", "月度估算费用额度", "0 disables; otherwise up to 1e12 in the configured tariff currency. Requires billing enabled. This is not an invoice.", "0 关闭本项；最高 1e12，单位为费用配置中的币种。需启用费用估算；不代表账单。", nil},
	{"alerts.budget.daily_bytes", "budget_alerts", "Completed-day traffic limit (bytes)", "完整日流量阈值（字节）", "0 disables; otherwise 1–9223372036854775807. Checks the most recent completed day, not a partial day.", "0 关闭本项；否则 1–9223372036854775807。检查最近一个已结束日期的流量。", nil},
	{"alerts.budget.daily_growth_ratio", "budget_alerts", "Daily growth multiple", "日流量增长倍数", "0 disables; otherwise greater than 1 and at most 100. Requires adequately covered target day and at least three adequately covered reference days and a positive mean.", "0 关闭本项；否则大于 1 且不超过 100。需目标日有足够覆盖，且至少三个参考日形成正数基线。", nil},
	{"alerts.budget.baseline_days", "budget_alerts", "Daily comparison history (days)", "日增量参考天数", "3–30 completed days; default 7. Missing history is unavailable, not zero traffic.", "3–30 个已结束日期；默认 7。缺少历史时显示数据不足，不按零流量计算。", nil},
	{"alerts.health.enabled", "health_alerts", "Enable health alerts", "启用健康告警", "Observe collection, storage and managed GeoIP updates. Disabled collectors are not failures. Delivery uses the existing notification queue.", "监测采集、存储及托管 GeoIP 更新；主动关闭的采集器不视为故障。使用现有通知队列投递。", yesNo},
	{"alerts.health.grace_period", "health_alerts", "Failure confirmation time", "异常确认时间", "10s–1h; default 2m. A condition must remain abnormal before its first alert; evaluation runs once per minute.", "10s–1h；默认 2m。异常持续达到此时间后告警；每分钟检查一次。", nil},
	{"alerts.health.recovery_period", "health_alerts", "Recovery confirmation time", "恢复确认时间", "10s–1h; default 1m. Unknown or unreadable status cannot confirm recovery.", "10s–1h；默认 1m。未知或读取失败不能确认恢复。", nil},
	{"alerts.health.reminder_interval", "health_alerts", "Ongoing failure reminders", "持续异常提醒间隔", "0s disables reminders; otherwise 1h–168h, default 24h. Event merging and silences also apply.", "0s 关闭重复提醒；否则 1h–168h，默认 24h。仍遵守告警合并和静默。", nil},
	{"alerts.health.geoip_failure_threshold", "health_alerts", "Consecutive GeoIP failures", "GeoIP 连续失败次数", "1–30; default 2. Unchanged but verified data counts as a successful check.", "1–30；默认 2。内容未变但验证成功也算更新检查成功。", nil},
	{"alerts.health.geoip_stale_after", "health_alerts", "Scheduled GeoIP check overdue after", "GeoIP 定时检查逾期时间", "24h–720h; default 72h. Applies to the last managed enabled schedule. Credentials remain private.", "24h–720h；默认 72h。适用于上次托管操作启用的更新计划；凭据保持私有。", nil},
	{"storage.max_bytes", "storage", "Database budget (bytes)", "数据库容量预算（字节）", "67108864–1099511627776; default 1073741824 (1 GiB).",
		"67108864–1099511627776；默认 1073741824（1 GiB）。", nil},
	{"storage.min_free_bytes", "storage", "Minimum free disk space (bytes)", "最低磁盘剩余空间（字节）", "0–1099511627776; default 134217728 (128 MiB).", "0–1099511627776；默认 134217728（128 MiB）。", nil},
	{"paths.database", "paths", "Database file (advanced)", "数据库文件（高级）", "Under /var/lib/noderampart. Saving does not move an existing database.", "须位于 /var/lib/noderampart；保存不会迁移已有数据库。", nil},
	{"paths.sensor_socket", "paths", "Sensor socket (advanced)", "采集器套接字（高级）", "Under /run/noderampart; at most 100 bytes. Both services need this setting.", "须位于 /run/noderampart；最多 100 字节；两个服务共同使用。", nil},
	{"paths.control_socket", "paths", "Control socket (advanced)", "控制套接字（高级）", "Under /run/noderampart; different from the sensor socket.", "须位于 /run/noderampart；不可与采集器套接字相同。", nil},
}

var yesNo = []string{"true", "false"}

func configValue(cfg *config.Config, path string) (reflect.Value, error) {
	v := reflect.ValueOf(cfg).Elem()
	for _, part := range strings.Split(path, ".") {
		if v.Kind() != reflect.Struct {
			return reflect.Value{}, errors.New("unknown setting")
		}
		found := false
		for i := 0; i < v.NumField(); i++ {
			if strings.Split(v.Type().Field(i).Tag.Get("json"), ",")[0] == part {
				v, found = v.Field(i), true
				break
			}
		}
		if !found {
			return reflect.Value{}, errors.New("unknown setting")
		}
	}
	return v, nil
}

func fieldText(cfg *config.Config, path string) string {
	v, err := configValue(cfg, path)
	if err != nil {
		return ""
	}
	if d, ok := v.Interface().(config.Duration); ok {
		return d.String()
	}
	if v.Kind() == reflect.Slice {
		return strings.Join(v.Interface().([]string), ", ")
	}
	return fmt.Sprint(v.Interface())
}

func setField(cfg *config.Config, path, text string) error {
	if path == "schema_version" {
		return errors.New("schema version is read-only")
	}
	v, err := configValue(cfg, path)
	if err != nil {
		return err
	}
	if len(text) > 4096 || strings.IndexFunc(text, func(r rune) bool { return r < 0x20 || r >= 0x7f && r <= 0x9f }) >= 0 {
		return errors.New("setting contains unsupported characters or is too long")
	}
	if v.Type() == reflect.TypeOf(config.Duration{}) {
		d, err := time.ParseDuration(text)
		if err != nil {
			return errors.New("use a duration such as 1s, 5m or 24h")
		}
		v.Set(reflect.ValueOf(config.Duration{Duration: d}))
		return nil
	}
	switch v.Kind() {
	case reflect.String:
		v.SetString(text)
	case reflect.Bool:
		b, e := strconv.ParseBool(text)
		if e != nil {
			return errors.New("choose enabled or disabled")
		}
		v.SetBool(b)
	case reflect.Int, reflect.Int64:
		n, e := strconv.ParseInt(text, 10, v.Type().Bits())
		if e != nil {
			return errors.New("enter a whole number within the allowed range")
		}
		v.SetInt(n)
	case reflect.Uint64:
		n, e := strconv.ParseUint(text, 10, 64)
		if e != nil {
			return errors.New("enter a non-negative whole number")
		}
		v.SetUint(n)
	case reflect.Float64:
		n, e := strconv.ParseFloat(text, 64)
		if e != nil || math.IsNaN(n) || math.IsInf(n, 0) {
			return errors.New("enter a decimal number")
		}
		v.SetFloat(n)
	case reflect.Slice:
		names := []string{}
		if strings.TrimSpace(text) != "" {
			for _, name := range strings.Split(text, ",") {
				names = append(names, strings.TrimSpace(name))
			}
		}
		v.Set(reflect.ValueOf(names))
	default:
		return errors.New("setting type is unsupported")
	}
	return nil
}
