// SPDX-License-Identifier: MIT

package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/protocol"
)

func TestSensorObservationGapUsesCommittedWindowAndKnownDuration(t *testing.T) {
	a := eventTestApp(t)
	a.options.Config.Sensor.BatchInterval.Duration = time.Minute
	ctx := context.Background()
	start := time.Now().UTC().Truncate(time.Millisecond)
	first := protocol.Batch{ProtocolVersion: protocol.Version, SentAt: start, IntervalMillis: 60000, Interface: "lab0"}
	a.handleBatch(ctx, first)
	// A 65.001s discarded window is followed by a valid 60.001s window.
	recovered := first
	recovered.SentAt = start.Add(125002 * time.Millisecond)
	recovered.IntervalMillis = 60001
	recovered.IPCDroppedBatches = 1
	a.handleBatch(ctx, recovered)
	gaps, err := a.options.Store.CoverageGaps(ctx, start.Add(-time.Second), recovered.SentAt.Add(time.Second), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(gaps) != 1 || gaps[0].Reason != "sensor_observation_gap" || !gaps[0].Start.Equal(start) || !gaps[0].End.Equal(start.Add(65001*time.Millisecond)) || gaps[0].Count != 0 {
		t.Fatalf("known missing interval was concealed or count invented: %+v", gaps)
	}
	// Millisecond serialization uncertainty does not invent another gap.
	contiguous := recovered
	contiguous.SentAt = recovered.SentAt.Add(60002 * time.Millisecond)
	contiguous.IPCDroppedBatches = 0
	a.handleBatch(ctx, contiguous)
	gaps, err = a.options.Store.CoverageGaps(ctx, start.Add(-time.Second), contiguous.SentAt.Add(time.Second), 10)
	if err != nil || len(gaps) != 1 {
		t.Fatalf("timestamp rounding created a false gap: %+v error=%v", gaps, err)
	}
}

func TestSensorObservationCommitDoesNotAdvanceOnStorageFailure(t *testing.T) {
	a := eventTestApp(t)
	ctx := context.Background()
	start := time.Now().UTC().Truncate(time.Millisecond)
	b := protocol.Batch{ProtocolVersion: protocol.Version, SentAt: start, IntervalMillis: 1000, Interface: "lab0"}
	a.handleBatch(ctx, b)
	eventTestBudget(t, a, true)
	b.SentAt = start.Add(time.Second)
	a.handleBatch(ctx, b)
	if got := a.sensorCommittedByInterface["lab0"]; !got.Equal(start) {
		t.Fatal("failed persistence advanced the observation marker", got)
	}
	eventTestBudget(t, a, false)
	b.SentAt = start.Add(2 * time.Second)
	a.handleBatch(ctx, b)
	gaps, err := a.options.Store.CoverageGaps(ctx, start.Add(-time.Second), b.SentAt.Add(time.Second), 10)
	if err != nil || len(gaps) != 1 || !gaps[0].Start.Equal(start) || !gaps[0].End.Equal(start.Add(time.Second)) {
		t.Fatalf("uncommitted observation disappeared from coverage: %+v error=%v", gaps, err)
	}
	if got := a.sensorCommittedByInterface["lab0"]; !got.Equal(b.SentAt) {
		t.Fatal("successful recovery did not advance committed observation", got)
	}
}
