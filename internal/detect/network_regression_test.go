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

func networkBatch(at time.Time, flows ...protocol.Flow) protocol.Batch {
	batch := protocol.Batch{ProtocolVersion: protocol.Version, SentAt: at, IntervalMillis: 1000, Interface: "eth0", Flows: flows}
	for _, flow := range flows {
		if flow.Direction == model.DirectionInbound {
			batch.RXBytes += flow.Bytes
			batch.RXPackets += flow.Packets
			switch flow.Protocol {
			case "tcp":
				if flow.TCPFlags&0x12 == 0x02 {
					batch.InboundSYN += flow.Packets
				}
			case "udp":
				batch.InboundUDP += flow.Packets
			case "icmp", "icmpv6":
				batch.InboundICMP += flow.Packets
			}
		} else {
			batch.TXBytes += flow.Bytes
			batch.TXPackets += flow.Packets
		}
	}
	return batch
}

func scanFlows(transport string, flags uint8) []protocol.Flow {
	var flows []protocol.Flow
	for port := 40000; port < 40020; port++ {
		flows = append(flows, protocol.Flow{Direction: model.DirectionInbound, RemoteIP: "192.0.2.7", Protocol: transport, LocalPort: uint16(port), RemotePort: 53, TCPFlags: flags, Packets: 1, Bytes: 60})
	}
	return flows
}

func hasScan(events []model.Event) bool {
	for _, event := range events {
		if event.Kind == "port_scan" {
			return true
		}
	}
	return false
}

func warmUDP(n *Network, now time.Time) time.Time {
	for i := 0; i <= int(udpReplyWindow/time.Second); i++ {
		n.Observe(networkBatch(now.Add(time.Duration(i) * time.Second)))
	}
	return now.Add(udpReplyWindow + time.Second)
}

func TestNetworkTCPScanExcludesRepliesAndNonSYNFlags(t *testing.T) {
	for _, flags := range []uint8{0x12, 0x10, 0x18, 0x11, 0x04, 0x01, 0x03, 0x06, 0x00} {
		t.Run(fmt.Sprintf("flags_%02x", flags), func(t *testing.T) {
			n := NewNetwork(config.Defaults().Detection)
			if events := n.Observe(networkBatch(time.Now().UTC(), scanFlows("tcp", flags)...)); hasScan(events) {
				t.Fatalf("ordinary replies/non-SYN traffic became a SYN scan: %#v", events)
			}
			if n.Stats().ScanSources != 0 {
				t.Fatal("reply traffic consumed scan state")
			}
		})
	}
	for _, flags := range []uint8{0x02, 0xc2} {
		n := NewNetwork(config.Defaults().Detection)
		if !hasScan(n.Observe(networkBatch(time.Now().UTC(), scanFlows("tcp", flags)...))) {
			t.Fatalf("SYN probes with flags %02x did not trigger", flags)
		}
	}
}

func TestNetworkUDPReplyCorrelationAcrossBatchesAndWithinBatch(t *testing.T) {
	for _, sameBatch := range []bool{false, true} {
		t.Run(fmt.Sprint(sameBatch), func(t *testing.T) {
			n := NewNetwork(config.Defaults().Detection)
			now := warmUDP(n, time.Now().UTC())
			replies := scanFlows("udp", 0)
			requests := append([]protocol.Flow(nil), replies...)
			for i := range requests {
				requests[i].Direction = model.DirectionOutbound
			}
			var events []model.Event
			if sameBatch {
				// Replies intentionally precede their outbound requests.
				events = n.Observe(networkBatch(now, append(replies, requests...)...))
			} else {
				n.Observe(networkBatch(now, requests...))
				events = n.Observe(networkBatch(now.Add(time.Second), replies...))
			}
			stats := n.Stats()
			if hasScan(events) || stats.ScanSources != 0 || stats.UDPRepliesExcludedPackets != 20 {
				t.Fatalf("UDP replies misclassified: stats=%#v events=%#v", stats, events)
			}
		})
	}
}

