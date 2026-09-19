// SPDX-License-Identifier: MIT

package sensor

import (
	"encoding/binary"
	"net/netip"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/model"
)

func TestIPv6NonInitialFragmentNeverTraversesFragmentPayload(t *testing.T) {
	for _, next := range []uint8{60, 0, 43, 51, 44, 17, 6} {
		frame := make([]byte, 14+40+8+8)
		binary.BigEndian.PutUint16(frame[12:14], etherTypeIPv6)
		ip := frame[14:]
		ip[0], ip[6], ip[7] = 0x60, 44, 64
		binary.BigEndian.PutUint16(ip[4:6], 16)
		source := netip.MustParseAddr("2001:db8::1").As16()
		copy(ip[8:24], source[:])
		dest := netip.MustParseAddr("2001:db8::2").As16()
		copy(ip[24:40], dest[:])
		ip[40] = next
		binary.BigEndian.PutUint16(ip[42:44], 16)
		for i := 48; i < len(ip); i++ {
			ip[i] = 0xff
		}
		packet, err := DecodeEthernet(frame, model.DirectionInbound)
		if err != nil {
			t.Fatalf("next header %d: valid fragment rejected: %v", next, err)
		}
		if packet.RemoteIP.String() != "2001:db8::1" || packet.Bytes != uint64(len(frame)) || packet.LocalPort != 0 || packet.RemotePort != 0 || packet.TCPFlags != 0 {
			t.Fatalf("fragment fabricated transport data or lost IP accounting: %#v", packet)
		}
		wantProtocol := "other"
		if next == 17 {
			wantProtocol = "udp"
		}
		if next == 6 {
			wantProtocol = "tcp"
		}
		if packet.Protocol != wantProtocol {
			t.Fatalf("next %d protocol=%s want=%s", next, packet.Protocol, wantProtocol)
		}
	}
}

func TestIPv6FirstFragmentTraversesRemainingExtensions(t *testing.T) {
	frame := make([]byte, 14+40+8+8+8)
	binary.BigEndian.PutUint16(frame[12:14], etherTypeIPv6)
	ip := frame[14:]
	ip[0], ip[6] = 0x60, 44
	binary.BigEndian.PutUint16(ip[4:6], 24)
	ip[40], ip[48] = 60, 17
	binary.BigEndian.PutUint16(ip[42:44], 1) // Offset zero, more fragments.
	binary.BigEndian.PutUint16(ip[56:58], 53)
	binary.BigEndian.PutUint16(ip[58:60], 40000)
	packet, err := DecodeEthernet(frame, model.DirectionInbound)
	if err != nil || packet.Protocol != "udp" || packet.LocalPort != 40000 || packet.RemotePort != 53 {
		t.Fatalf("first fragment lost UDP tuple: %#v, %v", packet, err)
	}
}

func TestDecodeTransportPortsFollowDirection(t *testing.T) {
	for _, transport := range []uint8{6, 17} {
		data := make([]byte, 20)
		binary.BigEndian.PutUint16(data[0:2], 53)
		binary.BigEndian.PutUint16(data[2:4], 40000)
		for _, direction := range []model.Direction{model.DirectionInbound, model.DirectionOutbound} {
			packet := Packet{Direction: direction}
			decodeTransport(&packet, transport, data, true)
			local, remote := uint16(40000), uint16(53)
			if direction == model.DirectionOutbound {
				local, remote = remote, local
			}
			if packet.LocalPort != local || packet.RemotePort != remote {
				t.Fatalf("protocol %d direction %s tuple %#v", transport, direction, packet)
			}
		}
	}
}

func TestAggregatorPreservesSeparateRemotePortTuples(t *testing.T) {
	now := time.Now().UTC()
	aggregator := NewAggregator("eth0", 10, now)
	for _, remotePort := range []uint16{53, 123, 53} {
		aggregator.Observe(Packet{Direction: model.DirectionOutbound, RemoteIP: netip.MustParseAddr("192.0.2.1"), Protocol: "udp", LocalPort: 40000, RemotePort: remotePort, Bytes: 60})
	}
	batch := aggregator.Flush(now.Add(time.Second))
	if len(batch.Flows) != 2 {
		t.Fatalf("distinct remote tuples merged: %#v", batch.Flows)
	}
	counts := map[uint16]uint64{}
	for _, flow := range batch.Flows {
		counts[flow.RemotePort] += flow.Packets
	}
	if counts[53] != 2 || counts[123] != 1 || batch.TXPackets != 3 {
		t.Fatalf("wrong tuple aggregation: %#v", batch)
	}
}
