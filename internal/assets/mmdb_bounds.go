// SPDX-License-Identifier: MIT

package assets

import (
	"context"
	"errors"
	"unicode/utf8"
)

// The upstream reader validates format correctness, but reflect-backed decoding
// reserves the declared map/slice capacity before consuming its contents. This
// allocation-free preflight bounds declarations and pointer expansion first.
// Encoding follows the MaxMind DB data-section control-byte format; it does not
// interpret geolocation records or replace the upstream complete verifier.
func checkMMDBValues(ctx context.Context, data []byte, metadata bool) error {
	p := mmdbValueBounds{ctx: ctx, data: data}
	if metadata && (len(data) == 0 || data[0]>>5 != 7) {
		return errors.New("GeoIP metadata must be a bounded map")
	}
	for offset := 0; offset < len(data); {
		p.recordValues, p.recordBytes = 0, 0
		next, err := p.value(offset, 0)
		if err != nil || next <= offset {
			return errors.New("GeoIP value declarations or pointer expansion exceed safe bounds")
		}
		if metadata && next != len(data) {
			return errors.New("GeoIP metadata has trailing values")
		}
		offset = next
	}
	return ctx.Err()
}

type mmdbValueBounds struct {
	ctx                                   context.Context
	data                                  []byte
	operations, recordValues, recordBytes int
}

var errMMDBBounds = errors.New("invalid or excessive MMDB value")

func (p *mmdbValueBounds) value(offset, depth int) (int, error) {
	p.operations++
	p.recordValues++
	if offset < 0 || offset >= len(p.data) || depth > 32 || p.operations > 64_000_000 || p.recordValues > 16384 {
		return 0, errMMDBBounds
	}
	if p.operations%1024 == 0 && p.ctx.Err() != nil {
		return 0, errMMDBBounds
	}
	control := p.data[offset]
	offset++
	kind, size := int(control>>5), int(control&31)
	if kind == 1 {
		n := ((size >> 3) & 3) + 1
		if offset+n > len(p.data) || n == 4 && size != 24 {
			return 0, errMMDBBounds
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
			return 0, errMMDBBounds
		}
		if _, err := p.value(int(pointer), depth+1); err != nil {
			return 0, err
		}
		return offset + n, nil
	}
	if kind == 0 {
		if offset >= len(p.data) {
			return 0, errMMDBBounds
		}
		kind = int(p.data[offset]) + 7
		offset++
	}
	if size >= 29 {
		n := size - 28
		if offset+n > len(p.data) {
			return 0, errMMDBBounds
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
			return 0, errMMDBBounds
		}
		count := size
		if kind == 7 {
			count *= 2
		}
		// Include a conservative allocation charge for map slots and interfaces.
		p.recordBytes += count * 128
		if p.recordBytes > 8<<20 {
			return 0, errMMDBBounds
		}
		for i := 0; i < count; i++ {
			next, err := p.value(offset, depth+1)
			if err != nil {
				return 0, err
			}
			offset = next
		}
		return offset, nil
	case 14:
		if size > 1 {
			return 0, errMMDBBounds
		}
		return offset, nil
	case 2, 4:
		if size > 64<<10 {
			return 0, errMMDBBounds
		}
	case 3:
		if size != 8 {
			return 0, errMMDBBounds
		}
	case 5:
		if size > 2 {
			return 0, errMMDBBounds
		}
	case 6, 8:
		if size > 4 {
			return 0, errMMDBBounds
		}
	case 9:
		if size > 8 {
			return 0, errMMDBBounds
		}
	case 10:
		if size > 16 {
			return 0, errMMDBBounds
		}
	case 15:
		if size != 4 {
			return 0, errMMDBBounds
		}
	default:
		return 0, errMMDBBounds
	}
	if size > len(p.data)-offset {
		return 0, errMMDBBounds
	}
	p.recordBytes += size
	if p.recordBytes > 8<<20 || kind == 2 && !utf8.Valid(p.data[offset:offset+size]) {
		return 0, errMMDBBounds
	}
	return offset + size, nil
}
