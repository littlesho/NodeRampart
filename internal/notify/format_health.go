// SPDX-License-Identifier: MIT

package notify

import (
	"strconv"
	"strings"
	"time"

	"github.com/littlesho/NodeRampart/internal/assets"
	"github.com/littlesho/NodeRampart/internal/collector"
	"github.com/littlesho/NodeRampart/internal/model"
)

// Diagnostics are projected again at notification admission. Persisted event
// evidence is not a trusted source of error prose, even if it resembles an enum
// or a previously sanitized failure_detail. Only the producer's validated
// diagnostic contract and deliberately translated categories may leave here.
type healthDiagnosticField struct {
	label eventText
	value string
}

type healthDiagnosticProjection struct {
	fields  []healthDiagnosticField
	compact string
}

type healthFailureText struct {
	chinese string
	compact eventText
}

var journalFailureText = map[string]healthFailureText{
	"process_start_failed":       {"未能启动 journalctl。", eventText{"journalctl start failed", "journalctl 启动失败"}},
	"executable_not_found":       {"未找到 journalctl 可执行文件。", eventText{"journalctl executable missing", "缺少 journalctl 程序"}},
	"executable_invalid":         {"journalctl 可执行文件的格式无效或无法执行。", eventText{"journalctl executable invalid", "journalctl 程序无效"}},
	"permission_denied":          {"读取 SSH 日志或运行 journalctl 时权限不足。", eventText{"journal permission denied", "日志访问权限不足"}},
	"file_descriptor_limit":      {"journalctl 遇到打开文件描述符数量限制。", eventText{"file descriptor limit reached", "文件描述符达到限制"}},
	"memory_allocation_failed":   {"journalctl 操作无法分配所需内存。", eventText{"memory allocation failed", "内存分配失败"}},
	"resource_unavailable":       {"启动或运行 journalctl 所需的系统资源暂时不可用。", eventText{"system resource unavailable", "系统资源暂不可用"}},
	"no_space":                   {"日志访问报告空间不足；请检查文件系统空间、inode 容量及 inotify 监视数量限制。", eventText{"journal space or watch limit reached", "日志访问空间或监视数量受限"}},
	"io_error":                   {"journalctl 遇到输入输出错误。", eventText{"journal I/O error", "日志输入输出错误"}},
	"journal_files_unavailable":  {"journalctl 无法找到或打开日志文件。", eventText{"journal files unavailable", "日志文件不可用"}},
	"unsupported_option":         {"此系统的 journalctl 不支持请求的命令行选项。", eventText{"journalctl option unsupported", "journalctl 选项不受支持"}},
	"journal_format_unsupported": {"journalctl 不支持所读取的日志文件格式。", eventText{"journal format unsupported", "日志格式不受支持"}},
	"journal_corrupt":            {"journalctl 报告日志文件损坏。", eventText{"journal corruption reported", "日志报告损坏"}},
	"process_signaled":           {"journalctl 进程被信号终止；仅凭该信号无法确定发送者，也不能认定发生了内存耗尽。", eventText{"journalctl terminated by signal", "journalctl 被信号终止"}},
	"process_exited":             {"journalctl 进程退出，日志跟随已中断。", eventText{"journalctl exited", "journalctl 已退出"}},
	"stream_read_failed":         {"未能读取 journalctl 的输出流。", eventText{"journal stream read failed", "日志输出流读取失败"}},
	"cursor_unavailable":         {"已记录的日志游标不可用，无法从该位置继续读取。", eventText{"saved journal cursor unavailable", "已存日志游标不可用"}},
	"backfill_time_limit":        {"日志补采达到时间范围上限；更早的记录可能未采集。", eventText{"journal backfill time limit", "日志补采达到时间上限"}},
	"backfill_count_limit":       {"日志补采达到记录数量上限；部分记录可能未采集。", eventText{"journal backfill count limit", "日志补采达到数量上限"}},
	"invalid_cursor":             {"已记录的日志游标格式无效。", eventText{"saved journal cursor invalid", "已存日志游标无效"}},
	"malformed_record":           {"日志记录格式无效、超过大小限制或缺少可读消息；需要成功持久保存新的可信记录才能确认恢复。", eventText{"malformed journal record", "日志记录格式无效"}},
	"untrusted_origin":           {"日志记录未通过 SSH 来源检查；请检查用户标识、可执行程序、服务或会话单元及传输类型。", eventText{"journal origin not trusted", "日志来源未通过信任检查"}},
	"untrusted_uid":              {"日志发送进程的用户标识不是要求的 root，SSH 来源检查未通过。", eventText{"journal UID not trusted", "日志用户标识未通过信任检查"}},
	"untrusted_executable":       {"日志记录的可执行程序不在允许的 OpenSSH 路径范围内，SSH 来源检查未通过。", eventText{"journal executable not trusted", "日志程序未通过信任检查"}},
	"untrusted_unit":             {"日志记录不属于允许的 SSH 系统服务或登录会话，或带有用户服务单元信息。", eventText{"journal unit not trusted", "日志单元未通过信任检查"}},
	"untrusted_transport":        {"日志记录的传输类型不受支持；SSH 来源检查要求 journal 或 syslog 传输。", eventText{"journal transport not trusted", "日志传输类型未通过信任检查"}},
	"missing_origin_metadata":    {"日志记录缺少用户标识、可执行程序或传输类型等必需元数据，无法验证 SSH 来源。", eventText{"journal origin metadata missing", "缺少日志来源元数据"}},
	"persist_failed":             {"未能持久保存日志采集结果或检查点。", eventText{"journal persistence failed", "日志持久保存失败"}},
	"recovery_pending":           {"已记录的日志采集故障仍待恢复；尚无新记录成功持久保存以确认恢复。", eventText{"journal recovery awaiting trusted record", "日志恢复等待可信记录"}},
}

