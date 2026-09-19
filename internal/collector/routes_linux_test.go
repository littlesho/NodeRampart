// SPDX-License-Identifier: MIT
//go:build linux

package collector

import (
	"encoding/binary"
	"errors"
	"net"
	"testing"

	"golang.org/x/sys/unix"
)

func routeFixture(family byte, index, metric, table uint32) []byte {
	data := make([]byte, unix.SizeofRtMsg)
	data[0] = family
	data[4] = byte(table)
	data[7] = unix.RTN_UNICAST
	for _, attr := range []struct {
		kind  uint16
		value uint32
	}{{unix.RTA_OIF, index}, {unix.RTA_PRIORITY, metric}, {unix.RTA_TABLE, table}} {
		value := make([]byte, 8)
		binary.NativeEndian.PutUint16(value[:2], 8)
		binary.NativeEndian.PutUint16(value[2:4], attr.kind)
		binary.NativeEndian.PutUint32(value[4:], attr.value)
		data = append(data, value...)
	}
	return data
}

func TestDefaultRouteParserSupportsIPv6AndRejectsNonMainOrMalformed(t *testing.T) {
	for _, family := range []byte{unix.AF_INET, unix.AF_INET6} {
		value, ok, err := parseDefaultRoute(routeFixture(family, 7, 1024, unix.RT_TABLE_MAIN))
		if err != nil || !ok || value.index != 7 || value.metric != 1024 {
			t.Fatal("valid default rejected", err)
		}
		if _, ok, err := parseDefaultRoute(routeFixture(family, 7, 1, 1000)); err != nil || ok {
			t.Fatal("policy table mixed with main routing", err)
		}
		data := routeFixture(family, 7, 1024, unix.RT_TABLE_MAIN)
		data[1] = 64
		if _, ok, err := parseDefaultRoute(data); err != nil || ok {
			t.Fatal("specific route treated as default", err)
		}
	}
	valid := routeFixture(unix.AF_INET6, 7, 1024, unix.RT_TABLE_MAIN)
	for _, data := range [][]byte{valid[:11], valid[:13], append(append([]byte(nil), valid...), 1), append(append([]byte(nil), valid...), valid[12:20]...)} {
		if _, _, err := parseDefaultRoute(data); err == nil {
			t.Fatal("truncated/duplicate route accepted")
		}
	}
	multipath := append(append([]byte(nil), valid...), 4, 0, unix.RTA_MULTIPATH, 0)
	if candidate, ok, err := parseDefaultRoute(multipath); err != nil || !ok || !candidate.unsupported {
		t.Fatal("unsupported multipath was hidden", err)
	}
}

func TestDefaultRouteSelectionIPv6OnlyDedupAndUnavailableLinks(t *testing.T) {
	lookup := func(index int) (*net.Interface, error) {
		if index == 0 {
			return nil, errors.New("missing")
		}
		return &net.Interface{Index: index, Name: map[int]string{1: "eth_test", 2: "eth_other"}[index], Flags: net.FlagUp}, nil
	}
	value, err := selectDefaultLinks(map[int][]routeCandidate{unix.AF_INET6: {{family: unix.AF_INET6, index: 1}}}, lookup)
	if err != nil || len(value) != 1 || value[0].Name != "eth_test" {
		t.Fatal("IPv6-only route was not selected", err)
	}
	value, err = selectDefaultLinks(map[int][]routeCandidate{unix.AF_INET: {{index: 1}}, unix.AF_INET6: {{index: 1}}}, lookup)
	if err != nil || len(value) != 1 {
		t.Fatal("dual-stack interface counted twice", err)
	}
	value, err = selectDefaultLinks(map[int][]routeCandidate{unix.AF_INET: {{index: 1}}, unix.AF_INET6: {{index: 2}}}, lookup)
	if err != nil || len(value) != 2 {
		t.Fatal("distinct family interfaces lost", err)
	}
	if _, err := selectDefaultLinks(map[int][]routeCandidate{unix.AF_INET6: {{index: 0}}}, lookup); err == nil {
		t.Fatal("missing link accepted")
	}
	if _, err := selectDefaultLinks(map[int][]routeCandidate{unix.AF_INET6: {{index: 1, unsupported: true}}}, lookup); err == nil {
		t.Fatal("multipath silently approximated")
	}
}

func FuzzDefaultRoute(f *testing.F) {
	f.Add(routeFixture(unix.AF_INET6, 7, 1024, unix.RT_TABLE_MAIN))
	f.Add([]byte{1, 2, 3})
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 64<<10 {
			t.Skip()
		}
		_, _, _ = parseDefaultRoute(data)
	})
}
