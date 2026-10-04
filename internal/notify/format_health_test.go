// SPDX-License-Identifier: MIT

package notify

import (
	"go/ast"
	"go/token"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/littlesho/NodeRampart/internal/assets"
	"github.com/littlesho/NodeRampart/internal/collector"
	"github.com/littlesho/NodeRampart/internal/model"
)

func healthDiagnosticEvent(kind string, extra map[string]string) model.Event {
	event := model.Event{
		ID: "evt_health_diagnostic", IncidentID: "inc_health_diagnostic", Kind: kind,
		Phase: "start", Severity: model.SeverityMedium,
		ObservedAt: time.Date(2026, 10, 3, 7, 4, 23, 0, time.UTC),
		Summary:    "raw_generated_summary_should_remain_local",
		Evidence: map[string]string{
			"reason": "journal_unavailable", "condition_since_utc": "2026-10-03T07:01:25Z",
			"diagnostic_at_utc": "2026-10-03T07:01:25Z", "diagnostic_scope": "current",
			"failure_detail": "private_raw_error_with_credentials_should_remain_local",
		},
	}
	if kind == "health_geoip_update" {
		event.Evidence["reason"] = "update_failed"
	}
	for key, value := range extra {
		event.Evidence[key] = value
	}
	return event
}

func TestHealthDiagnosticsReachAllEventNotificationFormats(t *testing.T) {
	location, err := time.LoadLocation("Asia/Kathmandu")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, kind string
		fields     map[string]string
		fullEN     []string
		fullZH     []string
		compactEN  []string
		compactZH  []string
	}{
		{
			name: "journal permissions", kind: "health_ssh_journal",
			fields: map[string]string{"component_reason": "start_failed", "journal_state": "retrying", "failure_cause": "permission_denied"},
			fullEN: []string{"Access was denied", "Journal state: retrying"}, fullZH: []string{"权限不足", "日志采集状态: 重试中"},
			compactEN: []string{"journal permission denied"}, compactZH: []string{"日志访问权限不足"},
		},
		{
			name: "journal signal", kind: "health_ssh_journal",
			fields: map[string]string{"component_reason": "process_exited", "journal_state": "retrying", "failure_cause": "process_signaled", "journal_signal": "SIGKILL"},
			fullEN: []string{"terminated by a signal", "SIGKILL"}, fullZH: []string{"被信号终止", "SIGKILL"},
			compactEN: []string{"journalctl terminated by signal", "SIGKILL"}, compactZH: []string{"journalctl 被信号终止", "SIGKILL"},
		},
		{
			name: "journal exit status", kind: "health_ssh_journal",
			fields: map[string]string{"component_reason": "process_exited", "journal_state": "retrying", "failure_cause": "process_exited", "journal_exit_code": "1"},
			fullEN: []string{"journalctl exit code: 1"}, fullZH: []string{"journalctl 退出码: 1"},
			compactEN: []string{"journalctl exited", "exit 1"}, compactZH: []string{"journalctl 已退出", "退出码 1"},
		},
		{
			name: "journal origin", kind: "health_ssh_journal",
			fields: map[string]string{"component_reason": "untrusted_origin", "journal_state": "degraded", "failure_cause": "untrusted_executable"},
			fullEN: []string{"executable", "Journal state: degraded"}, fullZH: []string{"可执行程序不在允许的 OpenSSH 路径范围", "日志采集状态: 异常"},
			compactEN: []string{"journal executable not trusted"}, compactZH: []string{"日志程序未通过信任检查"},
		},
		{
			name: "geo privilege drop", kind: "health_geoip_update",
			fields: map[string]string{"component_reason": "download_failed", "failure_stage": "validation", "failure_cause": "validator_privilege_drop_failed", "geoip_edition": "City"},
			fullEN: []string{"could not drop privileges", "Failure stage: validate MMDB", "GeoIP database: City"}, fullZH: []string{"无法降低运行权限", "故障阶段: 验证 MMDB", "City 城市库"},
			compactEN: []string{"MMDB validator privilege drop failed", "City"}, compactZH: []string{"MMDB 验证器降权失败", "City"},
		},
		{
			name: "geo HTTP status", kind: "health_geoip_update",
			fields: map[string]string{"component_reason": "download_failed", "failure_stage": "download", "failure_cause": "http_status", "geoip_edition": "ASN", "http_status": "403"},
			fullEN: []string{"HTTP 403", "HTTP status: 403", "GeoIP database: ASN"}, fullZH: []string{"HTTP 状态码: 403", "ASN 自治系统库"},
			compactEN: []string{"download HTTP error", "ASN", "HTTP 403"}, compactZH: []string{"下载 HTTP 状态异常", "ASN", "HTTP 403"},
		},
		{
			name: "geo DNS", kind: "health_geoip_update",
			fields: map[string]string{"component_reason": "download_failed", "failure_stage": "download", "failure_cause": "dns_lookup_failed", "geoip_edition": "City"},
			fullEN: []string{"could not be resolved by DNS"}, fullZH: []string{"DNS 无法解析"},
			compactEN: []string{"download DNS lookup failed"}, compactZH: []string{"下载域名解析失败"},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			event := healthDiagnosticEvent(test.kind, test.fields)
			for _, language := range []string{"en", "zh"} {
				wantFull, wantCompact := test.fullEN, test.compactEN
				if language == "zh" {
					wantFull, wantCompact = test.fullZH, test.compactZH
				}
				localized := FormatEventLocalized("lab-node", event, language, location)
				assertNotificationHTML(t, localized)
				native := FormatNativeEvent("lab-node", event, language, location)
				for _, body := range []string{localized, native} {
					for _, fragment := range append(append([]string{}, wantFull...), "2026-10-03 12:46:25", "UTC+05:45") {
						if !strings.Contains(body, fragment) {
							t.Fatalf("%s diagnostic omitted %q: %s", language, fragment, body)
						}
					}
				}
				if len(native) > 1800 || !utf8.ValidString(native) || !strings.Contains(native, "sudo noderampart events show --id "+event.ID) {
					t.Fatal("native diagnostic exceeded its bound or lost its local reference", native)
				}
				official, semantic := FormatOfficialEvent("lab-node", event, language, location)
				for _, fragment := range wantCompact {
					if !strings.Contains(semantic.BoundedSummary, fragment) || !strings.Contains(official, fragment) {
						t.Fatal("official summary lost specific failure evidence", fragment, official)
					}
				}
				if !safeOfficialText(semantic.BoundedSummary, 1024) || !safeOfficialText(official, 1800) {
					t.Fatal("official diagnostic violated the retained summary contract")
				}
				for _, body := range []string{localized, native, official} {
					if strings.Contains(body, event.Summary) || strings.Contains(body, event.Evidence["failure_detail"]) || strings.Contains(body, "unrecognized diagnostic") || strings.Contains(body, "未识别的诊断") {
						t.Fatal("valid diagnostic fell back or exposed arbitrary prose", body)
					}
				}
			}
			if body := FormatEvent("lab-node", event); !strings.Contains(body, test.fullEN[0]) {
				t.Fatal("webhook English entry point omitted the diagnosis", body)
			}
		})
	}
}

