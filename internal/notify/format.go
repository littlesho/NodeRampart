// SPDX-License-Identifier: MIT

package notify

import (
	"strings"
	"time"

	"github.com/littlesho/NodeRampart/internal/model"
)

// FormatEvent retains the English/UTC entry point used by Webhook rendering.
func FormatEvent(hostname string, event model.Event) string {
	return FormatEventLocalized(hostname, event, "en", time.UTC)
}

func joinWithin(lines []string, maximum int) string {
	return joinWithinMarker(lines, maximum, "… truncated")
}

func joinWithinMarker(lines []string, maximum int, truncation string) string {
	marker := "\n" + truncation
	limit := maximum
	// Reserve a complete localized marker only when content exceeds the limit.
	total := 0
	for i, line := range lines {
		separator := 0
		if i > 0 {
			separator = 1
		}
		if len(line)+separator > maximum-total {
			limit = maximum - len(marker)
			break
		}
		total += len(line) + separator
	}
	var output strings.Builder
	for index, line := range lines {
		separator := ""
		if index > 0 {
			separator = "\n"
		}
		if output.Len()+len(separator)+len(line) > limit {
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