var geoFailureText = map[string]healthFailureText{
	"configuration/unavailable":                  {"无法加载地理数据库更新所需的配置。", eventText{"update configuration unavailable", "更新配置不可用"}},
	"credentials/unavailable":                    {"已保存的 MaxMind 凭据不可用。", eventText{"MaxMind credentials unavailable", "MaxMind 凭据不可用"}},
	"credentials/invalid":                        {"已保存的 MaxMind 凭据格式无效。", eventText{"MaxMind credential format invalid", "MaxMind 凭据格式无效"}},
	"download/failed":                            {"地理数据库下载请求失败。", eventText{"download request failed", "下载请求失败"}},
	"download/dns_lookup_failed":                 {"DNS 无法解析地理数据库下载服务器的主机名。", eventText{"download DNS lookup failed", "下载域名解析失败"}},
	"download/network_unreachable":               {"无法到达地理数据库下载网络或服务器。", eventText{"download network unreachable", "下载网络或主机不可达"}},
	"download/connection_refused":                {"地理数据库下载连接被拒绝。", eventText{"download connection refused", "下载连接被拒绝"}},
	"download/timeout":                           {"地理数据库下载超过时间限制。", eventText{"download timed out", "下载超时"}},
	"download/cancelled":                         {"地理数据库下载操作已取消。", eventText{"download cancelled", "下载已取消"}},
	"download/tls_failed":                        {"地理数据库下载的 TLS 握手或证书验证失败。", eventText{"download TLS failed", "下载 TLS 握手或证书验证失败"}},
	"download/redirect_rejected":                 {"地理数据库下载重定向被服务器地址策略拒绝。", eventText{"download redirect rejected", "下载重定向被拒绝"}},
	"download/http_status":                       {"地理数据库下载服务器返回了非预期 HTTP 状态。", eventText{"download HTTP error", "下载 HTTP 状态异常"}},
	"download/response_read_failed":              {"未能完整读取地理数据库下载响应。", eventText{"download response read failed", "下载响应读取失败"}},
	"download/response_too_large":                {"地理数据库下载响应超过大小限制。", eventText{"download response too large", "下载响应过大"}},
	"staging/failed":                             {"下载的地理数据库无法安全写入临时存放区。", eventText{"database staging failed", "数据库临时存放失败"}},
	"staging/temporary_directory_unavailable":    {"地理数据库更新的临时目录不可用。", eventText{"temporary directory unavailable", "临时目录不可用"}},
	"staging/temporary_directory_unsafe":         {"地理数据库临时目录未通过所有者或权限检查。", eventText{"temporary directory unsafe", "临时目录权限或所有者不符"}},
	"staging/storage_full":                       {"地理数据库临时存放因存储空间或配额不足而失败。", eventText{"storage space or quota exhausted", "存储空间或配额耗尽"}},
	"staging/permission_denied":                  {"文件系统权限拒绝写入地理数据库临时存放区。", eventText{"database staging permission denied", "数据库临时存放权限不足"}},
	"archive/invalid":                            {"地理数据库压缩包未通过完整性或结构检查。", eventText{"database archive invalid", "数据库压缩包无效"}},
	"validation/validation_rejected":             {"下载的数据未通过 MMDB 数据库验证。", eventText{"MMDB validation rejected data", "MMDB 数据验证不通过"}},
	"validation/resource_budget":                 {"MMDB 验证超过资源限制。", eventText{"MMDB validation resource limit", "MMDB 验证达到资源限制"}},
	"validation/validator_privilege_drop_failed": {"MMDB 验证器无法降低运行权限；请检查更新服务的进程能力及用户命名空间策略。", eventText{"MMDB validator privilege drop failed", "MMDB 验证器降权失败"}},
	"validation/validator_setup_failed":          {"MMDB 验证器无法建立沙箱或资源限制。", eventText{"MMDB validator sandbox setup failed", "MMDB 验证器沙箱设置失败"}},
	"validation/validator_failed":                {"MMDB 验证器未能返回经过验证的结果。", eventText{"MMDB validator failed", "MMDB 验证器失败"}},
	"validation/validator_response_invalid":      {"MMDB 验证器返回了无效结果。", eventText{"MMDB validator response invalid", "MMDB 验证器结果无效"}},
	"validation/source_changed":                  {"待验证的 MMDB 文件在验证过程中发生变化。", eventText{"MMDB source changed during validation", "MMDB 文件在验证时变化"}},
	"validation/timeout":                         {"MMDB 验证器超过运行时间限制。", eventText{"MMDB validation timed out", "MMDB 验证超时"}},
	"validation/cancelled":                       {"MMDB 数据库验证操作已取消。", eventText{"MMDB validation cancelled", "MMDB 验证已取消"}},
	"activation/failed":                          {"经过验证的地理数据库无法启用。", eventText{"verified database activation failed", "已验证数据库启用失败"}},
	"activation/recovery_pending":                {"存在待完成的配置恢复，地理数据库启用被阻止。", eventText{"configuration recovery blocks activation", "配置待恢复，阻止数据库启用"}},
	"activation/metadata_save_failed":            {"地理数据库数据已通过验证，但更新元数据无法保存。", eventText{"update metadata save failed", "更新元数据保存失败"}},
	"cleanup/failed":                             {"无法清理已被替换的旧地理数据库。", eventText{"old database cleanup failed", "旧数据库清理失败"}},
}

