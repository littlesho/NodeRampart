// SPDX-License-Identifier: MIT

package report

import (
	"context"
	"encoding/binary"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/billing"
	"github.com/littlesho/NodeRampart/internal/model"
	"github.com/littlesho/NodeRampart/internal/store"
)

func reportZone(t *testing.T, name string) *time.Location {
	t.Helper()
	location, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return location
}
func reportTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func TestPreviousDayUsesRealCivilDayBoundaries(t *testing.T) {
	for _, tc := range []struct{ name, zone, now, date, start, end string }{
		{"missing_midnight_start", "America/Santiago", "2026-09-07T09:00:00-03:00", "2026-09-06", "2026-09-06T04:00:00Z", "2026-09-07T03:00:00Z"},
		{"missing_midnight_end", "America/Santiago", "2026-09-06T09:00:00-03:00", "2026-09-05", "2026-09-05T04:00:00Z", "2026-09-06T04:00:00Z"},
		{"repeated_midnight_start", "America/Havana", "2026-11-02T09:00:00-05:00", "2026-11-01", "2026-11-01T04:00:00Z", "2026-11-02T05:00:00Z"},
		{"repeated_midnight_end", "America/Havana", "2026-11-01T09:00:00-05:00", "2026-10-31", "2026-10-31T04:00:00Z", "2026-11-01T04:00:00Z"},
		{"ordinary_23_hour_day", "America/New_York", "2026-03-09T09:00:00-04:00", "2026-03-08", "2026-03-08T05:00:00Z", "2026-03-09T04:00:00Z"},
		{"ordinary_25_hour_day", "America/New_York", "2026-11-02T09:00:00-05:00", "2026-11-01", "2026-11-01T04:00:00Z", "2026-11-02T05:00:00Z"},
		{"month_end_missing_midnight", "Africa/Cairo", "2014-08-01T09:00:00+03:00", "2014-07-31", "2014-07-30T22:00:00Z", "2014-07-31T22:00:00Z"},
		{"month_start_missing_midnight", "Africa/Cairo", "2014-08-02T09:00:00+03:00", "2014-08-01", "2014-07-31T22:00:00Z", "2014-08-01T21:00:00Z"},
		{"whole_day_skipped", "Pacific/Apia", "2011-12-31T09:00:00+14:00", "2011-12-29", "2011-12-29T10:00:00Z", "2011-12-30T10:00:00Z"},
		{"after_skipped_day_year_boundary", "Pacific/Apia", "2012-01-01T09:00:00+14:00", "2011-12-31", "2011-12-30T10:00:00Z", "2011-12-31T10:00:00Z"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			location := reportZone(t, tc.zone)
			date, start, end := PreviousDay(reportTime(t, tc.now), location)
			if date != tc.date || !start.Equal(reportTime(t, tc.start)) || !end.Equal(reportTime(t, tc.end)) {
				t.Fatalf("date=%s interval=[%s,%s), want %s [%s,%s)", date, start, end, tc.date, tc.start, tc.end)
			}
			if start.In(location).Format("2006-01-02") != date || end.Add(-time.Nanosecond).In(location).Format("2006-01-02") != date {
				t.Fatal("reported date does not cover the interval edges")
			}
			if start.Add(-time.Nanosecond).In(location).Format("2006-01-02") == date {
				t.Fatal("first occurrence of the day's boundary was lost")
			}
		})
	}
}

func TestPreviousDayFixedZonesIncludingExtremeOffsets(t *testing.T) {
	for _, offset := range []int{0, 9 * 3600, -7 * 3600, 48 * 3600, -48 * 3600, 1 << 40, -1 << 40} {
		t.Run(fmt.Sprint(offset), func(t *testing.T) {
			location := time.FixedZone("fixed", offset)
			now := time.Date(2026, 9, 7, 9, 0, 0, 0, location)
			date, start, end := PreviousDay(now, location)
			wantStart := time.Date(2026, 9, 6, 0, 0, 0, 0, location)
			wantEnd := time.Date(2026, 9, 7, 0, 0, 0, 0, location)
			if date != "2026-09-06" || !start.Equal(wantStart) || !end.Equal(wantEnd) || end.Sub(start) != 24*time.Hour {
				t.Fatalf("fixed offset %d was normalized incorrectly: %s %s %s", offset, date, start, end)
			}
		})
	}
}

