// SPDX-License-Identifier: MIT

package notify

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/littlesho/NodeRampart/internal/model"
)

func localizedEventFixture(kind, phase string) model.Event {
	at := time.Date(2026, 7, 15, 4, 5, 6, 0, time.UTC)
	event := model.Event{ID: "evt_fixture", IncidentID: "inc_fixture", ObservedAt: at, Kind: kind, Phase: phase, Severity: model.SeverityHigh, SourceRange: "2001:db8:1234::/48", Target: "tcp/443", Count: 8,
		Summary: "English generated summary must never be a template fallback",
		Geo:     model.Geo{CountryCode: "JP", Country: "Japan", Region: "東京都", City: "千代田区", ASN: 64500, ASNOrg: "Fixture & Co", ASNNetwork: "2001:db8::/32", DatabaseAge: "7d", Estimate: true}, Evidence: map[string]string{"interface": "eth0"}}
	fields := event.Evidence
	switch {
	case floodKind(kind):
		event.Count = 125
		fields["observed_rate"], fields["threshold_rate"], fields["threshold"] = "125.125", "100.001", "100 pps"
		if kind == "bandwidth_spike" {
			fields["threshold"] = "100 bytes/s"
		}
		fields["top_source_packets"], fields["top_source_bytes"], fields["source_scope"] = "500", "60000", "retained_flows"
		fields["coverage_complete"], fields["window_millis"] = "false", "60001"
		if phase == "recovery" {
			event.Count = 12
			fields["observed_rate"], fields["coverage_complete"] = "12.125", "true"
			fields["incident_duration_millis"] = "123001"
		}
	case kind == "port_scan":
		fields["packets"], fields["rule"], fields["threshold"], fields["window_seconds"], fields["elapsed_millis"], fields["coverage_complete"] = "20", "inbound_syn_or_unsolicited_udp", "8", "60", "59001", "false"
	case strings.HasPrefix(kind, "ssh_"):
		event.Target = "ssh user=测试<用户>"
		fields["method"], fields["window_millis"], fields["count_basis"] = "publickey", "60000", "openssh_final_failure"
		if kind == "ssh_login_success" {
			event.Count = 1
			fields["preceding_source_failures"], fields["window"], fields["preceding_source_failures_complete"] = "3", "1m0s", "false"
			fields["history_hint_state"], fields["history_basis"], fields["history_source_hint"], fields["history_time_hint"] = "available", "current_process_retained_successes_7d", "first_observed_prefix", "unseen_local_hour"
		} else {
			fields["invalid_user"], fields["detection_window_complete"] = "true", "false"
		}
	case strings.HasPrefix(kind, "health_"):
		event.Count, event.SourceRange, event.Target, event.Geo = 0, "", "", model.Geo{}
		fields["reason"], fields["condition_since_utc"], fields["observation"] = "component_unavailable", at.Add(-time.Hour).Format(time.RFC3339Nano), "recorded component health; does not guarantee complete collection or notification delivery"
		if phase == "recovery" {
			fields["reason"] = "healthy"
		}
	case strings.HasPrefix(kind, "budget_"):
		event.Count, event.SourceRange, event.Target, event.Geo = 0, "", "", model.Geo{}
		event.Severity = model.SeverityMedium
		fields["period"], fields["period_start_utc"], fields["period_end_utc"] = "2026-07", "2026-06-30T15:00:00Z", "2026-07-31T15:00:00Z"
		if strings.HasPrefix(kind, "budget_day_") {
			fields["period"], fields["period_start_utc"], fields["period_end_utc"] = "2026-07-15", "2026-07-14T15:00:00Z", "2026-07-15T15:00:00Z"
		}
		fields["observed_bytes"], fields["milestone"], fields["coverage"], fields["basis"], fields["reason"] = "80", "80", "incomplete", model.LegacyAlertBasis, "threshold_crossed"
		switch kind {
		case "budget_month_cost":
			fields["observed_bytes"] = "80000000000"
			fields["observed_cost"], fields["threshold_cost"], fields["currency"] = "80", "100", "USD"
		case "budget_day_growth":
			fields["baseline_mean_bytes"], fields["baseline_days"], fields["growth_ratio"], fields["milestone"] = "32", "7", "2.5", "100"
			event.Severity = model.SeverityHigh
		case "budget_day_bytes":
			fields["observed_bytes"], fields["threshold_bytes"], fields["milestone"] = "100", "100", "100"
			event.Severity = model.SeverityHigh
		default:
			fields["threshold_bytes"] = "100"
		}
	}
	return event
}

