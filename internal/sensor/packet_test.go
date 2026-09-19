// SPDX-License-Identifier: MIT

package sensor

import (
	"bytes"
	"encoding/binary"
	"net/netip"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/model"
	"github.com/littlesho/NodeRampart/internal/protocol"
)

func TestDecodeIPv4TCP(t *testing.T) {
	frame := make([]byte, 14+20+20)
	binary.BigEndian.PutUint16(frame[12:14], etherTypeIPv4)
	ip := frame[14:]
	ip[0] = 0x45
	binary.BigEndian.PutUint16(ip[2:4], 40)
	ip[8], ip[9] = 64, 6
	copy(ip[12:16], []byte{203, 0, 113, 9})
	copy(ip[16:20], []byte{192, 0, 2, 5})
	tcp := ip[20:]
	binary.BigEndian.PutUint16(tcp[0:2], 40000)
	binary.BigEndian.PutUint16(tcp[2:4], 22)
	tcp[12], tcp[13] = 0x50, 0x02
	packet, err := DecodeEthernet(frame, model.DirectionInbound)
	if err != nil {
		t.Fatal(err)
	}
	if packet.RemoteIP.String() != "203.0.113.9" || packet.Protocol != "tcp" || packet.LocalPort != 22 || packet.TCPFlags != 0x02 {
		t.Fatalf("unexpected packet: %#v", packet)
	}
}

func TestAggregatorBoundsCardinality(t *testing.T) {
	now := time.Now()
	a := NewAggregator("eth0", 2, now)
	for _, ip := range []string{"192.0.2.1", "192.0.2.2", "192.0.2.3"} {
		frame := makeIPv4UDP(t, ip)
		packet, err := DecodeEthernet(frame, model.DirectionInbound)
		if err != nil {
			t.Fatal(err)
		}
		a.Observe(packet)
	}
	batch := a.Flush(now.Add(time.Second))
	if len(batch.Flows) != 2 || batch.OverflowPackets != 1 || batch.InboundUDP != 3 {
		t.Fatalf("unexpected bounded batch: %#v", batch)
	}
}

func TestAggregatorCapacityFitsProtocolFrame(t *testing.T) {
	now := time.Now().UTC()
	aggregator := NewAggregator("eth0", protocol.MaxFlowsPerBatch, now)
	for i := 0; i <= protocol.MaxFlowsPerBatch; i++ {
		aggregator.Observe(Packet{Direction: model.DirectionInbound, RemoteIP: netip.MustParseAddr("2001:db8:ffff:ffff:ffff:ffff:ffff:ffff"), Protocol: "udp", LocalPort: uint16(i + 1), Bytes: 128})
	}
	batch := aggregator.Flush(now.Add(time.Second))
	if len(batch.Flows) != protocol.MaxFlowsPerBatch || batch.RXPackets != protocol.MaxFlowsPerBatch+1 || batch.OverflowPackets != 1 || batch.OverflowBytes != 128 {
		t.Fatal("capacity boundary did not retain global totals and overflow")
	}
	if err := batch.Validate(); err != nil {
		t.Fatal(err)
	}
	var frame bytes.Buffer
	if err := protocol.WriteFrame(&frame, batch); err != nil {
		t.Fatalf("maximum configured capacity cannot be sent: %v", err)
	}
}

func makeIPv4UDP(t *testing.T, source string) []byte {
	t.Helper()
	address, err := netip.ParseAddr(source)
	if err != nil || !address.Is4() {
		t.Fatalf("invalid test IP %q", source)
	}
	octets := address.As4()
	frame := make([]byte, 14+20+8)
	binary.BigEndian.PutUint16(frame[12:14], etherTypeIPv4)
	ip := frame[14:]
	ip[0] = 0x45
	binary.BigEndian.PutUint16(ip[2:4], 28)
	ip[9] = 17
	copy(ip[12:16], octets[:])
	copy(ip[16:20], []byte{192, 0, 2, 5})
	binary.BigEndian.PutUint16(ip[20:22], 40000)
	binary.BigEndian.PutUint16(ip[22:24], 53)
	return frame
}

func FuzzDecodeEthernet(f *testing.F) {
	f.Add([]byte{})
	seed := make([]byte, 14+20+8)
	binary.BigEndian.PutUint16(seed[12:14], etherTypeIPv4)
	seed[14] = 0x45
	binary.BigEndian.PutUint16(seed[16:18], 28)
	seed[23] = 17
	copy(seed[26:30], []byte{192, 0, 2, 1})
	copy(seed[30:34], []byte{192, 0, 2, 2})
	f.Add(seed)
	f.Fuzz(func(t *testing.T, frame []byte) {
		_, _ = DecodeEthernet(frame, model.DirectionInbound)
		_, _ = DecodeEthernet(frame, model.DirectionOutbound)
	})
}
