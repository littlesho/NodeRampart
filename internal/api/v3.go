// SPDX-License-Identifier: MIT

package api

import "time"

type BackfillArgs struct {
	From    string `json:"from"`
	Through string `json:"through"`
}

type TimelineArgs struct {
	Start      time.Time `json:"start_utc"`
	End        time.Time `json:"end_utc"`
	AfterTime  time.Time `json:"after_utc,omitzero"`
	AfterID    string    `json:"after_id,omitempty"`
	IncidentID string    `json:"incident_id,omitempty"`
	Kind       string    `json:"kind,omitempty"`
	Limit      int       `json:"limit"`
}

type IncidentListArgs struct {
	Start      time.Time `json:"start_utc"`
	End        time.Time `json:"end_utc"`
	BeforeTime time.Time `json:"before_utc,omitzero"`
	BeforeID   string    `json:"before_id,omitempty"`
	Limit      int       `json:"limit"`
}

type SilenceArgs struct {
	IncidentID string    `json:"incident_id,omitempty"`
	Kind       string    `json:"kind,omitempty"`
	ExpiresAt  time.Time `json:"expires_at_utc"`
	Reason     string    `json:"reason,omitempty"`
}

type HealthArgs struct {
	Start  time.Time `json:"start_utc"`
	End    time.Time `json:"end_utc"`
	Limit  int       `json:"limit"`
	Offset int       `json:"offset"`
}

func ValidTimeRange(start, end time.Time, maxDays int) bool {
	return !start.IsZero() && start.Year() >= 1970 && end.Year() <= 9999 && start.Before(end) && end.Sub(start) <= time.Duration(maxDays)*24*time.Hour
}
