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

	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/model"
	"github.com/littlesho/NodeRampart/internal/store"
)

type Scheduler struct {
	Store               *store.Store
	Builder             *Builder
	DailyAt             string
	Destination         string
	Destinations        []string
	NotificationPrivacy string
	NativeLanguages     map[string]string
	OfficialNotifiers   map[string]OfficialPreparer
	Hostname            string
	TelegramLanguage    string
	Logger              *slog.Logger
	BackfillDays        int
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
	destinations := append([]string(nil), s.Destinations...)
	if s.Destination != "" {
		destinations = append(destinations, s.Destination)
	}
	if len(destinations) > 12 {
		return fmt.Errorf("too many report notification targets")
	}
	generated := map[string]bool{}
	ordered := make([]string, 0, len(destinations))
	for _, destination := range destinations {
		if destination == "" {
			continue
		}
		exists, err := s.Store.ReportGenerated(ctx, date, destination)
		if err != nil {
			return err
		}
		if _, seen := generated[destination]; !seen {
			ordered = append(ordered, destination)
		}
		generated[destination] = exists
	}
	allGenerated := len(generated) > 0
	for _, exists := range generated {
		allGenerated = allGenerated && exists
	}
	if allGenerated {
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
	snapshot, _, err := s.Builder.archiveDate(ctx, date, now)
	if err != nil {
		return err
	}
	if len(generated) == 0 {
		return nil
	}
	timezone := ""
	if len(snapshot.Document) != 0 {
		document, err := DecodeDocument(snapshot.Document)
		if err != nil {
			return err
		}
		timezone = document.Timezone
	}
	telegramLanguage := "en"
	for destination := range generated {
		if strings.HasPrefix(destination, "telegram:") {
			telegramLanguage, err = notificationLanguage(s.TelegramLanguage)
			if err != nil {
				return err
			}
			break
		}
	}
	var targetErrors []error
	for _, destination := range ordered {
		if generated[destination] {
			continue
		}
		channel := ""
		language := "en"
		if strings.HasPrefix(destination, "telegram:") {
			channel = "telegram"
			language = telegramLanguage
		} else if strings.HasPrefix(destination, "webhook:") {
			channel = "webhook"
		}
		if candidate := strings.SplitN(destination, ":", 2)[0]; config.IsNativeChannel(candidate) || config.IsOfficialChannel(candidate) {
			channel = candidate
			language, err = notificationLanguage(s.NativeLanguages[channel])
			if err != nil {
				return err
			}
		}
		if config.IsNativeChannel(channel) || config.IsOfficialChannel(channel) {
			activated, err := s.Store.NotificationTargetActivatedAt(ctx, channel, destination)
			if err != nil {
				return err
			}
			if activated.IsZero() || !end.After(activated) {
				continue
			}
		}
		var notificationBody string
		if config.IsNativeChannel(channel) || config.IsOfficialChannel(channel) {
			notificationBody, err = NativeNotificationBody(snapshot, language)
		} else if channel == "telegram" {
			notificationBody, err = NotificationBodyLocalized(snapshot, language)
		} else {
			notificationBody, err = NotificationBody(snapshot)
		}
		if err != nil {
			return err
		}
		message := store.OutboxMessage{ID: model.NewID("msg"), DedupeKey: "daily:" + date + ":" + destination, Channel: channel, PrivacyMode: s.NotificationPrivacy, Destination: destination, Body: notificationBody, Language: language, Timezone: timezone}
		if config.IsOfficialChannel(channel) {
			message, err = prepareOfficialDaily(snapshot, s.Hostname, language, destination, s.NotificationPrivacy, timezone, s.OfficialNotifiers[channel])
			if err != nil {
				return err
			}
		}
		inserted, err := s.Store.Enqueue(ctx, message)
		if err != nil {
			targetErrors = append(targetErrors, fmt.Errorf("%s daily notification admission failed: %w", channel, err))
			continue
		}
		if inserted && s.Logger != nil {
			s.Logger.Info("daily report queued", "date", date)
		}
		if err := s.Store.MarkReportGenerated(ctx, date, destination, now.UTC()); err != nil {
			targetErrors = append(targetErrors, fmt.Errorf("%s daily notification decision failed: %w", channel, err))
		}
	}
	return errors.Join(targetErrors...)
}