func TestCivilBoundaryRejectsExcessiveTransitionsWithinBound(t *testing.T) {
	// A synthetic TZif with more transitions than the search budget. Failure
	// must be explicit instead of looping indefinitely or inventing a period.
	count := maxCivilZoneIntervals + 10
	data := make([]byte, 44+count*5+2*6+4)
	copy(data, "TZif")
	binary.BigEndian.PutUint32(data[32:36], uint32(count))
	binary.BigEndian.PutUint32(data[36:40], 2)
	binary.BigEndian.PutUint32(data[40:44], 4)
	first := time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC).Unix()
	for i := 0; i < count; i++ {
		binary.BigEndian.PutUint32(data[44+i*4:], uint32(first+int64(i+1)*60))
		data[44+count*4+i] = byte(i % 2)
	}
	types := 44 + count*5
	binary.BigEndian.PutUint32(data[types+6:types+10], 1)
	data[types+11] = 2
	copy(data[types+12:], "A\x00B\x00")
	location, err := time.LoadLocationFromTZData("dense-transitions", data)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resolveCivilTime(time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC), location); ok {
		t.Fatal("excessive transition data did not fail closed")
	}
}

func TestSchedulerNormalizesDSTClockAndKeepsOneArchive(t *testing.T) {
	for _, tc := range []struct{ name, zone, dailyAt, before, due, again, date string }{
		{"missing_midnight", "America/Santiago", "00:30", "", "2026-09-06T04:00:00Z", "2026-09-06T05:00:00Z", "2026-09-05"},
		{"missing_ordinary_hour", "America/New_York", "02:30", "2026-03-08T06:59:59Z", "2026-03-08T07:00:00Z", "2026-03-08T08:00:00Z", "2026-03-07"},
		{"repeated_midnight", "America/Havana", "00:30", "2026-11-01T04:29:59Z", "2026-11-01T04:30:00Z", "2026-11-01T05:30:00Z", "2026-10-31"},
		{"repeated_ordinary_hour", "America/New_York", "01:30", "2026-11-01T05:29:59Z", "2026-11-01T05:30:00Z", "2026-11-01T06:30:00Z", "2026-10-31"},
		{"restart_during_second_midnight", "America/Havana", "00:30", "", "2026-11-01T05:10:00Z", "2026-11-01T05:30:00Z", "2026-10-31"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, err := store.Open(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			ctx := context.Background()
			s := Scheduler{Store: db, Builder: &Builder{Store: db, Location: reportZone(t, tc.zone), Hostname: "fixture", TopN: 5}, DailyAt: tc.dailyAt, Destination: "telegram"}
			if tc.before != "" {
				if err := s.check(ctx, reportTime(t, tc.before)); err != nil {
					t.Fatal(err)
				}
				if _, err := db.Report(ctx, tc.date); !store.IsNotFound(err) {
					t.Fatalf("report created before first real deadline: %v", err)
				}
			}
			due := reportTime(t, tc.due)
			if err := s.check(ctx, due); err != nil {
				t.Fatal(err)
			}
			snapshot, err := db.Report(ctx, tc.date)
			if err != nil {
				t.Fatal(err)
			}
			date, start, end := PreviousDay(due, s.Builder.Location)
			if date != tc.date || !snapshot.PeriodStart.Equal(start) || !snapshot.PeriodEnd.Equal(end) {
				t.Fatalf("archive key/period mismatch: %#v", snapshot)
			}
			if err := s.check(ctx, reportTime(t, tc.again)); err != nil {
				t.Fatal(err)
			}
			again, err := db.Report(ctx, tc.date)
			if err != nil {
				t.Fatal(err)
			}
			if !again.GeneratedAt.Equal(snapshot.GeneratedAt) || again.Body != snapshot.Body {
				t.Fatal("repeated civil clock regenerated archive")
			}
			reports, err := db.Reports(ctx, "", 10)
			if err != nil || len(reports) != 1 {
				t.Fatalf("duplicate or misdated archive: %#v %v", reports, err)
			}
			queue, err := db.QueueStatus(ctx, time.Now().UTC())
			if err != nil || queue.Pending != 1 {
				t.Fatalf("repeated clock duplicated notification: %#v %v", queue, err)
			}
		})
	}
}

