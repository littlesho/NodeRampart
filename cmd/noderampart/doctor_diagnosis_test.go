// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
)

func TestDoctorStrictUnknownKeepsJSONAndDefaultExitContract(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.json")
	var output bytes.Buffer
	if err := doctorCommand([]string{"--config", path}, &output); err != nil {
		t.Fatal("existing informational contract changed", err)
	}
	output.Reset()
	err := doctorCommand([]string{"--strict", "--config", path}, &output)
	var exit *diagnosticExit
	if !errors.As(err, &exit) || exit.code != 2 {
		t.Fatal("strict config/diagnosis failure did not return 2", err)
	}
	var result doctorResult
	if json.Unmarshal(output.Bytes(), &result) != nil || result.Overall != "unknown" || result.StrictExitCode != 2 {
		t.Fatal("stdout ceased to be machine readable", output.String())
	}
}

func TestDoctorConfirmedDegradationAndNotApplicable(t *testing.T) {
	result := doctorResult{Checks: []doctorCheck{{Name: "sensor", State: "not_applicable"}, {Name: "storage", State: "degraded", ReasonCode: "storage_unready"}}}
	finalizeDoctor(&result)
	if result.StrictExitCode != 1 || result.Overall != "degraded" {
		t.Fatal(result)
	}
	result.Checks = result.Checks[:1]
	finalizeDoctor(&result)
	if result.StrictExitCode != 0 {
		t.Fatal("not applicable failed strict diagnostics", result)
	}
}
