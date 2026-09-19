// SPDX-License-Identifier: MIT

//go:build linux

package sensor

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/detect"
	"github.com/littlesho/NodeRampart/internal/model"
)

const (
	netnsExchanges = 20
	netnsUDPPort   = 39001
	netnsTCPPort   = 39002
)

var netnsPayload = []byte("noderampart isolated reply test")

// This test is only enabled by scripts/test-netns.py in an explicitly authorized
// disposable VM. It never creates namespaces or changes the host network itself.
func TestNetNSNormalReplies(t *testing.T) {
	if os.Getenv("NODERAMPART_NETNS_LAB") != "authorized-disposable-vm-v1" {
		t.Skip("privileged AF_PACKET fixture: use scripts/test-netns.py in an explicitly authorized disposable VM")
	}
	role := requireNetNSLab(t)
	if role == "server" {
		runNetNSServer(t)
		return
	}
	runNetNSCapture(t)
}

func requireNetNSLab(t *testing.T) string {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Fatal("fixture requires root inside an authorized disposable VM namespace")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, "/usr/bin/systemd-detect-virt", "--vm", "--quiet").Run(); err != nil {
		t.Fatal("fixture requires successful VM detection")
	}
	role := os.Getenv("NODERAMPART_NETNS_ROLE")
	interfaceName, expectedAddress, suffix := "nrclient", "192.0.2.1/30", "c"
	if role == "server" {
		interfaceName, expectedAddress, suffix = "nrserver", "192.0.2.2/30", "s"
	} else if role != "client" {
		t.Fatal("fixture role is invalid")
	}
	name := os.Getenv("NODERAMPART_NETNS_NAME")
	if !regexp.MustCompile(`^nr-test-[a-f0-9]{24}-` + suffix + `$`).MatchString(name) {
		t.Fatal("fixture namespace name is invalid")
	}
	current, err := os.Stat("/proc/self/ns/net")
	if err != nil {
		t.Fatal("cannot verify current network namespace")
	}
	initial, err := os.Stat("/proc/1/ns/net")
	if err != nil || os.SameFile(current, initial) {
		t.Fatal("fixture must never run in the initial network namespace")
	}
	expected, err := os.Stat(filepath.Join("/run/netns", name))
	if err != nil || !os.SameFile(current, expected) {
		t.Fatal("fixture namespace does not match the harness-created namespace")
	}
	interfaces, err := net.Interfaces()
	if err != nil || len(interfaces) != 2 {
		t.Fatal("fixture requires only loopback and one isolated veth")
	}
	seenVeth := false
	for _, iface := range interfaces {
		if iface.Name == "lo" {
			continue
		}
		if iface.Name != interfaceName {
			t.Fatal("unexpected interface in fixture namespace")
		}
		addresses, err := iface.Addrs()
		if err != nil {
			t.Fatal("cannot verify isolated interface address")
		}
		for _, address := range addresses {
			ip, _, err := net.ParseCIDR(address.String())
			if err != nil {
				t.Fatal("invalid isolated interface address")
			}
			if ip.To4() != nil {
				if address.String() != expectedAddress {
					t.Fatal("unexpected IPv4 address in fixture namespace")
				}
				seenVeth = true
			} else if !ip.IsLinkLocalUnicast() {
				t.Fatal("unexpected routable IPv6 address in fixture namespace")
			}
		}
	}
	if !seenVeth {
		t.Fatal("isolated veth address is missing")
	}
	routes, err := os.ReadFile("/proc/net/route")
	if err != nil {
		t.Fatal("cannot verify isolated routes")
	}
	for _, line := range strings.Split(strings.TrimSpace(string(routes)), "\n")[1:] {
		fields := strings.Fields(line)
		// Only the connected 192.0.2.0/30 route is permitted, never a gateway.
		if len(fields) < 8 || fields[0] != interfaceName || fields[1] != "000200C0" || fields[2] != "00000000" || fields[7] != "FCFFFFFF" {
			t.Fatal("unexpected route in fixture namespace")
		}
	}
	directory := os.Getenv("NODERAMPART_NETNS_DIRECTORY")
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 || !strings.HasPrefix(filepath.Base(directory), "noderampart-netns-") {
		t.Fatal("fixture requires a private harness directory")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 {
		t.Fatal("fixture directory must be root-owned")
	}
	return role
}

