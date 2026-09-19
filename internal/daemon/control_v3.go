// SPDX-License-Identifier: MIT

package daemon

import (
	"context"
	"time"

	"github.com/littlesho/NodeRampart/internal/api"
	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/report"
	"github.com/littlesho/NodeRampart/internal/store"
)

func (a *App) dispatchControlV3(ctx context.Context, request api.Request) api.Response {
	response := api.Response{Version: api.Version, OK: true}
	invalid := func() api.Response { return controlFailure("invalid command arguments") }
	switch request.Command {
	case "report_backfill":
		var args api.BackfillArgs
		now := time.Now().UTC()
		location, err := config.ReportLocation(a.options.Config.Reports)
		if err != nil || api.DecodeArgs(request.Args, &args) != nil || report.ValidateBackfill(args.From, args.Through, now, location) != nil {
			return invalid()
		}
		// Keep one second for returning bounded progress before socket expiry.
		workCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		result, err := a.report.Backfill(workCtx, args.From, args.Through, now)
		response.Data = result
		if err != nil {
			response.OK, response.Error = false, "backfill stopped; inspect completed dates and resume from next_date"
		}
	case "health":
		var args api.HealthArgs
		if api.DecodeArgs(request.Args, &args) != nil {
			return invalid()
		}
		query := store.IntegrityQuery(args)
		if query.Validate() != nil {
			return invalid()
		}
		value, err := a.options.Store.Integrity(ctx, query)
		if err != nil {
			return controlFailure("historical integrity evidence is unavailable")
		}
		interfaces, err := a.options.Store.InterfaceHistory(ctx, args.Start, args.End, 100)
		if err != nil {
			return controlFailure("interface history is unavailable")
		}
		response.Data = map[string]any{"history": value, "current": a.Status(ctx), "interface_traffic": interfaces}
	case "events_timeline", "incident_show":
		var args api.TimelineArgs
		if api.DecodeArgs(request.Args, &args) != nil {
			return invalid()
		}
		query := store.TimelineQuery(args)
		if query.Validate() != nil {
			return invalid()
		}
		if request.Command == "incident_show" {
			if args.IncidentID == "" {
				return invalid()
			}
			value, err := a.options.Store.Incident(ctx, query)
			if err != nil {
				return controlFailure("incident is unavailable in the requested retained period")
			}
			response.Data = value
		} else {
			value, err := a.options.Store.Timeline(ctx, query)
			if err != nil {
				return controlFailure("event timeline is unavailable")
			}
			response.Data = value
		}
	case "incident_list":
		var args api.IncidentListArgs
		if api.DecodeArgs(request.Args, &args) != nil {
			return invalid()
		}
		q := store.TimelineQuery{Start: args.Start, End: args.End, AfterTime: args.BeforeTime, AfterID: args.BeforeID, Limit: args.Limit}
		if q.Validate() != nil {
			return invalid()
		}
		value, err := a.options.Store.Incidents(ctx, args.Start, args.End, args.BeforeTime, args.BeforeID, args.Limit)
		if err != nil {
			return controlFailure("incident history is unavailable")
		}
		response.Data = value
	case "notify_silence_add":
		var args api.SilenceArgs
		if api.DecodeArgs(request.Args, &args) != nil {
			return invalid()
		}
		value, err := a.options.Store.AddSilence(ctx, store.Silence{IncidentID: args.IncidentID, Kind: args.Kind, ExpiresAt: args.ExpiresAt, Reason: args.Reason}, time.Now().UTC())
		if err != nil {
			return controlFailure("silence could not be created; check selectors, expiry, capacity and storage health")
		}
		response.Data = value
	case "notify_silence_list":
		if api.DecodeArgs(request.Args, &struct{}{}) != nil {
			return invalid()
		}
		value, err := a.options.Store.Silences(ctx, time.Now().UTC())
		if err != nil {
			return controlFailure("silence history is unavailable")
		}
		response.Data = map[string]any{"silences": value, "limit": store.MaxSilences}
	case "notify_silence_remove":
		var args api.IDArgs
		if api.DecodeArgs(request.Args, &args) != nil || !api.ValidID(args.ID) {
			return invalid()
		}
		if err := a.options.Store.RemoveSilence(ctx, args.ID, time.Now().UTC()); err != nil {
			return controlFailure("silence could not be revoked")
		}
		response.Data = map[string]string{"id": args.ID, "state": "revoked"}
	default:
		return a.dispatchControlFeatures(ctx, request)
	}
	return response
}
