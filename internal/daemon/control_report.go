// SPDX-License-Identifier: MIT

package daemon

import (
	"context"
	"time"

	"github.com/littlesho/NodeRampart/internal/api"
)

func (a *App) dispatchControlReports(ctx context.Context, request api.Request) api.Response {
	response := api.Response{Version: api.Version, OK: true}
	if a.report == nil {
		return controlFailure("report builder unavailable")
	}
	switch request.Command {
	case "report_trend":
		var args api.ReportTrendArgs
		if api.DecodeArgs(request.Args, &args) != nil || args.Days != 7 && args.Days != 30 {
			return controlFailure("trend days must be 7 or 30")
		}
		value, err := a.report.Trend(ctx, args.Days, time.Now().UTC())
		if err != nil {
			return controlFailure("trend data unavailable")
		}
		response.Data = value
	case "report_forecast":
		if api.DecodeArgs(request.Args, &struct{}{}) != nil {
			return controlFailure("invalid forecast arguments")
		}
		value, err := a.report.Forecast(ctx, time.Now().UTC())
		if err != nil {
			return controlFailure("forecast data unavailable")
		}
		response.Data = value
	case "report_export":
		var args api.ReportExportArgs
		if api.DecodeArgs(request.Args, &args) != nil || !api.ValidDate(args.Date) || args.Format != "html" && args.Format != "json" {
			return controlFailure("invalid report export arguments")
		}
		value, err := a.options.Store.Report(ctx, args.Date)
		if err != nil {
			return controlFailure("report archive unavailable")
		}
		response.Data = value
	default:
		return controlFailure("unknown command")
	}
	return response
}
