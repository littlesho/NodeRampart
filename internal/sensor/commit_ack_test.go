// SPDX-License-Identifier: MIT

package sensor

import (
	"bufio"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/protocol"
)

func awaitDeliveryState(t *testing.T, sender *BatchSender, want func(DeliveryState) bool) DeliveryState {
	t.Helper()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		states := sender.DeliveryStatus()
		if len(states) > 0 && want(states[0]) {
			return states[0]
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatal("delivery acknowledgement state did not arrive", states)
			return DeliveryState{}
		}
	}
}

func TestSenderCumulativeHealthPartialACKAndConcurrentStatus(t *testing.T) {
	client, server := net.Pipe()
	sender := &BatchSender{connect: func(time.Duration) (batchConnection, error) { return client, nil }}
	frames := make(chan protocol.Batch, 4)
	done := make(chan struct{})
	go func() {
		defer close(done)
		reader := bufio.NewReader(server)
		for {
			var b protocol.Batch
			if protocol.ReadFrame(reader, &b) != nil {
				return
			}
			frames <- b
		}
	}()
	t.Cleanup(func() {
		sender.Close()
		server.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("fixture reader leaked")
		}
	})
	statusStop, statusDone := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(statusDone)
		for {
			select {
			case <-statusStop:
				return
			default:
				sender.DeliveryStatus()
			}
		}
	}()
	defer func() { close(statusStop); <-statusDone }()
	first := deliveryBatch(1)
	first.ProtocolVersion = protocol.Version
	first.KernelDrops = 3
	if err := sender.Send(first); err != nil {
		t.Fatal(err)
	}
	wireFirst := <-frames
	if state := sender.DeliveryStatus()[0]; state.CommittedSequence != 0 || state.Complete {
		t.Fatal("write success fabricated commit", state)
	}
	bad := protocol.CommitACK{ProtocolVersion: protocol.Version, SessionID: wireFirst.SessionID, Interface: wireFirst.Interface, Sequence: 99, CommittedSequence: 99, HealthCommitted: true, TrafficCommitted: true, EventsCommitted: true, NotificationsCommitted: true, Complete: true}
	if err := protocol.WriteFrame(server, bad); err != nil {
		t.Fatal(err)
	}
	second := deliveryBatch(2)
	second.ProtocolVersion = protocol.Version
	second.KernelDrops = 4
	if err := sender.Send(second); err != nil {
		t.Fatal(err)
	}
	wireSecond := <-frames
	if wireSecond.Sequence != 2 || wireSecond.SessionID != wireFirst.SessionID || wireSecond.KernelDrops != 7 {
		t.Fatal("unconfirmed health lost/repeated", wireSecond)
	}
	delta, err := wireSecond.CollectorHealth().Delta(wireFirst.CollectorHealth())
	if err != nil || delta.KernelDrops != 4 {
		t.Fatal(delta, err)
	}
	partial := protocol.CommitACK{ProtocolVersion: protocol.Version, SessionID: wireSecond.SessionID, Interface: wireSecond.Interface, Sequence: 2, CommittedSequence: 2, HealthCommitted: true, TrafficCommitted: true, NotificationsCommitted: true, Reason: "derived_events_pending"}
	if err := protocol.WriteFrame(server, partial); err != nil {
		t.Fatal(err)
	}
	if state := awaitDeliveryState(t, sender, func(s DeliveryState) bool { return s.CommittedSequence == 2 }); state.Complete || state.Reason != "derived_events_pending" {
		t.Fatal("partial ACK became complete", state)
	}
	third := deliveryBatch(3)
	third.ProtocolVersion = protocol.Version
	third.KernelDrops = 0
	if err := sender.Send(third); err != nil {
		t.Fatal(err)
	}
	wireThird := <-frames
	complete := partial
	complete.Sequence = 3
	complete.CommittedSequence = 3
	complete.EventsCommitted = true
	complete.Complete = true
	complete.Reason = "committed"
	if err := protocol.WriteFrame(server, complete); err != nil {
		t.Fatal(err)
	}
	if state := awaitDeliveryState(t, sender, func(s DeliveryState) bool { return s.Complete }); state.CommittedSequence != 3 || wireThird.KernelDrops != 7 {
		t.Fatal(state, wireThird)
	}
}

func TestSenderDeliveryStateBoundRetirementAndSequenceExhaustion(t *testing.T) {
	conn := &recordingConnection{}
	sender := &BatchSender{connect: func(time.Duration) (batchConnection, error) { return conn, nil }}
	for i := 0; i < protocol.MaxInterfaces; i++ {
		b := deliveryBatch(1)
		b.ProtocolVersion = protocol.Version
		b.Interface = fmt.Sprintf("lab%d", i)
		if err := sender.Send(b); err != nil {
			t.Fatal(err)
		}
	}
	b := deliveryBatch(1)
	b.ProtocolVersion = protocol.Version
	b.Interface = "lab8"
	if err := sender.Send(b); err == nil {
		t.Fatal("delivery state grew past interface bound")
	}
	if len(sender.DeliveryStatus()) != protocol.MaxInterfaces {
		t.Fatal("delivery state bound changed")
	}
	sender.RetireInterface("lab0")
	if err := sender.Send(b); err != nil {
		t.Fatal("retirement did not free state", err)
	}
	sender.deliveryMu.Lock()
	sender.delivery["lab1"].SentSequence = 1<<63 - 1
	sender.delivery["lab1"].Complete = true
	sender.deliveryMu.Unlock()
	b.Interface = "lab1"
	if err := sender.Send(b); err == nil {
		t.Fatal("sequence wrapped")
	}
	for _, state := range sender.DeliveryStatus() {
		if state.Interface == "lab1" && (state.Complete || state.Reason != "sequence_exhausted") {
			t.Fatal("exhausted sequence still healthy", state)
		}
	}
}

func TestSenderCumulativeHealthSaturationKeepsOriginalLimit(t *testing.T) {
	conn := &recordingConnection{}
	sender := &BatchSender{connect: func(time.Duration) (batchConnection, error) { return conn, nil }}
	b := deliveryBatch(1)
	b.ProtocolVersion = protocol.Version
	b.KernelPackets = protocol.MaxBatchPackets
	if err := sender.Send(b); err != nil {
		t.Fatal(err)
	}
	readDeliveredBatch(t, conn)
	b.KernelPackets = 1
	if err := sender.Send(b); err != nil {
		t.Fatal(err)
	}
	got := readDeliveredBatch(t, conn)
	if got.KernelPackets != protocol.MaxBatchPackets || !got.HealthCountersSaturated {
		t.Fatal("cumulative bound was weakened", got)
	}
}
