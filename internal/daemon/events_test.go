// SPDX-License-Identifier: MIT

package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/enrich"
	"github.com/littlesho/NodeRampart/internal/model"
	"github.com/littlesho/NodeRampart/internal/privacy"
	"github.com/littlesho/NodeRampart/internal/protocol"
	"github.com/littlesho/NodeRampart/internal/store"
)

type unusedEventSender struct{}

func (unusedEventSender) Send(context.Context, string) error {
	panic("test must not send notifications")
}

func eventTestApp(t *testing.T) *App {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	geo, err := enrich.Open("", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = geo.Close() })
	transformer, err := privacy.New("prefix", "")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.Detection.SYNPacketsPerSecond = 10
	cfg.Detection.UpdateInterval = config.Duration{Duration: 2 * time.Second}
	cfg.Detection.RecoveryWindows = 2
	a, err := New(Options{Config: cfg, Store: db, Geo: geo, StorePrivacy: transformer, NotifyPrivacy: transformer, Notifier: unusedEventSender{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func eventTestBudget(t *testing.T, a *App, blocked bool) {
	t.Helper()
	minimum := int64(0)
	if blocked {
		minimum = 1 << 40
	}
	if err := a.options.Store.ConfigureBudget(context.Background(), store.BudgetConfig{MaxBytes: 64 << 20, MinFreeBytes: minimum}); err != nil {
		t.Fatal(err)
	}
	status, err := a.options.Store.BudgetStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if blocked && status.AvailableBytes >= 1<<40 {
		t.Skip("low-disk fixture requires less than 1 TiB free")
	}
}

func TestFloodPhasesRetryAsOneIncidentWithoutDuplicateNotifications(t *testing.T) {
	a := eventTestApp(t)
	ctx := context.Background()
	eventTestBudget(t, a, true)
	now := time.Now().UTC().Truncate(time.Second)
	for i, packets := range []uint64{20, 20, 20, 0, 0} {
		b := protocol.Batch{ProtocolVersion: protocol.Version, SentAt: now.Add(time.Duration(i) * time.Second), IntervalMillis: 1000, Interface: "eth0", RXPackets: packets, RXBytes: 60 * packets, InboundSYN: packets}
		if packets > 0 {
			b.Flows = []protocol.Flow{{Direction: model.DirectionInbound, RemoteIP: "203.0.113.7", Protocol: "tcp", LocalPort: 22, TCPFlags: 2, Packets: packets, Bytes: 60 * packets}}
		}
		a.handleBatch(ctx, b)
	}
	if len(a.pendingEvents) != 3 {
		t.Fatalf("missing pending start/update/recovery: %d", len(a.pendingEvents))
	}
	first := append([]byte(nil), a.pendingEvents[0].data...)
	if bytes.Contains(first, []byte("203.0.113.7")) {
		t.Fatal("retry queue bypassed configured IP privacy")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	a.flushEvents(canceled)
	if !bytes.Equal(first, a.pendingEvents[0].data) || len(a.pendingEvents) != 3 {
		t.Fatal("failed retry changed event identity")
	}
	eventTestBudget(t, a, false)
	a.flushEvents(ctx) // No new sensor batch is needed to recover.
	var firstWrite eventWrite
	if err := json.Unmarshal(first, &firstWrite); err != nil {
		t.Fatal(err)
	}
	if err := a.options.Store.InsertEventNotification(ctx, firstWrite.Event, firstWrite.Notification); err != nil {
		t.Fatal(err)
	}
	events, err := a.options.Store.Events(ctx, store.EventQuery{Start: now.Add(-time.Second), End: now.Add(time.Minute), Kind: "syn_flood", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Fatalf("events after retry: %d", len(events))
	}
	phases := map[string]bool{}
	for _, e := range events {
		phases[e.Phase] = true
		if e.IncidentID != firstWrite.Event.IncidentID {
			t.Fatal("retry split incident")
		}
	}
	if !phases["start"] || !phases["update"] || !phases["recovery"] {
		t.Fatalf("missing phases: %v", phases)
	}
	messages, err := a.options.Store.Pending(ctx, time.Now().UTC().Add(time.Minute), 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 3 || a.eventIngest.Pending != 0 || a.eventIngest.PendingBytes != 0 || a.storageFailures["event"] {
		t.Fatal("retry duplicated notifications or failed to recover state")
	}
}

func TestEventQueueBoundsAndDurableLoss(t *testing.T) {
	for _, large := range []bool{false, true} {
		name := "count"
		if large {
			name = "bytes"
		}
		t.Run(name, func(t *testing.T) {
			a := eventTestApp(t)
			now := time.Now().UTC()
			for i := 0; i < maxPendingEvents+7; i++ {
				e := model.Event{ID: model.NewID("evt"), ObservedAt: now, Kind: "port_scan", Severity: model.SeverityMedium}
				if large {
					e.Evidence = map[string]string{"bounded": strings.Repeat("x", 60<<10)}
				}
				a.queueEvent(e)
				if large && a.eventIngest.Dropped > 0 {
					break
				}
			}
			if a.eventIngest.Dropped == 0 || a.eventIngest.Pending > maxPendingEvents || a.eventIngest.PendingBytes > maxPendingEventBytes {
				t.Fatal("queue capacity not enforced")
			}
			if large && a.eventIngest.Pending >= maxPendingEvents {
				t.Fatal("byte limit did not precede count limit")
			}
			dropped := a.eventIngest.Dropped
			a.flushEvents(context.Background())
			gaps, err := a.options.Store.CoverageGaps(context.Background(), now.Add(-time.Second), now.Add(time.Minute), 100)
			if err != nil {
				t.Fatal(err)
			}
			if len(gaps) != 1 || gaps[0].Reason != "event_queue_capacity" || gaps[0].Count != dropped || a.eventIngest.UnrecordedDropped != 0 {
				t.Fatalf("missing precise queue-loss evidence: %#v", gaps)
			}
			a.flushEvents(context.Background())
			gaps, err = a.options.Store.CoverageGaps(context.Background(), now.Add(-time.Second), now.Add(time.Minute), 100)
			if err != nil || len(gaps) != 1 || a.eventIngest.Dropped != dropped {
				t.Fatal("retry duplicated loss accounting")
			}
		})
	}
}

func TestInvalidEventCannotPoisonRetryQueue(t *testing.T) {
	a := eventTestApp(t)
	now := time.Now().UTC()
	a.queueEvent(model.Event{ObservedAt: now, Kind: "port_scan", Evidence: map[string]string{"oversized": strings.Repeat("x", 65<<10)}})
	a.handleEvent(context.Background(), model.Event{ID: "evt_valid", ObservedAt: now, Kind: "port_scan"})
	if _, err := a.options.Store.Event(context.Background(), "evt_valid"); err != nil {
		t.Fatal(err)
	}
	if a.eventIngest.Pending != 0 || a.eventIngest.Dropped != 1 || a.eventIngest.UnrecordedDropped != 0 {
		t.Fatal("oversized metadata poisoned the queue")
	}
}

func TestShutdownDrainsMoreThanOnePassAndPersistsRestartUnknown(t *testing.T) {
	a := eventTestApp(t)
	now := time.Now().UTC()
	a.eventLosses[eventRestart] = eventLoss{active: true, start: now, end: now}
	for i := 0; i < maxEventFlush+3; i++ {
		a.queueEvent(model.Event{ID: model.NewID("evt"), ObservedAt: now, Kind: "port_scan"})
	}
	a.finishEvents()
	if a.eventIngest.Pending != 0 {
		t.Fatal("healthy shutdown discarded a queue tail")
	}
	gaps, err := a.options.Store.CoverageGaps(context.Background(), now.Add(-time.Second), now.Add(time.Minute), 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(gaps) != 1 || gaps[0].Reason != "process_restart_pending_unknown" || gaps[0].Count != 0 {
		t.Fatalf("restart uncertainty was lost or invented a loss count: %#v", gaps)
	}
}

func TestBufferedOldBatchDoesNotClaimLiveSensorCoverage(t *testing.T) {
	a := eventTestApp(t)
	now := time.Now().UTC()
	for _, tc := range []struct {
		at    time.Time
		state string
	}{
		{now.Add(-time.Minute), "degraded"}, {now, "running"},
	} {
		a.handleBatch(context.Background(), protocol.Batch{ProtocolVersion: protocol.Version, SentAt: tc.at, IntervalMillis: 1000, Interface: "eth0"})
		if a.sensorState != tc.state {
			t.Fatalf("buffered batch coverage=%s want %s", a.sensorState, tc.state)
		}
	}
}

func TestEventRetryTimerRecoversWhenSensorIsIdle(t *testing.T) {
	a := eventTestApp(t)
	a.options.Notifier = nil
	a.options.Config.Auth.Enabled = false
	a.options.Config.Reports.Enabled = false
	a.options.Config.Sensor.Interface = "nrtestmissing"
	directory := t.TempDir()
	a.options.Config.Paths.SensorSocket = filepath.Join(directory, "sensor.sock")
	a.options.Config.Paths.ControlSocket = filepath.Join(directory, "control.sock")
	uid := uint32(os.Getuid())
	a.options.SensorUID = &uid
	eventTestBudget(t, a, false)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(4 * time.Second):
			t.Error("daemon failed to stop")
		}
	})
	wait := func(predicate func() bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for !predicate() {
			if time.Now().After(deadline) {
				t.Fatal("timed out waiting for daemon progress")
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	wait(func() bool {
		states, err := a.options.Store.ComponentStatuses(ctx)
		if err != nil {
			return false
		}
		for _, state := range states {
			if state.Name == "report_scheduler" && state.State == "disabled" {
				return true
			}
		}
		return false
	})
	eventTestBudget(t, a, true)
	conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: a.options.Config.Paths.SensorSocket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	now := time.Now().UTC()
	batch := protocol.Batch{ProtocolVersion: protocol.Version, SentAt: now, IntervalMillis: 1000, Interface: a.options.Config.Sensor.Interface, RXPackets: 20, RXBytes: 1200, InboundSYN: 20,
		Flows: []protocol.Flow{{Direction: model.DirectionInbound, RemoteIP: "203.0.113.7", Protocol: "tcp", LocalPort: 22, TCPFlags: 2, Packets: 20, Bytes: 1200}}}
	if err := protocol.WriteFrame(conn, batch); err != nil {
		t.Fatal(err)
	}
	wait(func() bool {
		a.mu.RLock()
		defer a.mu.RUnlock()
		return a.eventIngest.Pending == 1 && a.storageFailures["event"]
	})
	eventTestBudget(t, a, false)
	wait(func() bool {
		a.mu.RLock()
		defer a.mu.RUnlock()
		return a.eventIngest.Pending == 0 && !a.eventIngest.LastPersistedAt.IsZero()
	})
	events, err := a.options.Store.Events(ctx, store.EventQuery{Start: now.Add(-time.Second), End: now.Add(time.Minute), Kind: "syn_flood", Limit: 100})
	if err != nil || len(events) != 1 {
		t.Fatalf("idle recovery did not persist exactly one event: count=%d error=%v", len(events), err)
	}
}
