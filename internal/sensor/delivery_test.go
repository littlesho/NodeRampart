// SPDX-License-Identifier: MIT

package sensor

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net"
	"reflect"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/model"
	"github.com/littlesho/NodeRampart/internal/protocol"
)

type recordingConnection struct {
	bytes.Buffer
	deadline      time.Time
	deadlineCalls int
	closed        bool
	maxWrite      int
	failAfter     int
	writes        int
}

func (c *recordingConnection) SetWriteDeadline(deadline time.Time) error {
	c.deadline, c.deadlineCalls = deadline, c.deadlineCalls+1
	return nil
}
func (c *recordingConnection) Close() error { c.closed = true; return nil }
func (c *recordingConnection) Write(payload []byte) (int, error) {
	c.writes++
	if c.failAfter > 0 && c.writes > c.failAfter {
		return 0, io.ErrClosedPipe
	}
	if c.maxWrite > 0 && len(payload) > c.maxWrite {
		payload = payload[:c.maxWrite]
	}
	return c.Buffer.Write(payload)
}

func deliveryBatch(packets uint64) protocol.Batch {
	return protocol.Batch{
		ProtocolVersion: protocol.Version, SentAt: time.Now().UTC(), IntervalMillis: 1000, Interface: "eth0",
		RXPackets: packets, TXPackets: packets + 1, RXBytes: packets * 100, TXBytes: (packets + 1) * 100,
		InboundUDP: packets, KernelPackets: 2*packets + 1, KernelDrops: packets,
		KernelStatsErrors: 1, ParseErrors: packets + 2, OverflowPackets: 1, OverflowBytes: 20,
		Flows: []protocol.Flow{{Direction: model.DirectionInbound, RemoteIP: "192.0.2.1", Protocol: "udp", LocalPort: 53, Packets: packets, Bytes: packets * 100}},
	}
}

func readDeliveredBatch(t *testing.T, connection *recordingConnection) protocol.Batch {
	t.Helper()
	var batch protocol.Batch
	if err := protocol.ReadFrame(bufio.NewReader(&connection.Buffer), &batch); err != nil {
		t.Fatal(err)
	}
	if err := batch.Validate(); err != nil {
		t.Fatal(err)
	}
	return batch
}

func TestBatchSenderRetainsHealthAcrossDialAndPartialWriteFailures(t *testing.T) {
	partial := &recordingConnection{failAfter: 1}
	good := &recordingConnection{maxWrite: 7}
	connections := 0
	sender := &BatchSender{connect: func() (batchConnection, error) {
		connections++
		switch connections {
		case 1:
			return nil, errors.New("daemon unavailable")
		case 2:
			return partial, nil
		default:
			return good, nil
		}
	}}
	t.Cleanup(func() { _ = sender.Close() })
	for packets := uint64(1); packets <= 2; packets++ {
		if err := sender.Send(deliveryBatch(packets)); err == nil {
			t.Fatal("expected delivery failure")
		}
		if len(sender.pending["eth0"].Flows) != 0 || sender.pending["eth0"].RXPackets != 0 || sender.pending["eth0"].InboundUDP != 0 {
			t.Fatal("failed flows or rate counters were retained")
		}
	}
	if !partial.closed || partial.Len() != 4 {
		t.Fatal("partial frame connection was not closed after writing its header")
	}
	if batches, packets, bytes := sender.PendingLoss(); batches != 2 || packets != 8 || bytes != 800 {
		t.Fatalf("pending loss = %d/%d/%d, want 2/8/800", batches, packets, bytes)
	}
	current := deliveryBatch(3)
	started := time.Now()
	if err := sender.Send(current); err != nil {
		t.Fatal(err)
	}
	got := readDeliveredBatch(t, good)
	if got.IPCDroppedBatches != 2 || got.IPCDroppedPackets != 8 || got.IPCDroppedBytes != 800 || got.KernelPackets != 15 || got.KernelDrops != 6 || got.KernelStatsErrors != 3 || got.ParseErrors != 12 || got.OverflowPackets != 3 || got.OverflowBytes != 60 {
		t.Fatalf("health lost or counted twice after reconnect: %#v", got)
	}
	if !reflect.DeepEqual(got.Flows, current.Flows) || got.RXPackets != current.RXPackets || got.TXPackets != current.TXPackets || got.RXBytes != current.RXBytes || got.TXBytes != current.TXBytes || got.InboundUDP != current.InboundUDP || got.IntervalMillis != current.IntervalMillis || !got.SentAt.Equal(current.SentAt) {
		t.Fatal("reconnect changed current traffic or its rate window")
	}
	if good.deadline.Before(started) || good.deadline.After(time.Now().Add(batchSendTimeout)) {
		t.Fatal("write deadline is missing or unbounded")
	}
	if batches, packets, bytes := sender.PendingLoss(); batches != 0 || packets != 0 || bytes != 0 {
		t.Fatal("success did not clear pending loss")
	}
	next := deliveryBatch(4)
	if err := sender.Send(next); err != nil {
		t.Fatal(err)
	}
	if got := readDeliveredBatch(t, good); !reflect.DeepEqual(got, next) {
		t.Fatalf("a later batch repeated delivered health: %#v", got)
	}
	if connections != 3 || good.deadlineCalls != 2 {
		t.Fatal("healthy connection was not reused with a fresh write deadline")
	}
}

