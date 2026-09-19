// SPDX-License-Identifier: MIT
//go:build linux

package collector

import (
	"errors"
	"golang.org/x/sys/unix"
	"net"
	"testing"
)

func TestPartialDefaultSelectionRetainsHealthyFamilyAndUsableFallback(t *testing.T) {
	for _, missing := range []bool{false, true} {
		refs, err := selectDefaultLinks(map[int][]routeCandidate{unix.AF_INET: {{index: 1}}, unix.AF_INET6: {{index: 2}}}, func(index int) (*net.Interface, error) {
			if index == 1 {
				return &net.Interface{Name: "lab4", Index: 1, Flags: net.FlagUp}, nil
			}
			if missing {
				return nil, errors.New("removed")
			}
			return &net.Interface{Name: "lab6", Index: 2}, nil
		})
		var partial *PartialDiscoveryError
		if !errors.As(err, &partial) || len(refs) != 1 || refs[0].Name != "lab4" {
			t.Fatalf("healthy family lost: %v %v", refs, err)
		}
	}
	lookup := func(index int) (*net.Interface, error) {
		if index == 1 {
			return nil, errors.New("unavailable")
		}
		return &net.Interface{Name: map[int]string{2: "backup", 3: "other"}[index], Index: index, Flags: net.FlagUp}, nil
	}
	for _, tc := range []struct {
		name    string
		routes  []routeCandidate
		partial bool
	}{
		{"metric then index", []routeCandidate{{index: 3, metric: 20}, {index: 1, metric: 10}, {index: 2, metric: 20}}, false},
		{"unsupported preferred", []routeCandidate{{index: 0, metric: 10, unsupported: true}, {index: 2, metric: 20}}, true},
		{"unsupported lower priority", []routeCandidate{{index: 2, metric: 10}, {index: 0, metric: 20, unsupported: true}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			refs, err := selectDefaultLinks(map[int][]routeCandidate{unix.AF_INET: tc.routes}, lookup)
			var partial *PartialDiscoveryError
			if len(refs) != 1 || refs[0].Name != "backup" || errors.As(err, &partial) != tc.partial || !tc.partial && err != nil {
				t.Fatalf("fallback=%v error=%v", refs, err)
			}
		})
	}
	refs, err := selectDefaultLinks(map[int][]routeCandidate{unix.AF_INET: {{index: 1}}}, lookup)
	var partial *PartialDiscoveryError
	if len(refs) != 0 || err == nil || errors.As(err, &partial) {
		t.Fatal("complete discovery failure was made partially usable")
	}
}
