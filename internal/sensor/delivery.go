// SPDX-License-Identifier: MIT

package sensor

import (
	"errors"
	"time"

	"github.com/littlesho/NodeRampart/internal/ipc"
	"github.com/littlesho/NodeRampart/internal/protocol"
)

const batchSendTimeout = 250 * time.Millisecond

type batchConnection interface {
	Write([]byte) (int, error)
	SetWriteDeadline(time.Time) error
	Close() error
}

// BatchSender retains only bounded health counters while the daemon is
// unavailable. Failed flow summaries are not replayed into a later rate window.
// Pending counters survive reconnects, but not a sensor process restart.
type BatchSender struct {
	connect    func() (batchConnection, error)
	connection batchConnection
	pending    map[string]protocol.Batch
}

func NewBatchSender(path string, expectedDaemonUID uint32) *BatchSender {
	return &BatchSender{connect: func() (batchConnection, error) {
		connection, err := ipc.DialUnixPeer(path, batchSendTimeout, expectedDaemonUID)
		if err != nil {
			return nil, err
		}
		return connection, nil
	}}
}

func (s *BatchSender) Close() error {
	if s.connection == nil {
		return nil
	}
	err := s.connection.Close()
	s.connection = nil
	return err
}

// PendingLoss returns the currently undelivered loss estimate for local logs.
func (s *BatchSender) PendingLoss() (batches, packets, bytes uint64) {
	for _, pending := range s.pending {
		batches += pending.IPCDroppedBatches
		packets += pending.IPCDroppedPackets
		bytes += pending.IPCDroppedBytes
	}
	return
}

// RetireInterface bounds churn without assigning one link's losses to another.
// The caller records the retired loss and reports the topology coverage gap.
func (s *BatchSender) RetireInterface(name string) (batches, packets, bytes uint64) {
	pending := s.pending[name]
	delete(s.pending, name)
	return pending.IPCDroppedBatches, pending.IPCDroppedPackets, pending.IPCDroppedBytes
}

func (s *BatchSender) Send(batch protocol.Batch) error {
	if !protocol.ValidInterfaceName(batch.Interface) {
		return errors.New("invalid delivery interface")
	}
	if s.pending == nil {
		s.pending = make(map[string]protocol.Batch)
	}
	if _, exists := s.pending[batch.Interface]; !exists && len(s.pending) >= protocol.MaxInterfaces {
		return errors.New("pending interface capacity reached")
	}
	mergeHealth(&batch, s.pending[batch.Interface])
	var err error
	if s.connection == nil {
		s.connection, err = s.connect()
	}
	if err == nil {
		err = s.connection.SetWriteDeadline(time.Now().Add(batchSendTimeout))
	}
	if err == nil {
		err = protocol.WriteFrame(s.connection, batch)
	}
	if err != nil {
		// A partial write is ambiguous, so report a conservative loss estimate.
		// Preserve health read before the failure, including reset-on-read
		// PACKET_STATISTICS, but never retain packet headers or flow entries.
		pending := protocol.Batch{}
		mergeHealth(&pending, batch)
		addHealth(&pending.IPCDroppedBatches, 1, protocol.MaxBatchPackets, &pending.HealthCountersSaturated)
		addHealth(&pending.IPCDroppedPackets, batch.RXPackets, protocol.MaxBatchPackets, &pending.HealthCountersSaturated)
		addHealth(&pending.IPCDroppedPackets, batch.TXPackets, protocol.MaxBatchPackets, &pending.HealthCountersSaturated)
		addHealth(&pending.IPCDroppedBytes, batch.RXBytes, protocol.MaxBatchBytes, &pending.HealthCountersSaturated)
		addHealth(&pending.IPCDroppedBytes, batch.TXBytes, protocol.MaxBatchBytes, &pending.HealthCountersSaturated)
		s.pending[batch.Interface] = pending
		_ = s.Close()
		return err
	}
	delete(s.pending, batch.Interface)
	return nil
}

func mergeHealth(destination *protocol.Batch, source protocol.Batch) {
	destination.HealthCountersSaturated = destination.HealthCountersSaturated || source.HealthCountersSaturated
	for _, counter := range []struct {
		destination *uint64
		source      uint64
		maximum     uint64
	}{
		{&destination.KernelPackets, source.KernelPackets, protocol.MaxBatchPackets},
		{&destination.KernelDrops, source.KernelDrops, protocol.MaxBatchPackets},
		{&destination.KernelStatsErrors, source.KernelStatsErrors, protocol.MaxBatchPackets},
		{&destination.ParseErrors, source.ParseErrors, protocol.MaxBatchPackets},
		{&destination.OverflowPackets, source.OverflowPackets, protocol.MaxBatchPackets},
		{&destination.OverflowBytes, source.OverflowBytes, protocol.MaxBatchBytes},
		{&destination.IPCDroppedBatches, source.IPCDroppedBatches, protocol.MaxBatchPackets},
		{&destination.IPCDroppedPackets, source.IPCDroppedPackets, protocol.MaxBatchPackets},
		{&destination.IPCDroppedBytes, source.IPCDroppedBytes, protocol.MaxBatchBytes},
	} {
		addHealth(counter.destination, counter.source, counter.maximum, &destination.HealthCountersSaturated)
	}
}

func addHealth(destination *uint64, value, maximum uint64, saturated *bool) {
	if *destination > maximum || value > maximum-*destination {
		*destination = maximum
		*saturated = true
		return
	}
	*destination += value
}
