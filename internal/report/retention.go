// SPDX-License-Identifier: MIT

package report

import (
	"fmt"
	"html"
	"time"

	"github.com/littlesho/NodeRampart/internal/store"
)

func retentionLines(view *store.RetentionView, start time.Time) []string {
	if view == nil {
		return []string{"Retention ledger unavailable; removed history cannot be inferred from remaining records."}
	}
	lines := []string{}
	if start.Before(view.TrackingStarted) || view.EvictedEntries > 0 {
		lines = append(lines, fmt.Sprintf("Retention tracking began %s; %d ledger entries retired. Earlier removal is unknown.", view.TrackingStarted.UTC().Format("2006-01-02 15:04Z"), view.EvictedEntries))
	}
	if len(view.Entries) == 0 {
		return append(lines, "No retained pruning entry overlaps this period; this does not prove complete data.")
	}
	lines = append(lines, "Retention ledger: affected spans are bounding ranges; counts are source rows, not lost packets.")
	for i, entry := range view.Entries {
		if i == 2 {
			break
		}
		lines = append(lines, fmt.Sprintf("• %s / %s: %d affected rows (%s — %s).", html.EscapeString(entry.Dataset), html.EscapeString(entry.Reason), entry.AffectedRows, entry.DataStart.UTC().Format("01-02 15:04Z"), entry.DataEnd.UTC().Format("01-02 15:04Z")))
	}
	if len(view.Entries) > 2 || view.More {
		lines = append(lines, "More pruning evidence is available with retention or in the TUI.")
	}
	return lines
}
