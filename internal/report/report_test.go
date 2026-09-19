// SPDX-License-Identifier: MIT

package report

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/model"
	"github.com/littlesho/NodeRampart/internal/protocol"
	"github.com/littlesho/NodeRampart/internal/store"
)

func TestDailyReportShowsCoverageGap(t *testing.T) {
	database, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	location := time.FixedZone("JST", 9*3600)
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, location)
	_, start, end := PreviousDay(now, location)
	if err := database.AddInterface(context.Background(), model.InterfaceTotals{HourUTC: start.Add(time.Hour), RXBytes: 2048, TXBytes: 1024}); err != nil {
		t.Fatal(err)
	}
	builder := Builder{Store: database, Hostname: "vps<&>", Location: location, TopN: 5}
	body, err := builder.Range(context.Background(), "test", start, end)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "Detailed sensor coverage unavailable") || !strings.Contains(body, "vps&lt;&amp;&gt;") {
		t.Fatalf("unexpected report: %s", body)
	}
}

func TestUnattributedFlowDoesNotReduceReconciliationGap(t *testing.T) {
	database, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	start := time.Now().UTC().Truncate(time.Hour)
	end := start.Add(time.Hour)
	if err := database.AddInterface(ctx, model.InterfaceTotals{HourUTC: start, RXBytes: 1000}); err != nil {
		t.Fatal(err)
	}
	if err := database.AddTraffic(ctx, model.Traffic{HourUTC: start, Direction: model.DirectionInbound, Bytes: 700, Packets: 7, Attributed: false}); err != nil {
		t.Fatal(err)
	}
	body, err := (&Builder{Store: database, Hostname: "test", Location: time.UTC, TopN: 5}).Range(ctx, "test", start, end)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "Unattributed/reconciliation: RX 1000 B") {
		t.Fatalf("unexpected data quality section: %s", body)
	}
}

func TestJoinWithinKeepsWholeLines(t *testing.T) {
	message := joinWithin([]string{"<b>report</b>", strings.Repeat("x", 200), "tail"}, 64)
	if len(message) > 64 || !strings.HasSuffix(message, "… truncated") || !strings.Contains(message, "</b>") {
		t.Fatalf("unsafe bounded report: %q", message)
	}
}

func TestReportShowsIPCLossAndSaturatedHealth(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	hour := time.Now().UTC().Truncate(time.Hour)
	if err := db.RecordBatchHealth(ctx, protocol.Batch{SentAt: hour, IPCDroppedBatches: 2, IPCDroppedPackets: 8, IPCDroppedBytes: 800, HealthCountersSaturated: true}); err != nil {
		t.Fatal(err)
	}
	body, err := (&Builder{Store: db, Hostname: "lab", Location: time.UTC, TopN: 5}).Range(ctx, "test", hour, hour.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "IPC send loss estimate: 2 batches · 8 packets / 800 B") || !strings.Contains(body, "totals are lower bounds") {
		t.Fatalf("loss or saturation missing from report: %s", body)
	}
}
