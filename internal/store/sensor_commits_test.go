// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/model"
	"github.com/littlesho/NodeRampart/internal/protocol"
)

func sensorCommitFixture(at time.Time, sequence uint64) (protocol.Batch, []model.Traffic) {
	b := protocol.Batch{ProtocolVersion: protocol.Version, SessionID: strings.Repeat("a", 32), Sequence: sequence, Interface: "lab0", SentAt: at, IntervalMillis: 1000, RXPackets: 1, RXBytes: 60, KernelPackets: sequence * 3, KernelDrops: sequence,
		Flows: []protocol.Flow{{Direction: model.DirectionInbound, RemoteIP: "192.0.2.1", Protocol: "tcp", LocalPort: 22, Packets: 1, Bytes: 60}}}
	return b, []model.Traffic{{HourUTC: at, Direction: model.DirectionInbound, Bytes: 60, Packets: 1}}
}

func assertSensorTotals(t *testing.T, s *Store, batches, bytes, drops uint64) {
	t.Helper()
	var gotBatches, gotBytes, gotDrops uint64
	if err := s.db.QueryRow(`SELECT COALESCE(SUM(batches),0),COALESCE(SUM(kernel_drops),0) FROM collector_health_hourly`).Scan(&gotBatches, &gotDrops); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT COALESCE(SUM(bytes),0) FROM traffic_hourly`).Scan(&gotBytes); err != nil {
		t.Fatal(err)
	}
	if gotBatches != batches || gotBytes != bytes || gotDrops != drops {
		t.Fatalf("batch/traffic/health drift: got %d/%d/%d want %d/%d/%d", gotBatches, gotBytes, gotDrops, batches, bytes, drops)
	}
}

func TestSensorCommitAtomicFailureDuplicateGapAndRestart(t *testing.T) {
	s := budgetStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	b, traffic := sensorCommitFixture(now, 1)
	ack, err := s.CommitSensorBatch(ctx, b, traffic, true, true, true, "committed")
	if err != nil || !ack.Complete || ack.CommittedSequence != 1 {
		t.Fatal(ack, err)
	}
	assertSensorTotals(t, s, 1, 60, 1)
	ack, err = s.CommitSensorBatch(ctx, b, nil, true, true, true, "committed")
	if err != nil || !ack.Duplicate || !ack.Complete {
		t.Fatal(ack, err)
	}
	assertSensorTotals(t, s, 1, 60, 1)
	// Fail after health insertion using a real SQLite write failure. Both
	// health and the watermark must roll back with traffic.
	if _, err := s.db.Exec(`CREATE TRIGGER reject_sensor_traffic BEFORE UPDATE ON traffic_hourly BEGIN SELECT RAISE(ABORT,'fixture rejection'); END`); err != nil {
		t.Fatal(err)
	}
	b, traffic = sensorCommitFixture(now.Add(time.Second), 2)
	ack, err = s.CommitSensorBatch(ctx, b, traffic, true, true, true, "committed")
	if err == nil || ack.Complete || ack.HealthCommitted || ack.CommittedSequence != 1 {
		t.Fatal("failed transaction acknowledged", ack, err)
	}
	assertSensorTotals(t, s, 1, 60, 1)
	if _, err := s.db.Exec(`DROP TRIGGER reject_sensor_traffic`); err != nil {
		t.Fatal(err)
	}
	ack, err = s.CommitSensorBatch(ctx, b, traffic, false, false, false, "derived_events_pending")
	if err != nil || ack.Complete || !ack.HealthCommitted || !ack.TrafficCommitted || ack.EventsCommitted {
		t.Fatal(ack, err)
	}
	assertSensorTotals(t, s, 2, 120, 2)
	path := s.path
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenWithBudget(path, BudgetConfig{MaxBytes: 64 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	ack, err = reopened.CommitSensorBatch(ctx, b, nil, true, true, true, "committed")
	if err != nil || !ack.Duplicate || ack.Complete || ack.EventsCommitted {
		t.Fatal("restart fabricated complete processing", ack, err)
	}
	assertSensorTotals(t, reopened, 2, 120, 2)
	b, traffic = sensorCommitFixture(now.Add(3*time.Second), 4)
	ack, err = reopened.CommitSensorBatch(ctx, b, traffic, true, true, true, "committed")
	if err != nil || ack.CommittedSequence != 4 {
		t.Fatal(ack, err)
	}
	assertSensorTotals(t, reopened, 3, 180, 4) // Cumulative lost health is recovered, not repeated.
	watermark, ok, err := reopened.SensorWatermark(ctx, b.SessionID, b.Interface)
	if err != nil || !ok || watermark.SequenceGaps != 1 || watermark.Duplicates != 2 {
		t.Fatal(watermark, ok, err)
	}
	var gaps int
	if err := reopened.db.QueryRow(`SELECT COUNT(*) FROM coverage_gaps WHERE reason='sensor_sequence_gap' AND count=1`).Scan(&gaps); err != nil || gaps != 1 {
		t.Fatal("missing sequence was hidden", gaps, err)
	}
	backup := filepath.Join(t.TempDir(), "snapshot.db")
	if _, err := reopened.Backup(ctx, backup); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyBackup(ctx, backup); err != nil {
		t.Fatal(err)
	}
}

func TestSensorWatermarkRetirementRejectsUncertainOldSession(t *testing.T) {
	s := budgetStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	first, traffic := sensorCommitFixture(now, 1)
	for i := 0; i <= MaxSensorWatermarks; i++ {
		b := first
		b.SessionID = fmt.Sprintf("%032x", i+1)
		b.SentAt = now.Add(time.Duration(i) * time.Second)
		if i == 0 {
			first = b
		}
		if _, err := s.CommitSensorBatch(ctx, b, traffic, true, true, true, "committed"); err != nil {
			t.Fatal(i, err)
		}
	}
	watermarks, err := s.SensorWatermarks(ctx)
	if err != nil || len(watermarks) != MaxSensorWatermarks {
		t.Fatal(len(watermarks), err)
	}
	ack, err := s.CommitSensorBatch(ctx, first, traffic, true, true, true, "committed")
	if err == nil || ack.Complete || ack.Duplicate || ack.Reason != "retired_or_nonmonotonic_session" {
		t.Fatal("retired history adopted", ack, err)
	}
	assertSensorTotals(t, s, MaxSensorWatermarks+1, (MaxSensorWatermarks+1)*60, MaxSensorWatermarks+1)
}

func TestSensorCommitCancellationAndCumulativeResetRejectWithoutProgress(t *testing.T) {
	s := budgetStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	b, traffic := sensorCommitFixture(now, 1)
	if _, err := s.CommitSensorBatch(ctx, b, traffic, true, true, true, "committed"); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	b, traffic = sensorCommitFixture(now.Add(time.Second), 2)
	if ack, err := s.CommitSensorBatch(canceled, b, traffic, true, true, true, "committed"); err == nil || ack.Complete {
		t.Fatal(ack, err)
	}
	b.KernelDrops = 0
	if ack, err := s.CommitSensorBatch(ctx, b, traffic, true, true, true, "committed"); err == nil || ack.Complete {
		t.Fatal("counter reset was silently accepted", ack, err)
	}
	b, traffic = sensorCommitFixture(now.Add(time.Second), 2)
	for _, completion := range []struct {
		complete bool
		reason   string
	}{{true, "derived_events_pending"}, {false, "committed"}} {
		if ack, err := s.CommitSensorBatch(ctx, b, traffic, true, true, completion.complete, completion.reason); err == nil || ack.Complete {
			t.Fatal("inconsistent completion persisted", ack, err)
		}
	}
	assertSensorTotals(t, s, 1, 60, 1)
	if _, err := s.db.Exec(`DELETE FROM sensor_commit_state`); err != nil {
		t.Fatal(err)
	}
	if err := verifySensorCommitState(ctx, s.db); err == nil {
		t.Fatal("missing singleton appeared valid")
	}
}
