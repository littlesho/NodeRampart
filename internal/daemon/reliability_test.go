// SPDX-License-Identifier: MIT

package daemon

import (
	"context"
	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/enrich"
	"github.com/littlesho/NodeRampart/internal/model"
	"github.com/littlesho/NodeRampart/internal/privacy"
	"github.com/littlesho/NodeRampart/internal/protocol"
	"github.com/littlesho/NodeRampart/internal/store"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNetworkEventSurvivesStorageRecovery(t *testing.T) {
	for _, outage := range []bool{false, true} {
		name := "control"
		if outage {
			name = "temporary_storage_failure"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			db, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			geo, err := enrich.Open("", "")
			if err != nil {
				t.Fatal(err)
			}
			defer geo.Close()
			transformer, err := privacy.New("prefix", "")
			if err != nil {
				t.Fatal(err)
			}
			cfg := config.Defaults()
			cfg.Detection.ScanUniquePorts = 2
			app, err := New(Options{Config: cfg, Store: db, Geo: geo, StorePrivacy: transformer, NotifyPrivacy: transformer, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
			if err != nil {
				t.Fatal(err)
			}
			configure := func(minFree int64) {
				t.Helper()
				if err := db.ConfigureBudget(ctx, store.BudgetConfig{MaxBytes: 64 << 20, MinFreeBytes: minFree}); err != nil {
					t.Fatal(err)
				}
			}
			configure(0)
			status, err := db.BudgetStatus(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if status.AvailableBytes >= 1<<40 {
				t.Skip("fixture requires filesystem below 1 TiB free")
			}
			now := time.Now().UTC().Truncate(time.Second)
			send := func(i int) {
				t.Helper()
				batch := protocol.Batch{ProtocolVersion: protocol.Version, SentAt: now.Add(time.Duration(i) * time.Second), IntervalMillis: 1000, Interface: "eth0", RXBytes: 60, RXPackets: 1, InboundSYN: 1, Flows: []protocol.Flow{{Direction: model.DirectionInbound, RemoteIP: "203.0.113.7", Protocol: "tcp", LocalPort: uint16(21 + i), TCPFlags: 0x02, Packets: 1, Bytes: 60}}}
				if err := batch.Validate(); err != nil {
					t.Fatal(err)
				}
				app.handleBatch(ctx, batch)
			}
			send(0)
			if outage {
				configure(1 << 40)
			}
			send(1)
			if outage {
				if !app.storageFailures["event"] {
					t.Fatal("fixture did not cause event storage failure")
				}
				configure(0)
			}
			send(2)
			send(3)
			events, err := db.Events(ctx, store.EventQuery{Start: now.Add(-time.Second), End: now.Add(time.Minute), Kind: "port_scan", Limit: 100})
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("outage=%v stored_scan_events=%d storage_failures=%v", outage, len(events), app.storageFailures)
			if len(events) != 1 {
				t.Errorf("expected one retained scan event after storage recovered, got %d", len(events))
			}
		})
	}
}

func TestSensorAcceptsLegitimateBufferedBatches(t *testing.T) {
	cfg := config.Defaults()
	cfg.Sensor.BatchInterval = config.Duration{Duration: 100 * time.Millisecond}
	uid := uint32(os.Getuid())
	app := &App{options: Options{Config: cfg, SensorUID: &uid, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(t.TempDir(), "sensor.sock"), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	output := make(chan protocol.Batch)
	done := make(chan error, 1)
	go func() { done <- app.serveSensor(ctx, listener, output) }()
	defer func() {
		cancel()
		_ = listener.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("listener failed to stop")
		}
	}()
	conn, err := net.DialUnix("unix", nil, listener.Addr().(*net.UnixAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	now := time.Now().UTC()
	for i := 0; i < 3; i++ {
		if i > 0 {
			time.Sleep(100 * time.Millisecond)
		}
		batch := protocol.Batch{ProtocolVersion: protocol.Version, SentAt: now.Add(time.Duration(i) * 100 * time.Millisecond), IntervalMillis: 100, Interface: "eth0"}
		if err := batch.Validate(); err != nil {
			t.Fatal(err)
		}
		if err := protocol.WriteFrame(conn, batch); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			deadline := time.Now().Add(time.Second)
			for {
				app.mu.RLock()
				pid := app.sensorPeer.PID
				app.mu.RUnlock()
				if pid != 0 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("receiver never accepted first batch")
				}
				time.Sleep(time.Millisecond)
			}
		}
	}
	// The consumer was stalled while three valid batches were produced at the
	// configured cadence. Releasing it models a drained application queue.
	received := 0
	timer := time.NewTimer(500 * time.Millisecond)
	defer timer.Stop()
	for received < 3 {
		select {
		case <-output:
			received++
		case <-timer.C:
			t.Fatalf("legitimate buffered batches accepted=%d, want 3; no sender write failed", received)
		}
	}
}
