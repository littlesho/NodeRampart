// SPDX-License-Identifier: MIT
//go:build linux

package collector

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"sort"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const maxRouteDumpBytes = 4 << 20
const maxRouteMessages = 16384

// Linux UAPI RTA_NH_ID is not named by the pinned x/sys version.
const routeNextHopID = 30

type routeCandidate struct {
	family      int
	index       int
	metric      uint32
	unsupported bool
}

// The route fixed header and attributes follow the kernel rt-route contract:
// https://www.kernel.org/doc/html/v6.15/networking/netlink_spec/rt_route.html
// Parsing retains no addresses or raw route inventory.
func parseDefaultRoute(data []byte) (routeCandidate, bool, error) {
	var route routeCandidate
	if len(data) < unix.SizeofRtMsg {
		return route, false, errors.New("truncated route header")
	}
	route.family = int(data[0])
	table := uint32(data[4])
	if (route.family != unix.AF_INET && route.family != unix.AF_INET6) || data[1] != 0 || data[2] != 0 || data[3] != 0 || data[7] != unix.RTN_UNICAST {
		return route, false, nil
	}
	if binary.NativeEndian.Uint32(data[8:12])&unix.RTM_F_CLONED != 0 {
		return route, false, nil
	}
	attrs := data[unix.SizeofRtMsg:]
	seen := map[uint16]bool{}
	for len(attrs) > 0 {
		if len(attrs) < 4 {
			return route, false, errors.New("truncated route attribute")
		}
		length := int(binary.NativeEndian.Uint16(attrs[:2]))
		kind := binary.NativeEndian.Uint16(attrs[2:4]) & 0x3fff
		if length < 4 || length > len(attrs) {
			return route, false, errors.New("invalid route attribute length")
		}
		value := attrs[4:length]
		switch kind {
		case unix.RTA_OIF, unix.RTA_PRIORITY, unix.RTA_TABLE:
			if len(value) != 4 || seen[kind] {
				return route, false, errors.New("invalid or duplicate route scalar")
			}
			seen[kind] = true
			number := binary.NativeEndian.Uint32(value)
			switch kind {
			case unix.RTA_OIF:
				if number > 1<<31-1 {
					return route, false, errors.New("invalid route interface")
				}
				route.index = int(number)
			case unix.RTA_PRIORITY:
				route.metric = number
			case unix.RTA_TABLE:
				table = number
			}
		case unix.RTA_MULTIPATH, routeNextHopID:
			route.unsupported = true
		case unix.RTA_DST, unix.RTA_SRC:
			want := 4
			if route.family == unix.AF_INET6 {
				want = 16
			}
			if len(value) != want {
				return route, false, errors.New("invalid default route address size")
			}
			for _, b := range value {
				if b != 0 {
					return route, false, nil
				}
			}
		}
		aligned := (length + 3) &^ 3
		if aligned > len(attrs) {
			if length != len(attrs) {
				return route, false, errors.New("invalid route attribute padding")
			}
			aligned = length
		}
		attrs = attrs[aligned:]
	}
	return route, table == unix.RT_TABLE_MAIN && (route.index > 0 || route.unsupported), nil
}

