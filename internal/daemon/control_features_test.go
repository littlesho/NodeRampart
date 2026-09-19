// SPDX-License-Identifier: MIT

package daemon

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/api"
	"github.com/littlesho/NodeRampart/internal/evidence"
	"github.com/littlesho/NodeRampart/internal/store"
)

func TestFeatureControlContracts(t *testing.T) {
	app := controlTestApp(t)
	now := time.Now().UTC()
	for _, tc := range []struct {
		command string
		args    any
	}{
		{"alerts_status", struct{}{}},
		{"retention", store.RetentionQuery{Start: now.Add(-time.Hour), End: now, Limit: 20}},
		{"evidence_snapshot", api.EvidenceArgs{Start: now.Add(-time.Hour), End: now}},
	} {
		r := controlRequest(t, app, tc.command, tc.args)
		if !r.OK {
			t.Fatal(tc.command, r.Error)
		}
		if tc.command == "evidence_snapshot" {
			data, err := json.Marshal(r.Data)
			var bundle evidence.Bundle
			if err != nil || json.Unmarshal(data, &bundle) != nil || bundle.Validate() != nil {
				t.Fatal("invalid sharing contract", err)
			}
		}
		r = controlRequest(t, app, tc.command, map[string]string{"synthetic_secret_key": "synthetic_secret_value"})
		if r.OK || strings.Contains(r.Error, "synthetic_secret") {
			t.Fatal("unknown argument was accepted or echoed", tc.command)
		}
	}
}
