// SPDX-License-Identifier: MIT

package daemon

import (
	"strconv"
	"time"

	"github.com/littlesho/NodeRampart/internal/assets"
	"github.com/littlesho/NodeRampart/internal/collector"
	"github.com/littlesho/NodeRampart/internal/model"
)

// monitorDiagnostic is an observation-local snapshot. It does not alter
// monitorData's durable v2 format or incident identity/debounce semantics.
// Only fixed validated categories can be projected into a persisted event.
type monitorDiagnostic struct {
	journal *collector.JournalStatus
	geo     *assets.Health
}

func (d monitorDiagnostic) addEvidence(event *model.Event) {
	if event.Phase == "recovery" {
		// Recovery is a fresh healthy observation. Do not borrow a previous
		// failure's details or imply that a historical collection gap vanished.
		return
	}
	evidence := event.Evidence
	switch event.Kind {
	case "health_ssh_journal":
		if d.journal == nil {
			return
		}
		status := *d.journal
		switch status.State {
		case "starting", "retrying", "gap", "degraded":
			evidence["journal_state"] = status.State
		default:
			return
		}
		if reason := collector.SafeJournalReason(status.Reason); reason != "" {
			evidence["component_reason"] = reason
		}
		diagnostic := status.SafeDiagnostic()
		if diagnostic.Cause == "" {
			return
		}
		evidence["failure_cause"] = diagnostic.Cause
		evidence["diagnostic_scope"] = diagnostic.Scope
		if !diagnostic.At.IsZero() && !diagnostic.At.After(event.ObservedAt) {
			evidence["diagnostic_at_utc"] = diagnostic.At.UTC().Format(time.RFC3339Nano)
		}
		if diagnostic.ExitCode != nil {
			evidence["journal_exit_code"] = strconv.Itoa(*diagnostic.ExitCode)
		}
		if diagnostic.Signal != "" {
			evidence["journal_signal"] = diagnostic.Signal
		}
	case "health_geoip_update":
		if d.geo == nil || d.geo.Validate() != nil {
			return
		}
		health := *d.geo
		evidence["component_reason"] = health.Result
		if !health.CheckedAt.IsZero() && !health.CheckedAt.After(event.ObservedAt) {
			evidence["diagnostic_at_utc"] = health.CheckedAt.UTC().Format(time.RFC3339Nano)
		}
		if diagnostic := health.Diagnostic; diagnostic != nil {
			evidence["diagnostic_scope"] = "current"
			evidence["failure_stage"] = diagnostic.Stage
			evidence["failure_cause"] = diagnostic.Reason
			if diagnostic.Edition != "" {
				evidence["geoip_edition"] = diagnostic.Edition
			}
			if diagnostic.HTTPStatus != 0 {
				evidence["http_status"] = strconv.Itoa(diagnostic.HTTPStatus)
			}
		}
	}
}
