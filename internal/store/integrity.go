// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/littlesho/NodeRampart/internal/api"
)

const maxIntegrityComponents = 128
const maxIntegritySegments = 2*maxCoverageIntervals + 2*maxIntegrityComponents

type IntegrityQuery struct {
	Start  time.Time `json:"start_utc"`
	End    time.Time `json:"end_utc"`
	Limit  int       `json:"limit"`
	Offset int       `json:"offset"`
}

func (q IntegrityQuery) Validate() error {
	if !api.ValidTimeRange(q.Start, q.End, 400) || !api.ValidLimit(q.Limit) || q.Offset < 0 || q.Offset > maxIntegritySegments {
		return errors.New("health range must be positive and at most 400 days, limit 1..100, with a bounded offset")
	}
	return nil
}

type IntegritySegment struct {
	Name                string    `json:"name"`
	State               string    `json:"state"`
	Start               time.Time `json:"start_utc"`
	End                 time.Time `json:"end_utc"`
	ConflictingEvidence bool      `json:"conflicting_evidence"`
}

type IntegrityComponent struct {
	Name       string `json:"name"`
	RunningMS  int64  `json:"running_milliseconds"`
	DegradedMS int64  `json:"degraded_milliseconds"`
	DisabledMS int64  `json:"disabled_milliseconds"`
	UnknownMS  int64  `json:"unknown_milliseconds"`
	ConflictMS int64  `json:"conflicting_milliseconds"`
}