func TestHealthDiagnosticLegacyAndRetainedFailureSemantics(t *testing.T) {
	legacy := healthDiagnosticEvent("health_geoip_update", map[string]string{"component_reason": "download_failed"})
	delete(legacy.Evidence, "diagnostic_scope")
	for _, language := range []string{"en", "zh"} {
		want := localText(language == "zh", "No specific failure detail was recorded.", "未记录具体故障详情。")
		for _, body := range []string{FormatEventLocalized("fixture", legacy, language, time.UTC), FormatNativeEvent("fixture", legacy, language, time.UTC)} {
			if !strings.Contains(body, want) || strings.Contains(body, "MMDB") || strings.Contains(body, "DNS") || strings.Contains(body, "HTTP") {
				t.Fatal("legacy metadata must neither fabricate a cause nor hide that detail is absent", body)
			}
		}
	}
	event := healthDiagnosticEvent("health_ssh_journal", map[string]string{"component_reason": "process_started", "journal_state": "retrying", "failure_cause": "permission_denied", "diagnostic_scope": "last_failure"})
	for _, language := range []string{"en", "zh"} {
		for _, body := range []string{FormatEventLocalized("fixture", event, language, time.UTC), FormatNativeEvent("fixture", event, language, time.UTC)} {
			if !strings.Contains(body, localText(language == "zh", "last recorded failure", "上次记录的故障")) {
				t.Fatal("retained failure was misrepresented as a new failure", body)
			}
		}
		_, semantic := FormatOfficialEvent("fixture", event, language, time.UTC)
		if !strings.Contains(semantic.BoundedSummary, localText(language == "zh", "Last failure:", "上次故障：")) {
			t.Fatal("compact summary lost retained-failure scope", semantic.BoundedSummary)
		}
	}
}

