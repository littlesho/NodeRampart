// SPDX-License-Identifier: MIT

package report

import (
	"context"
	"fmt"
	"html"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/littlesho/NodeRampart/internal/billing"
	"github.com/littlesho/NodeRampart/internal/model"
	"github.com/littlesho/NodeRampart/internal/store"
)

type Builder struct {
	Store       *store.Store
	Hostname    string
	Location    *time.Location
	TopN        int
	Billing     *billing.Profile
	archiveOnce sync.Once
	archiveGate chan struct{}
}

// lockArchive serializes immutable date construction without making a waiting
// control request outlive its deadline. The zero-value Builder remains usable.
func (b *Builder) lockArchive(ctx context.Context) (func(), error) {
	b.archiveOnce.Do(func() { b.archiveGate = make(chan struct{}, 1) })
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case b.archiveGate <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-b.archiveGate
			return nil, err
		}
		return func() { <-b.archiveGate }, nil
	}
}

// PreviousDay returns the most recent existing civil day before today's local
// date. Wholly skipped dates have no report; repeated midnight begins at its
// first occurrence. Unsupported transition data returns an empty/zero period,
// which the report builder rejects rather than archiving an invented interval.
func PreviousDay(now time.Time, location *time.Location) (string, time.Time, time.Time) {
	if location == nil {
		location = time.Local
	}
	local := now.In(location)
	today := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
	end, ok := resolveCivilTime(today, location)
	if !ok {
		return "", time.Time{}, time.Time{}
	}
	previous := today
	for skipped := 0; skipped < maxSkippedCivilDays; skipped++ {
		previous = previous.AddDate(0, 0, -1)
		start, ok := resolveCivilTime(previous, location)
		if !ok {
			return "", time.Time{}, time.Time{}
		}
		if start.Before(end) {
			return previous.Format("2006-01-02"), start, end
		}
	}
	return "", time.Time{}, time.Time{}
}

func (b *Builder) Daily(ctx context.Context, now time.Time) (string, string, error) {
	date, start, end := PreviousDay(now, b.location())
	body, err := b.Range(ctx, "Daily security report "+date, start, end)
	return date, body, err
}

func (b *Builder) location() *time.Location {
	if b.Location == nil {
		return time.Local
	}
	return b.Location
}

func (b *Builder) Range(ctx context.Context, title string, start, end time.Time) (string, error) {
	return b.rangeAt(ctx, title, start, end, time.Now().UTC())
}

func (b *Builder) rangeAt(ctx context.Context, title string, start, end, asOf time.Time) (string, error) {
	body, _, err := b.rangeWithBilling(ctx, title, start, end, asOf)
	return body, err
}