type IntegrityLoss struct {
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

type IntegrityView struct {
	Retention          *RetentionView       `json:"retention,omitempty"`
	Start              time.Time            `json:"start_utc"`
	End                time.Time            `json:"end_utc"`
	AsOf               time.Time            `json:"as_of_utc"`
	Components         []IntegrityComponent `json:"components"`
	Segments           []IntegritySegment   `json:"segments"`
	More               bool                 `json:"more"`
	NextOffset         int                  `json:"next_offset,omitempty"`
	HistoryTruncated   bool                 `json:"history_truncated"`
	Gaps               []CoverageGap        `json:"gaps"`
	GapsTruncated      bool                 `json:"gaps_truncated"`
	LossHours          []IntegrityLoss      `json:"loss_hours"`
	LossHoursTruncated bool                 `json:"loss_hours_truncated"`
	Notes              []string             `json:"notes"`
}

type coverageEdge struct {
	at    int64
	state string
	delta int
}

// Integrity reads historical evidence in one SQLite snapshot. Component running
// time is not presented as a proof of lossless capture or durable ingestion.
func (s *Store) Integrity(ctx context.Context, q IntegrityQuery) (IntegrityView, error) {
	if err := q.Validate(); err != nil {
		return IntegrityView{}, err
	}
	tx, asOf, err := s.beginReadSnapshot(ctx)
	if err != nil {
		return IntegrityView{}, err
	}
	defer tx.Rollback()
	view, err := readIntegrity(ctx, tx, q, asOf)
	if err != nil {
		return view, err
	}
	return view, tx.Commit()
}

func readIntegrity(ctx context.Context, db timelineReader, q IntegrityQuery, asOf time.Time) (IntegrityView, error) {
	view, err := readIntegrityCore(ctx, db, q, asOf)
	if err != nil {
		return view, err
	}
	retention, err := readRetention(ctx, db, RetentionQuery{Start: q.Start, End: q.End, Limit: 20}, asOf)
	if err != nil {
		return view, err
	}
	view.Retention = &retention
	return view, nil
}

// readIntegrityCore reads coverage without unrelated retention inventories.
func readIntegrityCore(ctx context.Context, db timelineReader, q IntegrityQuery, asOf time.Time) (IntegrityView, error) {
	view := IntegrityView{Start: q.Start.UTC(), End: q.End.UTC(), AsOf: asOf.UTC(), Components: []IntegrityComponent{}, Segments: []IntegritySegment{}, Gaps: []CoverageGap{}, LossHours: []IntegrityLoss{}, Notes: []string{
		"Running intervals describe component heartbeats, not a guarantee of complete data or successful event persistence.",
		"Unrecorded and expired intervals are unknown. Gap history is bounded; absence of a retained gap does not prove lossless coverage.",
		"Loss counters use overlapping UTC hours; gap counts cover their original intervals and are not prorated. A gap with count zero means unknown loss, including restart and interface-selection gaps.",
		"Pagination uses a fixed requested period but a new evidence snapshot per request; recent heartbeat tails can change.",
	}}
	if err := q.Validate(); err != nil {
		return view, err
	}
	edges := map[string][]coverageEdge{"sensor_feed": {}, "ssh_journal": {}, "interface_counter": {}}
	rows, err := db.QueryContext(ctx, `SELECT i.name,i.state,i.started_at,COALESCE(i.ended_at,c.updated_at,i.started_at)
 FROM coverage_intervals i LEFT JOIN component_status c ON c.name=i.name
 WHERE i.started_at<? AND COALESCE(i.ended_at,c.updated_at,i.started_at)>? ORDER BY i.id LIMIT ?`, q.End.UnixMilli(), q.Start.UnixMilli(), maxCoverageIntervals+1)
	if err != nil {
		return view, err
	}
	seen := 0
	for rows.Next() {
		var name, state string
		var start, end int64
		if err := rows.Scan(&name, &state, &start, &end); err != nil {
			rows.Close()
			return view, err
		}
		seen++
		if seen > maxCoverageIntervals {
			view.HistoryTruncated = true
			break
		}
		if len(name) > 64 || name == "" {
			view.HistoryTruncated = true
			continue
		}
		if _, ok := edges[name]; !ok && len(edges) >= maxIntegrityComponents {
			view.HistoryTruncated = true
			continue
		}
		start = max(start, q.Start.UnixMilli())
		end = min(end, q.End.UnixMilli(), view.AsOf.UnixMilli())
		if start >= end {
			continue
		}
		if state != "running" && state != "degraded" && state != "disabled" && state != "unknown" {
			state = "unknown"
			view.HistoryTruncated = true
		}
		edges[name] = append(edges[name], coverageEdge{start, state, 1}, coverageEdge{end, state, -1})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return view, err
	}
	if err := rows.Close(); err != nil {
		return view, err
	}
	names := make([]string, 0, len(edges))
	for name := range edges {
		names = append(names, name)
	}
	sort.Strings(names)
	all := make([]IntegritySegment, 0, 2*seen+len(names))
	for _, name := range names {
		segments, totals := integritySegments(name, edges[name], q.Start.UnixMilli(), q.End.UnixMilli())
		all = append(all, segments...)
		view.Components = append(view.Components, totals)
	}
	sort.Slice(all, func(i, j int) bool {
		if !all[i].Start.Equal(all[j].Start) {
			return all[i].Start.Before(all[j].Start)
		}
		if all[i].Name != all[j].Name {
			return all[i].Name < all[j].Name
		}
		return all[i].End.Before(all[j].End)
	})
	if q.Offset < len(all) {
		end := min(len(all), q.Offset+q.Limit)
		view.Segments = all[q.Offset:end]
		view.More = end < len(all)
		if view.More {
			view.NextOffset = end
		}
	}
	rows, err = db.QueryContext(ctx, `SELECT name,reason,started_at,ended_at,count FROM coverage_gaps WHERE started_at<?
 AND (ended_at>? OR (started_at=ended_at AND started_at>=?)) ORDER BY started_at,id LIMIT 101`, q.End.UnixMilli(), q.Start.UnixMilli(), q.Start.UnixMilli())
	if err != nil {
		return view, err
	}
	for rows.Next() {
		var gap CoverageGap
		var start, end int64
		if err := rows.Scan(&gap.Name, &gap.Reason, &start, &end, &gap.Count); err != nil {
			rows.Close()
			return view, err
		}
		if len(view.Gaps) == 100 {
			view.GapsTruncated = true
			break
		}
		gap.Start, gap.End = time.UnixMilli(start).UTC(), time.UnixMilli(end).UTC()
		view.Gaps = append(view.Gaps, gap)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return view, err
	}
	if err := rows.Close(); err != nil {
		return view, err
	}
	endHour := q.End.UTC().Truncate(time.Hour)
	if endHour.Before(q.End) {
		endHour = endHour.Add(time.Hour)
	}
	rows, err = db.QueryContext(ctx, `SELECT hour_utc,overflow_packets,overflow_bytes,parse_errors,kernel_drops,kernel_stats_errors,ipc_dropped_batches,ipc_dropped_packets,ipc_dropped_bytes,health_counter_saturations
 FROM collector_health_hourly WHERE hour_utc>=? AND hour_utc<? AND (overflow_packets>0 OR overflow_bytes>0 OR parse_errors>0 OR kernel_drops>0 OR kernel_stats_errors>0 OR ipc_dropped_batches>0 OR ipc_dropped_packets>0 OR ipc_dropped_bytes>0 OR health_counter_saturations>0) ORDER BY hour_utc LIMIT 101`, q.Start.UTC().Truncate(time.Hour).Unix(), endHour.Unix())
	if err != nil {
		return view, err
	}
	for rows.Next() {
		var loss IntegrityLoss
		var hour int64
		if err := rows.Scan(&hour, &loss.OverflowPackets, &loss.OverflowBytes, &loss.ParseErrors, &loss.KernelDrops, &loss.KernelStatsErrors, &loss.IPCDroppedBatches, &loss.IPCDroppedPackets, &loss.IPCDroppedBytes, &loss.Saturations); err != nil {
			rows.Close()
			return view, err
		}
		if len(view.LossHours) == 100 {
			view.LossHoursTruncated = true
			break
		}
		loss.Hour = time.Unix(hour, 0).UTC()
		view.LossHours = append(view.LossHours, loss)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return view, err
	}
	if err := rows.Close(); err != nil {
		return view, err
	}
	return view, nil
}

func integritySegments(name string, edges []coverageEdge, start, end int64) ([]IntegritySegment, IntegrityComponent) {
	totals := IntegrityComponent{Name: name}
	sort.Slice(edges, func(i, j int) bool { return edges[i].at < edges[j].at })
	active := map[string]int{}
	segments := []IntegritySegment{}
	appendSegment := func(from, to int64) {
		if to <= from {
			return
		}
		state, distinct := "unknown", 0
		for key, count := range active {
			if count > 0 {
				state = key
				distinct++
			}
		}
		conflict := distinct > 1
		if conflict {
			state = "unknown"
			totals.ConflictMS += to - from
		}
		switch state {
		case "running":
			totals.RunningMS += to - from
		case "degraded":
			totals.DegradedMS += to - from
		case "disabled":
			totals.DisabledMS += to - from
		default:
			totals.UnknownMS += to - from
		}
		if len(segments) > 0 {
			last := &segments[len(segments)-1]
			if last.State == state && last.ConflictingEvidence == conflict && last.End.UnixMilli() == from {
				last.End = time.UnixMilli(to).UTC()
				return
			}
		}
		segments = append(segments, IntegritySegment{Name: name, State: state, Start: time.UnixMilli(from).UTC(), End: time.UnixMilli(to).UTC(), ConflictingEvidence: conflict})
	}
	previous := start
	for i := 0; i < len(edges); {
		at := edges[i].at
		appendSegment(previous, at)
		for i < len(edges) && edges[i].at == at {
			active[edges[i].state] += edges[i].delta
			i++
		}
		previous = at
	}
	appendSegment(previous, end)
	return segments, totals
}
