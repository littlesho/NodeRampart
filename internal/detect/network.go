// SPDX-License-Identifier: MIT

package detect

import (
	"fmt"
	"net/netip"
	"strconv"
	"sync"
	"time"

	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/model"
	"github.com/littlesho/NodeRampart/internal/protocol"
)

type floodState struct {
	incidentID          string
	started, lastUpdate time.Time
	quiet               int
}
type scanState struct {
	started, last time.Time
	ports         map[uint16]struct{}
	packets       uint64
	emitted       bool
}

const (
	maxScanSources      = 8192
	maxUDPRequestTuples = 16384
	maxScanPortEntries  = 262144
	udpReplyWindow      = 30 * time.Second
)

// NetworkStats contains process-lifetime counters and current bounded-state
// coverage, with no addresses or packet data. Observe and Stats may run together.
type NetworkStats struct {
	Scope                       string                  `json:"scope,omitempty"`
	Interfaces                  []InterfaceNetworkStats `json:"interfaces,omitempty"`
	InterfaceLimit              int                     `json:"interface_limit,omitempty"`
	EvictedInterfaces           uint64                  `json:"evicted_interfaces,omitempty"`
	ScanSources                 int                     `json:"scan_sources"`
	ScanSourceLimit             int                     `json:"scan_source_limit"`
	ScanIgnoredPackets          uint64                  `json:"scan_ignored_packets"`
	ScanStateSaturated          bool                    `json:"scan_state_saturated"`
	ScanPortEntries             int                     `json:"scan_port_entries"`
	ScanPortLimit               int                     `json:"scan_port_limit"`
	ScanPortStateSaturated      bool                    `json:"scan_port_state_saturated"`
	ScanCoverageComplete        bool                    `json:"scan_coverage_complete"`
	ScanCoverageIncompleteUntil time.Time               `json:"scan_coverage_incomplete_until_utc,omitzero"`
	UDPRequestTuples            int                     `json:"udp_request_tuples"`
	UDPRequestLimit             int                     `json:"udp_request_limit"`
	UDPRequestIgnoredPackets    uint64                  `json:"udp_request_ignored_packets"`
	UDPUnclassifiedPackets      uint64                  `json:"udp_unclassified_packets"`
	UDPRepliesExcludedPackets   uint64                  `json:"udp_replies_excluded_packets"`
	UDPStateSaturated           bool                    `json:"udp_state_saturated"`
	UDPScanCoverageComplete     bool                    `json:"udp_scan_coverage_complete"`
	RecoverySuppressedBatches   uint64                  `json:"recovery_suppressed_batches"`
	CountersSaturated           bool                    `json:"counters_saturated"`
}

type udpTuple struct {
	iface, remoteIP       string
	localPort, remotePort uint16
}

type Network struct {
	mu                 sync.Mutex
	config             config.DetectionConfig
	floods             map[string]*floodState
	scans              map[string]*scanState
	udpRequests        map[udpTuple]time.Time
	udpUncertainUntil  time.Time
	lastBatch          time.Time
	lastInterface      string
	stats              NetworkStats
	scanLimit          int
	udpLimit           int
	continuityReset    bool
	portBudget         *scanPortBudget
	scanPorts          int
	scanUncertainUntil time.Time
}

// One fleet shares this small admission counter. It permits an interface to
// reach the legal 65535-port threshold without multiplying total map capacity.
type scanPortBudget struct {
	mu          sync.Mutex
	used, limit int
}

func (b *scanPortBudget) reserve() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.used >= b.limit {
		return false
	}
	b.used++
	return true
}
func (b *scanPortBudget) release(count int) { b.mu.Lock(); b.used -= count; b.mu.Unlock() }
func (b *scanPortBudget) saturated() bool   { b.mu.Lock(); defer b.mu.Unlock(); return b.used >= b.limit }

func NewNetwork(cfg config.DetectionConfig) *Network {
	return newNetworkLimits(cfg, maxScanSources, maxUDPRequestTuples)
}

