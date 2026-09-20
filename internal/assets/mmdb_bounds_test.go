// SPDX-License-Identifier: MIT

package assets

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
	"unsafe"
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

// Shared arrays contain only synthetic one-byte strings; repeated pointers are
// top-level entries, so sharing is legitimate rather than one oversized record.
func sharedMMDBValues(references, children int) []byte {
	data := []byte{byte(children), 4}
	for range children {
		data = append(data, 0x41, 'x')
	}
	for range references {
		data = append(data, 0x20, 0)
	}
	return data
}

func TestMMDBCompletedTargetMemoization(t *testing.T) {
	data := sharedMMDBValues(1000, 20)
	p := newMMDBValueBounds(context.Background(), data)
	p.workLimit = 3000
	if err := p.check(false); err != nil {
		t.Fatal(err)
	}
	if p.expandedValues != 21+1000*22 || p.expandedBytes != 1001*(20*128+20) {
		t.Fatalf("lost logical expansion charge: values=%d bytes=%d", p.expandedValues, p.expandedBytes)
	}
	if p.cacheHits != 999 || p.operations != 2041 || p.maxRecordValues != 22 || p.maxRecordBytes != 2580 {
		t.Fatalf("unexpected sharing counts: work=%d hits=%d record=%d/%d", p.operations, p.cacheHits, p.maxRecordValues, p.maxRecordBytes)
	}
	uncached := newMMDBValueBounds(context.Background(), data)
	uncached.cacheSlots, uncached.workLimit = 0, p.workLimit
	if !errors.Is(uncached.check(false), errMMDBResourceBudget) || uncached.operations != 3001 {
		t.Fatal("uncached recursive work should exhaust the same physical budget")
	}
	// A cached target's end must not replace the pointer's own end: an unsafe
	// encoding after the pointer must still be visited and rejected.
	if checkMMDBValues(context.Background(), append(data, 0xff), false) == nil {
		t.Fatal("trailing invalid declaration skipped")
	}
}

func TestMMDBCachedExpansionBudgets(t *testing.T) {
	for _, which := range []string{"values", "bytes"} {
		t.Run(which, func(t *testing.T) {
			p := newMMDBValueBounds(context.Background(), sharedMMDBValues(1000, 20))
			if which == "values" {
				p.valueLimit = 100
			} else {
				p.byteLimit = 10000
			}
			if !errors.Is(p.check(false), errMMDBResourceBudget) || p.cacheHits == 0 {
				t.Fatal("Verify expansion budget bypassed through cache")
			}
		})
	}
	// Many uses of a validated 64 KiB string exceed one record's allocation
	// charge even though the pointer target itself is small enough to accept.
	data := append([]byte{0x5e, 0xfe, 0xe3}, bytes.Repeat([]byte{'x'}, 65536)...)
	// 65536 - 285 == 65251 (0xfee3).
	data = append(data, 29, 4, 100) // array of 129 pointers
	for range 129 {
		data = append(data, 0x20, 0)
	}
	p := newMMDBValueBounds(context.Background(), data)
	if !errors.Is(p.check(false), errMMDBResourceBudget) || p.cacheHits == 0 || p.recordBytes <= mmdbRecordByteLimit {
		t.Fatalf("cached bytes bypassed record bound: bytes=%d hits=%d", p.recordBytes, p.cacheHits)
	}
	// The existing exponential fanout fixture also warms the cache before a
	// subsequent record exceeds its logical value limit.
	data = []byte{0x41, 'x'}
	previous := 0
	for range 16 {
		start := len(data)
		data = append(data, 2, 4, 0x20, byte(previous), 0x20, byte(previous))
		previous = start
	}
	p = newMMDBValueBounds(context.Background(), data)
	if !errors.Is(p.check(false), errMMDBResourceBudget) || p.cacheHits == 0 || p.recordValues <= mmdbRecordValueLimit {
		t.Fatal("cached fanout bypassed record value bound")
	}
}

func TestMMDBCacheRelativeDepth(t *testing.T) {
	for _, containers := range []int{28, 29} {
		// target at zero has relative height 3. The first pointer warms it at
		// depth 1. Reuse at depth containers+1 must add that same height.
		data := []byte{1, 4, 1, 4, 1, 4, 0x41, 'x', 0x20, 0}
		for range containers {
			data = append(data, 1, 4)
		}
		data = append(data, 0x20, 0)
		p := newMMDBValueBounds(context.Background(), data)
		err := p.check(false)
		if containers == 28 && err != nil || containers == 29 && !errors.Is(err, errMMDBResourceBudget) || p.cacheHits == 0 {
			t.Fatalf("containers=%d, depth=%d hits=%d err=%v", containers, p.maxDepth, p.cacheHits, err)
		}
	}
}