func runNetNSServer(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(12 * time.Second)
	udp, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("192.0.2.2"), Port: netnsUDPPort})
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	if err := udp.SetDeadline(deadline); err != nil {
		t.Fatal(err)
	}
	tcp, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.ParseIP("192.0.2.2"), Port: netnsTCPPort})
	if err != nil {
		t.Fatal(err)
	}
	defer tcp.Close()
	if err := tcp.SetDeadline(deadline); err != nil {
		t.Fatal(err)
	}
	ready, err := os.OpenFile(filepath.Join(os.Getenv("NODERAMPART_NETNS_DIRECTORY"), "server-ready"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err := ready.Close(); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 128)
	for i := 0; i < netnsExchanges; i++ {
		n, peer, err := udp.ReadFromUDP(buffer)
		if err != nil {
			t.Fatal(err)
		}
		if !peer.IP.Equal(net.ParseIP("192.0.2.1")) || peer.Port != 41000+i || !bytes.Equal(buffer[:n], netnsPayload) {
			t.Fatal("unexpected isolated UDP request")
		}
		if _, err := udp.WriteToUDP(buffer[:n], peer); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < netnsExchanges; i++ {
		connection, err := tcp.AcceptTCP()
		if err != nil {
			t.Fatal(err)
		}
		peer := connection.RemoteAddr().(*net.TCPAddr)
		if !peer.IP.Equal(net.ParseIP("192.0.2.1")) || peer.Port != 42000+i {
			connection.Close()
			t.Fatal("unexpected isolated TCP request")
		}
		err = connection.SetDeadline(deadline)
		if err == nil {
			_, err = io.ReadFull(connection, buffer[:len(netnsPayload)])
		}
		if err == nil && !bytes.Equal(buffer[:len(netnsPayload)], netnsPayload) {
			err = fmt.Errorf("unexpected isolated TCP payload")
		}
		if err == nil {
			_, err = connection.Write(netnsPayload)
		}
		connection.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
}

func exchangeNetNSTraffic(ctx context.Context) error {
	for _, transport := range []string{"udp4", "tcp4"} {
		for i := 0; i < netnsExchanges; i++ {
			port := netnsUDPPort
			dialer := net.Dialer{Timeout: time.Second, LocalAddr: &net.UDPAddr{IP: net.ParseIP("192.0.2.1"), Port: 41000 + i}}
			if transport == "tcp4" {
				port = netnsTCPPort
				dialer.LocalAddr = &net.TCPAddr{IP: net.ParseIP("192.0.2.1"), Port: 42000 + i}
			}
			connection, err := dialer.DialContext(ctx, transport, fmt.Sprintf("192.0.2.2:%d", port))
			if err != nil {
				return err
			}
			err = connection.SetDeadline(time.Now().Add(time.Second))
			if err == nil {
				_, err = connection.Write(netnsPayload)
			}
			buffer := make([]byte, len(netnsPayload))
			if err == nil {
				_, err = io.ReadFull(connection, buffer)
			}
			connection.Close()
			if err != nil {
				return err
			}
			if !bytes.Equal(buffer, netnsPayload) {
				return fmt.Errorf("isolated reply payload mismatch")
			}
		}
	}
	return nil
}

func runNetNSCapture(t *testing.T) {
	t.Helper()
	capture, err := OpenCapture("nrclient", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer capture.Close()
	// Bound the blocking receive during teardown without changing production
	// socket behavior. Cancellation is checked by Run when this timeout expires.
	if err := syscall.SetsockoptTimeval(capture.fd, syscall.SOL_SOCKET, syscall.SO_RCVTIMEO, &syscall.Timeval{Sec: 5}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	packets := make(chan Packet, 2048)
	captured := make(chan error, 1)
	go func() { captured <- capture.Run(ctx, packets) }()
	defer func() {
		cancel()
		select {
		case err := <-captured:
			if err != nil {
				t.Errorf("capture stopped unexpectedly: %v", err)
			}
		case <-time.After(6 * time.Second):
			t.Error("capture teardown exceeded its receive deadline")
		}
	}()
	traffic := make(chan error, 1)
	go func() { traffic <- exchangeNetNSTraffic(ctx) }()
	started := time.Now()
	aggregator := NewAggregator("nrclient", 512, started)
	// Four observations per local port: outgoing UDP, incoming UDP, outgoing
	// TCP SYN, incoming TCP SYN-ACK. Seeing all prevents a quiet/no-capture pass.
	var seen [4][netnsExchanges]bool
	observations := 0
	trafficFinished := false
	for observations != 4*netnsExchanges || !trafficFinished {
		select {
		case err := <-traffic:
			if err != nil {
				t.Fatal(err)
			}
			trafficFinished = true
		case packet := <-packets:
			aggregator.Observe(packet)
			if packet.Protocol != "tcp" && packet.Protocol != "udp" {
				continue
			}
			if packet.RemoteIP.String() != "192.0.2.2" {
				t.Fatal("capture observed an unexpected remote address")
			}
			kind, index := 0, int(packet.LocalPort)-41000
			if packet.Protocol == "tcp" {
				if packet.RemotePort != netnsTCPPort {
					t.Fatal("TCP RemotePort was not preserved")
				}
				kind, index = 2, int(packet.LocalPort)-42000
				if packet.TCPFlags&0x02 == 0 {
					continue
				}
				if packet.Direction == model.DirectionInbound && packet.TCPFlags&0x12 != 0x12 {
					t.Fatal("inbound normal TCP handshake was not SYN-ACK")
				}
			} else if packet.RemotePort != netnsUDPPort {
				t.Fatal("UDP RemotePort was not preserved")
			}
			if packet.Direction == model.DirectionInbound {
				kind++
			}
			if index < 0 || index >= netnsExchanges {
				t.Fatal("capture LocalPort was not preserved")
			}
			if !seen[kind][index] {
				seen[kind][index] = true
				observations++
			}
		case <-ctx.Done():
			t.Fatalf("capture did not observe all bounded request/reply tuples: %d/%d", observations, 4*netnsExchanges)
		}
	}
	batch := aggregator.Flush(started.Add(time.Second))
	_, drops, err := capture.Stats()
	if err != nil || drops != 0 || capture.ParseErrors() != 0 || batch.OverflowPackets != 0 {
		t.Fatal("isolated capture had kernel, parser, or aggregation loss")
	}
	detector := detect.NewNetwork(config.Defaults().Detection)
	for _, event := range detector.Observe(batch) {
		if event.Kind == "port_scan" {
			t.Fatal("ordinary namespace TCP/UDP replies produced a port scan")
		}
	}
	stats := detector.Stats()
	if stats.ScanSources != 0 || stats.UDPRepliesExcludedPackets != netnsExchanges || stats.UDPRequestTuples != netnsExchanges || stats.UDPUnclassifiedPackets != 0 {
		t.Fatalf("ordinary replies did not retain exact tuple correlation: %+v", stats)
	}
	t.Log("observed all 20 UDP and 20 TCP request/reply tuples with RemotePort; no scan sources")
}
