// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/protocol"
)

func TestIntegrityDistinguishesRunningPersistenceGapsAndUnknownTime(t *testing.T) {
	s := budgetStore(t)
	ctx := context.Background()
	base := time.Now().UTC().Add(-time.Hour).Truncate(time.Millisecond)
	for _, transition := range []struct {
		name, state string
		seconds     int
	}{
		{"ssh_journal", "disabled", 5}, {"sensor_feed", "running", 10}, {"sensor_feed", "degraded", 20}, {"sensor_feed", "running", 30},
	} {
		if err := s.SetComponentStatus(ctx, transition.name, transition.state, base.Add(time.Duration(transition.seconds)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.HeartbeatCoverage(ctx, base.Add(40*time.Second)); err != nil {
		t.Fatal(err)
	}
	for _, gap := range []CoverageGap{
		{Name: "network_events", Reason: "process_restart_pending_unknown", Start: base, End: base, Count: 0},
		{Name: "sensor_feed", Reason: "synthetic_loss", Start: base.Add(12 * time.Second), End: base.Add(15 * time.Second), Count: 7},
		{Name: "sensor_feed", Reason: "outside_end", Start: base.Add(time.Minute), End: base.Add(time.Minute)},
		{Name: "sensor_feed", Reason: "outside_start", Start: base.Add(-time.Second), End: base},
	} {
		if err := s.RecordCoverageGap(ctx, gap); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RecordBatchHealth(ctx, protocol.Batch{SentAt: base, KernelDrops: 2}); err != nil {
		t.Fatal(err)
	}
	q := IntegrityQuery{Start: base, End: base.Add(time.Minute), Limit: 2}
	view, err := s.Integrity(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	components := map[string]IntegrityComponent{}
	for _, c := range view.Components {
		components[c.Name] = c
		if c.RunningMS+c.DegradedMS+c.DisabledMS+c.UnknownMS != 60_000 {
			t.Fatal("coverage durations do not conserve the requested period", c)
		}
	}
	sensor := components["sensor_feed"]
	if sensor.RunningMS != 20_000 || sensor.DegradedMS != 10_000 || sensor.UnknownMS != 30_000 || components["ssh_journal"].DisabledMS != 35_000 || components["interface_counter"].UnknownMS != 60_000 {
		t.Fatalf("coverage invented data beyond heartbeats: %+v", components)
	}
	if len(view.Gaps) != 2 || view.Gaps[0].Count != 0 || len(view.LossHours) != 1 || view.LossHours[0].KernelDrops != 2 {
		t.Fatalf("loss evidence or half-open boundaries failed: %+v", view)
	}
	seen := map[string]bool{}
	for {
		for _, segment := range view.Segments {
			key := fmt.Sprintf("%s/%s/%s", segment.Name, segment.Start, segment.End)
			if seen[key] {
				t.Fatal("health continuation duplicated a segment")
			}
			seen[key] = true
		}
		if !view.More {
			break
		}
		q.Offset = view.NextOffset
		view, err = s.Integrity(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) != 9 {
		t.Fatalf("incomplete segmented coverage: %d", len(seen))
	}
}

func TestIntegrityConflictingClockEvidenceIsUnknownAndBounded(t *testing.T) {
	s := budgetStore(t)
	ctx := context.Background()
	base := time.Now().UTC().Add(-time.Hour).Truncate(time.Millisecond)
	for _, row := range []struct {
		state      string
		start, end int
	}{{"running", 0, 20}, {"degraded", 10, 30}} {
		if _, err := s.db.Exec(`INSERT INTO coverage_intervals(name,state,started_at,ended_at) VALUES ('sensor_feed',?,?,?)`, row.state, base.Add(time.Duration(row.start)*time.Second).UnixMilli(), base.Add(time.Duration(row.end)*time.Second).UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}
	view, err := s.Integrity(ctx, IntegrityQuery{Start: base, End: base.Add(40 * time.Second), Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range view.Components {
		if c.Name == "sensor_feed" && (c.RunningMS != 10_000 || c.DegradedMS != 10_000 || c.UnknownMS != 20_000 || c.ConflictMS != 10_000) {
			t.Fatal("overlapping states were counted as healthy twice", c)
		}
	}
	for _, q := range []IntegrityQuery{{Start: base, End: base.Add(401 * 24 * time.Hour), Limit: 20}, {Start: base, End: base.Add(time.Hour), Limit: 101}, {Start: base, End: base.Add(time.Hour), Limit: 20, Offset: maxIntegritySegments + 1}} {
		if _, err := s.Integrity(ctx, q); err == nil {
			t.Fatal("unbounded health query accepted")
		}
	}
	for i := range 102 {
		if err := s.RecordCoverageGap(ctx, CoverageGap{Name: "sensor_feed", Reason: "synthetic", Start: base.Add(time.Duration(i) * time.Millisecond), End: base.Add(time.Duration(i) * time.Millisecond)}); err != nil {
			t.Fatal(err)
		}
	}
	view, err = s.Integrity(ctx, IntegrityQuery{Start: base, End: base.Add(time.Hour), Limit: 100})
	if err != nil || len(view.Gaps) != 100 || !view.GapsTruncated {
		t.Fatal("gap truncation was hidden", err)
	}
}
