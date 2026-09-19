// SPDX-License-Identifier: MIT

// Package evidence projects retained observations onto a sharing-oriented
// allowlist, then writes bounded, offline artifacts. It never sends data.
package evidence

import (
	"time"

	"github.com/littlesho/NodeRampart/internal/model"
)

const (
	FormatVersion         = 1
	MaxJSONBytes          = 1 << 20
	MaxHTMLBytes          = 4 << 20
	MaxOutputBytes        = 8 << 20
	MaxRecords            = 100
	PrivacyNotice         = "Identifiers are pseudonymized with a fresh secret for this export. UTC timing, counts, behavior and rule fingerprints remain potentially linkable. Review before sharing."
	SnapshotNotice        = "Retained database evidence was read in one SQLite snapshot. Monitor states are the latest persisted states and may concern a different period. No live process facts, raw configuration, logs or filesystem state are included. Retention and collection gaps can omit evidence."
	CorrelationNotice     = "Stored source/time context only; shared prefixes or pseudonyms do not prove a common host, actor or causal relationship."
	RuleFingerprintNotice = "The rule fingerprint describes selected identity-free rules loaded at export time, not the rules used by every historical event. It excludes interface selection, paths, accounts and tariffs. Historical rule versions are unavailable."
)

// Bundle is an independent versioned DTO. It must not acquire raw configuration,
// status, event, report, notification body or arbitrary text/map fields.
type Bundle struct {
	FormatVersion        int       `json:"format_version"`
	Kind                 string    `json:"kind"`
	Start                time.Time `json:"start_utc"`
	End                  time.Time `json:"end_utc"`
	SnapshotAt           time.Time `json:"snapshot_at_utc"`
	Privacy              string    `json:"privacy"`
	Consistency          string    `json:"consistency"`
	Producer             Producer  `json:"producer"`
	RuleFingerprint      string    `json:"rule_fingerprint,omitempty"`
	RuleFingerprintScope string    `json:"rule_fingerprint_scope"`
	Incident             *Incident `json:"incident,omitempty"`
	Events               []Event   `json:"events"`
	EventsTruncated      bool      `json:"events_truncated"`
	RelatedSSH           []Event   `json:"related_ssh"`
	RelatedTruncated     bool      `json:"related_truncated"`
	Coverage             Coverage  `json:"coverage"`
	Retention            Retention `json:"retention"`
	Monitors             []Monitor `json:"monitors"`
}

type Producer struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
}

type Incident struct {
	Alias            string    `json:"alias"`
	Kind             string    `json:"kind"`
	FirstObservedAt  time.Time `json:"first_observed_at_utc"`
	LastObservedAt   time.Time `json:"last_observed_at_utc"`
	RetainedEvents   int64     `json:"retained_events"`
	HasStart         bool      `json:"has_retained_start"`
	HasRecovery      bool      `json:"has_retained_recovery"`
	ContextStart     time.Time `json:"context_start_utc"`
	ContextEnd       time.Time `json:"context_end_utc"`
	SourcesTruncated bool      `json:"context_sources_truncated"`
	Correlation      string    `json:"correlation"`
}

type Event struct {
	Alert         *model.AlertContext `json:"alert,omitempty"`
	Alias         string              `json:"alias"`
	IncidentAlias string              `json:"incident_alias,omitempty"`
	SourceAlias   string              `json:"source_alias,omitempty"`
	ObservedAt    time.Time           `json:"observed_at_utc"`
	Kind          string              `json:"kind"`
	Phase         string              `json:"phase"`
	Severity      string              `json:"severity"`
	Count         uint64              `json:"count"`
	Delivery      Delivery            `json:"delivery"`
}

type Delivery struct {
	Decision          string    `json:"decision"`
	NotificationAlias string    `json:"notification_alias,omitempty"`
	State             string    `json:"state"`
	Attempts          int       `json:"attempts"`
	MergedEvents      int64     `json:"merged_events"`
	SilenceAlias      string    `json:"silence_alias,omitempty"`
	SentAt            time.Time `json:"sent_at_utc,omitzero"`
}