func localizedGoldenCases() []model.Event {
	var kinds []string
	for kind := range eventTitles {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	var cases []model.Event
	for _, kind := range kinds {
		phases := []string{"start"}
		switch {
		case floodKind(kind):
			phases = []string{"start", "update", "recovery"}
		case strings.HasPrefix(kind, "health_"):
			phases = []string{"start", "recovery"}
		case strings.HasPrefix(kind, "budget_"):
			phases = []string{"start", "update"}
		case kind == "ssh_login_success":
			phases = []string{"observed"}
		}
		for _, phase := range phases {
			cases = append(cases, localizedEventFixture(kind, phase))
		}
	}
	return cases
}

func assertNotificationHTML(t *testing.T, body string) {
	t.Helper()
	if len(body) > eventBodyBytes || !utf8.ValidString(body) || strings.Contains(body, "%!") {
		t.Fatalf("invalid body (%d bytes): %q", len(body), body)
	}
	for _, r := range body {
		if unicode.IsControl(r) && r != '\n' {
			t.Fatalf("control character %U", r)
		}
	}
	// HTML escaping uses valid numeric entities for quotes. Only a balanced <b>
	// tag is intentional; all externally supplied markup must remain text.
	decoder := xml.NewDecoder(strings.NewReader("<body>" + body + "</body>"))
	decoder.Entity = map[string]string{"nbsp": " "}
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("malformed escaped HTML: %v\n%s", err, body)
		}
		if start, ok := token.(xml.StartElement); ok && start.Name.Local != "body" && start.Name.Local != "b" {
			t.Fatalf("unexpected markup %s", start.Name.Local)
		}
	}
}

