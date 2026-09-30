// SPDX-License-Identifier: MIT

package sensor

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"sort"
	"sync"
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
	connect    func(time.Duration) (batchConnection, error)
	connection batchConnection
	pending    map[string]protocol.Batch
	deliveryMu sync.Mutex
	delivery   map[string]*DeliveryState
	ackDone    <-chan struct{}
}

type DeliveryState struct {
	Interface         string `json:"interface"`
	SessionID         string `json:"session_id"`
	SentSequence      uint64 `json:"sent_sequence"`
	CommittedSequence uint64 `json:"committed_sequence"`
	Complete          bool   `json:"complete"`
	Reason            string `json:"reason"`
	health            protocol.CollectorHealth
}

// DeliveryStatus distinguishes socket writes from independently received
// durable acknowledgements. No absent acknowledgement is treated as success.
func (s *BatchSender) DeliveryStatus() []DeliveryState {
	s.deliveryMu.Lock()
	defer s.deliveryMu.Unlock()
	result := make([]DeliveryState, 0, len(s.delivery))
	for _, state := range s.delivery {
		result = append(result, *state)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Interface < result[j].Interface })
	return result
}

func (s *BatchSender) identify(batch *protocol.Batch) error {
	if batch.ProtocolVersion < 5 {
		return nil
	}
	s.deliveryMu.Lock()
	defer s.deliveryMu.Unlock()
	if s.delivery == nil {
		s.delivery = make(map[string]*DeliveryState)
	}
	state := s.delivery[batch.Interface]
	if state == nil {
		if len(s.delivery) >= protocol.MaxInterfaces {
			return errors.New("delivery state capacity reached")
		}
		var nonce [16]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return errors.New("sensor session unavailable")
		}
		state = &DeliveryState{Interface: batch.Interface, SessionID: hex.EncodeToString(nonce[:]), Reason: "unconfirmed"}
		state.health = s.pending[batch.Interface].CollectorHealth()
		s.delivery[batch.Interface] = state
	}
	if state.SentSequence >= 1<<63-1 {
		state.Complete = false
		state.Reason = "sequence_exhausted"
		return errors.New("sensor sequence exhausted")
	}
	state.SentSequence++
	state.Complete = false
	state.Reason = "unconfirmed"
	batch.SessionID, batch.Sequence = state.SessionID, state.SentSequence
	total := protocol.Batch{}
	state.health.Apply(&total)
	mergeHealth(&total, *batch)
	state.health = total.CollectorHealth()
	state.health.Apply(batch)
	return nil
}

func (s *BatchSender) readACKs(connection batchConnection) {
	reader, ok := connection.(interface {
		io.Reader
		SetReadDeadline(time.Time) error
	})
	if !ok {
		return
	} // Write-only fixtures and legacy custom transports.
	done := make(chan struct{})
	s.ackDone = done
	go func() {
		defer close(done)
		input := bufio.NewReaderSize(reader, 4096)
		for {
			if err := reader.SetReadDeadline(time.Now().Add(3 * time.Minute)); err != nil {
				return
			}
			var ack protocol.CommitACK
			if err := protocol.ReadFrame(input, &ack); err != nil {
				return
			}
			if ack.ProtocolVersion != protocol.Version || !protocol.ValidSessionID(ack.SessionID) || !protocol.ValidInterfaceName(ack.Interface) || ack.Sequence == 0 || ack.CommittedSequence > ack.Sequence && !ack.Duplicate || len(ack.Reason) > 64 {
				continue
			}
			s.deliveryMu.Lock()
			state := s.delivery[ack.Interface]
			if state != nil && state.SessionID == ack.SessionID && ack.Sequence <= state.SentSequence && ack.CommittedSequence <= state.SentSequence {
				if ack.HealthCommitted && ack.TrafficCommitted && ack.CommittedSequence >= state.CommittedSequence {
					state.CommittedSequence = ack.CommittedSequence
				}
				if ack.Sequence == state.SentSequence {
					state.Complete = ack.Complete && ack.HealthCommitted && ack.TrafficCommitted && ack.EventsCommitted && ack.NotificationsCommitted
					state.Reason = ack.Reason
				}
			}
			s.deliveryMu.Unlock()
		}
	}()
}

