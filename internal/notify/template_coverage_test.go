// SPDX-License-Identifier: MIT

package notify

import (
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/littlesho/NodeRampart/internal/model"
)

// These contracts intentionally enumerate the actual producer vocabulary. A new
// kind, evidence field or fixed summary requires a reviewed localized template;
// passing an English generated sentence through as "original text" is unsafe.
var eventEvidenceKeys = []string{
	"baseline_days", "baseline_mean_bytes", "basis", "component_reason", "condition_since_utc", "count_basis", "coverage", "coverage_complete", "currency", "detection_window_complete", "diagnostic_at_utc", "diagnostic_scope", "elapsed_millis", "failure_cause", "failure_stage", "geoip_edition", "growth_ratio", "history_basis", "history_hint_state", "history_source_hint", "history_time_hint", "http_status", "incident_duration_millis", "interface", "invalid_user", "journal_exit_code", "journal_signal", "journal_state", "method", "milestone", "observation", "observed_bytes", "observed_cost", "observed_rate", "packets", "period", "period_end_utc", "period_start_utc", "preceding_source_failures", "preceding_source_failures_complete", "reason", "rule", "source_scope", "threshold", "threshold_bytes", "threshold_cost", "threshold_rate", "top_source_bytes", "top_source_packets", "window", "window_millis", "window_seconds",
}

