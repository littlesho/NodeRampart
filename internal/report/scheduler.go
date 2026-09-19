// SPDX-License-Identifier: MIT

package report

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/littlesho/NodeRampart/internal/model"
	"github.com/littlesho/NodeRampart/internal/store"
)

type Scheduler struct {
	Store        *store.Store
	Builder      *Builder
	DailyAt      string
	Destination  string
	Logger       *slog.Logger
	BackfillDays int
}

func (s *Scheduler) Run(ctx context.Context) error {
	if s.Store == nil || s.Builder == nil {
		return nil
	}
	if s.Logger == nil {
		s.Logger = slog.Default()
	}
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		if err := s.check(ctx, time.Now()); err != nil {
			s.Logger.Warn("daily report check failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (s *Scheduler) check(ctx context.Context, now time.Time) error {
	work, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	previousErr := s.checkPrevious(work, now)
	if previousErr != nil && !errors.Is(previousErr, store.ErrReportPeriodConflict) {
		return previousErr
	}
	// A previous-day timezone conflict is local to that immutable date. Keep
	// reporting it while allowing unrelated dates to make bounded progress.
	return errors.Join(previousErr, s.backfill(work, now))
}

func (s *Scheduler) checkPrevious(ctx context.Context, now time.Time) error {
	parts := strings.Split(s.DailyAt, ":")
	if len(parts) != 2 {
		return fmt.Errorf("invalid daily report time")
	}
	hour, hourErr := strconv.Atoi(parts[0])
	minute, minuteErr := strconv.Atoi(parts[1])
	if hourErr != nil || minuteErr != nil || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return fmt.Errorf("invalid daily report time")
	}
	location := s.Builder.location()
	local := now.In(location)
	clock := time.Date(local.Year(), local.Month(), local.Day(), hour, minute, 0, 0, time.UTC)
	scheduled, ok := resolveCivilTime(clock, location)
	if !ok {
		return fmt.Errorf("unsupported daily report clock transition")
	}
	if local.Before(scheduled) {
		return nil
	}
	date, start, end := PreviousDay(now, location)
	if s.Destination != "" {
		exists, err := s.Store.ReportGenerated(ctx, date, s.Destination)
		if err != nil {
			return err
		}
		if exists {
			archived, err := s.Store.Report(ctx, date)
			if store.IsNotFound(err) {
				return nil
			}
			if err != nil {
				return err
			}
			if !archived.PeriodStart.Equal(start) || !archived.PeriodEnd.Equal(end) {
				return store.ErrReportPeriodConflict
			}
			return nil
		}
	}
	snapshot, _, err := s.Builder.archiveDate(ctx, date, now)
	if err != nil {
		return err
	}
	if s.Destination == "" {
		return nil
	}
	inserted, err := s.Store.Enqueue(ctx, store.OutboxMessage{ID: model.NewID("msg"), DedupeKey: "daily:" + date + ":" + s.Destination, Destination: s.Destination, Body: snapshot.Body})
	if err != nil {
		return err
	}
	if inserted && s.Logger != nil {
		s.Logger.Info("daily report queued", "date", date)
	}
	return s.Store.MarkReportGenerated(ctx, date, s.Destination, now.UTC())
}
