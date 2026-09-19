// SPDX-License-Identifier: MIT

package privacy

import "testing"

func TestPrefixMode(t *testing.T) {
	transformer, err := New("prefix", "")
	if err != nil {
		t.Fatal(err)
	}
	full, prefix := transformer.IP("203.0.113.9")
	if full != "" || prefix != "203.0.113.0/24" {
		t.Fatalf("got %q %q", full, prefix)
	}
}
