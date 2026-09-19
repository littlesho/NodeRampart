// SPDX-License-Identifier: MIT

package protocol

import (
	"bufio"
	"bytes"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/model"
)

func TestRemotePortFrameRoundTrip(t *testing.T) {
	batch := Batch{ProtocolVersion: Version, SentAt: time.Now().UTC(), IntervalMillis: 1000, Interface: "eth0", RXPackets: 1, RXBytes: 60,
		Flows: []Flow{{Direction: model.DirectionInbound, RemoteIP: "192.0.2.1", Protocol: "udp", LocalPort: 40000, RemotePort: 53, Packets: 1, Bytes: 60}}}
	var frame bytes.Buffer
	if err := WriteFrame(&frame, batch); err != nil {
		t.Fatal(err)
	}
	var got Batch
	if err := ReadFrame(bufio.NewReader(&frame), &got); err != nil {
		t.Fatal(err)
	}
	if err := got.Validate(); err != nil {
		t.Fatal(err)
	}
	if got.Flows[0].RemotePort != 53 || got.Flows[0].LocalPort != 40000 {
		t.Fatalf("lost request/reply tuple: %#v", got.Flows[0])
	}
}
