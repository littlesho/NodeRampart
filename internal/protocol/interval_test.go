// SPDX-License-Identifier: MIT

package protocol

import (
	"testing"
	"time"
)

func TestElapsedIntervalBoundsAndLegacyCompatibility(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	for _, version := range []int{1, 2, 3, Version} {
		for _, millis := range []int64{99, 100, 59999, 60000, 60001, 65000, 65001} {
			b := Batch{ProtocolVersion: version, SentAt: now, IntervalMillis: millis, Interface: "lab0"}
			valid := millis >= 100 && millis <= 60000 || version >= 4 && millis >= 100 && millis <= 65000
			if err := b.ValidateAt(now); (err == nil) != valid {
				t.Errorf("version=%d interval=%d valid=%v error=%v", version, millis, valid, err)
			}
		}
	}
	for _, tc := range []struct{ nominal, elapsed time.Duration }{
		{100 * time.Millisecond, 350 * time.Millisecond},
		{time.Second, 1250 * time.Millisecond},
		{2 * time.Second, 2250 * time.Millisecond},
		{10 * time.Second, 11 * time.Second},
		{time.Minute, 65 * time.Second},
	} {
		if got := MaxElapsedInterval(tc.nominal); got != tc.elapsed {
			t.Errorf("nominal=%s limit=%s want=%s", tc.nominal, got, tc.elapsed)
		}
	}
}
