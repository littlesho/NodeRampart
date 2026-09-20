// SPDX-License-Identifier: MIT

package assets

import (
	"context"
	"errors"
	"unicode/utf8"
)

const (
	mmdbWorkLimit = 64_000_000
	// Verify decodes shared targets afresh. Cache hits must still charge their
	// full expansion, separately from preflight work. The cumulative byte
	// charge also bounds copies/allocations that a value count alone misses.
	mmdbExpandedValueLimit = 128_000_000
	mmdbExpandedByteLimit  = 8 << 30
	mmdbRecordValueLimit   = 16384
	mmdbRecordByteLimit    = 8 << 20
	mmdbDepthLimit         = 32
	mmdbCacheSlots         = 1 << 18 // 20-byte slots: at most 5 MiB, independent of declarations.
)

// The upstream reader allocates from declarations before decoding their contents.
// Preflight validates every encoding and bounds each logical expansion first.
// Only fully validated pointer targets are memoized; no decoded data is cached.
func checkMMDBValues(ctx context.Context, data []byte, metadata bool) error {
	p := newMMDBValueBounds(ctx, data)
	return p.check(metadata)
}

func newMMDBValueBounds(ctx context.Context, data []byte) *mmdbValueBounds {
	slots := mmdbCacheSlots
	for slots > 1 && slots > len(data) {
		slots /= 2
	}
	return &mmdbValueBounds{ctx: ctx, data: data, workLimit: mmdbWorkLimit,
		valueLimit: mmdbExpandedValueLimit, byteLimit: mmdbExpandedByteLimit, cacheSlots: slots}
}

func (p *mmdbValueBounds) check(metadata bool) error {
	if err := p.ctx.Err(); err != nil {
		return err
	}
	// End offsets fit the compact summaries on all supported architectures.
	if len(p.data) > maxDatabase {
		return errMMDBResourceBudget
	}
	if metadata && (len(p.data) == 0 || p.data[0]>>5 != 7) {
		return errors.New("GeoIP metadata must be a bounded map")
	}
	for offset := 0; offset < len(p.data); {
		p.recordValues, p.recordBytes = 0, 0
		next, err := p.value(offset, 0)
		if err != nil {
			return err
		}
		if next <= offset {
			return errMMDBBounds
		}
		if !mmdbAdd(&p.expandedValues, p.recordValues, p.valueLimit) ||
			!mmdbAdd(&p.expandedBytes, p.recordBytes, p.byteLimit) {
			return errMMDBResourceBudget
		}
		p.maxRecordValues = max(p.maxRecordValues, p.recordValues)
		p.maxRecordBytes = max(p.maxRecordBytes, p.recordBytes)
		if metadata && next != len(p.data) {
			return errors.New("GeoIP metadata has trailing values")
		}
		offset = next
	}
	return p.ctx.Err()
}

type mmdbValueSummary struct {
	end, values, bytes uint32
	height             uint8
}

type mmdbCacheEntry struct {
	key     uint32 // data-relative offset + 1; zero is an unused slot.
	summary mmdbValueSummary
}

type mmdbValueBounds struct {
	ctx                                                            context.Context
	data                                                           []byte
	operations, recordValues, recordBytes                          uint64
	expandedValues, expandedBytes, maxRecordValues, maxRecordBytes uint64
	workLimit, valueLimit, byteLimit                               uint64
	cacheSlots                                                     int
	cache                                                          []mmdbCacheEntry
	cacheHits, cacheMisses, cacheEntries, cacheEvictions           uint64
	active                                                         [mmdbDepthLimit + 1]int
	maxDepth                                                       int
}

var errMMDBBounds = errors.New("invalid MMDB value or pointer structure")
var errMMDBResourceBudget = errors.New("GeoIP MMDB validation resource budget exceeded")

// Charge before using a counter. Saturation preserves a useful failure count
// without letting an overflow wrap under a bound (including on 32-bit hosts).
func mmdbAdd(counter *uint64, amount, limit uint64) bool {
	if amount > ^uint64(0)-*counter {
		*counter = ^uint64(0)
		return false
	}
	*counter += amount
	return *counter <= limit
}

func (p *mmdbValueBounds) value(offset, depth int) (int, error) {
	s, err := p.parse(offset, depth, false)
	return int(s.end), err
}