func NewBatchSender(path string, expectedDaemonUID uint32) *BatchSender {
	return &BatchSender{connect: func(timeout time.Duration) (batchConnection, error) {
		connection, err := ipc.DialUnixPeer(path, timeout, expectedDaemonUID)
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
	if s.ackDone != nil {
		<-s.ackDone
		s.ackDone = nil
	}
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
	s.deliveryMu.Lock()
	delete(s.delivery, name)
	s.deliveryMu.Unlock()
	return pending.IPCDroppedBatches, pending.IPCDroppedPackets, pending.IPCDroppedBytes
}

func (s *BatchSender) Send(batch protocol.Batch) error {
	return s.sendBefore(batch, time.Now().Add(batchSendTimeout))
}

// sendBefore lets a fleet share one deadline for an entire bounded round,
// including reconnect attempts. More interfaces do not multiply stall time.
func (s *BatchSender) sendBefore(batch protocol.Batch, deadline time.Time) error {
	if !protocol.ValidInterfaceName(batch.Interface) {
		return errors.New("invalid delivery interface")
	}
	if s.pending == nil {
		s.pending = make(map[string]protocol.Batch)
	}
	if _, exists := s.pending[batch.Interface]; !exists && len(s.pending) >= protocol.MaxInterfaces {
		return errors.New("pending interface capacity reached")
	}
	if batch.ProtocolVersion < 5 {
		mergeHealth(&batch, s.pending[batch.Interface])
	}
	if err := s.identify(&batch); err != nil {
		s.retainLoss(batch)
		return err
	}
	if s.ackDone != nil {
		select {
		case <-s.ackDone:
			_ = s.Close()
		default:
		}
	}
	var err error
	remaining := time.Until(deadline)
	if remaining <= 0 {
		err = os.ErrDeadlineExceeded
	} else if s.connection == nil {
		s.connection, err = s.connect(remaining)
		if err == nil {
			s.readACKs(s.connection)
		}
	}
	if err == nil {
		err = s.connection.SetWriteDeadline(deadline)
	}
	if err == nil {
		err = protocol.WriteFrame(s.connection, batch)
	}
	if err != nil {
		// A partial write is ambiguous, so report a conservative loss estimate.
		// Preserve health read before the failure, including reset-on-read
		// PACKET_STATISTICS, but never retain packet headers or flow entries.
		s.retainLoss(batch)
		_ = s.Close()
		return err
	}
	delete(s.pending, batch.Interface)
	return nil
}

// discard retains only health for a window that exceeded the sampling bound.
// Its real elapsed value and packet detail are never placed in a later window.
func (s *BatchSender) discard(batch protocol.Batch) {
	_ = s.identify(&batch)
	if s.pending == nil {
		s.pending = make(map[string]protocol.Batch)
	}
	if batch.ProtocolVersion < 5 {
		mergeHealth(&batch, s.pending[batch.Interface])
	}
	s.retainLoss(batch)
	_ = s.Close()
}

func (s *BatchSender) retainLoss(batch protocol.Batch) {
	if batch.ProtocolVersion >= 5 && batch.SessionID != "" {
		s.deliveryMu.Lock()
		state := s.delivery[batch.Interface]
		if state != nil {
			total := protocol.Batch{}
			state.health.Apply(&total)
			addHealth(&total.IPCDroppedBatches, 1, protocol.MaxBatchPackets, &total.HealthCountersSaturated)
			addHealth(&total.IPCDroppedPackets, batch.RXPackets, protocol.MaxBatchPackets, &total.HealthCountersSaturated)
			addHealth(&total.IPCDroppedPackets, batch.TXPackets, protocol.MaxBatchPackets, &total.HealthCountersSaturated)
			addHealth(&total.IPCDroppedBytes, batch.RXBytes, protocol.MaxBatchBytes, &total.HealthCountersSaturated)
			addHealth(&total.IPCDroppedBytes, batch.TXBytes, protocol.MaxBatchBytes, &total.HealthCountersSaturated)
			state.health = total.CollectorHealth()
			state.Complete = false
			state.Reason = "transport_failed"
			pending := s.pending[batch.Interface]
			addHealth(&pending.IPCDroppedBatches, 1, protocol.MaxBatchPackets, &pending.HealthCountersSaturated)
			addHealth(&pending.IPCDroppedPackets, batch.RXPackets, protocol.MaxBatchPackets, &pending.HealthCountersSaturated)
			addHealth(&pending.IPCDroppedPackets, batch.TXPackets, protocol.MaxBatchPackets, &pending.HealthCountersSaturated)
			addHealth(&pending.IPCDroppedBytes, batch.RXBytes, protocol.MaxBatchBytes, &pending.HealthCountersSaturated)
			addHealth(&pending.IPCDroppedBytes, batch.TXBytes, protocol.MaxBatchBytes, &pending.HealthCountersSaturated)
			s.pending[batch.Interface] = pending
		}
		s.deliveryMu.Unlock()
		return
	}
	pending := protocol.Batch{}
	mergeHealth(&pending, batch)
	addHealth(&pending.IPCDroppedBatches, 1, protocol.MaxBatchPackets, &pending.HealthCountersSaturated)
	addHealth(&pending.IPCDroppedPackets, batch.RXPackets, protocol.MaxBatchPackets, &pending.HealthCountersSaturated)
	addHealth(&pending.IPCDroppedPackets, batch.TXPackets, protocol.MaxBatchPackets, &pending.HealthCountersSaturated)
	addHealth(&pending.IPCDroppedBytes, batch.RXBytes, protocol.MaxBatchBytes, &pending.HealthCountersSaturated)
	addHealth(&pending.IPCDroppedBytes, batch.TXBytes, protocol.MaxBatchBytes, &pending.HealthCountersSaturated)
	s.pending[batch.Interface] = pending
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
