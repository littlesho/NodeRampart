// SPDX-License-Identifier: MIT

package daemon

import "time"

// Diagnosis is a bounded explanation of current observations. It never applies
// a repair. Unknown required observations take precedence over a healthy claim.
type Diagnosis struct {
	Version int               `json:"schema_version"`
	State   string            `json:"state"`
	Code    int               `json:"strict_exit_code"`
	Checks  []DiagnosticCheck `json:"checks"`
}

type DiagnosticCheck struct {
	Reason   string `json:"reason_code"`
	State    string `json:"state"`
	Impact   string `json:"impact"`
	NextStep string `json:"next_step"`
}

func Diagnose(status Status, foreignKeysRequired bool) Diagnosis {
	return DiagnoseAt(status, foreignKeysRequired, time.Now().UTC())
}

func DiagnoseAt(status Status, foreignKeysRequired bool, now time.Time) Diagnosis {
	d := Diagnosis{Version: 1, State: "healthy", Checks: []DiagnosticCheck{}}
	add := func(code, state, impact, next string) {
		d.Checks = append(d.Checks, DiagnosticCheck{code, state, impact, next})
		if state == "unknown" {
			d.State, d.Code = "unknown", 2
		} else if state == "degraded" && d.Code != 2 {
			d.State, d.Code = "degraded", 1
		}
	}
	if !status.Readiness.ConfigurationLoaded || len(status.Readiness.ConfigFingerprint) != 64 {
		add("configuration_unknown", "unknown", "Loaded configuration could not be confirmed.", "Validate the configuration and compare CLI/daemon versions.")
	}
	for _, check := range []struct {
		code, impact, next string
		ready, enabled     bool
	}{
		{"storage_unready", "Durable writes are unavailable or their status is unknown.", "Inspect storage budget, free space and retained failure operations.", status.Readiness.StorageReady, true},
		{"interface_unready", "Interface totals do not have a fresh committed baseline.", "Check interface selection and permissions; wait for a complete sample.", status.Readiness.InterfaceReady, true},
		{"sensor_unready", "Packet observations are not ready for every selected interface.", "Inspect sensor service, peer identity, interval and loss diagnostics.", status.Readiness.SensorReady, status.SensorEnabled},
		{"journal_unready", "SSH journal coverage has not been confirmed.", "Inspect journal permissions and durable recovery reason; quiet logs do not prove recovery.", status.Readiness.JournalReady, status.AuthEnabled},
		{"auth_history_incomplete", "Authentication detection has incomplete bounded history.", "Inspect capacity refusals and wait for the affected detection window to expire.", status.Readiness.AuthDetectionReady, status.AuthEnabled},
	} {
		if !check.enabled {
			add(check.code, "not_applicable", "This component is disabled.", "No action required.")
		} else if !check.ready {
			add(check.code, "degraded", check.impact, check.next)
		}
	}
	for _, feature := range []struct{ name, code string }{
		{"geoip", "geoip_resource_unavailable"}, {"billing", "billing_resource_unavailable"},
		{"telegram", "telegram_credentials_unavailable"}, {"webhook", "webhook_credentials_unavailable"}, {"heartbeat", "heartbeat_credentials_unavailable"},
	} {
		if _, failed := status.OptionalFailures[feature.name]; failed {
			add(feature.code, "degraded", "An enabled optional feature is unavailable; base monitoring continues.", "Correct its protected local resource, validate configuration, then restart.")
		}
	}
	for _, component := range status.Components {
		if component.State != "degraded" && component.State != "failed" {
			continue
		}
		switch component.Name {
		case "notification_worker", "webhook_worker", "heartbeat", "report_scheduler", "geoip", "billing":
			add("optional_component_degraded", "degraded", "An optional component reports degraded operation.", "Inspect the component state and its fixed local failure reason; retry or restart after correction.")
		}
	}
	if status.SensorEnabled && (!status.Detection.ScanCoverageComplete || !status.Detection.UDPScanCoverageComplete) && status.Batches > 0 {
		add("network_detection_incomplete", "degraded", "Network detection coverage is incomplete; missing evidence is not zero.", "Inspect loss and state-capacity counters; preserve the reported coverage gap.")
	}
	if status.AuthEnabled && !status.AuthWindowReadyAfter.IsZero() && now.Before(status.AuthWindowReadyAfter) {
		add("auth_window_warmup", "unknown", "The authentication detection window still includes time before this process started.", "Allow the configured authentication window to elapse; retain restart and coverage-gap evidence.")
	}
	if status.SensorEnabled && status.SensorCommitUnknown {
		add("sensor_commit_unknown", "unknown", "Durable sensor commit watermarks could not be read.", "Inspect storage and sensor protocol versions; do not infer durability from socket receipt.")
	}
	if status.SensorEnabled && !status.SensorCommitUnknown && status.Batches > 0 {
		latest := map[string]int{}
		for i, watermark := range status.SensorCommits {
			previous, ok := latest[watermark.Interface]
			if !ok || watermark.SentAt.After(status.SensorCommits[previous].SentAt) {
				latest[watermark.Interface] = i
			}
		}
		partial, unknown := false, len(latest) == 0
		for _, i := range latest {
			partial = partial || !status.SensorCommits[i].Complete
		}
		for name, received := range status.SensorInterfaces {
			i, ok := latest[name]
			if !ok || received.After(status.SensorCommits[i].SentAt) {
				unknown = true
			}
		}
		if unknown {
			add("sensor_commit_unavailable", "unknown", "Current receipt has no confirmed v5 durable commit watermark.", "Check legacy protocol or pending storage work; upgrade daemon before sensor.")
		}
		if partial {
			add("sensor_commit_partial", "degraded", "The latest interface commit has incomplete event or notification admission.", "Inspect the retained commit reason and pending or rejected derived work.")
		}
	}
	if status.EventIngest.Pending > 0 || status.EventIngest.UnrecordedDropped > 0 {
		add("event_persistence_pending", "degraded", "Some derived events are awaiting persistence or a loss marker.", "Inspect storage and pending-event age; socket receipt is not durability.")
	}
	if status.Queue == nil {
		add("notification_status_unknown", "unknown", "Notification queue state could not be read.", "Inspect storage diagnostics and retry status.")
	} else if status.Queue.Rejected > 0 || status.Queue.Quarantined > 0 || status.Queue.Isolated > 0 {
		add("notification_attention", "degraded", "Retained notifications include rejected, quarantined or isolated work.", "Inspect notify status/list and explicitly handle isolated messages.")
	}
	if foreignKeysRequired {
		if status.ForeignKeys == nil {
			add("foreign_keys_unknown", "unknown", "Historical relationships could not be checked within the diagnostic deadline.", "Retry doctor; do not automatically delete historical rows.")
		} else if len(status.ForeignKeys.Violations) > 0 || status.ForeignKeys.Truncated {
			add("foreign_keys_violation", "degraded", "Retained history contains broken relationships.", "Preserve a private forensic copy and follow the isolated manual repair procedure.")
		}
	}
	if status.GeneratedAt.IsZero() || now.Sub(status.GeneratedAt) > time.Minute || status.GeneratedAt.After(now.Add(time.Second)) {
		add("status_stale", "unknown", "This status is too old to establish current health.", "Request a fresh authenticated status.")
	}
	return d
}