var geoStageText = map[string]eventText{
	"configuration": {"load configuration", "加载配置"},
	"credentials":   {"read credentials", "读取凭据"},
	"download":      {"download", "下载"},
	"staging":       {"stage downloaded data", "临时存放下载数据"},
	"archive":       {"check archive", "检查压缩包"},
	"validation":    {"validate MMDB", "验证 MMDB"},
	"activation":    {"activate database", "启用数据库"},
	"cleanup":       {"clean up old data", "清理旧数据"},
}

var journalReasonText = map[string]eventText{
	"start_failed":               {"journalctl start failed", "journalctl 启动失败"},
	"read_failed":                {"journal stream read failed", "日志输出流读取失败"},
	"process_exited":             {"journalctl exited", "journalctl 已退出"},
	"cursor_unavailable":         {"saved journal cursor unavailable", "已存日志游标不可用"},
	"backfill_time_limit":        {"backfill time limit reached", "补采达到时间上限"},
	"backfill_count_limit":       {"backfill count limit reached", "补采达到数量上限"},
	"invalid_cursor":             {"saved journal cursor invalid", "已存日志游标无效"},
	"malformed_record":           {"malformed journal record", "日志记录格式无效"},
	"untrusted_origin":           {"journal origin not trusted", "日志来源未通过信任检查"},
	"persist_failed":             {"journal persistence failed", "日志持久保存失败"},
	"process_started":            {"journalctl process started", "journalctl 进程已启动"},
	"record_persisted":           {"trusted journal record persisted", "可信日志记录已持久保存"},
	"recovery_state_unavailable": {"journal recovery state unavailable", "日志恢复状态不可用"},
	"worker_stopped":             {"journal worker stopped", "日志采集工作进程已停止"},
}

var geoResultText = map[string]eventText{
	"ok":                      {"update verified successfully", "更新验证成功"},
	"unchanged":               {"verified database unchanged", "已验证数据库无变化"},
	"download_failed":         {"download failed", "下载失败"},
	"activation_failed":       {"activation failed", "启用失败"},
	"schedule_failed":         {"update schedule failed", "更新计划失败"},
	"credentials_unavailable": {"credentials unavailable", "凭据不可用"},
	"unknown":                 {"update result unknown", "更新结果未知"},
}

var journalStateText = map[string]eventText{
	"starting": {"starting", "启动中"},
	"running":  {"running", "运行中"},
	"retrying": {"retrying", "重试中"},
	"degraded": {"degraded", "异常"},
	"gap":      {"collection gap recorded", "已记录采集缺口"},
}

