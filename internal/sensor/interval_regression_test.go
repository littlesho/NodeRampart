// SPDX-License-Identifier: MIT

package sensor

import (
	"bufio"
	"io"
	"log/slog"
	"net/netip"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/detect"
	"github.com/littlesho/NodeRampart/internal/model"
	"github.com/littlesho/NodeRampart/internal/protocol"
)

func TestAggregatorOneMinuteElapsedBoundaryIsNotTruncated(t *testing.T) {
	start := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	for _, elapsed := range []time.Duration{59999 * time.Millisecond, time.Minute, 60001 * time.Millisecond, 65 * time.Second} {
		a := NewAggregator("lab0", 8, start)
		a.Observe(Packet{Direction: model.DirectionInbound, RemoteIP: netip.MustParseAddr("192.0.2.1"), Protocol: "udp", Bytes: 60})
		b := a.Flush(start.Add(elapsed))
		if b.IntervalMillis != elapsed.Milliseconds() || !b.SentAt.Equal(start.Add(elapsed)) || b.RXPackets != 1 {
			t.Fatalf("real elapsed window changed: %+v", b)
		}
		if err := b.ValidateAt(start.Add(elapsed)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestOneMinuteJitterRetainsDetectionRateDenominator(t *testing.T) {
	cfg := config.Defaults().Detection
	cfg.SYNPacketsPerSecond = 100
	for _, millis := range []int64{60000, 60001} {
		detector := detect.NewNetwork(cfg)
		b := protocol.Batch{ProtocolVersion: protocol.Version, SentAt: time.Now().UTC(), IntervalMillis: millis, Interface: "lab0", RXPackets: 6000, InboundSYN: 6000}
		events := detector.Observe(b)
		want := 0
		if millis == 60000 {
			want = 1
		}
		if len(events) != want {
			t.Fatalf("interval=%d events=%d want=%d; real elapsed denominator changed", millis, len(events), want)
		}
	}
}

func TestFleetLongPauseRetainsLossAndRebaselines(t *testing.T) {
	start := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	now := start.Add(65001 * time.Millisecond)
	conn := &recordingConnection{}
	sender := &BatchSender{connect: func(time.Duration) (batchConnection, error) { return conn, nil }}
	a := NewAggregator("lab0", 8, start)
	a.now = func() time.Time { return now }
	packet := Packet{Direction: model.DirectionInbound, RemoteIP: netip.MustParseAddr("192.0.2.1"), Protocol: "udp", Bytes: 60}
	a.Observe(packet)
	slot := &captureSlot{capture: &fakeCapture{}, aggregator: a, done: make(chan struct{}), lastFlush: start}
	f := &Fleet{BatchInterval: time.Minute, Sender: sender, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), slots: map[string]*captureSlot{"lab0": slot}}
	f.flush(now)
	if conn.Len() != 0 || !slot.lastFlush.Equal(now) || !a.started.Equal(now) {
		t.Fatal("long window was sent or its observation baseline was not reset")
	}
	if batches, packets, bytes := sender.PendingLoss(); batches != 1 || packets != 1 || bytes != 60 {
		t.Fatalf("discarded window was not counted as loss: %d/%d/%d", batches, packets, bytes)
	}
	if p := sender.pending["lab0"]; len(p.Flows) != 0 || p.RXPackets != 0 || p.IntervalMillis != 0 {
		t.Fatal("old detail retained for replay into a new rate window")
	}
	a.Observe(packet)
	now = now.Add(60001 * time.Millisecond)
	f.flush(now)
	var b protocol.Batch
	if err := protocol.ReadFrame(bufio.NewReader(&conn.Buffer), &b); err != nil {
		t.Fatal(err)
	}
	if err := b.ValidateAt(now); err != nil {
		t.Fatal(err)
	}
	if b.IntervalMillis != 60001 || b.RXPackets != 1 || b.RXBytes != 60 || b.IPCDroppedBatches != 1 || b.IPCDroppedPackets != 1 || b.IPCDroppedBytes != 60 {
		t.Fatalf("recovery hid loss or replayed stale traffic: %+v", b)
	}
	if batches, _, _ := sender.PendingLoss(); batches != 0 {
		t.Fatal("recovery repeated delivered health")
	}
}