func newNetworkLimits(cfg config.DetectionConfig, scans, udp int) *Network {
	return &Network{config: cfg, floods: make(map[string]*floodState), scans: make(map[string]*scanState), udpRequests: make(map[udpTuple]time.Time), scanLimit: scans, udpLimit: udp, portBudget: &scanPortBudget{limit: maxScanPortEntries}}
}

func (n *Network) ResetContinuity() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.lastBatch = time.Time{}
	n.lastInterface = ""
	n.udpRequests = make(map[udpTuple]time.Time)
	n.continuityReset = true
}

func (n *Network) Stats() NetworkStats {
	n.mu.Lock()
	defer n.mu.Unlock()
	stats := n.stats
	stats.ScanSources, stats.ScanSourceLimit = len(n.scans), n.scanLimit
	stats.ScanStateSaturated = len(n.scans) >= n.scanLimit
	stats.ScanPortEntries, stats.ScanPortLimit = n.scanPorts, n.portBudget.limit
	stats.ScanPortStateSaturated = n.portBudget.saturated()
	stats.ScanCoverageIncompleteUntil = n.scanUncertainUntil
	stats.ScanCoverageComplete = !n.lastBatch.Before(n.scanUncertainUntil)
	stats.UDPRequestTuples, stats.UDPRequestLimit = len(n.udpRequests), n.udpLimit
	stats.UDPStateSaturated = len(n.udpRequests) >= n.udpLimit
	return stats
}