func (p *mmdbValueBounds) parse(offset, depth int, target bool) (mmdbValueSummary, error) {
	if !mmdbAdd(&p.operations, 1, p.workLimit) {
		return mmdbValueSummary{}, errMMDBResourceBudget
	}
	if offset < 0 || offset >= len(p.data) || depth < 0 {
		return mmdbValueSummary{}, errMMDBBounds
	}
	if depth > mmdbDepthLimit {
		return mmdbValueSummary{}, errMMDBResourceBudget
	}
	if p.operations == 1 || p.operations%1024 == 0 {
		if err := p.ctx.Err(); err != nil {
			return mmdbValueSummary{}, err
		}
	}
	// Active paths and completed entries are separate. Never publish a partial
	// summary or treat a cycle as a reusable result.
	for _, ancestor := range p.active[:depth] {
		if ancestor == offset {
			return mmdbValueSummary{}, errMMDBBounds
		}
	}
	p.active[depth] = offset
	p.maxDepth = max(p.maxDepth, depth)
	var slot int
	if target && p.cacheSlots > 0 {
		if p.cache == nil {
			p.cache = make([]mmdbCacheEntry, p.cacheSlots)
		}
		// Fixed, direct-mapped storage: hostile collisions only cause bounded
		// reparsing, not unbounded storage or an unbounded hash probe chain.
		slot = int((uint32(offset)*2654435761)>>14) & (len(p.cache) - 1)
		entry := p.cache[slot]
		if entry.key == uint32(offset)+1 {
			p.cacheHits++
			s := entry.summary
			p.maxDepth = max(p.maxDepth, depth+int(s.height))
			if depth+int(s.height) > mmdbDepthLimit ||
				!mmdbAdd(&p.recordValues, uint64(s.values), mmdbRecordValueLimit) ||
				!mmdbAdd(&p.recordBytes, uint64(s.bytes), mmdbRecordByteLimit) {
				return mmdbValueSummary{}, errMMDBResourceBudget
			}
			return s, nil
		}
		p.cacheMisses++
	}
	beforeValues, beforeBytes := p.recordValues, p.recordBytes
	if !mmdbAdd(&p.recordValues, 1, mmdbRecordValueLimit) {
		return mmdbValueSummary{}, errMMDBResourceBudget
	}
	finish := func(end int, height uint8) mmdbValueSummary {
		s := mmdbValueSummary{end: uint32(end), values: uint32(p.recordValues - beforeValues),
			bytes: uint32(p.recordBytes - beforeBytes), height: height}
		if target && p.cacheSlots > 0 {
			if p.cache[slot].key == 0 {
				p.cacheEntries++
			} else if p.cache[slot].key != uint32(p.active[depth])+1 {
				p.cacheEvictions++
			}
			p.cache[slot] = mmdbCacheEntry{key: uint32(p.active[depth]) + 1, summary: s}
		}
		return s
	}
	control := p.data[offset]
	offset++
	kind, size := int(control>>5), int(control&31)
	if kind == 1 {
		n := ((size >> 3) & 3) + 1
		if offset+n > len(p.data) || n == 4 && size != 24 {
			return mmdbValueSummary{}, errMMDBBounds
		}
		pointer := uint64(size & 7)
		if n == 4 {
			pointer = 0
		}
		for _, b := range p.data[offset : offset+n] {
			pointer = pointer<<8 | uint64(b)
		}
		if n == 2 {
			pointer += 2048
		} else if n == 3 {
			pointer += 526336
		}
		if pointer >= uint64(len(p.data)) {
			return mmdbValueSummary{}, errMMDBBounds
		}
		child, err := p.parse(int(pointer), depth+1, true)
		if err != nil {
			return mmdbValueSummary{}, err
		}
		// The pointer consumes its own encoding, never the target's encoding.
		return finish(offset+n, child.height+1), nil
	}
	if kind == 0 {
		if offset >= len(p.data) {
			return mmdbValueSummary{}, errMMDBBounds
		}
		kind = int(p.data[offset]) + 7
		offset++
	}
	if size >= 29 {
		n := size - 28
		if offset+n > len(p.data) {
			return mmdbValueSummary{}, errMMDBBounds
		}
		value := 0
		for _, b := range p.data[offset : offset+n] {
			value = value<<8 | int(b)
		}
		switch size {
		case 29:
			size = value + 29
		case 30:
			size = value + 285
		case 31:
			size = value + 65821
		}
		offset += n
	}
	switch kind {
	case 7, 11:
		if kind == 7 && size > 256 || kind == 11 && size > 1024 {
			return mmdbValueSummary{}, errMMDBResourceBudget
		}
		count := size
		if kind == 7 {
			count *= 2
		}
		// Include a conservative allocation charge for map slots and interfaces.
		if !mmdbAdd(&p.recordBytes, uint64(count)*128, mmdbRecordByteLimit) {
			return mmdbValueSummary{}, errMMDBResourceBudget
		}
		var height uint8
		for i := 0; i < count; i++ {
			child, err := p.parse(offset, depth+1, false)
			if err != nil {
				return mmdbValueSummary{}, err
			}
			offset = int(child.end)
			height = max(height, child.height+1)
		}
		return finish(offset, height), nil
	case 14:
		if size > 1 {
			return mmdbValueSummary{}, errMMDBBounds
		}
		return finish(offset, 0), nil
	case 2, 4:
		if size > 64<<10 {
			return mmdbValueSummary{}, errMMDBResourceBudget
		}
	case 3:
		if size != 8 {
			return mmdbValueSummary{}, errMMDBBounds
		}
	case 5:
		if size > 2 {
			return mmdbValueSummary{}, errMMDBBounds
		}
	case 6, 8:
		if size > 4 {
			return mmdbValueSummary{}, errMMDBBounds
		}
	case 9:
		if size > 8 {
			return mmdbValueSummary{}, errMMDBBounds
		}
	case 10:
		if size > 16 {
			return mmdbValueSummary{}, errMMDBBounds
		}
	case 15:
		if size != 4 {
			return mmdbValueSummary{}, errMMDBBounds
		}
	default:
		return mmdbValueSummary{}, errMMDBBounds
	}
	if size > len(p.data)-offset {
		return mmdbValueSummary{}, errMMDBBounds
	}
	if !mmdbAdd(&p.recordBytes, uint64(size), mmdbRecordByteLimit) {
		return mmdbValueSummary{}, errMMDBResourceBudget
	}
	if kind == 2 && !utf8.Valid(p.data[offset:offset+size]) {
		return mmdbValueSummary{}, errMMDBBounds
	}
	return finish(offset+size, 0), nil
}