func TestHealthDiagnosticOfficialSMSKeepsSpecificFailureWithinSegmentPolicy(t *testing.T) {
	event := healthDiagnosticEvent("health_geoip_update", map[string]string{"component_reason": "download_failed", "failure_stage": "validation", "failure_cause": "validator_privilege_drop_failed", "geoip_edition": "City"})
	for _, language := range []string{"en", "zh"} {
		_, semantic := FormatOfficialEvent("lab-node", event, language, time.UTC)
		body, _, segments, err := RenderOfficialSMS(semantic, language, 2)
		if err != nil || segments < 1 || segments > 2 || !strings.Contains(body, semantic.BoundedSummary) || !strings.Contains(body, "STOP") || !strings.Contains(body, semantic.LocalReference) {
			t.Fatal("bounded health SMS lost its specific cause, policy footer, or local reference", language, segments, err, body)
		}
	}
}

func TestHealthDiagnosticUnknownValuesNeverLeaveAsCodesOrProse(t *testing.T) {
	for _, kind := range []string{"health_ssh_journal", "health_geoip_update"} {
		for _, key := range []string{"component_reason", "failure_stage", "failure_cause", "geoip_edition", "http_status", "journal_state", "journal_exit_code", "journal_signal", "diagnostic_at_utc", "diagnostic_scope", "failure_detail"} {
			for _, secret := range []string{"synthetic_sensitive_credential_123456", "https://private.invalid/?license_key=private_value", "private server error\nwith credential", strings.Repeat("secret", 10000)} {
				event := healthDiagnosticEvent(kind, map[string]string{"component_reason": "download_failed", "failure_stage": "download", "failure_cause": "http_status", "geoip_edition": "City", "http_status": "403"})
				if kind == "health_ssh_journal" {
					event = healthDiagnosticEvent(kind, map[string]string{"component_reason": "process_exited", "journal_state": "retrying", "failure_cause": "process_signaled", "journal_signal": "SIGKILL"})
				}
				event.Evidence[key] = secret
				for _, language := range []string{"en", "zh"} {
					official, _ := FormatOfficialEvent("fixture", event, language, time.UTC)
					for _, body := range []string{FormatEventLocalized("fixture", event, language, time.UTC), FormatNativeEvent("fixture", event, language, time.UTC), official} {
						if strings.Contains(body, "synthetic_sensitive") || strings.Contains(body, "private") || strings.Contains(body, "secret") || !utf8.ValidString(body) || len(body) > eventBodyBytes {
							t.Fatalf("%s/%s/%s diagnostic leaked untrusted content or broke output bounds", kind, key, language)
						}
					}
				}
			}
		}
	}
}

func TestHealthDiagnosticsDoNotAppearOnUnrelatedEvents(t *testing.T) {
	event := healthDiagnosticEvent("health_ssh_journal", map[string]string{"component_reason": "process_exited", "journal_state": "retrying", "failure_cause": "process_signaled", "journal_signal": "SIGKILL"})
	event.Kind = "ssh_login_success"
	for _, language := range []string{"en", "zh"} {
		official, _ := FormatOfficialEvent("fixture", event, language, time.UTC)
		for _, body := range []string{FormatEventLocalized("fixture", event, language, time.UTC), FormatNativeEvent("fixture", event, language, time.UTC), official} {
			if strings.Contains(body, "SIGKILL") || strings.Contains(body, "Failure detail") || strings.Contains(body, "故障详情") {
				t.Fatal("component diagnosis escaped its supported event kind")
			}
		}
	}
}