func TestSchedulerSkippedDateArchivesExistingDaysWithoutGap(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	s := Scheduler{Store: db, Builder: &Builder{Store: db, Location: reportZone(t, "Pacific/Apia"), Hostname: "fixture", TopN: 5}, DailyAt: "09:00"}
	for _, now := range []string{"2011-12-31T09:00:00+14:00", "2012-01-01T09:00:00+14:00"} {
		if err := s.check(ctx, reportTime(t, now)); err != nil {
			t.Fatal(err)
		}
	}
	before, err := db.Report(ctx, "2011-12-29")
	if err != nil {
		t.Fatal(err)
	}
	after, err := db.Report(ctx, "2011-12-31")
	if err != nil {
		t.Fatal(err)
	}
	if !before.PeriodEnd.Equal(after.PeriodStart) {
		t.Fatal("skipped calendar date introduced a real-time coverage gap")
	}
	if _, err := db.Report(ctx, "2011-12-30"); !store.IsNotFound(err) {
		t.Fatalf("nonexistent date received an invented report: %v", err)
	}
}

func TestDailyMidnightGapIncludesOnlyTheRealReportedDay(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	start := reportTime(t, "2026-09-06T04:00:00Z")
	end := reportTime(t, "2026-09-07T03:00:00Z")
	for i, at := range []time.Time{start.Add(-time.Millisecond), start, end.Add(-time.Millisecond), end} {
		kind := "inside"
		if i == 0 || i == 3 {
			kind = "outside"
		}
		if err := db.InsertEvent(ctx, model.Event{ID: fmt.Sprint(i), ObservedAt: at, Kind: kind, Severity: model.SeverityInfo}); err != nil {
			t.Fatal(err)
		}
	}
	builder := Builder{Store: db, Location: reportZone(t, "America/Santiago"), Hostname: "fixture", TopN: 5}
	date, body, err := builder.Daily(ctx, reportTime(t, "2026-09-07T09:00:00-03:00"))
	if err != nil {
		t.Fatal(err)
	}
	if date != "2026-09-06" || !strings.Contains(body, "inside/info: 2") || strings.Contains(body, "outside") {
		t.Fatalf("report queried the wrong civil interval: date=%s body=%s", date, body)
	}
}

func TestBillingMonthStartsAtFirstRealInstantAfterMissingMidnight(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err := db.AddInterface(ctx, model.InterfaceTotals{HourUTC: reportTime(t, "2014-07-31T21:00:00Z"), TXBytes: 99_000_000_000}); err != nil {
		t.Fatal(err)
	}
	if err := db.AddInterface(ctx, model.InterfaceTotals{HourUTC: reportTime(t, "2014-08-01T12:00:00Z"), TXBytes: 10_000_000_000}); err != nil {
		t.Fatal(err)
	}
	b := Builder{Store: db, Hostname: "fixture", Location: reportZone(t, "Africa/Cairo"), TopN: 5, Billing: &billing.Profile{SchemaVersion: 1, Provider: "custom", SourceRegion: "fixture", EffectiveDate: "2026-01-01", SourceURL: "https://example.com/pricing", Name: "fixture", Currency: "USD", InternetEgress: []billing.Tier{{PricePerGB: 1}}}}
	_, body, err := b.Daily(ctx, reportTime(t, "2014-08-02T09:00:00+03:00"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "10.00 USD for 10.000 billable GB") || !strings.Contains(body, "Accounting period: 2014-08-01 01:00") {
		t.Fatalf("billing month included the preceding calendar date: %s", body)
	}
}

func TestReportLabelsStoragePressureSourceBucket(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	start := time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)
	for _, source := range []string{"_storage_pressure", "_overflow"} {
		if err := db.AddAuth(ctx, start, "failure", source); err != nil {
			t.Fatal(err)
		}
	}
	body, err := (&Builder{Store: db, Hostname: "fixture", Location: time.UTC, TopN: 5}).Range(ctx, "fixture", start, start.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "Other source ranges (storage pressure)") || !strings.Contains(body, "Other source ranges (cardinality cap)") || strings.Contains(body, "_storage_pressure") {
		t.Fatalf("internal source bucket rendered as an address: %s", body)
	}
}
