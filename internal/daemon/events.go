// SPDX-License-Identifier: MIT

package daemon

import (
	"context"
	"encoding/json"
	"math"
	"time"

	"github.com/littlesho/NodeRampart/internal/model"
	"github.com/littlesho/NodeRampart/internal/store"
)

const (
	maxPendingEvents     = 1024
	maxPendingEventBytes = 4 << 20
	maxQueuedEventBytes  = 96 << 10
	maxEventFlush        = 64
)

const (
	eventCapacity = iota
	eventInvalid
	eventRestart
)

// EventIngestStatus describes the process-local retry queue. Only the App's
// ingestion loop mutates the queue; status readers use the copy under a.mu.
type EventIngestStatus struct {
	Pending           int       `json:"pending"`
	PendingBytes      int       `json:"pending_bytes"`
	PendingLimit      int       `json:"pending_limit"`
	ByteLimit         int       `json:"byte_limit"`
	OldestObservedAt  time.Time `json:"oldest_observed_at_utc,omitzero"`
	LastPersistedAt   time.Time `json:"last_persisted_at_utc,omitzero"`
	Dropped           uint64    `json:"dropped"`
	UnrecordedDropped uint64    `json:"unrecorded_dropped"`
	CountersSaturated bool      `json:"counters_saturated"`
}

type eventWrite struct {
	Event        model.Event          `json:"event"`
	Notification *store.OutboxMessage `json:"notification,omitempty"`
}

type queuedEvent struct {
	data       []byte
	observedAt time.Time
}

type eventLoss struct {
	active     bool
	start, end time.Time
	count      int64
}

func (a *App) queueEvent(event model.Event) {
	stored, message := a.prepareEvent(event)
	// Store an immutable, already privacy-transformed snapshot. Retries reuse
	// both IDs and the notification dedupe key; they never re-run the detector.
	geo, geoErr := json.Marshal(stored.Geo)
	evidence, evidenceErr := json.Marshal(stored.Evidence)
	data, err := json.Marshal(eventWrite{Event: stored, Notification: message})
	if err != nil || geoErr != nil || evidenceErr != nil || len(geo) > 16<<10 || len(evidence) > 64<<10 || len(data) > maxQueuedEventBytes {
		a.noteEventLoss(eventInvalid, stored.ObservedAt)
		return
	}
	if len(a.pendingEvents) >= maxPendingEvents || len(data) > maxPendingEventBytes-a.pendingEventBytes {
		a.noteEventLoss(eventCapacity, stored.ObservedAt)
		return
	}
	a.pendingEvents = append(a.pendingEvents, queuedEvent{data: data, observedAt: stored.ObservedAt})
	a.pendingEventBytes += len(data)
	a.publishEventQueue()
}

func (a *App) noteEventLoss(kind int, at time.Time) {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	loss := &a.eventLosses[kind]
	if !loss.active {
		*loss = eventLoss{active: true, start: at, end: at}
	}
	if at.Before(loss.start) {
		loss.start = at
	}
	if at.After(loss.end) {
		loss.end = at
	}
	if loss.count < math.MaxInt64 {
		loss.count++
	}
	a.mu.Lock()
	if a.eventIngest.Dropped < math.MaxUint64 {
		a.eventIngest.Dropped++
	} else {
		a.eventIngest.CountersSaturated = true
	}
	if loss.count == math.MaxInt64 {
		a.eventIngest.CountersSaturated = true
	}
	a.mu.Unlock()
	a.publishEventQueue()
}

func (a *App) publishEventQueue() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.eventIngest.Pending = len(a.pendingEvents)
	a.eventIngest.PendingBytes = a.pendingEventBytes
	a.eventIngest.PendingLimit = maxPendingEvents
	a.eventIngest.ByteLimit = maxPendingEventBytes
	a.eventIngest.OldestObservedAt = time.Time{}
	if len(a.pendingEvents) > 0 {
		a.eventIngest.OldestObservedAt = a.pendingEvents[0].observedAt
	}
	a.eventIngest.UnrecordedDropped = uint64(a.eventLosses[eventCapacity].count) + uint64(a.eventLosses[eventInvalid].count)
}

func (a *App) flushEvents(ctx context.Context) {
	// Bound work per pass and retry on the timer even if the sensor goes idle.
	// Cancellation also bounds waiting behind an online backup.
	child, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	for count := 0; count < maxEventFlush && len(a.pendingEvents) > 0; count++ {
		var write eventWrite
		if err := json.Unmarshal(a.pendingEvents[0].data, &write); err != nil {
			// The bytes are produced locally by queueEvent; this is defensive.
			a.noteEventLoss(eventInvalid, a.pendingEvents[0].observedAt)
		} else {
			err := a.options.Store.InsertEventNotification(child, write.Event, write.Notification)
			a.recordWrite(err, "event", true)
			if err != nil {
				break
			}
			a.mu.Lock()
			a.eventIngest.LastPersistedAt = time.Now().UTC()
			a.mu.Unlock()
		}
		a.pendingEventBytes -= len(a.pendingEvents[0].data)
		a.pendingEvents[0] = queuedEvent{}
		a.pendingEvents = a.pendingEvents[1:]
	}
	if len(a.pendingEvents) == 0 {
		a.pendingEvents = nil
	}
	for kind, reason := range []string{"event_queue_capacity", "event_metadata_rejected", "process_restart_pending_unknown"} {
		loss := a.eventLosses[kind]
		if !loss.active {
			continue
		}
		err := a.options.Store.RecordCoverageGap(child, store.CoverageGap{Name: "network_events", Reason: reason, Start: loss.start, End: loss.end, Count: uint64(loss.count)})
		a.recordWrite(err, reason, false)
		if err == nil {
			a.eventLosses[kind] = eventLoss{}
		}
	}
	a.publishEventQueue()
}

func (a *App) finishEvents() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for {
		before := len(a.pendingEvents)
		a.flushEvents(ctx)
		if len(a.pendingEvents) == 0 || len(a.pendingEvents) == before || ctx.Err() != nil {
			break
		}
	}
	if len(a.pendingEvents) == 0 {
		return
	}
	// If storage remains unavailable even this marker may not be durable.
	// Every startup therefore records an explicit unknown pending-event marker.
	now := time.Now().UTC()
	err := a.options.Store.RecordCoverageGap(ctx, store.CoverageGap{Name: "network_events", Reason: "shutdown_unpersisted_events", Start: now, End: now, Count: uint64(len(a.pendingEvents))})
	a.options.Logger.Warn("uncommitted events remain at shutdown", "pending_events", len(a.pendingEvents), "loss_recorded", err == nil)
}