func (b *Builder) rangeWithBilling(ctx context.Context, title string, start, end, asOf time.Time) (string, *billing.Snapshot, error) {
	var pricing *billing.Snapshot
	if !start.Before(end) {
		return "", nil, fmt.Errorf("report period must have a positive duration")
	}
	summary, err := b.Store.Summary(ctx, start, end, b.TopN)
	if err != nil {
		return "", nil, err
	}
	integrity, err := b.Store.Integrity(ctx, store.IntegrityQuery{Start: start, End: end, Limit: 1})
	if err != nil {
		return "", nil, err
	}
	lines := []string{fmt.Sprintf("🛡 <b>NodeRampart — %s</b>", html.EscapeString(title)), "Host: " + html.EscapeString(b.Hostname), fmt.Sprintf("Period: %s — %s", start.Format("2006-01-02 15:04 UTC"), end.Format("2006-01-02 15:04 UTC"))}
	lines = append(lines, "Archive uses retained records; missing observations are not reconstructed. Aggregates include overlapping UTC hours.")
	if start.Before(asOf.AddDate(0, 0, -7)) {
		lines = append(lines, "⚠ Event detail older than seven days may be pruned; event counts describe retained detail only.")
	}
	if len(summary.Events) == 0 && len(summary.Auth) == 0 {
		lines = append(lines, "\n<b>Security</b>\nNo recorded security events.")
	} else {
		lines = append(lines, "\n<b>Security</b>")
		for _, item := range summary.Auth {
			lines = append(lines, fmt.Sprintf("• SSH %s observations: %d", html.EscapeString(item.Kind), item.Count))
		}
		for _, item := range summary.Events {
			lines = append(lines, fmt.Sprintf("• %s/%s: %d", html.EscapeString(item.Kind), html.EscapeString(string(item.Severity)), item.Count))
		}
	}
	lines = append(lines, "\n<b>Recorded coverage in this period</b>")
	for _, item := range integrity.Components {
		if item.Name != "sensor_feed" && item.Name != "ssh_journal" && item.Name != "interface_counter" {
			continue
		}
		states := []string{}
		for _, value := range []struct {
			state string
			ms    int64
		}{{"running", item.RunningMS}, {"degraded", item.DegradedMS}, {"disabled", item.DisabledMS}, {"unknown", item.UnknownMS}} {
			if value.ms > 0 {
				states = append(states, fmt.Sprintf("%s %s", value.state, (time.Duration(value.ms)*time.Millisecond).Round(time.Second)))
			}
		}
		lines = append(lines, "• "+html.EscapeString(item.Name)+": "+strings.Join(states, " / "))
	}
	lines = append(lines, "Unrecorded/conflicting time is unknown; running heartbeats do not prove complete data. Coverage history is bounded.")
	lines = append(lines, retentionLines(integrity.Retention, start)...)
	for i, gap := range integrity.Gaps {
		if i == 3 {
			lines = append(lines, "Additional coverage gaps recorded; inspect local status/history.")
			break
		}
		lines = append(lines, fmt.Sprintf("⚠ %s: %s (%s — %s)", html.EscapeString(gap.Name), html.EscapeString(gap.Reason), gap.Start.Format("01-02 15:04Z"), gap.End.Format("01-02 15:04Z")))
	}
	lines = append(lines, "\n<b>Interface traffic</b>", fmt.Sprintf("RX %s / TX %s", formatBytes(summary.Interface.RXBytes), formatBytes(summary.Interface.TXBytes)))
	type regionTotal struct {
		label  string
		rx, tx uint64
	}
	regions := map[string]*regionTotal{}
	for _, item := range summary.Traffic {
		label := item.Country
		if label == "_overflow" {
			label = "Other destinations (cardinality cap)"
		} else if label == "" {
			label = "Unknown"
		}
		if item.ASN != 0 {
			label = fmt.Sprintf("%s · AS%d %s", label, item.ASN, item.ASNOrg)
		}
		value := regions[label]
		if value == nil {
			value = &regionTotal{label: label}
			regions[label] = value
		}
		if item.Direction == model.DirectionInbound {
			value.rx += item.Bytes
		} else {
			value.tx += item.Bytes
		}
	}
	ordered := make([]*regionTotal, 0, len(regions))
	for _, value := range regions {
		ordered = append(ordered, value)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].rx+ordered[i].tx > ordered[j].rx+ordered[j].tx })
	unknownRX := positiveDifference(summary.Interface.RXBytes, summary.AttributedRXBytes)
	unknownTX := positiveDifference(summary.Interface.TXBytes, summary.AttributedTXBytes)
	lines = append(lines, "\n<b>Data quality</b>", fmt.Sprintf("Sensor batches: %d · parse errors: %d", summary.Batches, summary.ParseErrors), fmt.Sprintf("Kernel capture: %d packets · %d drops · %d stats errors", summary.KernelPackets, summary.KernelDrops, summary.KernelStatsErrors), fmt.Sprintf("Flow overflow: %d packets / %s", summary.OverflowPackets, formatBytes(summary.OverflowBytes)), fmt.Sprintf("Unattributed/reconciliation: RX %s / TX %s", formatBytes(unknownRX), formatBytes(unknownTX)))
	lines = append(lines, fmt.Sprintf("IPC send loss estimate: %d batches · %d packets / %s", summary.IPCDroppedBatches, summary.IPCDroppedPackets, formatBytes(summary.IPCDroppedBytes)))
	lines = append(lines, "Older sensors do not report IPC loss; recovered health may include earlier periods.")
	if summary.HealthCounterSaturations > 0 {
		lines = append(lines, "⚠ Sensor health counters reached their reporting limit; totals are lower bounds.")
	}
	if summary.Batches == 0 {
		lines = append(lines, "⚠ Detailed sensor coverage unavailable; regional traffic may be missing.")
	}
	if b.Billing != nil {
		lastLocal := end.Add(-time.Nanosecond).In(b.location())
		month := time.Date(lastLocal.Year(), lastLocal.Month(), 1, 0, 0, 0, 0, time.UTC)
		monthStart, ok := resolveCivilTime(month, b.location())
		if !ok {
			return "", nil, fmt.Errorf("unsupported billing calendar boundary")
		}
		monthTotals, monthErr := b.Store.InterfaceSummary(ctx, monthStart, end)
		if monthErr != nil {
			return "", nil, monthErr
		}
		pricing, err = billing.NewSnapshot(*b.Billing, monthTotals.TXBytes, monthStart, end, asOf, b.location().String())
		if err != nil {
			return "", nil, err
		}
		estimate := pricing.Estimate
		lines = append(lines, "\n<b>Billing estimate</b>", fmt.Sprintf("Accounting period: %s — %s (end exclusive; %s)", monthStart.In(b.location()).Format("2006-01-02 15:04"), end.In(b.location()).Format("2006-01-02 15:04"), html.EscapeString(b.location().String())), html.EscapeString(estimate.String()), "Configured tariff at generation; saved daily reports retain calculation inputs. Guest counters do not equal the cloud provider billing meter.")
	}
	if len(summary.TopSources) > 0 {
		lines = append(lines, "\n<b>Top source ranges</b>")
		for i, item := range summary.TopSources {
			if i >= b.TopN {
				break
			}
			label := item.SourceRange
			if label == "_overflow" {
				label = "Other source ranges (cardinality cap)"
			} else if label == "_storage_pressure" {
				label = "Other source ranges (storage pressure)"
			}
			lines = append(lines, fmt.Sprintf("• %s: %d observations", html.EscapeString(label), item.Count))
		}
	}
	if len(ordered) > 0 {
		lines = append(lines, "\n<b>Traffic by GeoIP/ASN estimate</b>")
		if summary.TrafficTruncated {
			lines = append(lines, "Attribution display limited to 4096 retained groups; reconciliation totals include all groups.")
		}
		for i, item := range ordered {
			if i >= b.TopN {
				break
			}
			lines = append(lines, fmt.Sprintf("• %s: RX %s / TX %s", html.EscapeString(item.label), formatBytes(item.rx), formatBytes(item.tx)))
		}
	}
	return joinWithin(lines, 4096), pricing, nil
}

func formatBytes(value uint64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	amount := float64(value)
	unit := 0
	for amount >= 1024 && unit < len(units)-1 {
		amount /= 1024
		unit++
	}
	if unit == 0 {
		return fmt.Sprintf("%d B", value)
	}
	return fmt.Sprintf("%.2f %s", amount, units[unit])
}
func positiveDifference(total, part uint64) uint64 {
	if part >= total {
		return 0
	}
	return total - part
}
func joinWithin(lines []string, maximum int) string {
	var output strings.Builder
	for index, line := range lines {
		separator := ""
		if index > 0 {
			separator = "\n"
		}
		if output.Len()+len(separator)+len(line) > maximum {
			marker := "\n… truncated"
			if output.Len()+len(marker) <= maximum {
				output.WriteString(marker)
			}
			break
		}
		output.WriteString(separator)
		output.WriteString(line)
	}
	return output.String()
}
