// SPDX-License-Identifier: MIT

package protocol

import (
	"strings"
	"testing"
	"time"
)

func TestSensorSessionValidationAndHealthDelta(t *testing.T) {
	now := time.Now().UTC()
	b := Batch{ProtocolVersion: Version, SessionID: strings.Repeat("a", 32), Sequence: 1, Interface: "lab0", SentAt: now, IntervalMillis: 100}
	if err := b.ValidateAt(now); err != nil {
		t.Fatal(err)
	}
	for _, session := range []string{"", strings.Repeat("a", 33), strings.Repeat("g", 32)} {
		bad := b
		bad.SessionID = session
		if bad.ValidateAt(now) == nil {
			t.Fatal("invalid session accepted", session)
		}
	}
	old := b
	old.ProtocolVersion = 4
	if old.ValidateAt(now) == nil {
		t.Fatal("legacy protocol accepted v5 identity")
	}
	current := CollectorHealth{KernelPackets: 100, KernelDrops: 2}
	before := CollectorHealth{KernelPackets: 90, KernelDrops: 1}
	delta, err := current.Delta(before)
	if err != nil || delta.KernelPackets != 10 || delta.KernelDrops != 1 {
		t.Fatal(delta, err)
	}
	current.KernelDrops = 0
	if _, err := current.Delta(before); err == nil {
		t.Fatal("negative health delta accepted")
	}
	current = CollectorHealth{KernelPackets: MaxBatchPackets + 1}
	if _, err := current.Delta(CollectorHealth{}); err == nil {
		t.Fatal("health upper bound weakened")
	}
}
