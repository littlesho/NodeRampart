// SPDX-License-Identifier: MIT

package assets

import (
	"context"
	"testing"
)

func TestMMDBValueDeclarationsAreBoundedBeforeReaderAllocation(t *testing.T) {
	for _, value := range [][]byte{
		{0xff, 0xff, 0xff, 0xff},    // A tiny file declares > 16 million map slots.
		{0x1f, 4, 0xff, 0xff, 0xff}, // Equivalent extended array declaration.
		{0x20, 0},                   // Pointer cycle.
		{0x21, 0xff},                // Out-of-range pointer.
		{0x5f, 0xff, 0xff, 0xff},    // Oversized string.
	} {
		if checkMMDBValues(context.Background(), value, false) == nil {
			t.Fatal("unsafe value accepted")
		}
	}
	data := syntheticMMDB("GeoLite2-City", 1767225600)
	// The metadata description map is normally decoded into an allocated map.
	// Replace the metadata root with an excessive declaration: reject before
	// entering any upstream reflect-backed decoder.
	for i := 0; i < len(data)-14; i++ {
		if data[i] == 0xab && data[i+1] == 0xcd && data[i+2] == 0xef {
			data = append(data[:i+14], 0xff, 0xff, 0xff, 0xff)
			break
		}
	}
	if _, err := verifyMMDB(context.Background(), data, "GeoLite2-City"); err == nil {
		t.Fatal("unsafe metadata accepted")
	}
}

func TestMMDBPointerExpansionAndValidSharing(t *testing.T) {
	// A string followed by a pointer to that string is valid shared data.
	if err := checkMMDBValues(context.Background(), []byte{0x41, 'x', 0x20, 0}, false); err != nil {
		t.Fatal(err)
	}
	// A chain of arrays with two aliases per level expands exponentially.
	data := []byte{0x41, 'x'}
	previous := 0
	for range 16 {
		start := len(data)
		data = append(data, 2, 4, 0x20, byte(previous), 0x20, byte(previous))
		previous = start
	}
	if checkMMDBValues(context.Background(), data, false) == nil {
		t.Fatal("exponential value expansion accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if checkMMDBValues(ctx, []byte{0x41, 'x'}, false) == nil {
		t.Fatal("cancelled preflight accepted")
	}
}

func FuzzMMDBValueBounds(f *testing.F) {
	f.Add([]byte{0x41, 'x', 0x20, 0})
	f.Add([]byte{0xff, 0xff, 0xff, 0xff})
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 64<<10 {
			return
		}
		_ = checkMMDBValues(context.Background(), data, false)
	})
}
