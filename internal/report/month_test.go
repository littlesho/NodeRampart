// SPDX-License-Identifier: MIT

package report

import (
	"testing"
	"time"
)

func TestMonthStartUsesCivilBoundary(t *testing.T) {
	for _, tc := range []struct{ zone, now, expected string }{
		{"UTC", "2026-09-12T00:00:00Z", "2026-09-01T00:00:00Z"},
		{"Asia/Kathmandu", "2026-09-12T00:00:00Z", "2026-08-31T18:15:00Z"},
		{"America/Havana", "2015-11-12T00:00:00Z", "2015-11-01T04:00:00Z"},
	} {
		t.Run(tc.zone, func(t *testing.T) {
			loc, err := time.LoadLocation(tc.zone)
			if err != nil {
				t.Fatal(err)
			}
			now, _ := time.Parse(time.RFC3339, tc.now)
			start, ok := MonthStart(now, loc)
			if !ok || start.Format(time.RFC3339) != tc.expected {
				t.Fatalf("month boundary %s, %t", start, ok)
			}
		})
	}
}
