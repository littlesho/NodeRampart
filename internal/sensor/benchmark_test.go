// SPDX-License-Identifier: MIT

package sensor

import (
	"encoding/binary"
	"testing"

	"github.com/littlesho/NodeRampart/internal/model"
)

// These in-memory decode measurements establish a repeatable cost baseline.
// They exclude AF_PACKET, scheduling, aggregation, IPC and database work and
// therefore cannot establish live capture throughput or a supported PPS rate.
func BenchmarkDecodeEthernet(b *testing.B) {
	tcp := make([]byte, 14+20+20)
	binary.BigEndian.PutUint16(tcp[12:14], etherTypeIPv4)
	ip := tcp[14:]
	ip[0], ip[8], ip[9] = 0x45, 64, 6
	binary.BigEndian.PutUint16(ip[2:4], 40)
	copy(ip[12:16], []byte{192, 0, 2, 1})
	copy(ip[16:20], []byte{192, 0, 2, 2})
	binary.BigEndian.PutUint16(ip[20:22], 40000)
	binary.BigEndian.PutUint16(ip[22:24], 22)
	ip[32], ip[33] = 0x50, 0x02

	ipv6 := func(payload, next int) []byte {
		frame := make([]byte, 14+40+payload)
		binary.BigEndian.PutUint16(frame[12:14], etherTypeIPv6)
		ip := frame[14:]
		ip[0], ip[6], ip[7] = 0x60, byte(next), 64
		binary.BigEndian.PutUint16(ip[4:6], uint16(payload))
		copy(ip[8:12], []byte{0x20, 0x01, 0x0d, 0xb8})
		copy(ip[24:28], []byte{0x20, 0x01, 0x0d, 0xb8})
		ip[23], ip[39] = 1, 2
		return frame
	}
	fragment := ipv6(16, 44)
	fragment[54] = 60 // Extension header was in the first fragment, now absent.
	binary.BigEndian.PutUint16(fragment[56:58], 16)
	for i := 62; i < len(fragment); i++ {
		fragment[i] = 0xff
	}

	extensions := ipv6(7*8+8, 0)
	for i := 0; i < 7; i++ {
		extensions[54+i*8] = 60
	}
	extensions[54+6*8] = 17
	udp := extensions[54+7*8:]
	binary.BigEndian.PutUint16(udp[0:2], 40000)
	binary.BigEndian.PutUint16(udp[2:4], 53)
	binary.BigEndian.PutUint16(udp[4:6], 8)

	for _, fixture := range []struct {
		name  string
		frame []byte
	}{
		{"IPv4_TCP", tcp}, {"IPv6_noninitial_fragment", fragment}, {"IPv6_seven_extensions_UDP", extensions},
	} {
		b.Run(fixture.name, func(b *testing.B) {
			if _, err := DecodeEthernet(fixture.frame, model.DirectionInbound); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := DecodeEthernet(fixture.frame, model.DirectionInbound); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