func defaultInterfaces(ctx context.Context) ([]InterfaceRef, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.NETLINK_ROUTE)
	if err != nil {
		return nil, errors.New("route discovery socket unavailable")
	}
	defer unix.Close(fd)
	if err := unix.Bind(fd, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		return nil, errors.New("route discovery bind failed")
	}
	if err := unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &unix.Timeval{Usec: 100000}); err != nil {
		return nil, errors.New("route discovery deadline unavailable")
	}
	address, err := unix.Getsockname(fd)
	if err != nil {
		return nil, errors.New("route discovery identity unavailable")
	}
	local, ok := address.(*unix.SockaddrNetlink)
	if !ok {
		return nil, errors.New("invalid route discovery socket")
	}
	request := make([]byte, unix.NLMSG_HDRLEN+unix.SizeofRtMsg)
	binary.NativeEndian.PutUint32(request[:4], uint32(len(request)))
	binary.NativeEndian.PutUint16(request[4:6], unix.RTM_GETROUTE)
	binary.NativeEndian.PutUint16(request[6:8], unix.NLM_F_REQUEST|unix.NLM_F_DUMP)
	binary.NativeEndian.PutUint32(request[8:12], 1)
	request[unix.NLMSG_HDRLEN] = unix.AF_UNSPEC
	if err := unix.Sendto(fd, request, 0, &unix.SockaddrNetlink{Family: unix.AF_NETLINK}); err != nil {
		return nil, errors.New("route discovery request failed")
	}
	// Keep candidates until availability is known. The dump byte/message
	// bounds also bound this collection, including duplicate defaults.
	candidates := map[int][]routeCandidate{}
	buffer := make([]byte, 64<<10)
	total, messages := 0, 0
	for {
		if err := ctx.Err(); err != nil {
			return nil, errors.New("route discovery deadline exceeded")
		}
		n, _, flags, peer, err := unix.Recvmsg(fd, buffer, nil, 0)
		if err == unix.EINTR || err == unix.EAGAIN {
			continue
		}
		if err != nil {
			return nil, errors.New("route discovery read failed")
		}
		sender, ok := peer.(*unix.SockaddrNetlink)
		if !ok || sender.Pid != 0 || flags&unix.MSG_TRUNC != 0 || n < unix.NLMSG_HDRLEN {
			return nil, errors.New("invalid route discovery reply")
		}
		total += n
		if total > maxRouteDumpBytes {
			return nil, errors.New("route discovery byte limit reached; configure interfaces explicitly")
		}
		parsed, err := syscall.ParseNetlinkMessage(buffer[:n])
		if err != nil {
			return nil, errors.New("malformed route discovery reply")
		}
		for _, message := range parsed {
			messages++
			if messages > maxRouteMessages {
				return nil, errors.New("route discovery record limit reached; configure interfaces explicitly")
			}
			if message.Header.Seq != 1 || message.Header.Pid != local.Pid || message.Header.Flags&unix.NLM_F_DUMP_INTR != 0 {
				return nil, errors.New("route discovery changed or has invalid identity")
			}
			switch message.Header.Type {
			case unix.NLMSG_DONE:
				if len(message.Data) != 0 && (len(message.Data) < 4 || binary.NativeEndian.Uint32(message.Data[:4]) != 0) {
					return nil, errors.New("route dump did not complete")
				}
				return selectDefaultLinks(candidates, net.InterfaceByIndex)
			case unix.NLMSG_ERROR:
				return nil, errors.New("kernel rejected route discovery")
			case unix.RTM_NEWROUTE:
				route, ok, err := parseDefaultRoute(message.Data)
				if err != nil {
					return nil, err
				}
				if !ok {
					continue
				}
				candidates[route.family] = append(candidates[route.family], route)
			}
		}
	}
}

func selectDefaultLinks(candidates map[int][]routeCandidate, lookup func(int) (*net.Interface, error)) ([]InterfaceRef, error) {
	result := []InterfaceRef{}
	seen := map[int]bool{}
	partial := false
	for _, family := range []int{unix.AF_INET, unix.AF_INET6} {
		routes := candidates[family]
		if len(routes) == 0 {
			continue
		}
		sort.SliceStable(routes, func(i, j int) bool {
			if routes[i].metric != routes[j].metric {
				return routes[i].metric < routes[j].metric
			}
			if routes[i].index != routes[j].index {
				return routes[i].index < routes[j].index
			}
			return routes[i].unsupported && !routes[j].unsupported
		})
		selected := false
		for _, route := range routes {
			if route.unsupported {
				// A lower-priority simple route cannot prove that the preferred
				// multipath/nexthop route is being observed completely.
				partial = true
				continue
			}
			iface, err := lookup(route.index)
			if err != nil || iface == nil || iface.Index <= 0 || iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 || !ValidInterfaceName(iface.Name) {
				continue
			}
			if !seen[iface.Index] {
				result = append(result, InterfaceRef{Name: iface.Name, Index: iface.Index})
				seen[iface.Index] = true
			}
			selected = true
			break
		}
		if !selected {
			partial = true
		}
	}
	if len(result) == 0 {
		return nil, errors.New("no usable IPv4 or IPv6 default route; configure interfaces explicitly")
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	if partial {
		return result, &PartialDiscoveryError{}
	}
	return result, nil
}
