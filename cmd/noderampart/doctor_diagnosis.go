// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"strings"

	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/daemon"
)

func appendRuntimeDiagnosis(result *doctorResult, data []byte, cfg config.Config) {
	var status daemon.Status
	if json.Unmarshal(data, &status) != nil || status.Diagnosis == nil || status.Diagnosis.Version != 1 {
		result.Checks = append(result.Checks, doctorCheck{Name: "runtime", State: "unknown", ReasonCode: "diagnostic_contract_unknown", Impact: "Daemon diagnostic fields are missing or incompatible.", NextStep: "Install matching CLI/daemon binaries and retry."})
		return
	}
	// Re-evaluate known observations rather than trusting an overall boolean.
	diagnosis := daemon.Diagnose(status, true)
	for _, check := range diagnosis.Checks {
		result.Checks = append(result.Checks, doctorCheck{Name: "runtime", State: check.State, ReasonCode: check.Reason, Impact: check.Impact, NextStep: check.NextStep})
	}
	if status.Readiness.ConfigFingerprint != config.Fingerprint(cfg) {
		result.Checks = append(result.Checks, doctorCheck{Name: "runtime", State: "degraded", ReasonCode: "configuration_mismatch", Impact: "The daemon is running a different effective configuration.", NextStep: "Use validated management apply/recovery, then confirm readiness."})
	}
}

func finalizeDoctor(result *doctorResult) {
	result.Overall, result.StrictExitCode = "healthy", 0
	for i := range result.Checks {
		check := &result.Checks[i]
		if check.ReasonCode == "" {
			name := check.Name
			if strings.HasSuffix(name, ".service") {
				name = "system_unit"
			}
			check.ReasonCode = name + "_" + check.State
			check.Impact = check.Detail
			check.NextStep = "Inspect this local prerequisite and request fresh daemon diagnostics."
			if check.State == "not_applicable" || check.State == "valid" || check.State == "present" || check.State == "reachable" {
				check.NextStep = "No action required for this local check."
			}
		}
		switch check.State {
		case "unknown", "unavailable":
			result.Overall, result.StrictExitCode = "unknown", 2
		case "degraded", "unsafe", "conflict", "mismatch", "missing", "override":
			if result.StrictExitCode != 2 {
				result.Overall, result.StrictExitCode = "degraded", 1
			}
		}
	}
}