func (n *Network) Observe(batch protocol.Batch) []model.Event {
	n.mu.Lock()
	defer n.mu.Unlock()
	seconds := float64(batch.IntervalMillis) / 1000
	if seconds <= 0 {
		return nil
	}
	// IntervalMillis loses less than a millisecond of precision at the sensor.
	// A missing, overlapping, reordered, or different-interface batch cannot
	// establish a consecutive quiet window for incident recovery.
	continuous := n.lastBatch.IsZero() || (batch.SentAt.After(n.lastBatch) &&
		batch.Interface == n.lastInterface &&
		absDuration(batch.SentAt.Sub(n.lastBatch)-time.Duration(batch.IntervalMillis)*time.Millisecond) <= time.Millisecond)
	complete := batch.KernelDrops == 0 && batch.KernelStatsErrors == 0 &&
		batch.ParseErrors == 0 && batch.OverflowPackets == 0 && batch.OverflowBytes == 0 &&
		batch.IPCDroppedBatches == 0 && batch.IPCDroppedPackets == 0 && batch.IPCDroppedBytes == 0 &&
		!batch.HealthCountersSaturated
	if n.lastBatch.IsZero() || !continuous || !complete {
		n.markUDPUncertain(batch.SentAt)
	}
	if batch.SentAt.After(n.lastBatch) {
		n.lastBatch, n.lastInterface = batch.SentAt, batch.Interface
	}
	n.prune(batch.SentAt)
	// Aggregated flows have no ordering within a batch. Collect outbound
	// requests first so a larger reply sorted ahead of its request is excluded.
	ignoredBefore := n.stats.UDPRequestIgnoredPackets
	for _, flow := range batch.Flows {
		if flow.Direction == model.DirectionOutbound && flow.Protocol == "udp" {
			if flow.LocalPort == 0 || flow.RemotePort == 0 {
				n.addCounter(&n.stats.UDPRequestIgnoredPackets, flow.Packets)
			} else {
				n.rememberUDP(flow, batch.Interface, batch.SentAt)
			}
		}
	}
	if n.stats.UDPRequestIgnoredPackets != ignoredBefore || len(n.udpRequests) >= n.udpLimit {
		n.markUDPUncertain(batch.SentAt)
	}
	n.stats.UDPScanCoverageComplete = batch.ProtocolVersion >= 3 && !batch.SentAt.Before(n.udpUncertainUntil) && continuous && complete
	topSources := make(map[string]map[string]protocol.Flow)
	scanIgnoredBefore := n.stats.ScanIgnoredPackets
	for _, flow := range batch.Flows {
		if flow.Direction != model.DirectionInbound {
			continue
		}
		addSource(topSources, "bandwidth_spike", flow)
		switch flow.Protocol {
		case "tcp":
			if flow.TCPFlags&0x02 != 0 && flow.TCPFlags&0x10 == 0 {
				addSource(topSources, "syn_flood", flow)
			}
			// SYN-ACK, ACK/data, FIN, and RST need separate probe evidence;
			// their local ephemeral ports do not establish a SYN scan.
			if flow.LocalPort != 0 && flow.TCPFlags&0x17 == 0x02 {
				n.observePort(flow, batch.SentAt)
			}
		case "udp":
			addSource(topSources, "udp_flood", flow)
			if flow.LocalPort == 0 || flow.RemotePort == 0 {
				n.addCounter(&n.stats.UDPUnclassifiedPackets, flow.Packets)
				continue
			}
			key := udpTuple{batch.Interface, flow.RemoteIP, flow.LocalPort, flow.RemotePort}
			if _, ok := n.udpRequests[key]; ok {
				n.addCounter(&n.stats.UDPRepliesExcludedPackets, flow.Packets)
			} else if !n.stats.UDPScanCoverageComplete {
				n.addCounter(&n.stats.UDPUnclassifiedPackets, flow.Packets)
			} else {
				n.observePort(flow, batch.SentAt)
			}
		case "icmp", "icmpv6":
			addSource(topSources, "icmp_flood", flow)
		}
	}
	reliable := !n.continuityReset && continuous && complete && n.stats.ScanIgnoredPackets == scanIgnoredBefore &&
		n.stats.UDPRequestIgnoredPackets == ignoredBefore && !n.stats.CountersSaturated
	n.continuityReset = false
	rates := []struct {
		kind      string
		value     float64
		threshold uint64
		severity  model.Severity
		unit      string
	}{
		{"syn_flood", float64(batch.InboundSYN) / seconds, n.config.SYNPacketsPerSecond, model.SeverityHigh, "pps"},
		{"udp_flood", float64(batch.InboundUDP) / seconds, n.config.UDPPacketsPerSecond, model.SeverityHigh, "pps"},
		{"icmp_flood", float64(batch.InboundICMP) / seconds, n.config.ICMPPacketsPerSecond, model.SeverityMedium, "pps"},
		{"bandwidth_spike", float64(batch.RXBytes) / seconds, n.config.BytesPerSecond, model.SeverityHigh, "bytes/s"},
	}
	var events []model.Event
	suppressed := false
	for _, rate := range rates {
		if rate.threshold == 0 {
			continue
		}
		flow := topSource(topSources[rate.kind], rate.kind == "bandwidth_spike")
		if !reliable && n.floods[rate.kind] != nil && rate.value < float64(rate.threshold)*n.config.RecoveryRatio {
			suppressed = true
		}
		event := n.observeFlood(rate.kind, rate.value, float64(rate.threshold), rate.severity, rate.unit, flow, batch.SentAt, reliable)
		if event != nil {
			event.Evidence["coverage_complete"] = strconv.FormatBool(reliable)
			event.Evidence["window_millis"] = strconv.FormatInt(batch.IntervalMillis, 10)
			events = append(events, *event)
		}
	}
	if suppressed {
		n.addCounter(&n.stats.RecoverySuppressedBatches, 1)
	}
	for _, event := range n.scanEvents(batch.SentAt) {
		event.Evidence["coverage_complete"] = strconv.FormatBool(!batch.SentAt.Before(n.scanUncertainUntil))
		events = append(events, event)
	}
	return events
}

func absDuration(value time.Duration) time.Duration {
	if value < 0 {
		return -value
	}
	return value
}

func (n *Network) prune(now time.Time) {
	for source, state := range n.scans {
		if now.Sub(state.started) > n.config.ScanWindow.Duration {
			n.deleteScan(source)
		}
	}
	for key, last := range n.udpRequests {
		if now.Sub(last) >= udpReplyWindow {
			delete(n.udpRequests, key)
		}
	}
}

func (n *Network) rememberUDP(flow protocol.Flow, iface string, now time.Time) {
	key := udpTuple{iface, flow.RemoteIP, flow.LocalPort, flow.RemotePort}
	if _, exists := n.udpRequests[key]; !exists && len(n.udpRequests) >= n.udpLimit {
		n.addCounter(&n.stats.UDPRequestIgnoredPackets, flow.Packets)
		return
	}
	if now.After(n.udpRequests[key]) {
		n.udpRequests[key] = now
	}
}

