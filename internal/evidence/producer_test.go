// SPDX-License-Identifier: MIT

package evidence

import (
	"strings"
	"testing"
)

func TestProducerOnlyAcceptsVersionAndCommitShapes(t *testing.T) {
	for _, v := range []string{"dev", "unknown", "0.4.0-alpha", "1.2.3", "1.2.3-rc.1"} {
		if safeVersion(v) != v {
			t.Fatal("valid version lost")
		}
	}
	for _, v := range []string{"/synthetic/private", "<script>", "0.4.0-alpha/path", strings.Repeat("x", 1000)} {
		if safeVersion(v) != "unknown" || safeCommit(v) != "unknown" {
			t.Fatal("unvalidated build metadata retained")
		}
	}
	for _, n := range []int{40, 64} {
		if safeCommit(strings.Repeat("a", n)) != strings.Repeat("a", n) {
			t.Fatal("valid commit lost")
		}
	}
	b := fixtureBundle(t)
	b.RuleFingerprint = strings.Repeat("b", 64)
	if err := b.Validate(); err != nil {
		t.Fatal(err)
	}
	b.RuleFingerprint = "synthetic_private_fingerprint"
	if err := b.Validate(); err == nil {
		t.Fatal("unvalidated rule fingerprint accepted")
	}
}