func TestBatchSenderWriteTimeoutRecovers(t *testing.T) {
	client, peer := net.Pipe()
	defer peer.Close()
	defer client.Close()
	watchdog := time.AfterFunc(5*time.Second, func() { _ = peer.Close() })
	defer watchdog.Stop()
	good := &recordingConnection{}
	connections := 0
	sender := &BatchSender{connect: func() (batchConnection, error) {
		connections++
		if connections == 1 {
			return client, nil
		}
		return good, nil
	}}
	defer sender.Close()
	err := sender.Send(deliveryBatch(1)) // The in-memory peer never reads.
	var networkError net.Error
	if !errors.As(err, &networkError) || !networkError.Timeout() {
		t.Fatalf("expected a bounded write timeout, got %v", err)
	}
	if err := sender.Send(deliveryBatch(2)); err != nil {
		t.Fatal(err)
	}
	if got := readDeliveredBatch(t, good); got.IPCDroppedBatches != 1 || got.IPCDroppedPackets != 3 || got.IPCDroppedBytes != 300 {
		t.Fatalf("timed-out batch missing from recovered health: %#v", got)
	}
}

func TestBatchSenderSaturationIsReportedAndCleared(t *testing.T) {
	good := &recordingConnection{}
	connections := 0
	sender := &BatchSender{connect: func() (batchConnection, error) {
		connections++
		if connections == 1 {
			return nil, io.ErrClosedPipe
		}
		return good, nil
	}}
	defer sender.Close()
	large := deliveryBatch(1)
	large.RXPackets, large.TXPackets = protocol.MaxBatchPackets, protocol.MaxBatchPackets
	large.RXBytes, large.TXBytes = protocol.MaxBatchBytes, protocol.MaxBatchBytes
	large.KernelPackets, large.KernelDrops, large.ParseErrors = protocol.MaxBatchPackets, protocol.MaxBatchPackets, protocol.MaxBatchPackets
	large.OverflowBytes = protocol.MaxBatchBytes
	if err := sender.Send(large); err == nil {
		t.Fatal("expected unavailable daemon")
	}
	if err := sender.Send(deliveryBatch(1)); err != nil {
		t.Fatal(err)
	}
	got := readDeliveredBatch(t, good)
	if !got.HealthCountersSaturated || got.IPCDroppedPackets != protocol.MaxBatchPackets || got.IPCDroppedBytes != protocol.MaxBatchBytes || got.KernelPackets != protocol.MaxBatchPackets || got.KernelDrops != protocol.MaxBatchPackets || got.ParseErrors != protocol.MaxBatchPackets || got.OverflowBytes != protocol.MaxBatchBytes {
		t.Fatalf("expected bounded counters and explicit lower-bound flag: %#v", got)
	}
	if err := sender.Send(deliveryBatch(1)); err != nil {
		t.Fatal(err)
	}
	if got := readDeliveredBatch(t, good); got.HealthCountersSaturated || got.IPCDroppedBatches != 0 {
		t.Fatal("saturation was not cleared after its successful report")
	}
}

func TestBatchSenderUnavailableSocket(t *testing.T) {
	sender := NewBatchSender(t.TempDir()+"/missing.sock", 0)
	defer sender.Close()
	if err := sender.Send(deliveryBatch(1)); err == nil {
		t.Fatal("expected missing socket error")
	}
	if batches, _, _ := sender.PendingLoss(); batches != 1 {
		t.Fatal("failed real dial did not retain loss")
	}
}