func TestMMDBCacheCapacityAndCollisions(t *testing.T) {
	data := []byte{0x41, 'a', 0x41, 'b'}
	for range 100 {
		data = append(data, 0x20, 0, 0x20, 2)
	}
	p := newMMDBValueBounds(context.Background(), data)
	p.cacheSlots = 1
	if err := p.check(false); err != nil {
		t.Fatal(err)
	}
	if len(p.cache) != 1 || p.cacheEntries != 1 || p.cacheEvictions != 199 || p.expandedValues != 402 || p.expandedBytes != 202 {
		t.Fatalf("bounded replacement changed accounting: entries=%d evictions=%d values=%d bytes=%d", p.cacheEntries, p.cacheEvictions, p.expandedValues, p.expandedBytes)
	}
	p = newMMDBValueBounds(context.Background(), data)
	p.cacheSlots, p.workLimit = 1, 100
	if !errors.Is(p.check(false), errMMDBResourceBudget) {
		t.Fatal("cache thrashing must still be work-bounded")
	}
	if size := unsafe.Sizeof(mmdbCacheEntry{}); size != 20 || uint64(size)*mmdbCacheSlots != 5<<20 {
		t.Fatalf("cache layout/budget changed: slot=%d", size)
	}
}

func TestMMDBMalformedSharedStructures(t *testing.T) {
	for _, data := range [][]byte{
		{0x20, 0},          // self-loop
		{0x20, 2, 0x20, 0}, // two-target cycle
		{1, 4, 0x20, 0},    // container cycle
		{0x20, 255},        // target outside data
		{0x20},             // truncated pointer
		{0x5f, 0xff},       // truncated size
		{0x42, 'x'},        // truncated string
		{0x41, 0xff},       // invalid UTF-8
		{0, 5},             // unsupported type
		{2, 7},             // invalid boolean
		{0x38, 0, 0, 0, 0}, // 4-byte pointer cycle
		{0x39, 0, 0, 0, 0}, // malformed 4-byte pointer
	} {
		p := newMMDBValueBounds(context.Background(), data)
		if p.check(false) == nil {
			t.Fatalf("malformed synthetic encoding accepted: %x", data)
		}
		if p.cacheEntries != 0 {
			t.Fatal("partial/cyclic result was published to cache")
		}
	}
}

func TestMMDBOffsetSpacesAndMetadataConstraints(t *testing.T) {
	metadata := []byte{0xe1, 0x41, 'k', 0x20, 1} // pointer to metadata-relative key
	if err := checkMMDBValues(context.Background(), metadata, true); err != nil {
		t.Fatal(err)
	}
	// Same offset in another section is a cycle, not the cached metadata key.
	if checkMMDBValues(context.Background(), []byte{0x40, 0x20, 1}, false) == nil {
		t.Fatal("metadata summary reused in data space")
	}
	if checkMMDBValues(context.Background(), append(metadata, 0x40), true) == nil ||
		checkMMDBValues(context.Background(), []byte{0x40}, true) == nil {
		t.Fatal("metadata map/trailing-value constraint lost")
	}
	// Data parsing before metadata must not seed its independently relative cache.
	if checkMMDBValues(context.Background(), []byte{0x40, 0x41, 'a', 0x20, 1}, false) != nil ||
		checkMMDBValues(context.Background(), []byte{0xe1, 0x20, 1, 0x40}, true) == nil {
		t.Fatal("data and metadata offset spaces not independent")
	}
}

func TestMMDBCounterBoundariesAndCancellation(t *testing.T) {
	for _, tc := range []struct {
		start, add, limit, want uint64
		ok                      bool
	}{
		{9, 1, 10, 10, true}, {10, 1, 10, 11, false},
		{^uint64(0) - 1, 2, ^uint64(0), ^uint64(0), false},
		{^uint64(0), 1, ^uint64(0), ^uint64(0), false},
	} {
		x := tc.start
		if ok := mmdbAdd(&x, tc.add, tc.limit); ok != tc.ok || x != tc.want {
			t.Fatal("overflow/limit accounting mismatch")
		}
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if !errors.Is(checkMMDBValues(ctx, sharedMMDBValues(10, 20), false), context.DeadlineExceeded) {
		t.Fatal("expired deadline not honored")
	}
	mid := &mmdbCancelContext{Context: context.Background()}
	p := newMMDBValueBounds(mid, sharedMMDBValues(10000, 20))
	if !errors.Is(p.check(false), context.Canceled) || p.operations > 1024 {
		t.Fatal("cancellation not polled during cache-heavy parsing")
	}
}

type mmdbCancelContext struct {
	context.Context
	calls int
}

func (c *mmdbCancelContext) Err() error {
	c.calls++
	if c.calls >= 3 {
		return context.Canceled
	}
	return nil
}

func BenchmarkMMDBSharedPreflight(b *testing.B) {
	data := sharedMMDBValues(20000, 20)
	for _, cached := range []bool{false, true} {
		b.Run(fmt.Sprintf("cached=%t", cached), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				p := newMMDBValueBounds(context.Background(), data)
				if !cached {
					p.cacheSlots = 0
				}
				if err := p.check(false); err != nil {
					b.Fatal(err)
				}
				b.ReportMetric(float64(p.operations), "work/op")
				b.ReportMetric(float64(p.expandedValues), "logical/op")
				b.ReportMetric(float64(p.cacheHits), "hits/op")
			}
		})
	}
}
