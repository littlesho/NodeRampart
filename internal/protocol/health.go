// SPDX-License-Identifier: MIT

package protocol

import "errors"

// Version 5 sends these reset-on-read observations as bounded cumulative
// session/interface counters. The receiver persists the last snapshot with its
// watermark and accounts only its delta. Traffic/rate windows remain separate.
type CollectorHealth struct {
	OverflowBytes     uint64 `json:"overflow_bytes"`
	OverflowPackets   uint64 `json:"overflow_packets"`
	ParseErrors       uint64 `json:"parse_errors"`
	KernelPackets     uint64 `json:"kernel_packets"`
	KernelDrops       uint64 `json:"kernel_drops"`
	KernelStatsErrors uint64 `json:"kernel_stats_errors"`
	IPCDroppedBatches uint64 `json:"ipc_dropped_batches"`
	IPCDroppedPackets uint64 `json:"ipc_dropped_packets"`
	IPCDroppedBytes   uint64 `json:"ipc_dropped_bytes"`
	Saturated         bool   `json:"saturated"`
}

func (b Batch) CollectorHealth() CollectorHealth {
	return CollectorHealth{b.OverflowBytes, b.OverflowPackets, b.ParseErrors, b.KernelPackets, b.KernelDrops, b.KernelStatsErrors, b.IPCDroppedBatches, b.IPCDroppedPackets, b.IPCDroppedBytes, b.HealthCountersSaturated}
}

func (h CollectorHealth) Apply(b *Batch) {
	b.OverflowBytes, b.OverflowPackets, b.ParseErrors = h.OverflowBytes, h.OverflowPackets, h.ParseErrors
	b.KernelPackets, b.KernelDrops, b.KernelStatsErrors = h.KernelPackets, h.KernelDrops, h.KernelStatsErrors
	b.IPCDroppedBatches, b.IPCDroppedPackets, b.IPCDroppedBytes = h.IPCDroppedBatches, h.IPCDroppedPackets, h.IPCDroppedBytes
	b.HealthCountersSaturated = h.Saturated
}

func (h CollectorHealth) Delta(previous CollectorHealth) (CollectorHealth, error) {
	delta := CollectorHealth{Saturated: h.Saturated}
	for _, counter := range []struct {
		now, before uint64
		out         *uint64
		limit       uint64
	}{
		{h.OverflowBytes, previous.OverflowBytes, &delta.OverflowBytes, MaxBatchBytes},
		{h.OverflowPackets, previous.OverflowPackets, &delta.OverflowPackets, MaxBatchPackets},
		{h.ParseErrors, previous.ParseErrors, &delta.ParseErrors, MaxBatchPackets},
		{h.KernelPackets, previous.KernelPackets, &delta.KernelPackets, MaxBatchPackets},
		{h.KernelDrops, previous.KernelDrops, &delta.KernelDrops, MaxBatchPackets},
		{h.KernelStatsErrors, previous.KernelStatsErrors, &delta.KernelStatsErrors, MaxBatchPackets},
		{h.IPCDroppedBatches, previous.IPCDroppedBatches, &delta.IPCDroppedBatches, MaxBatchPackets},
		{h.IPCDroppedPackets, previous.IPCDroppedPackets, &delta.IPCDroppedPackets, MaxBatchPackets},
		{h.IPCDroppedBytes, previous.IPCDroppedBytes, &delta.IPCDroppedBytes, MaxBatchBytes},
	} {
		if counter.now < counter.before || counter.now > counter.limit {
			return delta, errors.New("sensor cumulative health reset or overflow")
		}
		*counter.out = counter.now - counter.before
	}
	if previous.Saturated && !h.Saturated {
		return delta, errors.New("sensor health saturation reset")
	}
	return delta, nil
}
