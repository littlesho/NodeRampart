// SPDX-License-Identifier: MIT

//go:build linux

package sensor

import (
	"bufio"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/model"
	"github.com/littlesho/NodeRampart/internal/protocol"
)

func deliveryUnixListener(t *testing.T) (*net.UnixListener, *BatchSender) {
	t.Helper()
	dir, err := os.MkdirTemp("", "nr-delivery-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "sensor.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	sender := NewBatchSender(path, uint32(os.Geteuid()))
	t.Cleanup(func() { _ = sender.Close(); _ = listener.Close() })
	return listener, sender
}

func TestBatchSenderSlowUnixReaderTimesOutAndReconnects(t *testing.T) {
	listener, sender := deliveryUnixListener(t)
	accepted := make(chan *net.UnixConn, 1)
	acceptError := make(chan error, 1)
	go func() {
		conn, err := listener.AcceptUnix()
		if err != nil {
			acceptError <- err
			return
		}
		accepted <- conn // The real peer deliberately does not consume its frame.
	}()
	large := deliveryBatch(1)
	large.Flows = nil
	large.RXPackets, large.RXBytes, large.InboundUDP = protocol.MaxFlowsPerBatch, 1200*protocol.MaxFlowsPerBatch, protocol.MaxFlowsPerBatch
	for i := range protocol.MaxFlowsPerBatch {
		large.Flows = append(large.Flows, protocol.Flow{Direction: model.DirectionInbound, RemoteIP: "2001:db8:ffff:ffff:ffff:ffff:ffff:ffff", Protocol: "udp", LocalPort: uint16(i + 1), RemotePort: 65000, Packets: 1, Bytes: 1200})
	}
	start := time.Now()
	if err := sender.Send(large); err == nil {
		t.Fatal("unresponsive real Unix reader did not time out")
	}
	if time.Since(start) > time.Second {
		t.Fatal("single sender timeout exceeded its finite budget")
	}
	select {
	case conn := <-accepted:
		_ = conn.Close()
	case err := <-acceptError:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("Unix peer did not accept")
	}
	got := make(chan protocol.Batch, 1)
	go func() {
		conn, err := listener.AcceptUnix()
		if err != nil {
			acceptError <- err
			return
		}
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(time.Second))
		var b protocol.Batch
		if err := protocol.ReadFrame(bufio.NewReaderSize(conn, 64<<10), &b); err != nil {
			acceptError <- err
			return
		}
		got <- b
	}()
	current := deliveryBatch(2)
	if err := sender.Send(current); err != nil {
		t.Fatal(err)
	}
	select {
	case b := <-got:
		if b.IPCDroppedBatches != 1 || b.IPCDroppedPackets != large.RXPackets+large.TXPackets || b.IPCDroppedBytes != large.RXBytes+large.TXBytes || b.RXPackets != current.RXPackets || b.IntervalMillis != current.IntervalMillis {
			t.Fatalf("reconnect lost health or replayed prior traffic: %+v", b)
		}
	case err := <-acceptError:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("reconnected Unix peer did not decode")
	}
}

func TestFleetSharedUnixWriteDeadlineBoundsAllInterfaces(t *testing.T) {
	listener, sender := deliveryUnixListener(t)
	accepted := make(chan *net.UnixConn, 1)
	go func() {
		conn, err := listener.AcceptUnix()
		if err == nil {
			accepted <- conn
		}
	}()
	start := time.Now().UTC().Add(-2 * time.Second)
	f := &Fleet{BatchInterval: 2 * time.Second, Sender: sender, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), slots: map[string]*captureSlot{}}
	for i := range protocol.MaxInterfaces {
		name := fmt.Sprintf("lab%d", i)
		a := NewAggregator(name, protocol.MaxFlowsPerBatch/protocol.MaxInterfaces, start)
		for port := range protocol.MaxFlowsPerBatch / protocol.MaxInterfaces {
			a.Observe(Packet{Direction: model.DirectionInbound, RemoteIP: netip.MustParseAddr("2001:db8:ffff:ffff:ffff:ffff:ffff:ffff"), Protocol: "udp", LocalPort: uint16(port + 1), RemotePort: 65000, Bytes: 1200})
		}
		f.slots[name] = &captureSlot{capture: &fakeCapture{}, aggregator: a, done: make(chan struct{}), lastFlush: start}
	}
	before := time.Now()
	f.flush(time.Now().UTC())
	if time.Since(before) > 750*time.Millisecond {
		t.Fatal("interface count multiplied the fleet's write-blocking budget")
	}
	select {
	case conn := <-accepted:
		_ = conn.Close()
	case <-time.After(time.Second):
		t.Fatal("Unix peer did not accept")
	}
	if batches, packets, bytes := sender.PendingLoss(); batches == 0 || packets == 0 || bytes == 0 {
		t.Fatal("unresponsive reader did not preserve undelivered loss")
	}
	for _, p := range sender.pending {
		if len(p.Flows) != 0 || p.RXPackets != 0 {
			t.Fatal("stalled fleet retained flow detail")
		}
	}
}
