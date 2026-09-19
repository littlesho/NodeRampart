// SPDX-License-Identifier: MIT

package sensor

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/collector"
	"github.com/littlesho/NodeRampart/internal/model"
	"github.com/littlesho/NodeRampart/internal/protocol"
)

func TestDeliveryLossIsIsolatedAndRetirementBoundsChurn(t *testing.T) {
	connection := &recordingConnection{}
	fail := true
	s := &BatchSender{connect: func() (batchConnection, error) {
		if fail {
			return nil, errors.New("offline")
		}
		return connection, nil
	}}
	a := deliveryBatch(2)
	if err := s.Send(a); err == nil {
		t.Fatal("expected loss")
	}
	fail = false
	b := deliveryBatch(3)
	b.Interface = "eth1"
	if err := s.Send(b); err != nil {
		t.Fatal(err)
	}
	if err := s.Send(a); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(&connection.Buffer)
	for i := range 2 {
		var got protocol.Batch
		if err := protocol.ReadFrame(reader, &got); err != nil {
			t.Fatal(err)
		}
		if i == 0 && (got.IPCDroppedBatches != 0 || got.KernelDrops != b.KernelDrops) {
			t.Fatalf("eth0 loss contaminated eth1: %+v", got)
		}
		if i == 1 && (got.IPCDroppedBatches != 1 || got.IPCDroppedPackets != 5) {
			t.Fatalf("eth0 loss not retained: %+v", got)
		}
	}
	_ = s.Close()
	fail = true
	for i := range protocol.MaxInterfaces {
		b.Interface = fmt.Sprintf("lab%d", i)
		if err := s.Send(b); err == nil {
			t.Fatal("expected loss")
		}
	}
	b.Interface = "excess"
	if err := s.Send(b); err == nil || len(s.pending) != protocol.MaxInterfaces {
		t.Fatal("pending identities exceeded bound")
	}
	if batches, packets, bytes := s.RetireInterface("lab0"); batches != 1 || packets != 7 || bytes != 700 {
		t.Fatalf("retirement=%d/%d/%d", batches, packets, bytes)
	}
	if err := s.Send(b); err == nil || len(s.pending) != protocol.MaxInterfaces || s.pending["excess"].IPCDroppedBatches != 1 {
		t.Fatal("retirement did not free capacity")
	}
}

type fakeCapture struct{ closed atomic.Bool }

func (f *fakeCapture) Run(ctx context.Context, _ chan<- Packet) error { <-ctx.Done(); return nil }
func (f *fakeCapture) Close() error                                   { f.closed.Store(true); return nil }
func (*fakeCapture) Stats() (uint64, uint64, error)                   { return 0, 0, nil }
func (*fakeCapture) ParseErrors() uint64                              { return 0 }

func TestCaptureFleetPartitionsBudgetsAndRetiresBeforeReplacement(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	refs := []collector.InterfaceRef{{Name: "labA", Index: 1}, {Name: "labB", Index: 2}}
	opened := []*fakeCapture{}
	connection := &recordingConnection{}
	f := &Fleet{Limit: 2, MaxFlows: 4096, ReceiveBuffer: 1 << 20, BatchInterval: time.Second,
		Sender: &BatchSender{connect: func() (batchConnection, error) { return connection, nil }}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), slots: map[string]*captureSlot{}}
	f.resolve = func(context.Context, []string) ([]collector.InterfaceRef, error) { return refs, nil }
	f.open = func(_ string, buffer int) (captureSource, error) {
		if buffer != 1<<19 {
			t.Errorf("receive budget multiplied: %d", buffer)
		}
		if len(opened) >= 2 && (!opened[0].closed.Load() || !opened[1].closed.Load()) {
			t.Error("opened replacement before retiring old captures")
		}
		capture := &fakeCapture{}
		opened = append(opened, capture)
		return capture, nil
	}
	t.Cleanup(func() {
		for name := range f.slots {
			if err := f.retire(name); err != nil {
				t.Error(err)
			}
		}
	})
	if err := f.reconcile(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	for name, slot := range f.slots {
		if slot.aggregator.maxFlows != 2048 {
			t.Fatal("flow budget multiplied")
		}
		packet := Packet{Direction: model.DirectionInbound, RemoteIP: netip.MustParseAddr("192.0.2.1"), Protocol: "tcp", TCPFlags: 2, Bytes: 60}
		slot.aggregator.Observe(packet)
		if name == "labB" {
			slot.aggregator.Observe(packet)
		}
	}
	f.flush(time.Now().Add(time.Second))
	reader := bufio.NewReader(&connection.Buffer)
	for i := range 2 {
		var b protocol.Batch
		if err := protocol.ReadFrame(reader, &b); err != nil {
			t.Fatal(err)
		}
		if err := b.Validate(); err != nil {
			t.Fatal(err)
		}
		if b.RXPackets != uint64(i+1) {
			t.Fatalf("captures mixed: %+v", b)
		}
	}
	refs = []collector.InterfaceRef{{Name: "labB", Index: 9}, {Name: "labC", Index: 3}}
	if err := f.reconcile(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	if len(f.slots) != 2 || f.slots["labB"].ref.Index != 9 || !connection.closed {
		t.Fatal("replacement did not reset bounded captures and connection")
	}
}
