// SPDX-License-Identifier: MIT

package detect

import (
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/model"
)

func TestFleetSeparatesIncidentsAndResetsReconnectContinuity(t *testing.T) {
	cfg := config.Defaults().Detection
	cfg.SYNPacketsPerSecond, cfg.RecoveryWindows = 10, 2
	cfg.UpdateInterval = config.Duration{Duration: time.Hour}
	f := NewFleet(cfg, 2)
	now := time.Now().UTC()
	observe := func(name string, second int, high bool, generation uint64) []model.Event {
		b := networkBatch(now.Add(time.Duration(second) * time.Second))
		b.Interface, b.ConnectionID = name, generation
		if high {
			b.InboundSYN, b.RXPackets, b.RXBytes = 20, 20, 1200
		}
		return f.Observe(b)
	}
	start := observe("labA", 0, true, 1)
	if len(start) != 1 || start[0].Phase != "start" || start[0].Evidence["interface"] != "labA" {
		t.Fatalf("start=%+v", start)
	}
	for second := range 3 {
		if events := observe("labB", second, false, 1); len(events) != 0 {
			t.Fatalf("another link recovered incident: %+v", events)
		}
	}
	if events := observe("labA", 1, false, 1); len(events) != 0 {
		t.Fatalf("early recovery: %+v", events)
	}
	recovery := observe("labA", 2, false, 1)
	if len(recovery) != 1 || recovery[0].Phase != "recovery" || recovery[0].IncidentID != start[0].IncidentID {
		t.Fatalf("recovery=%+v", recovery)
	}
	start = observe("labA", 3, true, 1)
	if len(start) != 1 {
		t.Fatal("missing second incident")
	}
	for _, second := range []int{4, 5} {
		if events := observe("labA", second, false, 2); len(events) != 0 {
			t.Fatalf("reconnect credited unknown quiet period: %+v", events)
		}
	}
	recovery = observe("labA", 6, false, 2)
	if len(recovery) != 1 || recovery[0].IncidentID != start[0].IncidentID {
		t.Fatalf("reconnect recovery=%+v", recovery)
	}
	observe("labC", 7, false, 2)
	stats := f.Stats()
	if len(stats.Interfaces) != 2 || stats.EvictedInterfaces != 1 || stats.Interfaces[0].Name != "labA" || stats.ScanSourceLimit > maxScanSources || stats.UDPRequestLimit > maxUDPRequestTuples {
		t.Fatalf("unbounded state or wrong eviction: %+v", stats)
	}
	if stats.Interfaces[0].Stats.RecoverySuppressedBatches != 1 {
		t.Fatalf("continuity loss not exposed: %+v", stats.Interfaces[0])
	}
}
