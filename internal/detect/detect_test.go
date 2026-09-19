// SPDX-License-Identifier: MIT

package detect

import (
	"net/netip"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/collector"
	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/model"
	"github.com/littlesho/NodeRampart/internal/protocol"
)

func TestSYNFloodStartAndRecovery(t *testing.T) {
	cfg := config.Defaults().Detection
	cfg.SYNPacketsPerSecond = 100
	cfg.RecoveryWindows = 2
	detector := NewNetwork(cfg)
	now := time.Now().UTC()
	high := protocol.Batch{SentAt: now, IntervalMillis: 1000, InboundSYN: 120, RXPackets: 120, RXBytes: 7200, Flows: []protocol.Flow{{Direction: model.DirectionInbound, RemoteIP: "203.0.113.7", Protocol: "tcp", LocalPort: 443, TCPFlags: 0x02, Packets: 120, Bytes: 7200}}}
	events := detector.Observe(high)
	if len(events) != 1 || events[0].Kind != "syn_flood" || events[0].Phase != "start" {
		t.Fatalf("unexpected start: %#v", events)
	}
	quiet := protocol.Batch{SentAt: now.Add(time.Second), IntervalMillis: 1000}
	if events = detector.Observe(quiet); len(events) != 0 {
		t.Fatalf("early recovery: %#v", events)
	}
	quiet.SentAt = quiet.SentAt.Add(time.Second)
	events = detector.Observe(quiet)
	if len(events) != 1 || events[0].Phase != "recovery" {
		t.Fatalf("missing recovery: %#v", events)
	}
}

func TestFloodRecoveryRequiresConsecutiveQuietWindows(t *testing.T) {
	cfg := config.Defaults().Detection
	cfg.SYNPacketsPerSecond = 100
	cfg.RecoveryRatio = 0.5
	cfg.RecoveryWindows = 2
	detector := NewNetwork(cfg)
	now := time.Now().UTC()
	batch := func(at time.Time, packets uint64) protocol.Batch {
		flows := []protocol.Flow(nil)
		if packets > 0 {
			flows = []protocol.Flow{{Direction: model.DirectionInbound, RemoteIP: "203.0.113.7", Protocol: "tcp", TCPFlags: 0x02, Packets: packets, Bytes: packets * 60}}
		}
		return protocol.Batch{SentAt: at, IntervalMillis: 1000, InboundSYN: packets, RXPackets: packets, RXBytes: packets * 60, Flows: flows}
	}
	detector.Observe(batch(now, 120))
	detector.Observe(batch(now.Add(time.Second), 40))
	detector.Observe(batch(now.Add(2*time.Second), 75))
	if events := detector.Observe(batch(now.Add(3*time.Second), 40)); len(events) != 0 {
		t.Fatalf("non-consecutive quiet window recovered incident: %#v", events)
	}
	if events := detector.Observe(batch(now.Add(4*time.Second), 40)); len(events) != 1 || events[0].Phase != "recovery" {
		t.Fatalf("expected recovery after two consecutive windows: %#v", events)
	}
}

func TestAuthThreshold(t *testing.T) {
	cfg := config.Defaults().Auth
	cfg.Threshold = 3
	cfg.Cooldown = config.Duration{}
	detector := NewAuth(cfg)
	now := time.Now().UTC()
	var event *model.Event
	for i := 0; i < 3; i++ {
		event = detector.Observe(collector.AuthObservation{ObservedAt: now.Add(time.Duration(i) * time.Second), Kind: collector.AuthFailure, SourceIP: netip.MustParseAddr("203.0.113.9"), User: "root", Method: "password"})
	}
	if event == nil || event.Count != 3 || event.SourceRange != "203.0.113.0/24" {
		t.Fatalf("unexpected auth event: %#v", event)
	}
}

func TestAuthPrunesStaleSourcesAndCapsHistory(t *testing.T) {
	cfg := config.Defaults().Auth
	cfg.Threshold = maxFailuresPerAuthSource
	cfg.Cooldown = config.Duration{}
	detector := NewAuth(cfg)
	now := time.Now().UTC()
	detector.failures["192.0.2.1"] = []time.Time{now.Add(-2 * cfg.Window.Duration)}
	detector.lastSeen["192.0.2.1"] = now.Add(-2 * cfg.Window.Duration)
	detector.observed = 1_023
	observation := collector.AuthObservation{ObservedAt: now, Kind: collector.AuthFailure, SourceIP: netip.MustParseAddr("203.0.113.9"), User: "root", Method: "password"}
	detector.Observe(observation)
	if _, exists := detector.failures["192.0.2.1"]; exists {
		t.Fatal("stale source was not pruned")
	}
	for i := 0; i < maxFailuresPerAuthSource+10; i++ {
		observation.ObservedAt = now.Add(time.Duration(i) * time.Millisecond)
		detector.Observe(observation)
	}
	if got := len(detector.failures[observation.SourceIP.String()]); got != maxFailuresPerAuthSource {
		t.Fatalf("history length = %d, want %d", got, maxFailuresPerAuthSource)
	}
}