func TestNetworkUDPReplyRequiresMatchingRemotePortAndExpires(t *testing.T) {
	for _, changePort := range []bool{false, true} {
		t.Run(fmt.Sprint(changePort), func(t *testing.T) {
			n := NewNetwork(config.Defaults().Detection)
			now := warmUDP(n, time.Now().UTC())
			replies := scanFlows("udp", 0)
			requests := append([]protocol.Flow(nil), replies...)
			for i := range requests {
				requests[i].Direction = model.DirectionOutbound
			}
			n.Observe(networkBatch(now, requests...))
			if changePort {
				for i := range replies {
					replies[i].RemotePort++
				}
				now = now.Add(time.Second)
			} else {
				for i := 1; i < int(udpReplyWindow/time.Second); i++ {
					n.Observe(networkBatch(now.Add(time.Duration(i) * time.Second)))
				}
				now = now.Add(udpReplyWindow)
			}
			if !hasScan(n.Observe(networkBatch(now, replies...))) {
				t.Fatal("unmatched UDP probes were suppressed")
			}
		})
	}
}

func TestNetworkUDPUnknownCoverageDoesNotInventProbes(t *testing.T) {
	for _, mode := range []string{"cold", "gap", "loss", "legacy", "missing_port"} {
		t.Run(mode, func(t *testing.T) {
			n := NewNetwork(config.Defaults().Detection)
			now := time.Now().UTC()
			if mode != "cold" {
				now = warmUDP(n, now)
			}
			batch := networkBatch(now, scanFlows("udp", 0)...)
			switch mode {
			case "gap":
				batch.SentAt = now.Add(time.Second)
			case "loss":
				batch.KernelDrops = 1
			case "legacy":
				batch.ProtocolVersion = 2
			case "missing_port":
				for i := range batch.Flows {
					batch.Flows[i].RemotePort = 0
				}
			}
			if hasScan(n.Observe(batch)) || n.Stats().UDPUnclassifiedPackets != 20 {
				t.Fatalf("unknown UDP coverage became a probe: %#v", n.Stats())
			}
		})
	}
}

func TestNetworkUDPStateBoundAndCoverageOnAdmissionLoss(t *testing.T) {
	n := NewNetwork(config.Defaults().Detection)
	now := warmUDP(n, time.Now().UTC())
	for i := 0; i < maxUDPRequestTuples; i++ {
		n.udpRequests[udpTuple{"eth0", "198.51.100.1", uint16(i + 1), 53}] = now
	}
	requests := scanFlows("udp", 0)
	for i := range requests {
		requests[i].Direction = model.DirectionOutbound
	}
	flows := append(scanFlows("udp", 0), requests...)
	if hasScan(n.Observe(networkBatch(now, flows...))) {
		t.Fatal("untracked UDP requests caused a false scan")
	}
	stats := n.Stats()
	if stats.UDPRequestTuples != maxUDPRequestTuples || !stats.UDPStateSaturated || stats.UDPRequestIgnoredPackets != 20 || stats.UDPUnclassifiedPackets != 20 || stats.UDPScanCoverageComplete {
		t.Fatalf("UDP saturation not visible: %#v", stats)
	}
	for i := 1; i < int(udpReplyWindow/time.Second); i++ {
		n.Observe(networkBatch(now.Add(time.Duration(i) * time.Second)))
	}
	n.Observe(networkBatch(now.Add(udpReplyWindow), requests...))
	if n.Stats().UDPRequestTuples != len(requests) {
		t.Fatal("expired UDP tuples blocked admission")
	}
}

func TestNetworkPrunesExpiredScansBeforeAdmission(t *testing.T) {
	n := NewNetwork(config.Defaults().Detection)
	now := time.Now().UTC()
	for i := 0; i < maxScanSources; i++ {
		n.scans[fmt.Sprint(i)] = &scanState{started: now.Add(-2 * n.config.ScanWindow.Duration), last: now, ports: map[uint16]struct{}{22: {}}}
	}
	if !hasScan(n.Observe(networkBatch(now, scanFlows("tcp", 0x02)...))) {
		t.Fatal("expired scan windows blocked a new source")
	}
	if stats := n.Stats(); stats.ScanSources != 1 || stats.ScanIgnoredPackets != 0 {
		t.Fatalf("unexpected scan state: %#v", stats)
	}
}

func TestNetworkScanAdmissionLossAndBoundedPortEvidence(t *testing.T) {
	n := NewNetwork(config.Defaults().Detection)
	now := time.Now().UTC()
	for i := 0; i < maxScanSources; i++ {
		n.scans[fmt.Sprint(i)] = &scanState{started: now, last: now, ports: map[uint16]struct{}{22: {}}}
	}
	n.Observe(networkBatch(now, scanFlows("tcp", 0x02)...))
	if stats := n.Stats(); stats.ScanSources != maxScanSources || !stats.ScanStateSaturated || stats.ScanIgnoredPackets != 20 {
		t.Fatalf("scan saturation hidden: %#v", stats)
	}
	n = NewNetwork(config.Defaults().Detection)
	flows := scanFlows("tcp", 0x02)
	for i := 0; i < 100; i++ {
		flow := flows[0]
		flow.LocalPort = uint16(i + 1)
		flows = append(flows, flow)
	}
	n.Observe(networkBatch(now, flows...))
	if len(n.scans["192.0.2.7"].ports) != n.config.ScanUniquePorts {
		t.Fatal("scan retained ports after enough evidence")
	}
}

