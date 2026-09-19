// SPDX-License-Identifier: MIT

package enrich

import (
	"net/netip"
	"testing"
)

func TestPrivateAddressClassification(t *testing.T) {
	r, err := Open("", "")
	if err != nil {
		t.Fatal(err)
	}
	got := r.Lookup(netip.MustParseAddr("10.0.0.1"))
	if got.CountryCode != "PRIVATE" {
		t.Fatalf("unexpected geo: %#v", got)
	}
}

func TestCleanControlCharacters(t *testing.T) {
	if got := clean(" Example\nNetwork\x00 "); got != "ExampleNetwork" {
		t.Fatalf("got %q", got)
	}
}
