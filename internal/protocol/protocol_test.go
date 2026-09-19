// SPDX-License-Identifier: MIT

package protocol

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/model"
)

func TestFrameRoundTrip(t *testing.T) {
	want := Batch{ProtocolVersion: Version, SentAt: time.Now().UTC(), IntervalMillis: 1000, Interface: "eth0", RXPackets: 2, RXBytes: 120, Flows: []Flow{{Direction: model.DirectionInbound, RemoteIP: "203.0.113.9", Protocol: "tcp", LocalPort: 22, Packets: 2, Bytes: 120}}}
	var buffer bytes.Buffer
	if err := WriteFrame(&buffer, want); err != nil {
		t.Fatal(err)
	}
	var got Batch
	if err := ReadFrame(bufio.NewReader(&buffer), &got); err != nil {
		t.Fatal(err)
	}
	if err := got.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(got.Flows) != 1 || got.Flows[0].RemoteIP != want.Flows[0].RemoteIP {
		t.Fatalf("unexpected round trip: %#v", got)
	}
}

type partialWriter struct{ bytes.Buffer }

func (w *partialWriter) Write(p []byte) (int, error) {
	if len(p) > 3 {
		p = p[:3]
	}
	return w.Buffer.Write(p)
}

func TestWriteFrameHandlesPartialWrites(t *testing.T) {
	w := &partialWriter{}
	if err := WriteFrame(w, map[string]string{"status": "ok"}); err != nil {
		t.Fatal(err)
	}
	var got map[string]string
	if err := ReadFrame(bufio.NewReader(&w.Buffer), &got); err != nil {
		t.Fatal(err)
	}
	if got["status"] != "ok" {
		t.Fatalf("unexpected frame: %#v", got)
	}
}

func TestReadFrameRejectsTrailingJSON(t *testing.T) {
	payload, err := json.Marshal(map[string]string{"status": "ok"})
	if err != nil {
		t.Fatal(err)
	}
	payload = append(payload, []byte(` {}`)...)
	var frame bytes.Buffer
	if err := binary.Write(&frame, binary.BigEndian, uint32(len(payload))); err != nil {
		t.Fatal(err)
	}
	frame.Write(payload)
	var got map[string]string
	if err := ReadFrame(bufio.NewReader(&frame), &got); err == nil {
		t.Fatal("expected trailing JSON to be rejected")
	}
}

func TestBatchRejectsBadIP(t *testing.T) {
	b := Batch{ProtocolVersion: Version, SentAt: time.Now(), IntervalMillis: 1000, Flows: []Flow{{Direction: model.DirectionInbound, RemoteIP: "not-an-ip", Packets: 1, Bytes: 1}}}
	if err := b.Validate(); err == nil {
		t.Fatal("expected invalid IP error")
	}
}

func TestBatchRejectsUnboundedCounters(t *testing.T) {
	b := Batch{ProtocolVersion: Version, SentAt: time.Now(), IntervalMillis: 1000, Flows: []Flow{{Direction: model.DirectionInbound, RemoteIP: "203.0.113.8", Protocol: "tcp", Packets: maxFlowPackets + 1, Bytes: 1}}}
	if err := b.Validate(); err == nil {
		t.Fatal("expected oversized counters to be rejected")
	}
}

func TestBatchProtocolCompatibility(t *testing.T) {
	for _, version := range []int{0, 1, 2, Version, Version + 1} {
		batch := Batch{ProtocolVersion: version, SentAt: time.Now().UTC(), IntervalMillis: 1000, Interface: "eth0"}
		valid := version >= 1 && version <= Version
		var frame bytes.Buffer
		if err := WriteFrame(&frame, batch); err != nil {
			t.Fatal(err)
		}
		var got Batch
		if err := ReadFrame(bufio.NewReader(&frame), &got); err != nil {
			t.Fatal(err)
		}
		if err := got.Validate(); (err == nil) != valid {
			t.Fatalf("protocol version %d: valid=%v, error=%v", version, valid, err)
		}
	}
}

func TestBatchIPCLossCounterBounds(t *testing.T) {
	valid := Batch{ProtocolVersion: Version, SentAt: time.Now().UTC(), IntervalMillis: 1000, Interface: "eth0",
		IPCDroppedBatches: MaxBatchPackets, IPCDroppedPackets: MaxBatchPackets, IPCDroppedBytes: MaxBatchBytes, HealthCountersSaturated: true}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Batch){
		func(b *Batch) { b.IPCDroppedBatches++ },
		func(b *Batch) { b.IPCDroppedPackets++ },
		func(b *Batch) { b.IPCDroppedBytes++ },
	} {
		batch := valid
		change(&batch)
		if err := batch.Validate(); err == nil {
			t.Fatal("unbounded IPC loss counter accepted")
		}
	}
}
