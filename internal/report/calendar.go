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