type Coverage struct {
	Components         []Component `json:"components"`
	Segments           []Segment   `json:"segments"`
	SegmentsTruncated  bool        `json:"segments_truncated"`
	HistoryTruncated   bool        `json:"history_truncated"`
	Gaps               []Gap       `json:"gaps"`
	GapsTruncated      bool        `json:"gaps_truncated"`
	LossHours          []Loss      `json:"loss_hours"`
	LossHoursTruncated bool        `json:"loss_hours_truncated"`
	Qualification      string      `json:"qualification"`
}

type Component struct {
	Name       string `json:"name"`
	RunningMS  int64  `json:"running_milliseconds"`
	DegradedMS int64  `json:"degraded_milliseconds"`
	DisabledMS int64  `json:"disabled_milliseconds"`
	UnknownMS  int64  `json:"unknown_milliseconds"`
	ConflictMS int64  `json:"conflicting_milliseconds"`
}

type Segment struct {
	Name                string    `json:"name"`
	State               string    `json:"state"`
	Start               time.Time `json:"start_utc"`
	End                 time.Time `json:"end_utc"`
	ConflictingEvidence bool      `json:"conflicting_evidence"`
}

type Gap struct {
	Name   string    `json:"name"`
	Reason string    `json:"reason"`
	Start  time.Time `json:"start_utc"`
	End    time.Time `json:"end_utc"`
	Count  uint64    `json:"count"`
}

type Loss struct {
	Hour              time.Time `json:"hour_utc"`
	OverflowPackets   uint64    `json:"overflow_packets"`
	OverflowBytes     uint64    `json:"overflow_bytes"`
	ParseErrors       uint64    `json:"parse_errors"`
	KernelDrops       uint64    `json:"kernel_drops"`
	KernelStatsErrors uint64    `json:"kernel_stats_errors"`
	IPCDroppedBatches uint64    `json:"ipc_dropped_batches"`
	IPCDroppedPackets uint64    `json:"ipc_dropped_packets"`
	IPCDroppedBytes   uint64    `json:"ipc_dropped_bytes"`
	Saturations       uint64    `json:"counter_saturations"`
}

type Retention struct {
	TrackingStarted time.Time         `json:"tracking_started_utc,omitzero"`
	Entries         []RetentionEntry  `json:"entries"`
	Totals          []RetentionTotal  `json:"lifetime_totals"`
	Retained        []RetainedDataset `json:"retained_datasets"`
	Truncated       bool              `json:"entries_truncated"`
	EvictedEntries  int64             `json:"evicted_entries"`
	DetailLimit     int               `json:"detail_limit"`
	Qualification   string            `json:"qualification"`
}

type RetentionTotal struct {
	Dataset           string    `json:"dataset"`
	Reason            string    `json:"reason"`
	FirstAction       time.Time `json:"first_action_utc,omitzero"`
	LastAction        time.Time `json:"last_action_utc,omitzero"`
	DataStart         time.Time `json:"data_start_utc,omitzero"`
	DataEnd           time.Time `json:"data_end_utc,omitzero"`
	Operations        int64     `json:"operations"`
	AffectedRows      int64     `json:"affected_rows"`
	AggregateSurvives string    `json:"aggregate_survives"`
}

type RetentionEntry struct {
	RetentionTotal
	ActionDay time.Time `json:"action_day_utc"`
}

type RetainedDataset struct {
	Dataset string    `json:"dataset"`
	Rows    int64     `json:"rows"`
	Start   time.Time `json:"start_utc,omitzero"`
	End     time.Time `json:"end_utc,omitzero"`
}

type Monitor struct {
	Key           string    `json:"key"`
	Revision      int64     `json:"revision"`
	UpdatedAt     time.Time `json:"updated_at_utc"`
	IncidentAlias string    `json:"incident_alias,omitempty"`
	// Monitor state content is projected explicitly; no raw persisted JSON.
	State            string    `json:"state"`
	Period           string    `json:"period,omitempty"`
	Milestone        int       `json:"milestone"`
	Active           bool      `json:"active"`
	ObservedAt       time.Time `json:"observed_at_utc,omitzero"`
	ConditionSince   time.Time `json:"condition_since_utc,omitzero"`
	RecoverySince    time.Time `json:"recovery_since_utc,omitzero"`
	LastNotification time.Time `json:"last_notification_utc,omitzero"`
}