func diagnosticFunctionLiterals(t *testing.T, path, functionName string) []string {
	t.Helper()
	literals := map[string]bool{}
	ast.Inspect(parsedProducer(t, path), func(node ast.Node) bool {
		function, ok := node.(*ast.FuncDecl)
		if !ok {
			return true
		}
		if function.Name.Name == functionName {
			ast.Inspect(function.Body, func(node ast.Node) bool {
				if literal, ok := node.(*ast.BasicLit); ok && literal.Kind == token.STRING {
					literals[stringLiteral(literal)] = true
				}
				return true
			})
		}
		return false
	})
	return sortedSet(literals)
}

func TestHealthDiagnosticTranslationsCoverProducerVocabularies(t *testing.T) {
	observedJournal := map[string]bool{}
	for _, cause := range diagnosticFunctionLiterals(t, "../collector/journal_diagnostics.go", "JournalCauseDescription") {
		if collector.JournalCauseDescription(cause) == "" {
			continue
		}
		text, ok := journalFailureText[cause]
		if !ok || text.chinese == "" || text.compact.en == "" || text.compact.zh == "" || text.compact.en == text.compact.zh {
			t.Fatal("journal cause needs complete bilingual templates", cause)
		}
		observedJournal[cause] = true
	}
	coveredJournal := map[string]bool{}
	for cause := range journalFailureText {
		coveredJournal[cause] = true
	}
	if !reflect.DeepEqual(observedJournal, coveredJournal) {
		t.Fatalf("journal diagnostic vocabulary drift: producer %v, renderer %v", sortedSet(observedJournal), sortedSet(coveredJournal))
	}
	for _, test := range []struct {
		function string
		valid    func(string) string
		texts    map[string]eventText
	}{{"SafeJournalReason", collector.SafeJournalReason, journalReasonText}, {"SafeJournalState", collector.SafeJournalState, journalStateText}} {
		observed, covered := map[string]bool{}, map[string]bool{}
		for _, code := range diagnosticFunctionLiterals(t, "../collector/journal_diagnostics.go", test.function) {
			if test.valid(code) != "" {
				observed[code] = true
			}
		}
		for code, text := range test.texts {
			if text.en == "" || text.zh == "" || text.en == text.zh {
				t.Fatal("journal result/state lacks translation", code)
			}
			covered[code] = true
		}
		if !reflect.DeepEqual(observed, covered) {
			t.Fatal("journal result/state vocabulary drift", test.function, sortedSet(observed), sortedSet(covered))
		}
	}
	// Ask the actual validator which stage/reason pairs it accepts. New
	// producer enum literals must acquire a reviewed Chinese/compact template;
	// changing the notification map alone cannot hide a missing translation.
	literals := diagnosticFunctionLiterals(t, "../assets/geo_diagnostic.go", "Validate")
	observedGeo, coveredGeo, observedStages := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, stage := range literals {
		for _, reason := range literals {
			d := assets.GeoDiagnostic{Stage: stage, Reason: reason, Edition: "City"}
			if reason == "http_status" {
				d.HTTPStatus = 403
			}
			if d.Validate() != nil {
				continue
			}
			key := stage + "/" + reason
			text, ok := geoFailureText[key]
			if !ok || text.chinese == "" || text.compact.en == "" || text.compact.zh == "" || text.compact.en == text.compact.zh {
				t.Fatal("GeoIP cause needs complete bilingual templates", key)
			}
			if text, ok := geoStageText[stage]; !ok || text.en == "" || text.zh == "" || text.en == text.zh {
				t.Fatal("GeoIP stage needs translation", stage)
			}
			observedGeo[key], observedStages[stage] = true, true
		}
	}
	for key := range geoFailureText {
		coveredGeo[key] = true
	}
	if !reflect.DeepEqual(observedGeo, coveredGeo) || len(observedStages) != len(geoStageText) {
		t.Fatalf("GeoIP diagnostic vocabulary drift: producer %v, renderer %v", sortedSet(observedGeo), sortedSet(coveredGeo))
	}
}