func healthDiagnostic(event model.Event, location *time.Location, zh bool) healthDiagnosticProjection {
	var result healthDiagnosticProjection
	if event.Kind != "health_ssh_journal" && event.Kind != "health_geoip_update" {
		return result
	}
	fields := event.Evidence
	present := false
	for _, key := range []string{"component_reason", "failure_stage", "failure_cause", "geoip_edition", "http_status", "journal_state", "journal_exit_code", "journal_signal", "diagnostic_at_utc", "diagnostic_scope"} {
		present = present || fields[key] != ""
	}
	if !present {
		return result
	}
	add := func(en, chinese, value string) {
		result.fields = append(result.fields, healthDiagnosticField{eventText{en, chinese}, value})
	}
	unknown := localText(zh, "unrecognized diagnostic; inspect locally", "未识别的诊断；请在本地查看")
	addEnum := func(code, en, chinese string, values map[string]eventText) {
		if code != "" {
			value := unknown
			if text, ok := values[code]; ok {
				value = text.local(zh)
			}
			add(en, chinese, value)
		}
	}
	context := model.ProjectAlertContext(event.Kind, fields)
	if context == nil || context.Diagnostic == nil {
		add("Failure detail", "故障详情", unknown)
		result.compact = unknown
		return result
	}
	diagnostic := context.Diagnostic
	addEnum(diagnostic.DiagnosticScope, "Diagnostic scope", "诊断范围", map[string]eventText{
		"current":      {"current observation", "当前观察"},
		"last_failure": {"last recorded failure", "上次记录的故障"},
	})
	if diagnostic.FailureCause == "" {
		add("Failure detail", "故障详情", localText(zh, "No specific failure detail was recorded.", "未记录具体故障详情。"))
	}
	var compact []string
	if event.Kind == "health_geoip_update" {
		d := assets.GeoDiagnostic{Stage: diagnostic.FailureStage, Reason: diagnostic.FailureCause, Edition: diagnostic.GeoIPEdition}
		if diagnostic.HTTPStatus != nil {
			d.HTTPStatus = *diagnostic.HTTPStatus
		}
		translation, translated := geoFailureText[d.Stage+"/"+d.Reason]
		if d.Validate() == nil && translated {
			add("Failure detail", "故障详情", localText(zh, d.Summary(), translation.chinese))
			add("Failure stage", "故障阶段", geoStageText[d.Stage].local(zh))
			compact = append(compact, translation.compact.local(zh))
			if d.Edition != "" {
				edition := map[string]eventText{"City": {"City", "City 城市库"}, "ASN": {"ASN", "ASN 自治系统库"}}[d.Edition].local(zh)
				add("GeoIP database", "地理数据库", edition)
				compact = append(compact, d.Edition)
			}
			if d.HTTPStatus != 0 {
				status := strconv.Itoa(d.HTTPStatus)
				add("HTTP status", "HTTP 状态码", status)
				compact = append(compact, "HTTP "+status)
			}
		} else if diagnostic.FailureCause != "" || diagnostic.FailureStage != "" || diagnostic.GeoIPEdition != "" || diagnostic.HTTPStatus != nil {
			add("Failure detail", "故障详情", unknown)
			compact = append(compact, unknown)
		}
		addEnum(diagnostic.ComponentReason, "Component result", "组件结果", geoResultText)
		if len(compact) == 0 {
			if text, ok := geoResultText[diagnostic.ComponentReason]; ok {
				compact = append(compact, text.local(zh))
			}
		}
	} else {
		detail := collector.JournalCauseDescription(diagnostic.FailureCause)
		translation, translated := journalFailureText[diagnostic.FailureCause]
		if detail != "" && translated {
			add("Failure detail", "故障详情", localText(zh, detail, translation.chinese))
			compact = append(compact, translation.compact.local(zh))
			if diagnostic.JournalExitCode != nil {
				code := strconv.Itoa(*diagnostic.JournalExitCode)
				add("journalctl exit code", "journalctl 退出码", code)
				compact = append(compact, localText(zh, "exit ", "退出码 ")+code)
			}
			if diagnostic.JournalSignal != "" {
				add("journalctl signal", "journalctl 终止信号", diagnostic.JournalSignal)
				compact = append(compact, diagnostic.JournalSignal)
			}
		} else if diagnostic.FailureCause != "" || diagnostic.JournalExitCode != nil || diagnostic.JournalSignal != "" {
			add("Failure detail", "故障详情", unknown)
			compact = append(compact, unknown)
		}
		addEnum(diagnostic.ComponentReason, "Component result", "组件结果", journalReasonText)
		addEnum(diagnostic.JournalState, "Journal state", "日志采集状态", journalStateText)
		if len(compact) == 0 {
			if text, ok := journalReasonText[diagnostic.ComponentReason]; ok {
				compact = append(compact, text.local(zh))
			}
		}
	}
	if diagnostic.DiagnosticAt != nil {
		add("Diagnostic time", "诊断时间", eventTime(*diagnostic.DiagnosticAt, location, zh))
	}
	result.compact = strings.Join(compact, "; ")
	if result.compact != "" && diagnostic.DiagnosticScope == "last_failure" {
		result.compact = localText(zh, "Last failure: ", "上次故障：") + result.compact
	}
	return result
}
