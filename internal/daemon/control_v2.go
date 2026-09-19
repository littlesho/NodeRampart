// SPDX-License-Identifier: MIT

package daemon

import (
	"context"
	"encoding/json"
	"html"
	"path/filepath"
	"time"

	"github.com/littlesho/NodeRampart/internal/api"
	"github.com/littlesho/NodeRampart/internal/model"
	"github.com/littlesho/NodeRampart/internal/protocol"
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
	case "status", "doctor", "report_now", "notify_status", "notify_test":
		if api.DecodeArgs(request.Args, &struct{}{}) != nil {
			return invalid()
		}
		switch request.Command {
		case "status", "doctor":
			response.Data = a.Status(ctx)
		case "report_now":
			body, err := a.report.Range(ctx, "Last 24 hours", now.Add(-24*time.Hour), now)
			if err != nil {
				return controlFailure("report generation failed")
			}
			response.Data = map[string]string{"report": body}
		case "notify_status":
			status, err := a.options.Store.QueueStatus(ctx, now)
			if err != nil {
				return controlFailure("notification status unavailable")
			}
			response.Data = status
		case "notify_test":
			if a.options.Notifier == nil {
				return controlFailure("Telegram is disabled")
			}
			id := model.NewID("msg")
			_, err := a.options.Store.Enqueue(ctx, store.OutboxMessage{ID: id, DedupeKey: "test:" + model.NewID("once"), Destination: "telegram", Body: "✅ <b>NodeRampart notification test</b>\nHost: " + html.EscapeString(a.options.Config.Hostname)})
			if err != nil {
				return controlFailure("could not queue test notification")
			}
			response.Data = map[string]string{"status": "queued", "id": id}
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
