// SPDX-License-Identifier: MIT

package billing

import (
	"math"
	"strings"
	"testing"
	"time"
)

func snapshotProfile() Profile {
	return Profile{SchemaVersion: 1, Name: "fixture", Provider: "custom", SourceRegion: "fixture",
		Currency: "USD", EffectiveDate: "2026-01-01", SourceURL: "https://example.com/pricing",
		FreeGB: 1, InternetEgress: []Tier{{UpToGB: 10, PricePerGB: .1}, {PricePerGB: .05}}}
}

func TestExtremeEstimateIsUnavailable(t *testing.T) {
	p := snapshotProfile()
	p.InternetEgress = []Tier{{PricePerGB: 1e308}}
	if p.Validate() == nil {
		t.Fatal("overflowing tariff accepted")
	}
	e := p.Estimate(2 << 30)
	if !e.Unavailable || strings.Contains(e.String(), "Inf") || strings.Contains(e.String(), "0.00 USD") {
		t.Fatalf("unsupported tariff presented as cost: %+v", e)
	}
	p.InternetEgress[0].PricePerGB = MaxPricePerGB
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	e = p.Estimate(math.MaxUint64)
	if e.Unavailable || math.IsInf(e.Cost, 0) || e.Cost <= 0 {
		t.Fatal("valid boundary estimate unavailable")
	}
}

func TestSnapshotReproducesInputsAndRejectsTampering(t *testing.T) {
	p := snapshotProfile()
	now := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)
	s, err := NewSnapshot(p, 12_000_000_000, now.Add(-24*time.Hour), now, now, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	p.InternetEgress[0].PricePerGB = 999
	if s.Estimate.Cost != 1.05 || s.Profile.InternetEgress[0].PricePerGB != .1 {
		t.Fatal("snapshot shares mutable tariff")
	}
	data, err := EncodeSnapshot(s)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := DecodeSnapshot(data)
	if err != nil || restored.Estimate != restored.Profile.Estimate(restored.OutboundBytes) {
		t.Fatal("snapshot cannot reproduce calculation", err)
	}
	for _, bad := range []string{strings.Replace(data, `"outbound_bytes":12000000000`, `"outbound_bytes":13000000000`, 1), data + `{}`, `null`, strings.Repeat(" ", MaxSnapshotBytes+1)} {
		if _, err := DecodeSnapshot(bad); err == nil {
			t.Fatal("invalid snapshot accepted")
		}
	}
	if got, err := DecodeSnapshot(""); got != nil || err != nil {
		t.Fatal("legacy empty snapshot unavailable")
	}
}

func TestSnapshotKeepsSupportedURLWithinEncodingBound(t *testing.T) {
	p := snapshotProfile()
	p.SourceURL = "https://example.com/?" + strings.Repeat("&", 4000)
	now := time.Now().UTC()
	s, err := NewSnapshot(p, 12_000_000_000, now.Add(-time.Hour), now, now, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	data, err := EncodeSnapshot(s)
	if err != nil {
		t.Fatal("valid profile cannot be archived", err)
	}
	roundtrip, err := DecodeSnapshot(data)
	if err != nil || roundtrip.Profile.SourceURL != p.SourceURL {
		t.Fatal("snapshot URL changed", err)
	}
}
