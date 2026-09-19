// SPDX-License-Identifier: MIT
//go:build linux

package sensor

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/collector"
	"github.com/littlesho/NodeRampart/internal/protocol"
)

func TestNetNSMultipleInterfacesIPv6AndRouteChanges(t *testing.T) {
	if os.Getenv("NODERAMPART_MULTI_LAB") != "authorized-disposable-vm-v1" {
		t.Skip("privileged fixture: use scripts/test-multi-interface.py inside an authorized disposable VM")
	}
	if os.Geteuid() != 0 || exec.Command("/usr/bin/systemd-detect-virt", "--vm", "--quiet").Run() != nil {
		t.Fatal("requires disposable VM root")
	}
	name := os.Getenv("NODERAMPART_MULTI_NS")
	if !regexp.MustCompile(`^nr-multi-[a-f0-9]{24}-c$`).MatchString(name) {
		t.Fatal("invalid fixture namespace")
	}
	current, err := os.Stat("/proc/self/ns/net")
	if err != nil {
		t.Fatal("network namespace unavailable")
	}
	initial, err := os.Stat("/proc/1/ns/net")
	if err != nil || os.SameFile(current, initial) {
		t.Fatal("initial namespace refused")
	}
	expected, err := os.Stat(filepath.Join("/run/netns", name))
	if err != nil || !os.SameFile(current, expected) {
		t.Fatal("namespace ownership mismatch")
	}
	interfaces, err := net.Interfaces()
	if err != nil || len(interfaces) != 3 {
		t.Fatal("unexpected fixture interfaces")
	}
	seen := map[string]bool{}
	for _, iface := range interfaces {
		if iface.Name != "lo" && iface.Name != "nr4" && iface.Name != "nr6" {
			t.Fatal("unexpected fixture link")
		}
		seen[iface.Name] = true
		addresses, err := iface.Addrs()
		if err != nil {
			t.Fatal("fixture addresses unavailable")
		}
		for _, value := range addresses {
			ip, _, err := net.ParseCIDR(value.String())
			if err != nil {
				t.Fatal("invalid fixture address")
			}
			if iface.Name == "lo" {
				if !ip.IsLoopback() {
					t.Fatal("unexpected loopback address")
				}
				continue
			}
			if value.String() != "192.0.2.1/30" && value.String() != "2001:db8:3::1/126" && !ip.IsLinkLocalUnicast() {
				t.Fatal("non-fixture address refused")
			}
		}
	}
	if !seen["nr4"] || !seen["nr6"] {
		t.Fatal("missing isolated link")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	resolve := func(want []string) {
		t.Helper()
		refs, err := collector.ResolveInterfaces(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, ref := range refs {
			names = append(names, ref.Name)
		}
		sort.Strings(names)
		if fmt.Sprint(names) != fmt.Sprint(want) {
			t.Fatalf("wrong selected defaults: %v", names)
		}
	}
	resolve([]string{"nr4", "nr6"})
	output := make(chan protocol.Batch, 128)
	var readers sync.WaitGroup
	sender := &BatchSender{connect: func() (batchConnection, error) {
		client, peer := net.Pipe()
		readers.Add(1)
		go func() {
			defer readers.Done()
			defer peer.Close()
			reader := bufio.NewReader(peer)
			for {
				var b protocol.Batch
				if protocol.ReadFrame(reader, &b) != nil {
					return
				}
				select {
				case output <- b:
				case <-ctx.Done():
					return
				}
			}
		}()
		return client, nil
	}}
	fleet := Fleet{Limit: 2, MaxFlows: 128, ReceiveBuffer: 1 << 20, BatchInterval: 200 * time.Millisecond, Sender: sender, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	done := make(chan error, 1)
	go func() { done <- fleet.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(2 * time.Second):
			t.Error("capture fleet failed to stop")
		}
		_ = sender.Close()
		readers.Wait()
	}()
	waitInterfaces := func(want map[string]bool, requireUDP bool, after time.Time) {
		t.Helper()
		timer := time.NewTimer(3 * time.Second)
		defer timer.Stop()
		for len(want) > 0 {
			select {
			case b := <-output:
				if b.SentAt.Before(after) {
					continue
				}
				if err := b.Validate(); err != nil {
					t.Fatal(err)
				}
				if !requireUDP {
					delete(want, b.Interface)
					continue
				}
				for _, flow := range b.Flows {
					if flow.Protocol == "udp" && flow.LocalPort != 0 && flow.RemotePort == 39801 {
						delete(want, b.Interface)
					}
				}
			case <-timer.C:
				t.Fatalf("capture did not produce expected interface windows: %v", want)
			}
		}
	}
	waitInterfaces(map[string]bool{"nr4": true, "nr6": true}, false, time.Time{})
	for _, endpoint := range []struct{ network, address string }{{"udp4", "192.0.2.2:39801"}, {"udp6", "[2001:db8:3::2]:39801"}} {
		conn, err := net.DialTimeout(endpoint.network, endpoint.address, time.Second)
		if err != nil {
			t.Fatal("isolated UDP endpoint unavailable")
		}
		_ = conn.SetDeadline(time.Now().Add(time.Second))
		for range 5 {
			if _, err := conn.Write([]byte("synthetic")); err != nil {
				t.Fatal(err)
			}
			var reply [32]byte
			if _, err := conn.Read(reply[:]); err != nil {
				t.Fatal(err)
			}
		}
		conn.Close()
	}
	waitInterfaces(map[string]bool{"nr4": true, "nr6": true}, true, time.Time{})
	ip, err := exec.LookPath("ip")
	if err != nil {
		t.Fatal("iproute2 unavailable")
	}
	if exec.CommandContext(ctx, ip, "-4", "route", "del", "default").Run() != nil {
		t.Fatal("fixture route deletion failed")
	}
	resolve([]string{"nr6"})
	changed := time.Now().Add(1500 * time.Millisecond)
	waitInterfaces(map[string]bool{"nr6": true}, false, changed)
	if exec.CommandContext(ctx, ip, "-4", "route", "add", "default", "via", "192.0.2.2", "dev", "nr4").Run() != nil {
		t.Fatal("fixture route restoration failed")
	}
	resolve([]string{"nr4", "nr6"})
	waitInterfaces(map[string]bool{"nr4": true, "nr6": true}, false, time.Now())
}
