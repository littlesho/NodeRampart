// SPDX-License-Identifier: MIT

package detect

import (
	"fmt"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/model"
	"github.com/littlesho/NodeRampart/internal/protocol"
)

func scanBudgetFlow(source string, port uint16) protocol.Flow {
	return protocol.Flow{Direction: model.DirectionInbound, RemoteIP: source, Protocol: "tcp", TCPFlags: 2, LocalPort: port, Packets: 1, Bytes: 60}
}

func TestFleetSharedPortBudgetPreserves65535ThresholdAtEightInterfaces(t *testing.T) {
	cfg := config.Defaults().Detection
	cfg.ScanUniquePorts, cfg.SYNPacketsPerSecond = 65535, 0
	f := NewFleet(cfg, 8)
	now := time.Now().UTC()
	events := 0
	for first := 1; first <= 65535; first += protocol.MaxFlowsPerBatch {
		b := protocol.Batch{ProtocolVersion: protocol.Version, SentAt: now.Add(time.Duration(first) * time.Millisecond), IntervalMillis: 4096, Interface: "lab0"}
		for port := first; port <= min(first+protocol.MaxFlowsPerBatch-1, 65535); port++ {
			b.Flows = append(b.Flows, scanBudgetFlow("192.0.2.1", uint16(port)))
			b.RXPackets++
			b.RXBytes += 60
		}
		// A 65535-port observation fits the legal scan window; timestamps need
		// not count real packet arrival order inside each summarized batch.
		b.SentAt = now.Add(time.Duration(first) * time.Microsecond)
		for _, e := range f.Observe(b) {
			if e.Kind == "port_scan" {
				events++
				if e.Count != 65535 {
					t.Fatal("threshold count changed")
				}
			}
		}
	}
	if stats := f.Stats(); events != 1 || stats.ScanPortEntries != 65535 || stats.ScanPortLimit != maxScanPortEntries || stats.ScanIgnoredPackets != 0 {
		t.Fatalf("legal threshold became unreachable: events=%d stats=%+v", events, stats)
	}
}

func TestFleetSharedPortBudgetRefusalExpiryAndEviction(t *testing.T) {
	cfg := config.Defaults().Detection
	cfg.ScanUniquePorts, cfg.SYNPacketsPerSecond = 20, 0
	f := NewFleet(cfg, 2)
	f.portBudget.limit = 4
	now := time.Now().UTC()
	for i, name := range []string{"lab0", "lab1"} {
		b := protocol.Batch{SentAt: now, Interface: name, IntervalMillis: 1000, Flows: []protocol.Flow{scanBudgetFlow(fmt.Sprintf("192.0.2.%d", i+1), 1), scanBudgetFlow(fmt.Sprintf("192.0.2.%d", i+1), 2)}}
		f.Observe(b)
	}
	f.Observe(protocol.Batch{SentAt: now.Add(time.Second), Interface: "lab1", IntervalMillis: 1000, Flows: []protocol.Flow{scanBudgetFlow("192.0.2.2", 3)}})
	if stats := f.Stats(); stats.ScanPortEntries != 4 || stats.ScanIgnoredPackets != 1 || !stats.ScanPortStateSaturated || stats.ScanCoverageComplete || stats.ScanCoverageIncompleteUntil.IsZero() {
		t.Fatalf("global port refusal hidden: %+v", stats)
	}
	// Churn retires the old interface's entire allocation before opening new.
	f.Observe(protocol.Batch{SentAt: now.Add(2 * time.Second), Interface: "lab2", IntervalMillis: 1000, Flows: []protocol.Flow{scanBudgetFlow("192.0.2.3", 1)}})
	if stats := f.Stats(); stats.ScanPortEntries != 3 || stats.EvictedInterfaces != 1 {
		t.Fatalf("eviction leaked shared port allocation: %+v", stats)
	}
	f.Observe(protocol.Batch{SentAt: now.Add(cfg.ScanWindow.Duration + 3*time.Second), Interface: "lab1", IntervalMillis: 1000})
	if stats := f.Stats(); stats.ScanPortEntries != 1 || stats.ScanPortStateSaturated || !stats.ScanCoverageComplete {
		t.Fatalf("expiry leaked shared port allocation: %+v", stats)
	}
}
