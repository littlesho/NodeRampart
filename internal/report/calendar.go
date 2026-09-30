// SPDX-License-Identifier: MIT

package report

import "time"

const (
	civilSearchMargin     = 48 * time.Hour
	maxCivilZoneIntervals = 256
	maxSkippedCivilDays   = 7
)

// MonthStart returns the first real instant of the current local calendar month.
// A repeated midnight starts at its first occurrence; a skipped midnight starts
// at the first instant after the gap, consistently with daily reports.
func MonthStart(now time.Time, location *time.Location) (time.Time, bool) {
	if location == nil {
		location = time.Local
	}
	local := now.In(location)
	return resolveCivilTime(time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, time.UTC), location)
}

// BillingCycle resolves a fixed monthly civil start day. Legacy zero retains
// day one. It uses the same timezone transition policy as daily reports.
func BillingCycle(now time.Time, location *time.Location, day int) (time.Time, time.Time, bool) {
	if day == 0 {
		day = 1
	}
	if day < 1 || day > 28 || now.IsZero() || now.Year() < 1970 || now.Year() > 9999 {
		return time.Time{}, time.Time{}, false
	}
	if location == nil {
		location = time.Local
	}
	local := now.In(location)
	civil := time.Date(local.Year(), local.Month(), day, 0, 0, 0, 0, time.UTC)
	start, ok := resolveCivilTime(civil, location)
	if !ok {
		return time.Time{}, time.Time{}, false
	}
	if now.Before(start) {
		civil = civil.AddDate(0, -1, 0)
		start, ok = resolveCivilTime(civil, location)
		if !ok {
			return time.Time{}, time.Time{}, false
		}
	}
	end, ok := resolveCivilTime(civil.AddDate(0, 1, 0), location)
	return start, end, ok && start.Year() >= 1970 && end.Year() <= 9999 && start.Before(end) && !now.Before(start) && now.Before(end)
}

// resolveCivilTime interprets UTC fields as a local wall clock. It chooses the
// first occurrence of a repeated time and the first real instant after a gap.
// Unlike time.Date alone, this does not normalize a missing midnight into the
// preceding date. IANA offsets and date-line changes fit the four-day window.
// Fixed zones take a separate path, including offsets larger than a Duration.
func resolveCivilTime(civil time.Time, location *time.Location) (time.Time, bool) {
	guess := time.Date(civil.Year(), civil.Month(), civil.Day(), civil.Hour(), civil.Minute(), civil.Second(), civil.Nanosecond(), location)
	start, end := guess.ZoneBounds()
	if start.IsZero() && end.IsZero() {
		return guess.UTC(), true
	}
	cursor, limit := guess.UTC().Add(-civilSearchMargin), guess.UTC().Add(civilSearchMargin)
	for visited := 0; visited < maxCivilZoneIntervals && cursor.Before(limit); visited++ {
		local := cursor.In(location)
		_, offset := local.Zone()
		// All IANA civil offsets fit one day. Reject unsupported custom
		// transition data instead of overflowing the duration conversion.
		if offset < -24*60*60 || offset > 24*60*60 {
			return time.Time{}, false
		}
		zoneStart, zoneEnd := local.ZoneBounds()
		candidate := civil.Add(-time.Duration(offset) * time.Second)
		if !candidate.Before(cursor) && candidate.Before(limit) && (zoneEnd.IsZero() || candidate.Before(zoneEnd)) {
			return candidate.UTC(), true
		}
		if !zoneStart.IsZero() && zoneStart.Equal(cursor) {
			_, oldOffset := zoneStart.Add(-time.Nanosecond).In(location).Zone()
			if oldOffset < -24*60*60 || oldOffset > 24*60*60 {
				return time.Time{}, false
			}
			oldWall := zoneStart.UTC().Add(time.Duration(oldOffset) * time.Second)
			newWall := zoneStart.UTC().Add(time.Duration(offset) * time.Second)
			if !civil.Before(oldWall) && civil.Before(newWall) {
				return zoneStart.UTC(), true
			}
		}
		if zoneEnd.IsZero() || !zoneEnd.After(cursor) {
			break
		}
		cursor = zoneEnd.UTC()
	}
	return time.Time{}, false
}
