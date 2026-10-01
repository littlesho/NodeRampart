// SPDX-License-Identifier: MIT

package daemon

import (
	"context"
	"encoding/json"
	"path/filepath"
	"time"

	"github.com/littlesho/NodeRampart/internal/api"
	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/model"
	"github.com/littlesho/NodeRampart/internal/notify"
	"github.com/littlesho/NodeRampart/internal/protocol"
	"github.com/littlesho/NodeRampart/internal/report"
	"github.com/littlesho/NodeRampart/internal/store"
)

type eventListItem struct {
	ID          string         `json:"id"`
	ObservedAt  time.Time      `json:"observed_at_utc"`
	Kind        string         `json:"kind"`
	Severity    model.Severity `json:"severity"`
	Summary     string         `json:"summary"`
	SourceRange string         `json:"source_range,omitempty"`
	Count       uint64         `json:"count"`
}

func controlFailure(message string) api.Response {
	return api.Response{Version: api.Version, OK: false, Error: message}
}

func (a *App) controlResponse(ctx context.Context, request api.Request) api.Response {
	response := a.dispatchControl(ctx, request)
	data, err := json.Marshal(response)
	if err != nil || len(data) > protocol.MaxFrameSize {
		return controlFailure("response exceeds control protocol limit; request a smaller page")
	}
	return response
}

