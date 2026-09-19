// SPDX-License-Identifier: MIT

package detect

import (
	"sort"
	"sync"
	"time"

	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/model"
	"github.com/littlesho/NodeRampart/internal/protocol"
)

type InterfaceNetworkStats struct {
	Name     string       `json:"name"`
	LastSeen time.Time    `json:"last_seen_utc"`
	Stats    NetworkStats `json:"stats"`
}

type interfaceDetector struct {
	detector   *Network
	lastSeen   time.Time
	connection uint64
}

type Fleet struct {
	mu         sync.Mutex
	config     config.DetectionConfig
	limit      int
	interfaces map[string]*interfaceDetector
	evicted    uint64
}

func NewFleet(cfg config.DetectionConfig, limit int) *Fleet {
	limit = max(1, min(limit, protocol.MaxInterfaces))
	return &Fleet{config: cfg, limit: limit, interfaces: make(map[string]*interfaceDetector)}
}

func (f *Fleet) Observe(batch protocol.Batch) []model.Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !protocol.ValidInterfaceName(batch.Interface) {
		return nil
	}
	entry := f.interfaces[batch.Interface]
	if entry == nil {
		if len(f.interfaces) >= f.limit {
			oldest := ""
			for name, value := range f.interfaces {
				if oldest == "" || value.lastSeen.Before(f.interfaces[oldest].lastSeen) || value.lastSeen.Equal(f.interfaces[oldest].lastSeen) && name < oldest {
					oldest = name
				}
			}
			delete(f.interfaces, oldest)
			if f.evicted < ^uint64(0) {
				f.evicted++
			}
		}
		entry = &interfaceDetector{detector: newNetworkLimits(f.config, maxScanSources/f.limit, maxUDPRequestTuples/f.limit), connection: batch.ConnectionID}
		f.interfaces[batch.Interface] = entry
	} else if entry.connection != batch.ConnectionID {
		entry.detector.ResetContinuity()
		entry.connection = batch.ConnectionID
	}
	if !entry.lastSeen.IsZero() && !batch.SentAt.After(entry.lastSeen) {
		return nil
	}
	entry.lastSeen = batch.SentAt
	events := entry.detector.Observe(batch)
	for i := range events {
		if events[i].Evidence == nil {
			events[i].Evidence = map[string]string{}
		}
		events[i].Evidence["interface"] = batch.Interface
	}
	return events
}

func (f *Fleet) Stats() NetworkStats {
	f.mu.Lock()
	defer f.mu.Unlock()
	result := NetworkStats{Scope: "per_interface", InterfaceLimit: f.limit, EvictedInterfaces: f.evicted, Interfaces: []InterfaceNetworkStats{}, ScanSourceLimit: maxScanSources / f.limit * f.limit, UDPRequestLimit: maxUDPRequestTuples / f.limit * f.limit, UDPScanCoverageComplete: len(f.interfaces) > 0}
	names := make([]string, 0, len(f.interfaces))
	for name := range f.interfaces {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		entry := f.interfaces[name]
		stats := entry.detector.Stats()
		result.Interfaces = append(result.Interfaces, InterfaceNetworkStats{Name: name, LastSeen: entry.lastSeen, Stats: stats})
		result.ScanSources += stats.ScanSources
		result.UDPRequestTuples += stats.UDPRequestTuples
		result.ScanStateSaturated = result.ScanStateSaturated || stats.ScanStateSaturated
		result.UDPStateSaturated = result.UDPStateSaturated || stats.UDPStateSaturated
		result.UDPScanCoverageComplete = result.UDPScanCoverageComplete && stats.UDPScanCoverageComplete
		result.CountersSaturated = result.CountersSaturated || stats.CountersSaturated
		for _, counter := range []struct {
			dst   *uint64
			value uint64
		}{{&result.ScanIgnoredPackets, stats.ScanIgnoredPackets}, {&result.UDPRequestIgnoredPackets, stats.UDPRequestIgnoredPackets}, {&result.UDPUnclassifiedPackets, stats.UDPUnclassifiedPackets}, {&result.UDPRepliesExcludedPackets, stats.UDPRepliesExcludedPackets}, {&result.RecoverySuppressedBatches, stats.RecoverySuppressedBatches}} {
			if counter.value > ^uint64(0)-*counter.dst {
				*counter.dst = ^uint64(0)
				result.CountersSaturated = true
			} else {
				*counter.dst += counter.value
			}
		}
	}
	return result
}
