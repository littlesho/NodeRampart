// SPDX-License-Identifier: MIT

package sensor

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"time"

	"github.com/littlesho/NodeRampart/internal/collector"
	"github.com/littlesho/NodeRampart/internal/protocol"
)

type captureSource interface {
	Run(context.Context, chan<- Packet) error
	Close() error
	Stats() (uint64, uint64, error)
	ParseErrors() uint64
}

type captureSlot struct {
	ref        collector.InterfaceRef
	capture    captureSource
	aggregator *Aggregator
	cancel     context.CancelFunc
	done       chan struct{}
	lastFlush  time.Time
}

// Fleet owns at most Limit captures and a single bounded sensor connection.
// Runtime reconciliation is passive; packets are only observed on named links.
type Fleet struct {
	Interfaces    []string
	Limit         int
	MaxFlows      int
	ReceiveBuffer int
	BatchInterval time.Duration
	Sender        *BatchSender
	Logger        *slog.Logger
	resolve       func(context.Context, []string) ([]collector.InterfaceRef, error)
	open          func(string, int) (captureSource, error)
	slots         map[string]*captureSlot
	lastError     string
}

func (f *Fleet) Run(ctx context.Context) error {
	if f.Sender == nil || f.Limit < 1 || f.Limit > protocol.MaxInterfaces || f.MaxFlows < f.Limit || f.MaxFlows > protocol.MaxFlowsPerBatch || f.ReceiveBuffer < 64<<10 || f.BatchInterval < 100*time.Millisecond || f.BatchInterval > time.Minute {
		return errors.New("invalid capture fleet configuration")
	}
	if f.Logger == nil {
		f.Logger = slog.Default()
	}
	if f.resolve == nil {
		f.resolve = collector.ResolveInterfaces
	}
	if f.open == nil {
		f.open = func(name string, buffer int) (captureSource, error) { return OpenCapture(name, buffer) }
	}
	f.slots = make(map[string]*captureSlot)
	defer func() {
		for name := range f.slots {
			_ = f.retire(name)
		}
	}()
	batchTicks := time.NewTicker(f.BatchInterval)
	defer batchTicks.Stop()
	routeTicks := time.NewTicker(time.Second)
	defer routeTicks.Stop()
	if err := f.reconcile(ctx, time.Now().UTC()); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-routeTicks.C:
			if err := f.reconcile(ctx, time.Now().UTC()); err != nil {
				return err
			}
		case <-batchTicks.C:
			f.flush(time.Now().UTC())
		}
	}
}

