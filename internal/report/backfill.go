// SPDX-License-Identifier: MIT

package report

import (
	"context"
	"errors"
	"time"

	"github.com/littlesho/NodeRampart/internal/api"
	"github.com/littlesho/NodeRampart/internal/store"
)

type BackfillDate struct {
	Date        string    `json:"date"`
	State       string    `json:"state"`
	PeriodStart time.Time `json:"period_start_utc,omitzero"`
	PeriodEnd   time.Time `json:"period_end_utc,omitzero"`
}

type BackfillResult struct {
	From          string         `json:"from"`
	Through       string         `json:"through"`
	Dates         []BackfillDate `json:"dates"`
	Complete      bool           `json:"complete"`
	NextDate      string         `json:"next_date,omitempty"`
	StoppedReason string         `json:"stopped_reason,omitempty"`
	Delivery      string         `json:"delivery"`
	Conflicts     int            `json:"conflicts"`
}

func civilDate(now time.Time, location *time.Location) time.Time {
	local := now.In(location)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
}

// ValidateBackfill bounds calendar work independently of DST day length.
func ValidateBackfill(from, through string, now time.Time, location *time.Location) error {
	if location == nil {
		location = time.Local
	}
	if !api.ValidDate(from) || !api.ValidDate(through) || from > through {
		return errors.New("invalid backfill dates")
	}
	first, _ := time.Parse(time.DateOnly, from)
	last, _ := time.Parse(time.DateOnly, through)
	today := civilDate(now, location)
	if first.Year() < 1970 || last.Sub(first) >= 31*24*time.Hour || !last.Before(today) || first.Before(today.AddDate(-1, -1, 0)) {
		return errors.New("backfill requires at most 31 completed dates within thirteen months")
	}
	return nil
}

func (b *Builder) archiveDate(ctx context.Context, date string, now time.Time) (store.ReportSnapshot, string, error) {
	release, err := b.lockArchive(ctx)
	if err != nil {
		return store.ReportSnapshot{}, "unavailable", err
	}
	defer release()
	civil, err := time.Parse(time.DateOnly, date)
	if err != nil {
		return store.ReportSnapshot{}, "unavailable", err
	}
	start, okStart := resolveCivilTime(civil, b.location())
	end, okEnd := resolveCivilTime(civil.AddDate(0, 0, 1), b.location())
	if !okStart || !okEnd || end.Before(start) {
		return store.ReportSnapshot{}, "unavailable", errors.New("unsupported civil boundary")
	}
	if start.Equal(end) {
		return store.ReportSnapshot{}, "nonexistent_date", nil
	}
	if end.After(now) {
		return store.ReportSnapshot{}, "unavailable", errors.New("report date is not complete")
	}
	current, err := b.Store.Report(ctx, date)
	if err == nil {
		if !current.PeriodStart.Equal(start) || !current.PeriodEnd.Equal(end) {
			return current, "conflict", store.ErrReportPeriodConflict
		}
		return current, "already_present", nil
	}
	if !store.IsNotFound(err) {
		return current, "unavailable", err
	}
	title := "Daily security report " + date
	body, pricing, err := b.rangeWithBilling(ctx, title, start, end, now)
	if err != nil {
		return current, "unavailable", err
	}
	snapshot := store.ReportSnapshot{Billing: pricing, Date: date, Title: title, Body: body, PeriodStart: start, PeriodEnd: end, GeneratedAt: now.UTC()}
	inserted, err := b.Store.SaveReportIfAbsent(ctx, snapshot)
	if errors.Is(err, store.ErrReportPeriodConflict) {
		return current, "conflict", err
	}
	if err != nil {
		return current, "unavailable", err
	}
	current, err = b.Store.Report(ctx, date)
	state := "already_present"
	if inserted {
		state = "generated"
	}
	return current, state, err
}

// Backfill only creates local immutable archives. It never admits notifications.
func (b *Builder) Backfill(ctx context.Context, from, through string, now time.Time) (BackfillResult, error) {
	result := BackfillResult{From: from, Through: through, Dates: []BackfillDate{}, Delivery: "local_archive_only"}
	if err := ValidateBackfill(from, through, now, b.location()); err != nil {
		return result, err
	}
	first, _ := time.Parse(time.DateOnly, from)
	for date := first; date.Format(time.DateOnly) <= through; date = date.AddDate(0, 0, 1) {
		label := date.Format(time.DateOnly)
		if err := ctx.Err(); err != nil {
			result.NextDate, result.StoppedReason = label, "cancelled"
			return result, err
		}
		snapshot, state, err := b.archiveDate(ctx, label, now)
		result.Dates = append(result.Dates, BackfillDate{Date: label, State: state, PeriodStart: snapshot.PeriodStart, PeriodEnd: snapshot.PeriodEnd})
		if state == "conflict" {
			result.Conflicts++
		}
		if err != nil && !errors.Is(err, store.ErrReportPeriodConflict) {
			result.NextDate, result.StoppedReason = label, "storage_or_generation_unavailable"
			if ctx.Err() != nil {
				result.StoppedReason = "cancelled"
			}
			return result, err
		}
	}
	result.Complete = true // All dates inspected; conflicts remain per-date.
	return result, nil
}

func (s *Scheduler) backfill(ctx context.Context, now time.Time) error {
	if s.BackfillDays == 0 {
		return nil
	}
	if s.BackfillDays < 0 || s.BackfillDays > 31 {
		return errors.New("invalid automatic backfill bound")
	}
	today := civilDate(now, s.Builder.location())
	previous, _, _ := PreviousDay(now, s.Builder.location())
	created := 0
	for offset := s.BackfillDays; offset > 0 && created < 2; offset-- {
		date := today.AddDate(0, 0, -offset).Format(time.DateOnly)
		if date >= previous {
			continue
		} // Reserve previous day for normal schedule.
		_, state, err := s.Builder.archiveDate(ctx, date, now)
		if errors.Is(err, store.ErrReportPeriodConflict) {
			continue
		}
		if err != nil {
			return err
		}
		if state == "generated" {
			created++
		}
	}
	return nil
}
