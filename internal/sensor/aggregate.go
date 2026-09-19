// SPDX-License-Identifier: MIT

package sensor

import (
	"sort"
	"sync"
	"time"

	"github.com/littlesho/NodeRampart/internal/model"
	"github.com/littlesho/NodeRampart/internal/protocol"
)

type flowKey struct {
	direction  model.Direction
	remoteIP   string
	protocol   string
	localPort  uint16
	remotePort uint16
	tcpFlags   uint8
}

type counters struct {
	packets uint64
	bytes   uint64
}

type Aggregator struct {
	mu                                          sync.Mutex
	started                                     time.Time
	iface                                       string
	maxFlows                                    int
	flows                                       map[flowKey]counters
	rxBytes, txBytes, rxPackets, txPackets      uint64
	inboundSYN, inboundUDP, inboundICMP         uint64
	overflowBytes, overflowPackets, parseErrors uint64
}

func NewAggregator(iface string, maxFlows int, now time.Time) *Aggregator {
	return &Aggregator{started: now, iface: iface, maxFlows: maxFlows, flows: make(map[flowKey]counters)}
}

func (a *Aggregator) Observe(packet Packet) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if packet.Direction == model.DirectionInbound {
		a.rxBytes += packet.Bytes
		a.rxPackets++
		switch packet.Protocol {
		case "tcp":
			if packet.TCPFlags&0x02 != 0 && packet.TCPFlags&0x10 == 0 {
				a.inboundSYN++
			}
		case "udp":
			a.inboundUDP++
		case "icmp", "icmpv6":
			a.inboundICMP++
		}
	} else {
		a.txBytes += packet.Bytes
		a.txPackets++
	}
	key := flowKey{direction: packet.Direction, remoteIP: packet.RemoteIP.String(), protocol: packet.Protocol, localPort: packet.LocalPort, remotePort: packet.RemotePort, tcpFlags: packet.TCPFlags & 0x17}
	value, exists := a.flows[key]
	if !exists && len(a.flows) >= a.maxFlows {
		a.overflowBytes += packet.Bytes
		a.overflowPackets++
		return
	}
	value.bytes += packet.Bytes
	value.packets++
	a.flows[key] = value
}

func (a *Aggregator) ParseError() {
	a.mu.Lock()
	a.parseErrors++
	a.mu.Unlock()
}

func (a *Aggregator) Flush(now time.Time) protocol.Batch {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.flush(now)
}

// flushCurrent assigns the cutoff while holding the observation lock, so no
// packet arriving after that cutoff is included in the completed snapshot.
func (a *Aggregator) flushCurrent() protocol.Batch {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.flush(time.Now().UTC())
}

func (a *Aggregator) flush(now time.Time) protocol.Batch {
	interval := now.Sub(a.started)
	if interval < 100*time.Millisecond {
		interval = 100 * time.Millisecond
	}
	batch := protocol.Batch{
		ProtocolVersion: protocol.Version, SentAt: now.UTC(), IntervalMillis: interval.Milliseconds(), Interface: a.iface,
		RXBytes: a.rxBytes, TXBytes: a.txBytes, RXPackets: a.rxPackets, TXPackets: a.txPackets,
		InboundSYN: a.inboundSYN, InboundUDP: a.inboundUDP, InboundICMP: a.inboundICMP,
		OverflowBytes: a.overflowBytes, OverflowPackets: a.overflowPackets, ParseErrors: a.parseErrors,
		Flows: make([]protocol.Flow, 0, len(a.flows)),
	}
	for key, value := range a.flows {
		batch.Flows = append(batch.Flows, protocol.Flow{Direction: key.direction, RemoteIP: key.remoteIP, Protocol: key.protocol, LocalPort: key.localPort, RemotePort: key.remotePort, TCPFlags: key.tcpFlags, Packets: value.packets, Bytes: value.bytes})
	}
	sort.Slice(batch.Flows, func(i, j int) bool {
		if batch.Flows[i].Bytes != batch.Flows[j].Bytes {
			return batch.Flows[i].Bytes > batch.Flows[j].Bytes
		}
		if batch.Flows[i].RemoteIP != batch.Flows[j].RemoteIP {
			return batch.Flows[i].RemoteIP < batch.Flows[j].RemoteIP
		}
		if batch.Flows[i].Protocol != batch.Flows[j].Protocol {
			return batch.Flows[i].Protocol < batch.Flows[j].Protocol
		}
		if batch.Flows[i].LocalPort != batch.Flows[j].LocalPort {
			return batch.Flows[i].LocalPort < batch.Flows[j].LocalPort
		}
		if batch.Flows[i].RemotePort != batch.Flows[j].RemotePort {
			return batch.Flows[i].RemotePort < batch.Flows[j].RemotePort
		}
		if batch.Flows[i].Direction != batch.Flows[j].Direction {
			return batch.Flows[i].Direction < batch.Flows[j].Direction
		}
		return batch.Flows[i].TCPFlags < batch.Flows[j].TCPFlags
	})
	a.started = now
	a.flows = make(map[flowKey]counters)
	a.rxBytes, a.txBytes, a.rxPackets, a.txPackets = 0, 0, 0, 0
	a.inboundSYN, a.inboundUDP, a.inboundICMP = 0, 0, 0
	a.overflowBytes, a.overflowPackets, a.parseErrors = 0, 0, 0
	return batch
}
