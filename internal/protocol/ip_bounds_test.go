// SPDX-License-Identifier: MIT
package protocol

import (
	"github.com/littlesho/NodeRampart/internal/model"
	"strings"
	"testing"
	"time"
)

func TestRemoteIPBoundAndCanonicalIdentity(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"192.0.2.1", "192.0.2.1"},
		{"2001:0DB8:0:0:0:0:0:1", "2001:db8::1"},
		{"::FFFF:192.0.2.1", "192.0.2.1"},
		{"ffff:ffff:ffff:ffff:ffff:ffff:255.255.255.255", "ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			b := ipBoundBatch(tc.input)
			if err := b.Validate(); err != nil {
				t.Fatal(err)
			}
			if b.Flows[0].RemoteIP != tc.want {
				t.Fatalf("canonical identity=%q want=%q", b.Flows[0].RemoteIP, tc.want)
			}
		})
	}
	for _, ip := range []string{"fe80::1%eth0", "2001:db8::1%" + strings.Repeat("z", 512<<10), strings.Repeat("x", MaxRemoteIPText+1)} {
		b := ipBoundBatch(ip)
		if err := b.Validate(); err == nil || len(err.Error()) > 256 {
			t.Fatal("invalid IP accepted or included in oversized error")
		}
	}
}

func ipBoundBatch(ip string) Batch {
	return Batch{ProtocolVersion: Version, SentAt: time.Now().UTC(), IntervalMillis: 100, Interface: "lab0", RXPackets: 1, RXBytes: 60,
		Flows: []Flow{{Direction: model.DirectionInbound, RemoteIP: ip, Protocol: "tcp", LocalPort: 22, TCPFlags: 2, Packets: 1, Bytes: 60}}}
}
