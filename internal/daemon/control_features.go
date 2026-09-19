// SPDX-License-Identifier: MIT

package daemon

import (
	"context"

	"github.com/littlesho/NodeRampart/internal/api"
	"github.com/littlesho/NodeRampart/internal/store"
)

func (a *App) dispatchControlFeatures(ctx context.Context, request api.Request) api.Response {
	response := api.Response{Version: api.Version, OK: true}
	switch request.Command {
	case "alerts_status":
		if api.DecodeArgs(request.Args, &struct{}{}) != nil {
			return controlFailure("invalid command arguments")
		}
		response.Data = a.MonitorStatus()
	case "retention":
		var args store.RetentionQuery
		if api.DecodeArgs(request.Args, &args) != nil || args.Validate() != nil {
			return controlFailure("invalid retention query")
		}
		value, err := a.options.Store.Retention(ctx, args)
		if err != nil {
			return controlFailure("retention evidence is unavailable")
		}
		response.Data = value
	case "evidence_snapshot":
		var args api.EvidenceArgs
		if api.DecodeArgs(request.Args, &args) != nil || args.Validate() != nil {
			return controlFailure("invalid evidence query")
		}
		value, err := a.evidenceSnapshot(ctx, args)
		if err != nil {
			return controlFailure("bounded evidence snapshot is unavailable")
		}
		response.Data = value
	default:
		return controlFailure("unknown command")
	}
	return response
}
