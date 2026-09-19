// SPDX-License-Identifier: MIT

package protocol

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"strings"
	"time"

	"github.com/littlesho/NodeRampart/internal/model"
)

const (
	Version          = 3
	MaxFrameSize     = 1 << 20
	MaxFlowsPerBatch = 4096
	MaxInterfaces    = 8
	MaxBatchPackets  = 1 << 36
	MaxBatchBytes    = 1 << 40
	// Longest unzoned IPv6 text, including a dotted IPv4 suffix.
	MaxRemoteIPText = 45
	maxFlowPackets  = 1 << 36
	maxFlowBytes    = 1 << 40
)

func ValidInterfaceName(name string) bool {
	return name != "" && len(name) <= 15 && !strings.Contains(name, "/") && strings.IndexFunc(name, func(r rune) bool { return r <= 0x20 || r == 0x7f }) < 0
}

type Flow struct {
	Direction  model.Direction `json:"direction"`
	RemoteIP   string          `json:"remote_ip"`
	Protocol   string          `json:"protocol"`
	LocalPort  uint16          `json:"local_port,omitempty"`
	RemotePort uint16          `json:"remote_port,omitempty"`
	TCPFlags   uint8           `json:"tcp_flags,omitempty"`
	Packets    uint64          `json:"packets"`
	Bytes      uint64          `json:"bytes"`
}

type Batch struct {
	// ConnectionID is assigned by the daemon after authenticated decoding.
	// It is not a wire field and cannot be supplied by a sensor JSON frame.
	ConnectionID      uint64    `json:"-"`
	ProtocolVersion   int       `json:"protocol_version"`
	SentAt            time.Time `json:"sent_at_utc"`
	IntervalMillis    int64     `json:"interval_millis"`
	Interface         string    `json:"interface"`
	Flows             []Flow    `json:"flows"`
	RXBytes           uint64    `json:"rx_bytes"`
	TXBytes           uint64    `json:"tx_bytes"`
	RXPackets         uint64    `json:"rx_packets"`
	TXPackets         uint64    `json:"tx_packets"`
	InboundSYN        uint64    `json:"inbound_syn_packets"`
	InboundUDP        uint64    `json:"inbound_udp_packets"`
	InboundICMP       uint64    `json:"inbound_icmp_packets"`
	KernelPackets     uint64    `json:"kernel_packets"`
	KernelDrops       uint64    `json:"kernel_drops"`
	KernelStatsErrors uint64    `json:"kernel_stats_errors"`
	OverflowBytes     uint64    `json:"overflow_bytes"`
	OverflowPackets   uint64    `json:"overflow_packets"`
	ParseErrors       uint64    `json:"parse_errors"`
	// IPC loss is an estimate: a failed write does not prove that the peer
	// received no bytes. These counters never contribute to traffic rates.
	IPCDroppedBatches       uint64 `json:"ipc_dropped_batches,omitempty"`
	IPCDroppedPackets       uint64 `json:"ipc_dropped_packets,omitempty"`
	IPCDroppedBytes         uint64 `json:"ipc_dropped_bytes,omitempty"`
	HealthCountersSaturated bool   `json:"health_counters_saturated,omitempty"`
}

func (b *Batch) Validate() error {
	return b.ValidateAt(time.Now())
}

