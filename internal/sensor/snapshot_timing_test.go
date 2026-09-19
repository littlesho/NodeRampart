// SPDX-License-Identifier: MIT
package sensor

import (
	"bufio"
	"bytes"
	"errors"
	"github.com/littlesho/NodeRampart/internal/model"
	"github.com/littlesho/NodeRampart/internal/protocol"
	"io"
	"log/slog"
	"net/netip"
	"testing"
	"time"
)

type snapshotHookConnection struct {
	bytes.Buffer
	hook func()
	fail bool
}

func (c *snapshotHookConnection) Write(p []byte) (int, error) {
	if c.hook != nil {
		hook := c.hook
		c.hook = nil
		hook()
		if c.fail {
			c.fail = false
			return 0, io.ErrClosedPipe
		}
	}
	return c.Buffer.Write(p)
}
func (*snapshotHookConnection) SetWriteDeadline(time.Time) error { return nil }
func (*snapshotHookConnection) Close() error                     { return nil }

type snapshotHookCapture struct {
	fakeCapture
	hook func()
}

func (c *snapshotHookCapture) Stats() (uint64, uint64, error) {
	if c.hook != nil {
		hook := c.hook
		c.hook = nil
		hook()
	}
	return 0, 0, nil
}

func TestFleetSnapshotsPrecedeBlockingStatsAndDelivery(t *testing.T) {
	for _, phase := range []string{"send", "send failure", "statistics"} {
		t.Run(phase, func(t *testing.T) {
			started := time.Now().UTC().Add(-250 * time.Millisecond)
			conn := &snapshotHookConnection{fail: phase == "send failure"}
			f := &Fleet{BatchInterval: 100 * time.Millisecond, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Sender: &BatchSender{connect: func() (batchConnection, error) { return conn, nil }}, slots: map[string]*captureSlot{}}
			for _, name := range []string{"labA", "labB"} {
				f.slots[name] = &captureSlot{capture: &snapshotHookCapture{}, aggregator: NewAggregator(name, 64, started), done: make(chan struct{}), lastFlush: started}
			}
			var observed time.Time
			hook := func() {
				time.Sleep(150 * time.Millisecond)
				observed = time.Now().UTC()
				for range 30 {
					f.slots["labB"].aggregator.Observe(Packet{Direction: model.DirectionInbound, RemoteIP: netip.MustParseAddr("192.0.2.1"), Protocol: "tcp", LocalPort: 22, TCPFlags: 2, Bytes: 60})
				}
			}
			if phase == "statistics" {
				f.slots["labA"].capture.(*snapshotHookCapture).hook = hook
			} else {
				conn.hook = hook
			}
			readB := func() protocol.Batch {
				t.Helper()
				r := bufio.NewReader(&conn.Buffer)
				var result protocol.Batch
				for {
					var b protocol.Batch
					err := protocol.ReadFrame(r, &b)
					if errors.Is(err, io.EOF) {
						break
					}
					if err != nil {
						t.Fatal(err)
					}
					if err := b.Validate(); err != nil {
						t.Fatal(err)
					}
					if b.Interface == "labB" {
						result = b
					}
				}
				return result
			}
			f.flush(time.Now().UTC())
			first := readB()
			if first.Interface != "labB" || first.RXPackets != 0 || !observed.After(first.SentAt) || first.IntervalMillis != first.SentAt.Sub(started).Milliseconds() || !f.slots["labB"].lastFlush.Equal(first.SentAt) {
				t.Fatalf("blocked operation polluted prior snapshot: %+v", first)
			}
			if phase == "send failure" {
				if batches, _, _ := f.Sender.PendingLoss(); batches != 1 {
					t.Fatal("failed snapshot lost bounded delivery health")
				}
			}
			f.flush(time.Now().UTC())
			second := readB()
			if second.RXPackets != 30 || !second.SentAt.After(observed) || second.IntervalMillis != second.SentAt.Sub(first.SentAt).Milliseconds() {
				t.Fatalf("later packets lack matching actual interval: %+v", second)
			}
		})
	}
}
