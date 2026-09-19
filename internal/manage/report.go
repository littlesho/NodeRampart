// SPDX-License-Identifier: MIT

package manage

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"strings"
	"time"
	"unicode"

	"github.com/littlesho/NodeRampart/internal/billing"
)

const maxReportDisplayBytes = 64 << 10

// formatReport only uses the authenticated response. It never reads the active
// tariff or recalculates an archived body under today's configuration.
func formatReport(data []byte, archived bool) (string, error) {
	invalid := errors.New("saved report is unavailable or exceeds display bounds")
	if len(data) > maxReportDisplayBytes {
		return "", invalid
	}
	var value struct {
		Title, Body, Report string
		Billing             json.RawMessage `json:"billing"`
	}
	if json.Unmarshal(data, &value) != nil || len(value.Title) > 256 {
		return "", invalid
	}
	if value.Body == "" {
		value.Body = value.Report
	}
	if len(value.Body) > 4096 {
		return "", invalid
	}
	var out strings.Builder
	out.WriteString(reportText(value.Title) + "\n" + reportText(value.Body))
	if !archived {
		out.WriteString("\n\nCurrent report / 当前即时报告: no archived billing snapshot is attached. / 未附带已归档计费快照。\n")
		return out.String(), nil
	}
	out.WriteString("\n\nSaved billing snapshot / 已保存的计费快照\n")
	if len(value.Billing) == 0 || string(value.Billing) == "null" {
		out.WriteString("Unavailable for this archived report. Historical inputs were not saved (including older reports or billing disabled). / 此日报没有保存计费输入，可能是旧报告或当时未启用计费。\n")
		return out.String(), nil
	}
	var snapshot billing.Snapshot
	decoder := json.NewDecoder(bytes.NewReader(value.Billing))
	decoder.DisallowUnknownFields()
	decodeErr := decoder.Decode(&snapshot)
	if decodeErr == nil && decoder.Decode(new(any)) != io.EOF {
		decodeErr = invalid
	}
	// A normal IPC encoder can HTML-escape URL characters, making its JSON
	// larger than the stored representation. Bound the canonical snapshot too.
	_, snapshotErr := billing.EncodeSnapshot(&snapshot)
	if decodeErr != nil || snapshotErr != nil {
		out.WriteString("Unavailable: saved billing inputs failed validation. / 已保存的计费输入未通过校验，无法展示。\n")
		return out.String(), nil
	}
	field := func(s string) string { return html.EscapeString(reportText(s)) }
	p := snapshot.Profile
	fmt.Fprintf(&out, "Inputs saved at report generation / 报告生成时保存的输入 (schema %d, calculation %d)\n", snapshot.SchemaVersion, snapshot.CalculationVersion)
	fmt.Fprintf(&out, "Basis / 依据: %s\nGenerated at UTC / 生成时间: %s\n", snapshot.Basis, snapshot.GeneratedAt.UTC().Format(time.RFC3339Nano))
	fmt.Fprintf(&out, "Billing period UTC / 计费期间: [%s, %s)\nTimezone / 统计时区: %s\n", snapshot.PeriodStart.UTC().Format(time.RFC3339Nano), snapshot.PeriodEnd.UTC().Format(time.RFC3339Nano), field(snapshot.Timezone))
	fmt.Fprintf(&out, "Profile / 价格配置: %s\nProvider / 厂商: %s\nRegion / 区域: %s\nCurrency / 币种: %s\nEffective date / 生效日期: %s\nSource / 来源: %s\n", field(p.Name), field(p.Provider), field(p.SourceRegion), p.Currency, p.EffectiveDate, field(p.SourceURL))
	fmt.Fprintf(&out, "Profile SHA-256 / 配置摘要: %s\nObserved guest TX / 已观测主机出站: %d bytes\nUnit / 单位: 1 pricing GB = %d bytes (profile unit_bytes=%d; 0 uses 1000000000)\n", snapshot.ProfileSHA256, snapshot.OutboundBytes, snapshot.UnitBytes, p.UnitBytes)
	fmt.Fprintf(&out, "Assigned free allowance / 本机分配免费额度: %g pricing GB\nPaid-volume tiers after the free allowance / 扣除免费额度后的付费阶梯:\n", p.FreeGB)
	lower := float64(0)
	for _, tier := range p.InternetEgress {
		if tier.UpToGB == 0 {
			fmt.Fprintf(&out, "  %g+ pricing GB: %g %s/GB (unbounded / 无上限)\n", lower, tier.PricePerGB, p.Currency)
		} else {
			fmt.Fprintf(&out, "  %g–%g pricing GB: %g %s/GB\n", lower, tier.UpToGB, tier.PricePerGB, p.Currency)
		}
		lower = tier.UpToGB
	}
	fmt.Fprintf(&out, "Saved estimate / 已保存估算: %g %s; %g billable pricing GB\n", snapshot.Estimate.Cost, snapshot.Estimate.Currency, snapshot.Estimate.BillableGB)
	out.WriteString("Guest TX under the tariff configured at generation is an estimate. Its effective date does not establish historical provider pricing. / 按生成报告时的价格估算主机出站流量；生效日期不证明云厂商历史账单价格。\n")
	if out.Len() > maxReportDisplayBytes {
		return "", invalid
	}
	return out.String(), nil
}

func reportText(value string) string {
	return strings.Map(func(r rune) rune {
		if r != '\n' && r != '\t' && unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, strings.ToValidUTF8(value, "�"))
}