func (f *Fleet) reconcile(ctx context.Context, now time.Time) error {
	refs, err := f.resolve(ctx, f.Interfaces)
	var partial *collector.PartialDiscoveryError
	if err != nil && !errors.As(err, &partial) || len(refs) == 0 || len(refs) > f.Limit {
		if err == nil {
			err = errors.New("resolved capture selection exceeds bounds or is empty")
		}
		if f.lastError != err.Error() {
			f.Logger.Warn("capture selection unavailable; retrying", "error", err)
			f.lastError = err.Error()
		}
		for name := range f.slots {
			if err := f.retire(name); err != nil {
				return err
			}
		}
		return nil
	}
	if err != nil {
		if f.lastError != err.Error() {
			f.Logger.Warn("capture selection degraded; retaining available interfaces", "error", err)
		}
		f.lastError = err.Error()
	} else {
		f.lastError = ""
	}
	wanted := make(map[string]collector.InterfaceRef, len(refs))
	for _, ref := range refs {
		if !protocol.ValidInterfaceName(ref.Name) {
			return errors.New("invalid resolved capture name")
		}
		if _, ok := wanted[ref.Name]; ok {
			return errors.New("duplicate resolved capture name")
		}
		wanted[ref.Name] = ref
	}
	for name, slot := range f.slots {
		ref, present := wanted[name]
		stopped := false
		select {
		case <-slot.done:
			stopped = true
		default:
		}
		if !present || ref.Index != slot.ref.Index || ref.Index <= 0 || stopped {
			if err := f.retire(name); err != nil {
				return err
			}
		}
	}
	for _, ref := range refs {
		if _, exists := f.slots[ref.Name]; exists || ref.Index <= 0 {
			continue
		}
		capture, err := f.open(ref.Name, f.ReceiveBuffer/f.Limit)
		if err != nil {
			f.Logger.Warn("interface capture unavailable; retrying", "interface", ref.Name, "error", err)
			continue
		}
		// A reopened capture has no continuity with its predecessor. Closing
		// the shared connection gives the daemon a fresh, locally assigned
		// connection generation without extending the wire protocol.
		_ = f.Sender.Close()
		child, cancel := context.WithCancel(ctx)
		started := time.Now().UTC()
		slot := &captureSlot{ref: ref, capture: capture, aggregator: NewAggregator(ref.Name, f.MaxFlows/f.Limit, started), cancel: cancel, done: make(chan struct{}), lastFlush: started}
		packets := make(chan Packet, 4096/f.Limit)
		aggregated := make(chan struct{})
		go func() {
			defer close(aggregated)
			for {
				select {
				case packet := <-packets:
					slot.aggregator.Observe(packet)
				case <-child.Done():
					return
				}
			}
		}()
		go func() {
			defer close(slot.done)
			err := capture.Run(child, packets)
			cancel()
			<-aggregated
			if err != nil {
				f.Logger.Warn("interface capture stopped", "interface", ref.Name, "error", err)
			}
		}()
		f.slots[ref.Name] = slot
	}
	return nil
}

func (f *Fleet) retire(name string) error {
	slot := f.slots[name]
	if slot == nil {
		return nil
	}
	slot.cancel()
	_ = slot.capture.Close()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case <-slot.done:
	case <-timer.C:
		return errors.New("capture did not stop within its deadline")
	}
	delete(f.slots, name)
	batches, packets, bytes := f.Sender.RetireInterface(name)
	tail := slot.aggregator.Flush(time.Now().UTC())
	if batches > 0 || tail.RXPackets+tail.TXPackets > 0 {
		f.Logger.Warn("retired interface has unpersisted observation detail", "interface", name, "undelivered_batches", batches, "undelivered_packets", packets, "undelivered_bytes", bytes, "unflushed_packets", tail.RXPackets+tail.TXPackets)
	}
	_ = f.Sender.Close()
	return nil
}

func (f *Fleet) flush(now time.Time) {
	names := make([]string, 0, len(f.slots))
	for name := range f.slots {
		names = append(names, name)
	}
	sort.Strings(names)
	type snapshot struct {
		slot  *captureSlot
		batch protocol.Batch
	}
	snapshots := make([]snapshot, 0, len(names))
	for _, name := range names {
		slot := f.slots[name]
		if now.Sub(slot.lastFlush) < max(100*time.Millisecond, f.BatchInterval/2) {
			continue
		}
		select {
		case <-slot.done:
			continue
		default:
		}
		batch := slot.aggregator.flushCurrent()
		slot.lastFlush = batch.SentAt
		snapshots = append(snapshots, snapshot{slot: slot, batch: batch})
	}
	// Snapshot all bounded aggregators before any statistics call or IPC write
	// can block. Each link has its own actual observation cutoff.
	for i := range snapshots {
		item := &snapshots[i]
		item.batch.ParseErrors = item.slot.capture.ParseErrors()
		packets, drops, err := item.slot.capture.Stats()
		if err != nil {
			item.batch.KernelStatsErrors = 1
		} else {
			item.batch.KernelPackets, item.batch.KernelDrops = packets, drops
		}
	}
	for _, item := range snapshots {
		if err := f.Sender.Send(item.batch); err != nil {
			batches, packets, bytes := f.Sender.PendingLoss()
			f.Logger.Warn("sensor batch delivery failed; health retained per interface", "error", err, "undelivered_batches", batches, "undelivered_packets", packets, "undelivered_bytes", bytes)
		}
	}
}