func (n *Network) markUDPUncertain(now time.Time) {
	if now.Before(n.lastBatch) {
		now = n.lastBatch
	}
	if until := now.Add(udpReplyWindow); until.After(n.udpUncertainUntil) {
		n.udpUncertainUntil = until
	}
}

func (n *Network) addCounter(counter *uint64, value uint64) {
	if ^uint64(0)-*counter < value {
		*counter = ^uint64(0)
		n.stats.CountersSaturated = true
	} else {
		*counter += value
	}
}

func (n *Network) observeFlood(kind string, value, threshold float64, severity model.Severity, unit string, flow protocol.Flow, now time.Time, reliable bool) *model.Event {
	state := n.floods[kind]
	if value >= threshold {
		if state == nil {
			state = &floodState{incidentID: model.NewID("inc"), started: now, lastUpdate: now}
			n.floods[kind] = state
			return floodEvent(kind, "start", state, value, threshold, severity, unit, flow, now)
		}
		state.quiet = 0
		if now.Sub(state.lastUpdate) >= n.config.UpdateInterval.Duration {
			state.lastUpdate = now
			return floodEvent(kind, "update", state, value, threshold, severity, unit, flow, now)
		}
		return nil
	}
	if state != nil && !reliable {
		state.quiet = 0
		return nil
	}
	if state != nil && value < threshold*n.config.RecoveryRatio {
		state.quiet++
		if state.quiet >= n.config.RecoveryWindows {
			delete(n.floods, kind)
			event := floodEvent(kind, "recovery", state, value, threshold, model.SeverityInfo, unit, flow, now)
			event.Summary = fmt.Sprintf("rate recovered to %.0f %s after %s", value, unit, now.Sub(state.started).Round(time.Second))
			return event
		}
	} else if state != nil {
		state.quiet = 0
	}
	return nil
}

func floodEvent(kind, phase string, state *floodState, value, threshold float64, severity model.Severity, unit string, flow protocol.Flow, now time.Time) *model.Event {
	event := &model.Event{ID: model.NewID("evt"), IncidentID: state.incidentID, ObservedAt: now.UTC(), Kind: kind, Phase: phase, Severity: severity, Count: uint64(value), Summary: fmt.Sprintf("observed %.0f %s; configured threshold %.0f %s", value, unit, threshold, unit), Evidence: map[string]string{"threshold": fmt.Sprintf("%.0f %s", threshold, unit), "observed_rate": strconv.FormatFloat(value, 'f', -1, 64), "threshold_rate": strconv.FormatFloat(threshold, 'f', -1, 64)}}
	if phase == "recovery" {
		event.Evidence["incident_duration_millis"] = strconv.FormatInt(now.Sub(state.started).Milliseconds(), 10)
	}
	if flow.RemoteIP != "" {
		event.SourceIP = flow.RemoteIP
		event.SourceRange = PrefixString(flow.RemoteIP)
		if flow.Protocol != "" {
			event.Target = flow.Protocol
			if flow.LocalPort != 0 {
				event.Target += "/" + strconv.Itoa(int(flow.LocalPort))
			}
		}
		event.Evidence["top_source_packets"] = strconv.FormatUint(flow.Packets, 10)
		event.Evidence["top_source_bytes"] = strconv.FormatUint(flow.Bytes, 10)
		event.Evidence["source_scope"] = "retained_flows"
	}
	return event
}

// Retain one aggregate per source, bounded by the accepted batch flow limit.
// A source spread across several services can exceed any single large flow.
func addSource(sources map[string]map[string]protocol.Flow, kind string, flow protocol.Flow) {
	if sources[kind] == nil {
		sources[kind] = make(map[string]protocol.Flow)
	}
	aggregate, exists := sources[kind][flow.RemoteIP]
	if !exists {
		sources[kind][flow.RemoteIP] = flow
		return
	}
	aggregate.Packets += flow.Packets
	aggregate.Bytes += flow.Bytes
	if aggregate.Protocol != flow.Protocol {
		aggregate.Protocol = ""
	}
	if aggregate.LocalPort != flow.LocalPort {
		aggregate.LocalPort = 0
	}
	sources[kind][flow.RemoteIP] = aggregate
}

