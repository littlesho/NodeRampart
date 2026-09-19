// SPDX-License-Identifier: MIT

package notify

import (
	"fmt"
	"html"
	"sort"
	"strings"

	"github.com/littlesho/NodeRampart/internal/model"
)

func FormatEvent(hostname string, event model.Event) string {
	icon := map[model.Severity]string{model.SeverityCritical: "🔴", model.SeverityHigh: "🔴", model.SeverityMedium: "🟠", model.SeverityLow: "🟡", model.SeverityInfo: "🔵"}[event.Severity]
	if icon == "" {
		icon = "🔵"
	}
	lines := []string{
		fmt.Sprintf("%s <b>NodeRampart %s — %s</b>", icon, html.EscapeString(strings.ToUpper(string(event.Severity))), html.EscapeString(event.Kind)),
		"Host: " + html.EscapeString(hostname),
		"Time: " + html.EscapeString(event.ObservedAt.UTC().Format("2006-01-02 15:04:05 UTC")),
	}
	if event.Phase != "" {
		lines = append(lines, "Phase: "+html.EscapeString(event.Phase))
	}
	source := event.SourceRange
	if event.SourceIP != "" {
		source = event.SourceIP
	}
	if source != "" {
		lines = append(lines, "Source: "+html.EscapeString(source))
	}
	if event.Geo.CountryCode != "" || event.Geo.Country != "" || event.Geo.Region != "" || event.Geo.City != "" {
		parts := make([]string, 0, 4)
		for _, part := range []string{event.Geo.CountryCode, event.Geo.Country, event.Geo.Region, event.Geo.City} {
			if part != "" {
				parts = append(parts, part)
			}
		}
		place := strings.Join(parts, " / ")
		lines = append(lines, "GeoIP: "+html.EscapeString(place)+" (estimate)")
	}
	if event.Geo.ASN != 0 {
		lines = append(lines, fmt.Sprintf("ASN: AS%d %s", event.Geo.ASN, html.EscapeString(event.Geo.ASNOrg)))
	}
	if event.Geo.ASNNetwork != "" {
		lines = append(lines, "ASN range: "+html.EscapeString(event.Geo.ASNNetwork))
	}
	if event.Geo.DatabaseAge != "" {
		lines = append(lines, "GeoIP DB age: "+html.EscapeString(event.Geo.DatabaseAge))
	}
	if event.Target != "" {
		lines = append(lines, "Target: "+html.EscapeString(event.Target))
	}
	if event.Count > 0 {
		lines = append(lines, fmt.Sprintf("Count: %d", event.Count))
	}
	if event.Summary != "" {
		lines = append(lines, "Evidence: "+html.EscapeString(event.Summary))
	}
	if len(event.Evidence) > 0 {
		keys := make([]string, 0, len(event.Evidence))
		for key := range event.Evidence {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			lines = append(lines, html.EscapeString(key)+": "+html.EscapeString(event.Evidence[key]))
		}
	}
	lines = append(lines, "Action: observed only", "Incident: "+html.EscapeString(event.IncidentID))
	// Leave room for the bounded incident-update count added by coalescing.
	return joinWithin(lines, 4096-128)
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
