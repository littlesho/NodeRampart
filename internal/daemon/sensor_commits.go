// SPDX-License-Identifier: MIT

package daemon

import (
	"context"
	"net/netip"
	"time"

	"github.com/littlesho/NodeRampart/internal/model"
	"github.com/littlesho/NodeRampart/internal/protocol"
)

// Only already transformed event IDs are retained for a failed commit, at most
// one batch per configured interface. This avoids applying a duplicate to the
// in-memory detector twice, without retaining/replaying packet flow snapshots.
type sensorPreparedEvents struct {
	session  string
	sequence uint64
	ids      []string
	rejected bool
}

func (a *App) handleSequencedBatch(ctx context.Context, b protocol.Batch) {
	ack := protocol.CommitACK{ProtocolVersion: protocol.Version, SessionID: b.SessionID, Interface: b.Interface, Sequence: b.Sequence, Reason: "storage_failed"}
	defer func() {
		if b.Acknowledge != nil {
			if err := b.Acknowledge(ack); err != nil {
				a.options.Logger.Warn("sensor commit acknowledgement unavailable", "interface", b.Interface)
			}
		}
	}()
	previous, exists, err := a.options.Store.SensorWatermark(ctx, b.SessionID, b.Interface)
	if err != nil {
		a.recordWrite(err, "sensor_commit", false)
		return
	}
	if exists && b.Sequence <= previous.Sequence {
		ack, err = a.options.Store.CommitSensorBatch(ctx, b, nil, previous.EventsCommitted, previous.NotificationsCommitted, previous.Complete, previous.Reason)
		a.recordWrite(err, "sensor_commit", false)
		return
	}
	delta, healthErr := b.CollectorHealth().Delta(previous.Health)
	if healthErr != nil {
		a.recordWrite(healthErr, "sensor_commit", false)
		ack.Reason = "invalid_cumulative_health"
		return
	}
	detectorBatch := b
	delta.Apply(&detectorBatch)
	if len(a.pendingEvents) > 0 {
		a.flushEvents(ctx)
	}
	a.mu.Lock()
	a.lastSensor = b.SentAt
	if a.sensorByInterface == nil {
		a.sensorByInterface = make(map[string]time.Time)
	}
	if _, exists := a.sensorByInterface[b.Interface]; !exists && len(a.sensorByInterface) >= a.options.Config.Sensor.InterfaceLimit() {
		oldest := ""
		for name, at := range a.sensorByInterface {
			if oldest == "" || at.Before(a.sensorByInterface[oldest]) {
				oldest = name
			}
		}
		delete(a.sensorByInterface, oldest)
	}
	a.sensorByInterface[b.Interface] = b.SentAt
	a.batches++
	a.mu.Unlock()
	a.refreshSensorState(ctx)
	if a.sensorPrepared == nil {
		a.sensorPrepared = make(map[string]*sensorPreparedEvents)
	}
	prepared := a.sensorPrepared[b.Interface]
	if prepared == nil || prepared.session != b.SessionID || prepared.sequence != b.Sequence {
		if len(a.sensorPrepared) >= protocol.MaxInterfaces {
			for name := range a.sensorPrepared {
				delete(a.sensorPrepared, name)
				break
			}
		}
		prepared = &sensorPreparedEvents{session: b.SessionID, sequence: b.Sequence}
		for _, event := range a.network.Observe(detectorBatch) {
			before := len(a.pendingEvents)
			a.queueEvent(event)
			if len(a.pendingEvents) > before {
				prepared.ids = append(prepared.ids, event.ID)
			} else {
				prepared.rejected = true
			}
		}
		a.sensorPrepared[b.Interface] = prepared
	}
	a.flushEvents(ctx)
	events, decisions, admitted, outcomeErr := a.options.Store.SensorEventOutcome(ctx, prepared.ids)
	if outcomeErr != nil {
		events, decisions, admitted = false, false, false
	}
	complete := events && decisions && admitted && !prepared.rejected
	reason := "committed"
	if !events || !decisions {
		reason = "derived_events_pending"
	} else if !admitted {
		reason = "notification_rejected"
	} else if prepared.rejected {
		events = false
		decisions = false
		complete = false
		reason = "derived_events_rejected"
	}
	traffic := make([]model.Traffic, 0, len(b.Flows))
	for _, flow := range b.Flows {
		address, parseErr := netip.ParseAddr(flow.RemoteIP)
		if parseErr != nil {
			continue
		}
		geo := a.options.Geo.Lookup(address)
		traffic = append(traffic, model.Traffic{HourUTC: b.SentAt, Direction: flow.Direction, Country: geo.CountryCode, Region: geo.Region, ASN: geo.ASN, ASNOrg: geo.ASNOrg, Bytes: flow.Bytes, Packets: flow.Packets, Attributed: geo.CountryCode != "" && geo.CountryCode != "PRIVATE"})
	}
	ack, err = a.options.Store.CommitSensorBatch(ctx, b, traffic, events, decisions, complete, reason)
	a.recordWrite(err, "sensor_commit", true)
	if err == nil {
		delete(a.sensorPrepared, b.Interface)
	}
}