func TestNetworkFloodRecoveryRequiresCompleteConsecutiveBatches(t *testing.T) {
	mutations := map[string]func(*protocol.Batch){
		"kernel_drop":    func(b *protocol.Batch) { b.KernelDrops = 1 },
		"kernel_stats":   func(b *protocol.Batch) { b.KernelStatsErrors = 1 },
		"parse_error":    func(b *protocol.Batch) { b.ParseErrors = 1 },
		"flow_overflow":  func(b *protocol.Batch) { b.OverflowPackets = 1 },
		"overflow_bytes": func(b *protocol.Batch) { b.OverflowBytes = 1 },
		"ipc_batch":      func(b *protocol.Batch) { b.IPCDroppedBatches = 1 },
		"ipc_packet":     func(b *protocol.Batch) { b.IPCDroppedPackets = 1 },
		"ipc_bytes":      func(b *protocol.Batch) { b.IPCDroppedBytes = 1 },
		"saturation":     func(b *protocol.Batch) { b.HealthCountersSaturated = true },
		"gap":            func(b *protocol.Batch) { b.SentAt = b.SentAt.Add(time.Second) },
		"overlap":        func(b *protocol.Batch) { b.IntervalMillis = 2000 },
		"interface":      func(b *protocol.Batch) { b.Interface = "eth1" },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			cfg := config.Defaults().Detection
			cfg.SYNPacketsPerSecond = 100
			cfg.RecoveryWindows = 2
			n := NewNetwork(cfg)
			now := time.Now().UTC()
			high := networkBatch(now)
			high.InboundSYN = 120
			n.Observe(high)
			n.Observe(networkBatch(now.Add(time.Second)))
			quiet := networkBatch(now.Add(2 * time.Second))
			mutate(&quiet)
			if events := n.Observe(quiet); len(events) != 0 {
				t.Fatalf("unreliable batch recovered incident: %#v", events)
			}
			if n.Stats().RecoverySuppressedBatches != 1 {
				t.Fatal("recovery suppression not visible")
			}
			quiet = networkBatch(quiet.SentAt.Add(time.Second))
			quiet.Interface = n.lastInterface
			if events := n.Observe(quiet); len(events) != 0 {
				t.Fatalf("quiet sequence survived lost evidence: %#v", events)
			}
			quiet.SentAt = quiet.SentAt.Add(time.Second)
			if events := n.Observe(quiet); len(events) != 1 || events[0].Phase != "recovery" {
				t.Fatalf("complete windows did not recover: %#v", events)
			}
		})
	}
}

func TestNetworkFloodTopSourceAggregatesAcrossFlows(t *testing.T) {
	cfg := config.Defaults().Detection
	cfg.SYNPacketsPerSecond = 1
	cfg.BytesPerSecond = 1
	n := NewNetwork(cfg)
	flows := []protocol.Flow{
		{Direction: model.DirectionInbound, RemoteIP: "192.0.2.1", Protocol: "tcp", LocalPort: 22, TCPFlags: 2, Packets: 60, Bytes: 3600},
		{Direction: model.DirectionInbound, RemoteIP: "192.0.2.1", Protocol: "tcp", LocalPort: 443, TCPFlags: 2, Packets: 60, Bytes: 3600},
		{Direction: model.DirectionInbound, RemoteIP: "192.0.2.2", Protocol: "tcp", LocalPort: 80, TCPFlags: 2, Packets: 100, Bytes: 6000},
	}
	events := n.Observe(networkBatch(time.Now().UTC(), flows...))
	if len(events) != 2 {
		t.Fatalf("unexpected floods: %#v", events)
	}
	for _, event := range events {
		if event.SourceIP != "192.0.2.1" || event.Target != "tcp" || event.Evidence["top_source_packets"] != "120" || event.Evidence["top_source_bytes"] != "7200" {
			t.Fatalf("misleading top source: %#v", event)
		}
	}
}
