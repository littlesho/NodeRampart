// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"time"

	"github.com/littlesho/NodeRampart/internal/api"
)

// EvidenceSnapshot is raw internal input to the evidence allowlist projection.
// It is deliberately not the public IPC/export DTO: identifiers and arbitrary
// stored strings must not be serialized directly.
type EvidenceSnapshot struct {
	Start, End, AsOf time.Time
	Incident         *IncidentView
	Timeline         TimelinePage
	Integrity        IntegrityView
	Retention        RetentionView
	Monitors         []MonitorState
}

func (s *Store) Evidence(ctx context.Context, args api.EvidenceArgs) (EvidenceSnapshot, error) {
	result := EvidenceSnapshot{Start: args.Start.UTC(), End: args.End.UTC()}
	if err := args.Validate(); err != nil {
		return result, err
	}
	tx, asOf, err := s.beginReadSnapshot(ctx)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	result.AsOf = asOf
	query := TimelineQuery{Start: args.Start, End: args.End, IncidentID: args.IncidentID, Limit: 100}
	if args.IncidentID != "" {
		value, err := readIncident(ctx, tx, query, result.AsOf)
		if err != nil {
			return result, err
		}
		result.Incident = &value
		result.Timeline = value.Timeline
	} else {
		result.Timeline, err = readTimeline(ctx, tx, query, result.AsOf)
		if err != nil {
			return result, err
		}
	}
	result.Integrity, err = readIntegrityCore(ctx, tx, IntegrityQuery{Start: args.Start, End: args.End, Limit: 100}, result.AsOf)
	if err != nil {
		return result, err
	}
	result.Retention, err = readRetention(ctx, tx, RetentionQuery{Start: args.Start, End: args.End, Limit: 100}, result.AsOf)
	if err != nil {
		return result, err
	}
	// Reuse the same inventory scan while preserving Integrity's 20-entry
	// summary shape; the evidence-level ledger retains up to 100 entries.
	summary := result.Retention
	if len(summary.Entries) > 20 {
		summary.Entries = summary.Entries[:20]
		summary.More = true
		summary.NextBeforeID = summary.Entries[19].ID
	}
	result.Integrity.Retention = &summary
	result.Monitors, err = readMonitorStates(ctx, tx)
	if err != nil {
		return result, err
	}
	if err := tx.Commit(); err != nil {
		return result, err
	}
	return result, nil
}