func parsedProducer(t *testing.T, path string) *ast.File {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func stringLiteral(expr ast.Expr) string {
	if literal, ok := expr.(*ast.BasicLit); ok && literal.Kind == token.STRING {
		text, _ := strconv.Unquote(literal.Value)
		return text
	}
	return ""
}
func mapStringString(expr ast.Expr) bool {
	m, ok := expr.(*ast.MapType)
	if !ok {
		return false
	}
	key, kok := m.Key.(*ast.Ident)
	value, vok := m.Value.(*ast.Ident)
	return kok && vok && key.Name == "string" && value.Name == "string"
}
func evidenceIndex(expr ast.Expr) (string, bool) {
	index, ok := expr.(*ast.IndexExpr)
	if !ok {
		return "", false
	}
	isEvidence := false
	switch value := index.X.(type) {
	case *ast.Ident:
		isEvidence = value.Name == "evidence"
	case *ast.SelectorExpr:
		isEvidence = value.Sel.Name == "Evidence"
	}
	key := stringLiteral(index.Index)
	return key, isEvidence && key != ""
}
func sortedSet(set map[string]bool) []string {
	result := make([]string, 0, len(set))
	for key := range set {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}

func TestLocalizedTemplatesCoverActualProducerContracts(t *testing.T) {
	paths := []string{"../detect/auth.go", "../detect/network.go", "../detect/fleet.go", "../daemon/auth_history.go", "../daemon/health.go", "../daemon/monitor_state.go", "../daemon/monitor_diagnostics.go"}
	keys, kinds, summaries := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, path := range paths {
		ast.Inspect(parsedProducer(t, path), func(node ast.Node) bool {
			if literal, ok := node.(*ast.BasicLit); ok && literal.Kind == token.STRING {
				value := stringLiteral(literal)
				if strings.HasPrefix(value, "budget_") || strings.HasPrefix(value, "health_") || value == "syn_flood" || value == "udp_flood" || value == "icmp_flood" || value == "bandwidth_spike" || value == "port_scan" || value == "ssh_login_success" || value == "ssh_brute_force" {
					if !strings.HasSuffix(value, "_") {
						kinds[value] = true
					}
				}
			}
			if composite, ok := node.(*ast.CompositeLit); ok && mapStringString(composite.Type) {
				for _, item := range composite.Elts {
					if field, ok := item.(*ast.KeyValueExpr); ok {
						keys[stringLiteral(field.Key)] = true
					}
				}
			}
			if assign, ok := node.(*ast.AssignStmt); ok {
				for _, lhs := range assign.Lhs {
					if key, ok := evidenceIndex(lhs); ok {
						keys[key] = true
					}
				}
			}
			addSummary := func(expr ast.Expr) {
				ast.Inspect(expr, func(n ast.Node) bool {
					if literal, ok := n.(*ast.BasicLit); ok && literal.Kind == token.STRING {
						summaries[stringLiteral(literal)] = true
					}
					return true
				})
			}
			if field, ok := node.(*ast.KeyValueExpr); ok {
				if key, ok := field.Key.(*ast.Ident); ok && key.Name == "Summary" {
					addSummary(field.Value)
				}
			}
			if assign, ok := node.(*ast.AssignStmt); ok {
				for _, lhs := range assign.Lhs {
					isSummary := false
					switch value := lhs.(type) {
					case *ast.SelectorExpr:
						isSummary = value.Sel.Name == "Summary"
					case *ast.Ident:
						isSummary = value.Name == "summary"
					}
					if isSummary {
						for _, rhs := range assign.Rhs {
							addSummary(rhs)
						}
					}
				}
			}
			return true
		})
	}
	delete(keys, "")
	delete(summaries, "")
	if got := sortedSet(keys); !reflect.DeepEqual(got, eventEvidenceKeys) {
		t.Fatalf("producer evidence needs explicit template coverage\ngot %q\ncovered %q", got, eventEvidenceKeys)
	}
	rendererKinds := map[string]bool{}
	for kind := range eventTitles {
		rendererKinds[kind] = true
	}
	if !reflect.DeepEqual(sortedSet(kinds), sortedSet(rendererKinds)) {
		t.Fatalf("uncovered kind: producer %q renderer %q", sortedSet(kinds), sortedSet(rendererKinds))
	}
	coveredSummary := []string{" authentication", "%d SSH authentication failures in %s", "Monitoring health condition recovered", "Monitoring health condition requires attention", "Observed guest TX estimate reached %d%% of the configured threshold", "at least %d unique local ports probed in %s", "observed %.0f %s; configured threshold %.0f %s", "rate recovered to %.0f %s after %s", "successful SSH "}
	sort.Strings(coveredSummary)
	if got := sortedSet(summaries); !reflect.DeepEqual(got, coveredSummary) {
		t.Fatalf("new producer summary needs deliberate scalar/template mapping: got %q, covered %q", got, coveredSummary)
	}
	for _, summary := range coveredSummary {
		for _, event := range localizedGoldenCases() {
			event.Summary = summary
			body := FormatEventLocalized("fixture", event, "zh", nil)
			if strings.Contains(body, summary) {
				t.Fatal("generated summary passthrough", event.Kind, summary)
			}
		}
	}
}

func TestLocalizedEventKindPhaseSeverityCatalog(t *testing.T) {
	// Independent of renderer maps and the golden generator. This is the public
	// producer inventory: two authentication, five network, four budget and five
	// component-health kinds. Source AST checks above catch vocabulary additions.
	kinds := []string{"ssh_login_success", "ssh_brute_force", "syn_flood", "udp_flood", "icmp_flood", "bandwidth_spike", "port_scan", "budget_month_bytes", "budget_month_cost", "budget_day_bytes", "budget_day_growth", "health_sensor", "health_interface_counter", "health_ssh_journal", "health_storage", "health_geoip_update"}
	covered := map[string]bool{}
	for _, kind := range kinds {
		text, ok := eventTitles[kind]
		if !ok || text.en == "" || text.zh == "" || text.en == text.zh {
			t.Fatal("missing complete bilingual kind template", kind)
		}
		covered[kind] = true
	}
	actual := map[string]bool{}
	for kind := range eventTitles {
		actual[kind] = true
	}
	if !reflect.DeepEqual(sortedSet(actual), sortedSet(covered)) {
		t.Fatal("independent kind inventory changed", sortedSet(actual))
	}
	for _, phase := range []struct{ code, zh string }{{"start", "开始"}, {"update", "更新"}, {"recovery", "恢复"}, {"observed", "已观察"}} {
		if phaseText(phase.code, true) != phase.zh || phaseText(phase.code, false) != phase.code {
			t.Fatal("phase translation missing", phase.code)
		}
	}
	for _, severity := range []struct{ code, zh string }{{"info", "信息"}, {"low", "低"}, {"medium", "中"}, {"high", "高"}, {"critical", "严重"}} {
		event := localizedEventFixture("ssh_login_success", "observed")
		event.Severity = model.Severity(severity.code)
		if body := FormatEventLocalized("fixture", event, "zh", nil); !strings.Contains(body, "NodeRampart "+severity.zh+" — ") {
			t.Fatal("severity translation missing", severity.code, body)
		}
	}
}

func TestLocalizedReasonCatalogCoversActualAllowedCodes(t *testing.T) {
	allowed := map[string]bool{}
	ast.Inspect(parsedProducer(t, "../model/alert.go"), func(node ast.Node) bool {
		if function, ok := node.(*ast.FuncDecl); ok {
			if function.Name.Name != "alertReason" {
				return false
			}
			ast.Inspect(function.Body, func(node ast.Node) bool {
				if clause, ok := node.(*ast.CaseClause); ok {
					for _, item := range clause.List {
						allowed[stringLiteral(item)] = true
					}
				}
				return true
			})
			return false
		}
		return true
	})
	ast.Inspect(parsedProducer(t, "../daemon/monitor_state.go"), func(node ast.Node) bool {
		if statement, ok := node.(*ast.SwitchStmt); ok {
			if selector, ok := statement.Tag.(*ast.SelectorExpr); ok && selector.Sel.Name == "Reason" {
				for _, node := range statement.Body.List {
					clause := node.(*ast.CaseClause)
					for _, item := range clause.List {
						allowed[stringLiteral(item)] = true
					}
				}
			}
		}
		return true
	})
	localized := map[string]bool{}
	for reason, text := range reasonText {
		localized[reason] = true
		if text.zh == "" || text.en == "" || text.zh == text.en {
			t.Fatal("unlocalized reason", reason)
		}
	}
	if !reflect.DeepEqual(sortedSet(allowed), sortedSet(localized)) {
		t.Fatalf("reason vocabulary changed: producer %q renderer %q", sortedSet(allowed), sortedSet(localized))
	}
}

func TestLocalizedHistoryEnumCoverage(t *testing.T) {
	fixed := map[string]map[string]bool{
		"history_hint_state":  {"observing": true, "history_unavailable": true, "history_incomplete": true, "available": true},
		"history_basis":       {"current_process_retained_successes_7d": true},
		"history_source_hint": {"first_observed_prefix": true, "first_observed_source": true},
		"history_time_hint":   {"unseen_local_hour": true},
	}
	observed := map[string]map[string]bool{}
	ast.Inspect(parsedProducer(t, "../daemon/auth_history.go"), func(node ast.Node) bool {
		if assign, ok := node.(*ast.AssignStmt); ok && len(assign.Lhs) == 1 && len(assign.Rhs) == 1 {
			if key, ok := evidenceIndex(assign.Lhs[0]); ok {
				if observed[key] == nil {
					observed[key] = map[string]bool{}
				}
				observed[key][stringLiteral(assign.Rhs[0])] = true
			}
		}
		return true
	})
	if !reflect.DeepEqual(observed, fixed) {
		t.Fatalf("historical evidence enum changed: %v", observed)
	}
}