// ValidateAt shares structural validation with offline replay. Live callers use
// Validate, retaining the wall-clock skew bound; replay supplies its file clock.
func (b *Batch) ValidateAt(reference time.Time) error {
	// New daemons accept the previous sensor during a daemon-first upgrade.
	// Version 3 adds remote ports for bounded UDP request/reply correlation.
	// Older daemons reject new fields, so upgrade the daemon before the sensor.
	if b.ProtocolVersion < 1 || b.ProtocolVersion > Version {
		return fmt.Errorf("unsupported sensor protocol version %d", b.ProtocolVersion)
	}
	if b.SentAt.IsZero() || b.SentAt.Year() < 1970 || b.SentAt.Year() > 9999 || reference.IsZero() {
		return errors.New("missing sent_at_utc")
	}
	if delta := reference.Sub(b.SentAt); delta > 10*time.Minute || delta < -10*time.Minute {
		return errors.New("sent_at_utc outside clock-skew bounds")
	}
	if b.IntervalMillis < 100 || b.IntervalMillis > 60_000 {
		return errors.New("interval_millis outside safe bounds")
	}
	if b.Interface == "" || len(b.Interface) > 15 {
		return errors.New("interface name is missing or too long")
	}
	for _, character := range b.Interface {
		if character <= 0x20 || character == 0x7f || character == '/' {
			return errors.New("interface name contains unsafe characters")
		}
	}
	if len(b.Flows) > MaxFlowsPerBatch {
		return errors.New("too many flows")
	}
	var flowRXBytes, flowTXBytes, flowRXPackets, flowTXPackets uint64
	for i := range b.Flows {
		f := &b.Flows[i]
		if f.Direction != model.DirectionInbound && f.Direction != model.DirectionOutbound {
			return fmt.Errorf("flow %d has invalid direction", i)
		}
		if len(f.RemoteIP) > MaxRemoteIPText || strings.Contains(f.RemoteIP, "%") {
			return fmt.Errorf("flow %d has an oversized or zoned remote IP", i)
		}
		address, err := netip.ParseAddr(f.RemoteIP)
		if err != nil {
			return fmt.Errorf("flow %d has invalid remote IP: %w", i, err)
		}
		// Detection and GeoIP must use the same identity for equivalent text
		// and IPv4-mapped addresses. Enrichment also uses Addr.Unmap.
		f.RemoteIP = address.Unmap().String()
		if f.Packets == 0 || f.Bytes == 0 {
			return fmt.Errorf("flow %d has zero counters", i)
		}
		if f.Packets > maxFlowPackets || f.Bytes > maxFlowBytes {
			return fmt.Errorf("flow %d counters exceed safe bounds", i)
		}
		if f.Protocol != "tcp" && f.Protocol != "udp" && f.Protocol != "icmp" && f.Protocol != "icmpv6" && f.Protocol != "other" {
			return fmt.Errorf("flow %d has invalid protocol", i)
		}
		if f.Direction == model.DirectionInbound {
			flowRXBytes += f.Bytes
			flowRXPackets += f.Packets
		} else {
			flowTXBytes += f.Bytes
			flowTXPackets += f.Packets
		}
	}
	for _, counter := range []uint64{b.RXBytes, b.TXBytes, b.OverflowBytes, b.IPCDroppedBytes} {
		if counter > MaxBatchBytes {
			return errors.New("batch byte counter exceeds safe bounds")
		}
	}
	for _, counter := range []uint64{b.RXPackets, b.TXPackets, b.InboundSYN, b.InboundUDP, b.InboundICMP, b.KernelPackets, b.KernelDrops, b.KernelStatsErrors, b.OverflowPackets, b.ParseErrors, b.IPCDroppedBatches, b.IPCDroppedPackets} {
		if counter > MaxBatchPackets {
			return errors.New("batch packet counter exceeds safe bounds")
		}
	}
	if flowRXBytes > b.RXBytes || flowTXBytes > b.TXBytes || flowRXPackets > b.RXPackets || flowTXPackets > b.TXPackets {
		return errors.New("flow counters exceed batch totals")
	}
	if b.InboundSYN+b.InboundUDP+b.InboundICMP > b.RXPackets {
		return errors.New("inbound protocol counters exceed received packets")
	}
	return nil
}

func WriteFrame(w io.Writer, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode frame: %w", err)
	}
	if len(payload) > MaxFrameSize {
		return fmt.Errorf("frame exceeds %d bytes", MaxFrameSize)
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(payload)))
	if err := writeAll(w, header[:]); err != nil {
		return fmt.Errorf("write frame header: %w", err)
	}
	if err := writeAll(w, payload); err != nil {
		return fmt.Errorf("write frame payload: %w", err)
	}
	return nil
}

func ReadFrame(r *bufio.Reader, value any) error {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return err
	}
	size := binary.BigEndian.Uint32(header[:])
	if size == 0 || size > MaxFrameSize {
		return fmt.Errorf("invalid frame size %d", size)
	}
	payload := make([]byte, int(size))
	if _, err := io.ReadFull(r, payload); err != nil {
		return fmt.Errorf("read frame payload: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return fmt.Errorf("decode frame: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("decode frame: trailing JSON data")
	}
	return nil
}

func writeAll(w io.Writer, payload []byte) error {
	for len(payload) > 0 {
		n, err := w.Write(payload)
		if err != nil {
			return err
		}
		if n <= 0 || n > len(payload) {
			return io.ErrShortWrite
		}
		payload = payload[n:]
	}
	return nil
}
