// SPDX-License-Identifier: MIT

package billing

import (
	"encoding/json"
	"testing"
)

func TestTieredEstimate(t *testing.T) {
	p := Profile{SchemaVersion: 1, Name: "test", Provider: "custom", SourceRegion: "test-1", Currency: "USD", EffectiveDate: "2026-01-01", SourceURL: "https://example.com/pricing", FreeGB: 1, InternetEgress: []Tier{{UpToGB: 10, PricePerGB: .1}, {UpToGB: 0, PricePerGB: .05}}}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	got := p.Estimate(12_000_000_000)
	if got.BillableGB != 11 || got.Cost < 1.04 || got.Cost > 1.06 {
		t.Fatalf("unexpected estimate: %#v", got)
	}
}

func TestValidateRequiresUnboundedFinalTier(t *testing.T) {
	p := Profile{SchemaVersion: 1, Name: "test", Provider: "custom", SourceRegion: "test-1", Currency: "USD", EffectiveDate: "2026-01-01", SourceURL: "https://example.com/pricing", InternetEgress: []Tier{{UpToGB: 10, PricePerGB: .1}}}
	if err := p.Validate(); err == nil {
		t.Fatal("expected bounded final tier to be rejected")
	}
}

func TestUnitBytesCompatibility(t *testing.T) {
	p := Profile{SchemaVersion: 1, Name: "test", Provider: "custom", SourceRegion: "test-1", Currency: "USD", EffectiveDate: "2026-01-01", SourceURL: "https://example.com/pricing", InternetEgress: []Tier{{PricePerGB: 1}}}
	for _, unit := range []uint64{0, 1_000_000_000, 1 << 30} {
		p.UnitBytes = unit
		if err := p.Validate(); err != nil {
			t.Fatal(err)
		}
		bytesPerUnit := unit
		if unit == 0 {
			bytesPerUnit = 1_000_000_000
		}
		if estimate := p.Estimate(bytesPerUnit); estimate.BillableGB != 1 || estimate.Cost != 1 {
			t.Fatalf("unit %d: %#v", unit, estimate)
		}
		raw, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		var restored Profile
		if err := json.Unmarshal(raw, &restored); err != nil || restored.UnitBytes != unit {
			t.Fatalf("unit round trip: %v", err)
		}
	}
	p.UnitBytes = 1024
	if err := p.Validate(); err == nil {
		t.Fatal("unsupported unit accepted")
	}
}