func topSource(sources map[string]protocol.Flow, byBytes bool) protocol.Flow {
	var top protocol.Flow
	for _, source := range sources {
		value, best := source.Packets, top.Packets
		if byBytes {
			value, best = source.Bytes, top.Bytes
		}
		if value > best || (value == best && (top.RemoteIP == "" || source.RemoteIP < top.RemoteIP)) {
			top = source
		}
	}
	return top
}

func (n *Network) observePort(flow protocol.Flow, now time.Time) {
	state := n.scans[flow.RemoteIP]
	if state == nil || now.Sub(state.started) > n.config.ScanWindow.Duration {
		if state == nil && len(n.scans) >= n.scanLimit {
			n.addCounter(&n.stats.ScanIgnoredPackets, flow.Packets)
			n.markScanIncomplete(now)
			return
		}
		if state != nil {
			n.deleteScan(flow.RemoteIP)
		}
		state = &scanState{started: now, ports: make(map[uint16]struct{})}
		n.scans[flow.RemoteIP] = state
	}
	state.last = now
	if !state.emitted && len(state.ports) < n.config.ScanUniquePorts {
		if _, exists := state.ports[flow.LocalPort]; !exists {
			if n.portBudget.reserve() {
				state.ports[flow.LocalPort] = struct{}{}
				n.scanPorts++
			} else {
				n.addCounter(&n.stats.ScanIgnoredPackets, flow.Packets)
				n.markScanIncomplete(now)
			}
		}
	}
	if ^uint64(0)-state.packets < flow.Packets {
		state.packets = ^uint64(0)
	} else {
		state.packets += flow.Packets
	}
}

func (n *Network) markScanIncomplete(at time.Time) {
	if until := at.Add(n.config.ScanWindow.Duration + time.Nanosecond); until.After(n.scanUncertainUntil) {
		n.scanUncertainUntil = until
	}
}

func (n *Network) scanEvents(now time.Time) []model.Event {
	var events []model.Event
	for source, state := range n.scans {
		if now.Sub(state.last) > n.config.ScanWindow.Duration {
			n.deleteScan(source)
			continue
		}
		if !state.emitted && len(state.ports) >= n.config.ScanUniquePorts {
			state.emitted = true
			events = append(events, model.Event{ID: model.NewID("evt"), IncidentID: model.NewID("inc"), ObservedAt: now.UTC(), Kind: "port_scan", Phase: "start", Severity: model.SeverityMedium, SourceIP: source, SourceRange: PrefixString(source), Count: uint64(len(state.ports)), Summary: fmt.Sprintf("at least %d unique local ports probed in %s", len(state.ports), now.Sub(state.started).Round(time.Second)), Evidence: map[string]string{"packets": strconv.FormatUint(state.packets, 10), "rule": "inbound_syn_or_unsolicited_udp", "threshold": strconv.Itoa(n.config.ScanUniquePorts), "window_seconds": strconv.FormatFloat(n.config.ScanWindow.Seconds(), 'f', -1, 64), "elapsed_millis": strconv.FormatInt(now.Sub(state.started).Milliseconds(), 10)}})
		}
	}
	return events
}

func (n *Network) deleteScan(source string) {
	if state := n.scans[source]; state != nil {
		n.portBudget.release(len(state.ports))
		n.scanPorts -= len(state.ports)
		delete(n.scans, source)
	}
}

func (n *Network) releaseScanState() {
	n.mu.Lock()
	defer n.mu.Unlock()
	for source := range n.scans {
		n.deleteScan(source)
	}
}

func PrefixString(value string) string {
	address, err := netip.ParseAddr(value)
	if err != nil {
		return ""
	}
	address = address.Unmap()
	bits := 48
	if address.Is4() {
		bits = 24
	}
	return netip.PrefixFrom(address, bits).Masked().String()
}