func TestFormatEventLocalizedCompleteGoldensAndImmutableEvent(t *testing.T) {
	location, err := time.LoadLocation("Asia/Kathmandu")
	if err != nil {
		t.Fatal(err)
	}
	for _, language := range []string{"en", "zh"} {
		t.Run(language, func(t *testing.T) {
			var actual strings.Builder
			for _, event := range localizedGoldenCases() {
				before, _ := json.Marshal(event)
				body := FormatEventLocalized("节点 <A&B>", event, language, location)
				assertNotificationHTML(t, body)
				if strings.Contains(body, event.Summary) || strings.Contains(body, "does not guarantee") && language == "zh" {
					t.Fatal("generated English prose leaked", body)
				}
				if language == "zh" {
					for _, english := range []string{"Host:", "Time:", "Phase:", "Evidence:", "Action:", "Count:", "Count basis:", "GeoIP scope:", "Country (GeoIP", "configured threshold", "unknown", "observed only", "recovery", "database estimate", "retained history", "unavailable", " packets/s", " bytes/s", " seconds", " milliseconds", " days"} {
						if strings.Contains(body, english) {
							t.Fatal("English template leaked", english, body)
						}
					}
					for _, title := range eventTitles {
						if strings.Contains(body, title.en) {
							t.Fatal("English event title leaked", title.en)
						}
					}
				}
				if !strings.Contains(body, "09:50:06 Asia/Kathmandu (UTC+05:45)") || !strings.Contains(body, "inc_fixture") || !strings.Contains(body, "evt_fixture") {
					t.Fatal("core event fields missing", body)
				}
				after, _ := json.Marshal(event)
				if string(before) != string(after) {
					t.Fatal("rendering changed durable machine data")
				}
				fmt.Fprintf(&actual, "=== %s/%s ===\n%s\n\n", event.Kind, event.Phase, body)
			}
			expected := strings.TrimSuffix(actual.String(), "\n")
			path := filepath.Join("testdata", "events_"+language+".golden")
			if os.Getenv("NR_UPDATE_NOTIFY_GOLDENS") == "1" {
				if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(expected), 0644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil || string(want) != expected {
				t.Fatalf("complete event golden differs: %s (%v)", path, err)
			}
		})
	}
}

func TestFormatEventLocalizedEnumerationsAndMeaning(t *testing.T) {
	// Keep synthetic golden examples consistent with the real flood producer:
	// the integer rate rounds down; recovery has fallen below its threshold.
	// This assertion is
	// independent of the golden files, so refreshing them cannot bless a conflict.
	for _, kind := range []string{"syn_flood", "udp_flood", "icmp_flood", "bandwidth_spike"} {
		for _, phase := range []string{"start", "update", "recovery"} {
			event := localizedEventFixture(kind, phase)
			rate, count := "125.125", uint64(125)
			if phase == "recovery" {
				rate, count = "12.125", 12
				if event.Evidence["incident_duration_millis"] != "123001" || event.Evidence["coverage_complete"] != "true" {
					t.Fatal("synthetic recovery lacks reliable coverage/duration", event)
				}
			}
			if event.Evidence["observed_rate"] != rate || event.Count != count {
				t.Fatal("inconsistent synthetic rate/count", kind, phase, event.Count, event.Evidence["observed_rate"])
			}
		}
	}
	// A normal example must agree with the producer's milestone contract.
	// Daily byte/growth rules emit 100%, while monthly rules may emit 80%.
	for _, phase := range []string{"start", "update"} {
		for _, kind := range []string{"budget_month_bytes", "budget_month_cost", "budget_day_bytes", "budget_day_growth"} {
			fields := localizedEventFixture(kind, phase).Evidence
			switch kind {
			case "budget_month_bytes":
				if fields["observed_bytes"] != "80" || fields["threshold_bytes"] != "100" || fields["milestone"] != "80" {
					t.Fatal("inconsistent monthly traffic fixture", fields)
				}
			case "budget_month_cost":
				if fields["observed_cost"] != "80" || fields["threshold_cost"] != "100" || fields["milestone"] != "80" {
					t.Fatal("inconsistent monthly cost fixture", fields)
				}
			case "budget_day_bytes":
				if fields["observed_bytes"] != "100" || fields["threshold_bytes"] != "100" || fields["milestone"] != "100" {
					t.Fatal("inconsistent daily traffic fixture", fields)
				}
			case "budget_day_growth":
				// 32 bytes/day × 2.5 = 80 bytes: a nonzero observed baseline.
				if fields["observed_bytes"] != "80" || fields["baseline_mean_bytes"] != "32" || fields["growth_ratio"] != "2.5" || fields["baseline_days"] != "7" || fields["milestone"] != "100" {
					t.Fatal("inconsistent growth fixture", fields)
				}
			}
		}
	}
	for reason, text := range reasonText {
		event := localizedEventFixture("health_storage", "start")
		event.Evidence["reason"] = reason
		for _, language := range []string{"en", "zh"} {
			body := FormatEventLocalized("fixture", event, language, time.UTC)
			if !strings.Contains(body, text.local(language == "zh")) {
				t.Fatalf("unrendered reason %s/%s: %s", reason, language, body)
			}
			if language == "zh" && strings.Contains(body, text.en) {
				t.Fatalf("English reason leaked %s", reason)
			}
		}
	}
	for _, check := range []struct{ key, value, want string }{
		{"method", "password", "认证方式: 密码"}, {"method", "publickey", "认证方式: 公钥"}, {"method", "keyboard-interactive", "认证方式: 键盘交互"}, {"method", "keyboard-interactive/pam", "认证方式: 键盘交互（PAM）"}, {"method", "hostbased", "认证方式: 主机认证"}, {"method", "gssapi-with-mic", "认证方式: GSSAPI 认证"}, {"method", "pam", "认证方式: PAM 认证"},
		{"history_hint_state", "observing", "历史观察期；证据不足，不判罕见"}, {"history_hint_state", "history_unavailable", "历史不可用；未作罕见判断"}, {"history_hint_state", "history_incomplete", "历史覆盖不完整；未作罕见判断"}, {"history_hint_state", "available", "可提供有界历史提示；并非入侵结论"},
		{"history_source_hint", "first_observed_source", "隐私转换后的来源"}, {"history_source_hint", "first_observed_prefix", "前缀不代表精确身份"}, {"history_time_hint", "unseen_local_hour", "仅表示偏离已知历史"},
		{"count_basis", "openssh_final_failure", "辅助日志不重复累计"}, {"history_basis", "current_process_retained_successes_7d", "冷启动、隐私变化或裁剪"},
		{"preceding_source_failures_complete", "true", "先前失败证据完整: 是"}, {"preceding_source_failures_complete", "false", "先前失败证据完整: 否"},
	} {
		event := localizedEventFixture("ssh_login_success", "observed")
		event.Evidence[check.key] = check.value
		body := FormatEventLocalized("fixture", event, "zh", time.UTC)
		if !strings.Contains(body, check.want) {
			t.Fatalf("%s=%s absent: %s", check.key, check.value, body)
		}
	}
	for _, value := range []struct{ code, want string }{{"unknown", "覆盖: 未知"}, {"incomplete", "覆盖: 不完整"}, {"adequate_recorded", "并非完整保证"}} {
		event := localizedEventFixture("budget_day_bytes", "start")
		event.Evidence["coverage"] = value.code
		if body := FormatEventLocalized("fixture", event, "zh", time.UTC); !strings.Contains(body, value.want) {
			t.Fatal(value, body)
		}
	}
	for _, severity := range []model.Severity{model.SeverityInfo, model.SeverityLow, model.SeverityMedium, model.SeverityHigh, model.SeverityCritical} {
		event := localizedEventFixture("port_scan", "start")
		event.Severity = severity
		if body := FormatEventLocalized("fixture", event, "zh", time.UTC); strings.Contains(body, strings.ToUpper(string(severity))) {
			t.Fatal("severity untranslated", body)
		}
	}
	cost := localizedEventFixture("budget_month_cost", "update")
	for _, language := range []string{"en", "zh"} {
		body := FormatEventLocalized("fixture", cost, language, time.UTC)
		if language == "zh" && !strings.Contains(body, "估算费用达到配置费用预算的 80%") || language == "en" && !strings.Contains(body, "estimated cost reached 80%") {
			t.Fatal("cost event mistaken for traffic threshold", body)
		}
		if language == "zh" && !strings.Contains(body, "估算费用: 80 USD") || language == "en" && !strings.Contains(body, "Estimated cost: 80 USD") {
			t.Fatal("normal cost fixture no longer agrees with its milestone", body)
		}
	}
	brute := localizedEventFixture("ssh_brute_force", "start")
	if body := FormatEventLocalized("fixture", brute, "zh", time.UTC); !strings.Contains(body, "在 60 秒内记录了 8 次 SSH 认证失败") {
		t.Fatal("wrong duration/count argument order", body)
	}
}

func TestFormatEventLocalizedRecordedZeroAndMissingValues(t *testing.T) {
	// Partial historical inputs may retain an actual zero without enough
	// evidence to reconstruct a threshold crossing. They are not normal alerts.
	cost := localizedEventFixture("budget_month_cost", "start")
	cost.Evidence["observed_cost"], cost.Evidence["observed_bytes"] = "0", "0"
	delete(cost.Evidence, "milestone")
	growth := localizedEventFixture("budget_day_growth", "start")
	growth.Evidence["baseline_mean_bytes"], growth.Evidence["observed_bytes"] = "0", "0"
	delete(growth.Evidence, "milestone")
	for _, language := range []string{"en", "zh"} {
		zh := language == "zh"
		body := FormatEventLocalized("fixture", cost, language, time.UTC)
		for _, want := range []string{localText(zh, "Estimated cost: 0 USD", "估算费用: 0 USD"), localText(zh, "Observed guest TX: 0 bytes", "已观测接口出站: 0 字节"), localText(zh, "comparison parameters are incomplete", "比较参数不足")} {
			if !strings.Contains(body, want) {
				t.Fatal("recorded zero/missing milestone meaning lost", want, body)
			}
		}
		if strings.Contains(body, "80%") || strings.Contains(body, "100%") {
			t.Fatal("partial zero evidence invented a crossing", body)
		}
		growthBody := FormatEventLocalized("fixture", growth, language, time.UTC)
		if !strings.Contains(growthBody, localText(zh, "Baseline daily mean (bytes): 0", "基线日均字节数: 0")) || strings.Contains(growthBody, "80%") || strings.Contains(growthBody, "100%") {
			t.Fatal("zero historical baseline invented a crossing", growthBody)
		}
	}
	delete(cost.Evidence, "observed_cost")
	delete(cost.Evidence, "observed_bytes")
	delete(growth.Evidence, "baseline_mean_bytes")
	for _, language := range []string{"en", "zh"} {
		zh := language == "zh"
		if body := FormatEventLocalized("fixture", cost, language, time.UTC); strings.Contains(body, localText(zh, "Estimated cost:", "估算费用:")) || strings.Contains(body, localText(zh, "Observed guest TX:", "已观测接口出站:")) {
			t.Fatal("missing observation was filled with zero", body)
		}
		if body := FormatEventLocalized("fixture", growth, language, time.UTC); strings.Contains(body, localText(zh, "Baseline daily mean (bytes):", "基线日均字节数:")) {
			t.Fatal("missing baseline was filled with zero", body)
		}
	}
}

func TestFormatEventLocalizedCountryCatalogAndOriginalNames(t *testing.T) {
	for _, check := range []struct{ code, en, zh string }{{"US", "United States", "美国"}, {"GB", "United Kingdom", "英国"}, {"CN", "China", "中国"}, {"JP", "Japan", "日本"}, {"DE", "Germany", "德国"}, {"FR", "France", "法国"}, {"BR", "Brazil", "巴西"}, {"IN", "India", "印度"}, {"ZA", "South Africa", "南非"}, {"SG", "Singapore", "新加坡"}, {"PRIVATE", "Private or local network", "私有或本地网络"}} {
		if countryName(check.code, false) != check.en || countryName(check.code, true) != check.zh {
			t.Fatal("CLDR country mismatch", check)
		}
	}
	event := localizedEventFixture("ssh_login_success", "observed")
	event.Geo = model.Geo{CountryCode: "XX", Country: "External <Original&Name>", Region: "External Region", City: "External City", ASNOrg: "External Organization", ASN: 64500, DatabaseAge: "not-a-numberd"}
	body := FormatEventLocalized("fixture", event, "zh", time.UTC)
	for _, want := range []string{"国家未知", "数据库原名", "External &lt;Original&amp;Name&gt;", "External Region", "External City", "External Organization", "地理数据库库龄: 未知", "并非核实的物理位置或入侵结论"} {
		if !strings.Contains(body, want) {
			t.Fatal("safe original name or qualification omitted", want, body)
		}
	}
	event.Geo = model.Geo{Country: "Private or local network"}
	if body := FormatEventLocalized("fixture", event, "zh", time.UTC); strings.Contains(body, "Private or local network") || !strings.Contains(body, "私有或本地网络") {
		t.Fatal("generated Geo description leaked", body)
	}
}

func TestFormatEventLocalizedUnknownLegacyAndNoSummaryFallback(t *testing.T) {
	event := localizedEventFixture("future_kind_v2", "future_phase")
	event.Severity = "future_severity"
	body := FormatEventLocalized("fixture", event, "zh", nil)
	for _, want := range []string{"未支持的事件", "此事件类型尚无通知模板", "future_kind_v2", "future_phase", "future_severity", "原始详情仅保留在本地"} {
		if !strings.Contains(body, want) {
			t.Fatal("unknown code/qualification lost", want, body)
		}
	}
	if strings.Contains(body, event.Summary) {
		t.Fatal("unknown kind used original English prose")
	}
	for _, kind := range []string{"syn_flood", "bandwidth_spike", "ssh_login_success", "ssh_brute_force", "port_scan", "budget_month_cost", "health_storage"} {
		event := localizedEventFixture(kind, "start")
		event.Evidence = map[string]string{}
		event.ObservedAt = time.Time{}
		body := FormatEventLocalized("fixture", event, "zh", nil)
		assertNotificationHTML(t, body)
		if strings.Contains(body, event.Summary) || strings.Contains(body, "healthy") || !strings.Contains(body, "时间: 未知") {
			t.Fatal("legacy missing evidence invented state", body)
		}
	}
	legacy := localizedEventFixture("syn_flood", "start")
	delete(legacy.Evidence, "observed_rate")
	delete(legacy.Evidence, "threshold_rate")
	if body := FormatEventLocalized("fixture", legacy, "zh", time.UTC); !strings.Contains(body, "原始速率参数未保留") || !strings.Contains(body, "配置速率阈值: 100 包/秒") {
		t.Fatal("legacy fixed scalar lost", body)
	}
	legacy = localizedEventFixture("ssh_login_success", "observed")
	delete(legacy.Evidence, "window_millis")
	if body := FormatEventLocalized("fixture", legacy, "zh", time.UTC); !strings.Contains(body, "检测窗口: 60 秒") {
		t.Fatal("legacy bounded duration lost", body)
	}
	for _, key := range []string{"reason", "basis", "coverage"} {
		event := localizedEventFixture("budget_day_bytes", "start")
		event.Evidence[key] = "future_code"
		if body := FormatEventLocalized("fixture", event, "zh", time.UTC); !strings.Contains(body, "future_code") || !strings.Contains(body, "未知") && !strings.Contains(body, "未识别") {
			t.Fatal("unknown fixed code lost", key, body)
		}
		event.Evidence[key] = "Unrecognized generated English sentence should remain local"
		if body := FormatEventLocalized("fixture", event, "zh", time.UTC); strings.Contains(body, event.Evidence[key]) {
			t.Fatal("unknown prose leaked", body)
		}
	}
}

func TestFormatEventLocalizedDSTAndWrapperAndTestBody(t *testing.T) {
	for _, check := range []struct {
		zone  string
		month time.Month
		want  string
	}{{"America/New_York", time.January, "07:00:00 America/New_York (UTC-05:00)"}, {"America/New_York", time.July, "08:00:00 America/New_York (UTC-04:00)"}, {"Asia/Kolkata", time.January, "17:30:00 Asia/Kolkata (UTC+05:30)"}, {"Asia/Kathmandu", time.January, "17:45:00 Asia/Kathmandu (UTC+05:45)"}} {
		loc, err := time.LoadLocation(check.zone)
		if err != nil {
			t.Fatal(err)
		}
		event := localizedEventFixture("ssh_login_success", "observed")
		event.ObservedAt = time.Date(2026, check.month, 15, 12, 0, 0, 0, time.UTC)
		for _, language := range []string{"en", "zh"} {
			if body := FormatEventLocalized("fixture", event, language, loc); !strings.Contains(body, check.want) {
				t.Fatal("wrong historical event offset", body)
			}
		}
	}
	event := localizedEventFixture("ssh_login_success", "observed")
	if FormatEvent("fixture", event) != FormatEventLocalized("fixture", event, "en", time.UTC) || FormatEvent("fixture", event) != FormatEventLocalized("fixture", event, "", nil) {
		t.Fatal("English UTC wrapper changed")
	}
	if got := FormatTest("host&name", "en"); got != "✅ <b>NodeRampart notification test</b>\nHost: host&amp;name" {
		t.Fatal("English test contract changed", got)
	}
	if got := FormatTest("host&name", "zh"); got != "✅ <b>NodeRampart 通知测试</b>\n主机: host&amp;name" {
		t.Fatal("Chinese test body incomplete", got)
	}
}

func TestFormatEventLocalizedBoundsUTF8HTMLAndCoreFields(t *testing.T) {
	event := localizedEventFixture("ssh_login_success", "observed")
	event.SourceIP = "192.0.2.1"
	event.Target = "ssh user=" + strings.Repeat("<&测试>\n", 10000)
	event.Geo = model.Geo{CountryCode: "XX", Country: strings.Repeat("<&测试>", 10000), Region: strings.Repeat("<&测试>", 10000), City: strings.Repeat("<&测试>", 10000), ASN: 64500, ASNOrg: strings.Repeat("<&测试>", 10000), ASNNetwork: strings.Repeat("<&测试>", 10000), DatabaseAge: "7d"}
	event.Evidence["interface"] = strings.Repeat("<&测试>", 10000)
	for _, language := range []string{"en", "zh"} {
		body := FormatEventLocalized(strings.Repeat("<&主机>\n", 10000), event, language, time.UTC)
		assertNotificationHTML(t, body)
		for _, core := range []string{"evt_fixture", "inc_fixture", "192.0.2.1", "2026-07-15 04:05:06 UTC (UTC+00:00)", "NodeRampart"} {
			if !strings.Contains(body, core) {
				t.Fatal("long external field displaced core", core, body)
			}
		}
		if language == "zh" && (!strings.Contains(body, "仅观察；未自动封禁") || !strings.Contains(body, "…")) {
			t.Fatal("action/truncation lost", body)
		}
		if language == "en" && (!strings.Contains(body, "no automatic blocking") || !strings.Contains(body, "…")) {
			t.Fatal("action/truncation lost", body)
		}
	}
	if got := escapedField(string([]byte{'a', 0xff, 'b', 0, 'c'}), 100); got != "a�b c" {
		t.Fatalf("invalid UTF8 discarded subsequent content: %q", got)
	}
	for _, raw := range []string{strings.Repeat("中", 100), strings.Repeat("&", 100), strings.Repeat("<", 100), "<script>alert('x')</script>"} {
		bounded := escapedField(raw, 30)
		if len(bounded) > 30 || !utf8.ValidString(bounded) || strings.Contains(bounded, "<") || strings.Contains(html.UnescapeString(bounded), "&am") {
			t.Fatal("unsafe field bound", bounded)
		}
	}
	for _, raw := range []string{"NaN", "Inf", "-1", "0x1p0", "1_000", " 1", "1\n", "1e121"} {
		if _, ok := number(raw); ok {
			t.Fatal("accepted non-decimal/invalid scalar", raw)
		}
	}
	for _, language := range []string{"en", "zh"} {
		marker := localText(language == "zh", "… truncated; more details are available locally", "… 内容已截短；更多详情请在本地查看")
		if body := joinWithinMarker([]string{"<b>core</b>", strings.Repeat("field", 100)}, 128, marker); !strings.Contains(body, marker) {
			t.Fatal("localized overall truncation marker absent", body)
		}
	}
	if value, ok := number("1000.001"); !ok || value != "1000.001" {
		t.Fatal("rate precision lost", value, ok)
	}
}

func TestFormatEventLocalizedPreservesLargeIntegerRateEvidence(t *testing.T) {
	for _, scalar := range []string{"9007199254740993", "18446744073709551615"} {
		parsed, err := strconv.ParseUint(scalar, 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		event := localizedEventFixture("bandwidth_spike", "start")
		event.Count = parsed
		event.Evidence["observed_rate"], event.Evidence["threshold_rate"] = scalar, scalar
		for _, language := range []string{"en", "zh"} {
			body := FormatEventLocalized("fixture", event, language, time.UTC)
			unit := localText(language == "zh", " bytes/s", " 字节/秒")
			if strings.Count(body, scalar+unit) != 3 {
				t.Fatalf("integer scalar changed during localized rendering (%s): %s", language, body)
			}
		}
	}
}

func TestFormatEventLocalizedFalseAndUnavailableBooleans(t *testing.T) {
	for _, check := range []struct{ kind, key, label string }{
		{"ssh_brute_force", "invalid_user", "无效账户"},
		{"ssh_brute_force", "detection_window_complete", "检测窗口完整"},
		{"ssh_login_success", "preceding_source_failures_complete", "先前失败证据完整"},
		{"syn_flood", "coverage_complete", "记录覆盖完整"},
	} {
		for _, value := range []string{"true", "false", "", "unknown"} {
			event := localizedEventFixture(check.kind, "start")
			if value == "" {
				delete(event.Evidence, check.key)
			} else {
				event.Evidence[check.key] = value
			}
			body := FormatEventLocalized("fixture", event, "zh", time.UTC)
			label := check.label + ": "
			switch value {
			case "true":
				if !strings.Contains(body, label+"是") {
					t.Fatal("true fact lost", check.key, body)
				}
			case "false":
				if !strings.Contains(body, label+"否") {
					t.Fatal("false fact lost", check.key, body)
				}
			default:
				if strings.Contains(body, label) {
					t.Fatal("unavailable boolean was filled with true/false", check.key, body)
				}
			}
		}
	}
}
