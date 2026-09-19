// SPDX-License-Identifier: MIT
package detect

import (
	"bufio"
	"bytes"
	"fmt"
	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/model"
	"github.com/littlesho/NodeRampart/internal/protocol"
	"strings"
	"testing"
	"time"
)

func TestValidatedFramesCannotRetainZonesOrDuplicateEquivalentIPKeys(t *testing.T) {
	n := NewNetwork(config.Defaults().Detection)
	now := time.Now().UTC()
	inputs := []string{"192.0.2.1", "::ffff:192.0.2.1", "::ffff:c000:201"}
	for i := range 16 {
		inputs = append(inputs, "2001:db8::1%"+fmt.Sprint(i)+strings.Repeat("z", 512<<10))
	}
	accepted := 0
	for i, ip := range inputs {
		batch := protocol.Batch{ProtocolVersion: protocol.Version, Interface: "lab0", SentAt: now.Add(time.Duration(i) * 100 * time.Millisecond), IntervalMillis: 100, RXPackets: 1, RXBytes: 60, InboundSYN: 1, Flows: []protocol.Flow{{Direction: model.DirectionInbound, RemoteIP: ip, Protocol: "tcp", LocalPort: uint16(i + 1), TCPFlags: 2, Packets: 1, Bytes: 60}}}
		var wire bytes.Buffer
		if err := protocol.WriteFrame(&wire, batch); err != nil {
			t.Fatal(err)
		}
		var decoded protocol.Batch
		if err := protocol.ReadFrame(bufio.NewReader(&wire), &decoded); err != nil {
			t.Fatal(err)
		}
		if err := decoded.Validate(); err != nil {
			continue
		}
		accepted++
		n.Observe(decoded)
	}
	if accepted != 3 || len(n.scans) != 1 || n.scans["192.0.2.1"] == nil || len(n.scans["192.0.2.1"].ports) != 3 {
		t.Fatal("oversized zone or equivalent-address state escaped validation")
	}
}
