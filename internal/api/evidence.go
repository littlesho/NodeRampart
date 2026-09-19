// SPDX-License-Identifier: MIT

package api

import (
	"errors"
	"time"
)

// EvidenceArgs fixes one bounded read snapshot. Evidence exports do not page.
type EvidenceArgs struct {
	Start      time.Time `json:"start_utc"`
	End        time.Time `json:"end_utc"`
	IncidentID string    `json:"incident_id,omitempty"`
}

func (a EvidenceArgs) Validate() error {
	if !ValidTimeRange(a.Start, a.End, 8) || a.IncidentID != "" && !ValidID(a.IncidentID) {
		return errors.New("evidence requires a positive range up to eight days and a valid optional incident ID")
	}
	return nil
}
