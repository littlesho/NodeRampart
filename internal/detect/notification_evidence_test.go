// SPDX-License-Identifier: MIT

package detect

import (
	"net/netip"
	"strconv"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/collector"
	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/model"
	"github.com/littlesho/NodeRampart/internal/protocol"
)

func TestAuthenticationNotificationScalarsPreserveMachineEvent(t *testing.T) {
	cfg := config.Defaults().Auth
	cfg.Window = config.Duration{Duration: 60 * time.Second}
	cfg.Threshold = 2
	detector := NewAuth(cfg)
	now := time.Date(2026, 7, 15, 4, 5, 6, 0, time.UTC)
	observation := collector.AuthObservation{ObservedAt: now, Kind: collector.AuthSuccess, Method: "publickey", User: "fixture", SourceIP: netip.MustParseAddr("192.0.2.1")}
	success := detector.Observe(observation)
	if success == nil || success.Summary != "successful SSH publickey authentication" || success.Evidence["method"] != "publickey" || success.Evidence["window_millis"] != "60000" || success.Count != 1 || success.Target != "ssh user=fixture" {
		t.Fatal("success lost template scalars or machine facts", success)
	}
	observation.Kind = collector.AuthFailure
	observation.Method = "password"
	observation.ObservedAt = now.Add(time.Second)
	if event := detector.Observe(observation); event != nil {
		t.Fatal("threshold changed", event)
	}
	observation.ObservedAt = now.Add(2 * time.Second)
	failure := detector.Observe(observation)
	if failure == nil || failure.Summary != "2 SSH authentication failures in 1m0s" || failure.Evidence["window_millis"] != "60000" || failure.Evidence["method"] != "password" || failure.Evidence["count_basis"] != "openssh_final_failure" {
		t.Fatal("failure changed or lacks bounded window", failure)
	}
	for _, event := range []*model.Event{success, failure} {
		if _, ok := event.Evidence["user"]; ok {
			t.Fatal("added duplicate private user evidence")
		}
		if _, ok := event.Evidence["source_ip"]; ok {
			t.Fatal("added duplicate private address evidence")
		}
	}
}

func TestNetworkNotificationScalarsKeepRatesAndActualDurations(t *testing.T) {
	now := time.Date(2026, 7, 15, 4, 5, 6, 0, time.UTC)
	state := &floodState{incidentID: "inc_fixture", started: now.Add(-60001 * time.Millisecond)}
	flow := protocol.Flow{Direction: model.DirectionInbound, RemoteIP: "192.0.2.1", Protocol: "tcp", LocalPort: 443, Packets: 1001, Bytes: 60060}
	event := floodEvent("syn_flood", "recovery", state, 1000.001, 1000.0001, model.SeverityInfo, "pps", flow, now)
	if event.Evidence["observed_rate"] != "1000.001" || event.Evidence["threshold_rate"] != "1000.0001" || event.Evidence["incident_duration_millis"] != "60001" || event.Evidence["threshold"] != "1000 pps" || event.Count != 1000 || event.Target != "tcp/443" {
		t.Fatal("precise scalar or old machine evidence changed", event)
	}
	cfg := config.Defaults().Detection
	cfg.ScanUniquePorts = 2
	cfg.ScanWindow = config.Duration{Duration: time.Minute}
	network := NewNetwork(cfg)
	for i := 0; i < 2; i++ {
		at := now.Add(time.Duration(i) * 59999 * time.Millisecond)
		batch := networkBatch(at, protocol.Flow{Direction: model.DirectionInbound, RemoteIP: "192.0.2.1", Protocol: "tcp", TCPFlags: 2, LocalPort: uint16(80 + i), Packets: 1, Bytes: 60})
		for _, scan := range network.Observe(batch) {
			if scan.Kind == "port_scan" {
				if scan.Evidence["elapsed_millis"] != "59999" || scan.Evidence["threshold"] != "2" || scan.Evidence["window_seconds"] != "60" || scan.Summary != "at least 2 unique local ports probed in 1m0s" {
					t.Fatal("scan changed or duration rounded in evidence", scan)
				}
				return
			}
		}
	}
	t.Fatal("scan threshold never reached")
}

func TestFleetNotificationRetainsActualSampleInterval(t *testing.T) {
	cfg := config.Defaults().Detection
	cfg.SYNPacketsPerSecond = 1
	fleet := NewFleet(cfg, 1)
	at := time.Date(2026, 7, 15, 4, 5, 6, 0, time.UTC)
	batch := networkBatch(at, protocol.Flow{Direction: model.DirectionInbound, RemoteIP: "192.0.2.1", Protocol: "tcp", TCPFlags: 2, LocalPort: 443, Packets: 121, Bytes: 7260})
	batch.IntervalMillis = 60001
	events := fleet.Observe(batch)
	for _, event := range events {
		if event.Kind == "syn_flood" {
			expected := float64(batch.InboundSYN) / (float64(batch.IntervalMillis) / 1000)
			if event.Evidence["observed_rate"] != strconv.FormatFloat(expected, 'f', -1, 64) || event.Evidence["window_millis"] != "60001" || event.Evidence["interface"] != "eth0" {
				t.Fatal("actual interval or precise rate lost", event)
			}
			return
		}
	}
	t.Fatal("missing rate event")
}
