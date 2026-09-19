// SPDX-License-Identifier: MIT

package store

import "time"

// Integer-millisecond records in [start,end) satisfy >=ceil(start), <ceil(end).
// UnixMilli floors, including before the epoch; preserve fractional inputs by
// rounding up only when they are not exactly representable in the database.
func ceilUnixMilli(at time.Time) int64 {
	value := at.UnixMilli()
	if !millisecondAligned(at) {
		value++
	}
	return value
}

func millisecondAligned(at time.Time) bool {
	return at.Nanosecond()%int(time.Millisecond) == 0
}
