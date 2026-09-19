// SPDX-License-Identifier: MIT

package report

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/billing"
	"github.com/littlesho/NodeRampart/internal/model"
	"github.com/littlesho/NodeRampart/internal/store"
)

func TestDailyBillingCalendarBoundaries(t *testing.T) {
	for _, tc := range []struct{ name, zone, date, month string }{
		{"month_end", "UTC", "2026-09-01", "2026-08-01"},
		{"year_end", "Asia/Shanghai", "2027-01-01", "2026-12-01"},
		{"negative_offset", "America/New_York", "2026-09-01", "2026-08-01"},
		{"spring_DST", "America/New_York", "2026-03-09", "2026-03-01"},
		{"autumn_DST", "America/New_York", "2026-11-02", "2026-11-01"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			location, err := time.LoadLocation(tc.zone)
			if err != nil {
				t.Fatal(err)
			}
			now, err := time.ParseInLocation("2006-01-02", tc.date, location)
			if err != nil {
				t.Fatal(err)
			}
			_, start, end := PreviousDay(now, location)
			db, err := store.Open(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			ctx := context.Background()
			if err := db.AddInterface(ctx, model.InterfaceTotals{HourUTC: start.Add(2 * time.Hour), TXBytes: 10_000_000_000}); err != nil {
				t.Fatal(err)
			}
			// The first hour outside the exclusive end must never inflate billing.
			if err := db.AddInterface(ctx, model.InterfaceTotals{HourUTC: end, TXBytes: 99_000_000_000}); err != nil {
				t.Fatal(err)
			}
			b := Builder{Store: db, Hostname: "lab", Location: location, TopN: 5, Billing: &billing.Profile{SchemaVersion: 1, Provider: "custom", SourceRegion: "fixture", EffectiveDate: "2026-01-01", SourceURL: "https://example.com/pricing", Name: "fixture", Currency: "USD", InternetEgress: []billing.Tier{{PricePerGB: 1}}}}
			_, body, err := b.Daily(ctx, now)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(body, "10.00 USD for 10.000 billable GB") || !strings.Contains(body, "Accounting period: "+tc.month+" 00:00") {
				t.Fatalf("wrong billing period/usage: %s", body)
			}
			if tc.name == "spring_DST" && end.Sub(start) != 23*time.Hour {
				t.Fatal("spring day not 23 hours")
			}
			if tc.name == "autumn_DST" && end.Sub(start) != 25*time.Hour {
				t.Fatal("autumn day not 25 hours")
			}
		})
	}
}