func (a *App) dispatchControl(ctx context.Context, request api.Request) api.Response {
	if request.Version != api.Version {
		return controlFailure("unsupported API version")
	}
	if len(request.Args) > api.MaxArgsSize {
		return controlFailure("invalid command arguments")
	}
	response := api.Response{Version: api.Version, OK: true}
	invalid := func() api.Response { return controlFailure("invalid command arguments") }
	now := time.Now().UTC()
	switch request.Command {
	case "status", "doctor", "report_now", "notify_status":
		if api.DecodeArgs(request.Args, &struct{}{}) != nil {
			return invalid()
		}
		switch request.Command {
		case "status":
			response.Data = a.Status(ctx)
		case "doctor":
			status := a.Status(ctx)
			check, cancel := context.WithTimeout(ctx, 2*time.Second)
			foreignKeys, err := a.options.Store.ForeignKeys(check)
			cancel()
			if err == nil {
				status.ForeignKeys = &foreignKeys
			}
			diagnosis := Diagnose(status, true)
			status.Diagnosis = &diagnosis
			response.Data = status
		case "report_now":
			if a.report == nil {
				return controlFailure("report builder unavailable")
			}
			document, err := a.report.StructuredRange(ctx, "Last 24 hours", now.Add(-24*time.Hour), now, now)
			if err != nil {
				return controlFailure("report generation failed")
			}
			response.Data = map[string]any{"report": report.ShortBody(document.Body), "document": document}
		case "notify_status":
			status, err := a.options.Store.QueueStatus(ctx, now)
			if err != nil {
				return controlFailure("notification status unavailable")
			}
			response.Data = status
		}
	case "notify_test", "notify_discard_isolated":
		var args api.NotifyChannelArgs
		if api.DecodeArgs(request.Args, &args) != nil {
			return invalid()
		}
		if args.Channel == "" {
			args.Channel = "telegram"
		}
		if !config.IsNotificationChannel(args.Channel) {
			return invalid()
		}
		if request.Command == "notify_discard_isolated" {
			count, err := a.options.Store.DiscardIsolatedNotifications(ctx, args.Channel, now)
			if err != nil {
				return controlFailure("isolated notification bodies could not be discarded")
			}
			response.Data = map[string]any{"channel": args.Channel, "discarded": count, "status": "isolated bodies discarded; active queue preserved"}
		} else {
			sender, destination, enabled := a.options.Notifier, a.options.NotificationDestination, a.options.Config.Notifications.Telegram.Enabled
			if args.Channel == "webhook" {
				sender, destination, enabled = a.options.WebhookNotifier, a.options.WebhookDestination, a.options.Config.Notifications.Webhook.Enabled
			}
			if config.IsNativeChannel(args.Channel) {
				sender, destination, enabled = a.options.NativeNotifiers[args.Channel], a.options.NativeDestinations[args.Channel], a.options.Config.Notifications.NativeChannels()[args.Channel].Enabled
			}
			if sender == nil || !enabled {
				return controlFailure("selected notification sender is disabled or unavailable")
			}
			id := model.NewID("msg")
			language := "en"
			if args.Channel == "telegram" {
				language = config.TelegramLanguage(a.options.Config.Notifications.Telegram)
			}
			body := notify.FormatTest(a.options.Config.Hostname, language)
			if config.IsNativeChannel(args.Channel) {
				language = config.NativeChannelLanguage(a.options.Config.Notifications.NativeChannels()[args.Channel])
				body = notify.FormatNativeTest(a.options.Config.Hostname, language)
			}
			_, err := a.options.Store.Enqueue(ctx, store.OutboxMessage{ID: id, DedupeKey: "test:" + model.NewID("once"), Channel: args.Channel, PrivacyMode: a.options.Config.Privacy.NotificationIP, Destination: destination, Language: language, Body: body})
			if err != nil {
				return controlFailure("could not queue test notification")
			}
			response.Data = map[string]string{"status": "queued", "id": id, "channel": args.Channel}
		}
	case "events_list":
		var args api.EventListArgs
		if api.DecodeArgs(request.Args, &args) != nil || args.Start.IsZero() || args.End.IsZero() || args.Start.After(args.End) || !api.ValidLimit(args.Limit) || (args.BeforeID != "" && !api.ValidID(args.BeforeID)) || (args.Kind != "" && !api.ValidID(args.Kind)) {
			return invalid()
		}
		events, err := a.options.Store.Events(ctx, store.EventQuery{Start: args.Start, End: args.End, BeforeID: args.BeforeID, Kind: args.Kind, Limit: args.Limit})
		if err != nil {
			return controlFailure("event history unavailable")
		}
		items := make([]eventListItem, 0, len(events))
		for _, e := range events {
			items = append(items, eventListItem{ID: e.ID, ObservedAt: e.ObservedAt, Kind: e.Kind, Severity: e.Severity, Summary: e.Summary, SourceRange: e.SourceRange, Count: e.Count})
		}
		data := map[string]any{"events": items}
		if len(events) == args.Limit {
			last := events[len(events)-1]
			data["next_before_id"] = last.ID
			data["next_until_utc"] = last.ObservedAt
		}
		response.Data = data
	case "events_show":
		var args api.IDArgs
		if api.DecodeArgs(request.Args, &args) != nil || !api.ValidID(args.ID) {
			return invalid()
		}
		event, err := a.options.Store.Event(ctx, args.ID)
		if store.IsNotFound(err) {
			return controlFailure("event not found")
		}
		if err != nil {
			return controlFailure("event unavailable")
		}
		response.Data = event
	case "report_list":
		var args api.ListArgs
		if api.DecodeArgs(request.Args, &args) != nil || !api.ValidLimit(args.Limit) || (args.Before != "" && !api.ValidDate(args.Before)) {
			return invalid()
		}
		reports, err := a.options.Store.Reports(ctx, args.Before, args.Limit)
		if err != nil {
			return controlFailure("report history unavailable")
		}
		data := map[string]any{"reports": reports}
		if len(reports) == args.Limit {
			data["next_before"] = reports[len(reports)-1].Date
		}
		response.Data = data
	case "report_show":
		var args api.DateArgs
		if api.DecodeArgs(request.Args, &args) != nil || !api.ValidDate(args.Date) {
			return invalid()
		}
		report, err := a.options.Store.Report(ctx, args.Date)
		if store.IsNotFound(err) {
			return controlFailure("report not found")
		}
		if err != nil {
			return controlFailure("report unavailable")
		}
		response.Data = report
	case "notify_list":
		var args api.ListArgs
		if api.DecodeArgs(request.Args, &args) != nil || !api.ValidLimit(args.Limit) || (args.Before != "" && !api.ValidID(args.Before)) {
			return invalid()
		}
		items, err := a.options.Store.Notifications(ctx, args.Before, args.Limit)
		if err != nil {
			return controlFailure("notification history unavailable")
		}
		data := map[string]any{"notifications": items}
		if len(items) == args.Limit {
			data["next_before"] = items[len(items)-1].ID
		}
		response.Data = data
	case "notify_retry", "notify_quarantine":
		var args api.IDArgs
		if api.DecodeArgs(request.Args, &args) != nil || !api.ValidID(args.ID) {
			return invalid()
		}
		var err error
		if request.Command == "notify_retry" {
			err = a.options.Store.RetryNotification(ctx, args.ID, now)
		} else {
			err = a.options.Store.QuarantineNotification(ctx, args.ID, now)
		}
		if err != nil {
			return controlFailure("notification is unavailable or cannot change state")
		}
		state := "queued"
		if request.Command == "notify_quarantine" {
			state = "quarantined"
		}
		response.Data = map[string]string{"id": args.ID, "status": state}
	case "notify_resume":
		var args api.DestinationArgs
		if api.DecodeArgs(request.Args, &args) != nil || !api.ValidID(args.Destination) {
			return invalid()
		}
		if args.Destination == "telegram" {
			if a.options.NotificationDestination == "" {
				return controlFailure("Telegram target identity unavailable")
			}
			args.Destination = a.options.NotificationDestination
		}
		if err := a.options.Store.ResumeDestination(ctx, args.Destination); err != nil {
			return controlFailure("notification destination could not be resumed")
		}
		response.Data = map[string]string{"destination": args.Destination, "status": "resumed"}
	case "backup_create":
		var args api.BackupArgs
		if api.DecodeArgs(request.Args, &args) != nil || !filepath.IsAbs(args.Output) || filepath.Clean(args.Output) != args.Output || filepath.Dir(args.Output) != filepath.Join(filepath.Dir(a.options.Config.Paths.Database), "backups") {
			return invalid()
		}
		info, err := a.options.Store.Backup(ctx, args.Output)
		if err != nil {
			return controlFailure("backup could not be created; target must be new and under the state backups directory")
		}
		response.Data = info
	default:
		return a.dispatchControlV3(ctx, request)
	}
	return response
}
