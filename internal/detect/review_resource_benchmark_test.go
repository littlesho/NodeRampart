// SPDX-License-Identifier: MIT

//go:build linux

package detect

import (
	"fmt"
	"net/netip"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/littlesho/NodeRampart/internal/collector"
	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/model"
	"github.com/littlesho/NodeRampart/internal/protocol"
)

// Run with -benchtime=1x. This isolated fixture fills detector budgets and the
// eight-frame decoded queue plus four-MiB retry payload. SQLite, packet receive
// buffers and real MMDB mappings are separate costs, not part of this result.
// Reported live heap/RSS are measured process values, never hard upper bounds.
func BenchmarkReviewDetectorResourceBudget(b *testing.B) {
	if b.N > 8 {
		b.Fatal("bounded resource fixture requires -benchtime=1x (maximum 8 iterations)")
	}
	var heapTotal, rssTotal, cpuTotal uint64
	for range b.N {
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		var usageBefore, usageAfter syscall.Rusage
		if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usageBefore); err != nil {
			b.Fatal(err)
		}
		authCfg := config.Defaults().Auth
		authCfg.Threshold, authCfg.Cooldown.Duration = 2, time.Hour
		auth := NewAuth(authCfg)
		now := time.Now().UTC()
		// The sparse-source shape exercises the bounded map cost in addition
		// to the 2-MiB compact timestamp payload.
		for i := range maxAuthFailureEntries {
			index := i % maxAuthSources
			auth.Observe(collector.AuthObservation{Kind: collector.AuthFailure, ObservedAt: now,
				SourceIP: netip.AddrFrom4([4]byte{198, 18, byte(index >> 8), byte(index)}), Method: "password"})
		}
		if stats := auth.StatsAt(now); stats.FailureCapacity != maxAuthFailureEntries || stats.RejectedFailures != 0 {
			b.Fatalf("auth budget fixture did not fill exactly: %+v", stats)
		}
		netCfg := config.Defaults().Detection
		netCfg.ScanUniquePorts = 65535
		netCfg.SYNPacketsPerSecond, netCfg.UDPPacketsPerSecond, netCfg.ICMPPacketsPerSecond, netCfg.BytesPerSecond = 0, 0, 0, 0
		network := NewNetwork(netCfg)
		for first := 0; first < maxScanPortEntries; first += protocol.MaxFlowsPerBatch {
			batch := protocol.Batch{ProtocolVersion: protocol.Version, Interface: "lab0", SentAt: now.Add(time.Duration(first+1) * time.Microsecond), IntervalMillis: 1000}
			for j := first; j < first+protocol.MaxFlowsPerBatch; j++ {
				index := j % maxScanSources
				batch.Flows = append(batch.Flows, scanBudgetFlow(fmt.Sprintf("198.19.%d.%d", index>>8, index&255), uint16(j/maxScanSources+1)))
			}
			network.Observe(batch)
		}
		for first := 0; first < maxUDPRequestTuples; first += protocol.MaxFlowsPerBatch {
			batch := protocol.Batch{ProtocolVersion: protocol.Version, Interface: "lab0", SentAt: now.Add(time.Second + time.Duration(first+1)*time.Microsecond), IntervalMillis: 1000}
			for j := first; j < first+protocol.MaxFlowsPerBatch; j++ {
				batch.Flows = append(batch.Flows, protocol.Flow{Direction: model.DirectionOutbound, Protocol: "udp", RemoteIP: "192.0.2.1", LocalPort: uint16(j + 1), RemotePort: 53, Packets: 1, Bytes: 60})
			}
			network.Observe(batch)
		}
		if stats := network.Stats(); stats.ScanPortEntries != maxScanPortEntries || stats.UDPRequestTuples != maxUDPRequestTuples {
			b.Fatalf("network budget fixture did not fill: %+v", stats)
		}
		queue := make(chan protocol.Batch, protocol.MaxInterfaces)
		for iface := range protocol.MaxInterfaces {
			batch := protocol.Batch{Interface: fmt.Sprintf("lab%d", iface)}
			for j := range protocol.MaxFlowsPerBatch {
				batch.Flows = append(batch.Flows, protocol.Flow{RemoteIP: fmt.Sprintf("2001:db8:ffff:ffff:ffff:ffff:%04x:%04x", iface, j), Protocol: "tcp"})
			}
			queue <- batch
		}
		retryPayload := make([]byte, 4<<20)
		for i := range retryPayload {
			retryPayload[i] = byte(i)
		}
		runtime.GC()
		runtime.ReadMemStats(&after)
		if after.HeapAlloc >= before.HeapAlloc {
			heapTotal += after.HeapAlloc - before.HeapAlloc
		}
		rssTotal += reviewRSS(b)
		if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usageAfter); err != nil {
			b.Fatal(err)
		}
		cpuTotal += uint64(reviewCPU(usageAfter) - reviewCPU(usageBefore))
		runtime.KeepAlive(auth)
		runtime.KeepAlive(network)
		runtime.KeepAlive(queue)
		runtime.KeepAlive(retryPayload)
	}
	b.ReportMetric(float64(heapTotal)/float64(b.N), "live-heap-delta-B")
	b.ReportMetric(float64(rssTotal)/float64(b.N), "measured-RSS-B")
	b.ReportMetric(float64(cpuTotal)/float64(b.N), "CPU-ns")
	b.ReportMetric(float64(maxAuthFailureEntries)*float64(unsafe.Sizeof(authTimestamp{})), "timestamp-payload-B")
	b.ReportMetric(float64(protocol.MaxInterfaces*protocol.MaxFlowsPerBatch)*float64(unsafe.Sizeof(protocol.Flow{})), "queue-flow-struct-B")
}

func reviewCPU(usage syscall.Rusage) int64 {
	return usage.Utime.Nano() + usage.Stime.Nano()
}

func reviewRSS(b *testing.B) uint64 {
	b.Helper()
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		b.Fatal(err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if fields := strings.Fields(line); len(fields) == 3 && fields[0] == "VmRSS:" && fields[2] == "kB" {
			value, err := strconv.ParseUint(fields[1], 10, 64)
			if err != nil {
				b.Fatal(err)
			}
			return value * 1024
		}
	}
	b.Fatal("Linux RSS field unavailable")
	return 0
}
