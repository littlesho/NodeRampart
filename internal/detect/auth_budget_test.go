// SPDX-License-Identifier: MIT

package detect

import (
	"fmt"
	"net/netip"
	"testing"
	"time"
	"unsafe"

	"github.com/littlesho/NodeRampart/internal/collector"
	"github.com/littlesho/NodeRampart/internal/config"
)

func TestAuthCompactTimestampsPreserveNanosecondWindowBoundaries(t *testing.T) {
	for _, year := range []int{1970, 2026, 9999} {
		cfg := config.Defaults().Auth
		cfg.Window.Duration, cfg.Cooldown.Duration, cfg.Threshold = time.Minute, 0, 3
		a := NewAuth(cfg)
		now := time.Date(year, 9, 30, 12, 0, 0, 123456789, time.UTC)
		obs := collector.AuthObservation{Kind: collector.AuthFailure, SourceIP: netip.MustParseAddr("192.0.2.7"), Method: "password"}
		for _, at := range []time.Time{now.Add(-time.Minute - time.Nanosecond), now.Add(-time.Minute), now} {
			obs.ObservedAt = at
			if a.Observe(obs) != nil {
				t.Fatalf("year %d expired nanosecond crossed threshold", year)
			}
		}
		if e := a.Observe(obs); e == nil || e.Count != 3 {
			t.Fatalf("year %d lost inclusive boundary: %+v", year, e)
		}
		obs.Kind = collector.AuthSuccess
		obs.ObservedAt = now.Add(-time.Nanosecond)
		if e := a.Observe(obs); e.Evidence["preceding_source_failures"] != "1" {
			t.Fatalf("year %d future failures included: %+v", year, e)
		}
	}
	if unsafe.Sizeof(authTimestamp{}) != 16 {
		t.Fatal("compact timestamp size changed")
	}
}

func TestAuthAllocatedCapacityRefusalAndExpiry(t *testing.T) {
	cfg := config.Defaults().Auth
	cfg.Threshold, cfg.Cooldown.Duration = 8, time.Hour
	a := NewAuth(cfg)
	a.entryLimit = 16
	now := time.Now().UTC()
	for _, source := range []string{"192.0.2.1", "192.0.2.2"} {
		for i := range 8 {
			e := a.Observe(collector.AuthObservation{ObservedAt: now, Kind: collector.AuthFailure, SourceIP: netip.MustParseAddr(source), Method: "password"})
			if (e != nil) != (i == 7) {
				t.Fatal("admitted threshold semantics changed")
			}
		}
	}
	obs := collector.AuthObservation{ObservedAt: now, Kind: collector.AuthFailure, SourceIP: netip.MustParseAddr("192.0.2.3"), Method: "password"}
	if a.Observe(obs) != nil {
		t.Fatal("refused evidence manufactured an alert")
	}
	stats := a.StatsAt(now)
	if stats.FailureEntries != 16 || stats.FailureCapacity != 16 || stats.RejectedFailures != 1 || stats.CoverageComplete || !stats.StateSaturated {
		t.Fatalf("capacity refusal hidden: %+v", stats)
	}
	if a.StatsAt(now.Add(cfg.Window.Duration)).CoverageComplete {
		t.Fatal("inclusive window cutoff hid refused evidence")
	}
	obs.Kind = collector.AuthSuccess
	if e := a.Observe(obs); e.Evidence["preceding_source_failures_complete"] != "false" {
		t.Fatal("partial history was presented as complete", e)
	}
	obs.Kind, obs.ObservedAt = collector.AuthFailure, now.Add(cfg.Window.Duration+time.Nanosecond)
	a.Observe(obs)
	stats = a.StatsAt(obs.ObservedAt)
	if stats.FailureCapacity > 16 || stats.FailureEntries != 1 || !stats.CoverageComplete || stats.RejectedFailures != 1 {
		t.Fatalf("expiry did not release timestamp capacity: %+v", stats)
	}
}

func TestAuthTotalSlotLimitPreservesExistingThresholdEvidence(t *testing.T) {
	cfg := config.Defaults().Auth
	cfg.Threshold, cfg.Cooldown.Duration = maxFailuresPerAuthSource, 0
	a := NewAuth(cfg)
	now := time.Now().UTC()
	for i := range maxAuthFailureEntries / maxFailuresPerAuthSource {
		source := fmt.Sprintf("192.0.2.%d", i+1)
		times := make([]authTimestamp, maxFailuresPerAuthSource)
		for j := range times {
			times[j] = compactAuthTime(now)
		}
		a.failures[source], a.lastSeen[source] = times, now
		a.entries += len(times)
		a.capacity += cap(times)
	}
	newObs := collector.AuthObservation{ObservedAt: now, Kind: collector.AuthFailure, SourceIP: netip.MustParseAddr("192.0.2.200"), Method: "password"}
	a.Observe(newObs)
	if stats := a.StatsAt(now); stats.FailureCapacity != maxAuthFailureEntries || stats.RejectedFailures != 1 || stats.CoverageComplete {
		t.Fatalf("aggregate cap not enforced: %+v", stats)
	}
	newObs.SourceIP = netip.MustParseAddr("192.0.2.1")
	if event := a.Observe(newObs); event == nil || event.Count != maxFailuresPerAuthSource || event.Evidence["detection_window_complete"] != "false" {
		t.Fatalf("existing critical evidence lost at cap: %+v", event)
	}
	if a.capacity != maxAuthFailureEntries {
		t.Fatal("existing history grew beyond capacity")
	}
}
