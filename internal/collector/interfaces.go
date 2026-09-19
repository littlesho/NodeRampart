// SPDX-License-Identifier: MIT

package collector

import (
	"context"
	"errors"
	"github.com/littlesho/NodeRampart/internal/protocol"
	"net"
	"sort"
)

const MaxInterfaces = protocol.MaxInterfaces

type InterfaceRef struct {
	Name  string `json:"name"`
	Index int    `json:"index"`
}

// PartialDiscoveryError means the returned links are usable but some default
// route evidence could not be resolved. Only callers explicitly handling this
// error may retain that partial selection; all other errors invalidate it.
type PartialDiscoveryError struct{}

func (*PartialDiscoveryError) Error() string {
	return "default route discovery is incomplete; observing available interfaces"
}

func ValidInterfaceName(name string) bool {
	return protocol.ValidInterfaceName(name)
}

// ResolveInterfaces preserves explicit selections, including unavailable links.
// Index zero tells the caller that this requested interface needs a retry.
// Automatic discovery may return usable links with *PartialDiscoveryError.
func ResolveInterfaces(ctx context.Context, names []string) ([]InterfaceRef, error) {
	if len(names) == 0 {
		return defaultInterfaces(ctx)
	}
	if len(names) > MaxInterfaces {
		return nil, errors.New("interface selection exceeds limit")
	}
	seen := make(map[string]bool, len(names))
	result := make([]InterfaceRef, 0, len(names))
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !ValidInterfaceName(name) || seen[name] {
			return nil, errors.New("invalid or duplicate interface selection")
		}
		seen[name] = true
		ref := InterfaceRef{Name: name}
		if iface, err := net.InterfaceByName(name); err == nil && iface.Flags&net.FlagUp != 0 {
			ref.Index = iface.Index
		}
		result = append(result, ref)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}
