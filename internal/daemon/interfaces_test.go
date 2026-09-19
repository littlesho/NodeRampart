// SPDX-License-Identifier: MIT

package daemon

import (
	"context"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/collector"
	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/protocol"
)

func TestSensorMultipleInterfaceStreamAndAllowlist(t *testing.T) {
	cfg := config.Defaults()
	cfg.Sensor.Interfaces = []string{"labA", "labB"}
	cfg.Sensor.BatchInterval = config.Duration{Duration: 100 * time.Millisecond}
	uid := uint32(os.Getuid())
	a := &App{options: Options{Config: cfg, SensorUID: &uid, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(t.TempDir(), "sensor.sock"), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	out := make(chan protocol.Batch, 8)
	done := make(chan error, 1)
	go func() { done <- a.serveSensor(ctx, listener, out) }()
	defer func() {
		cancel()
		_ = listener.Close()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("listener did not stop")
		}
	}()
	conn, err := net.DialUnix("unix", nil, listener.Addr().(*net.UnixAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	now := time.Now().UTC()
	for i := range 2 {
		for _, name := range cfg.Sensor.Interfaces {
			b := protocol.Batch{ProtocolVersion: protocol.Version, Interface: name, SentAt: now.Add(time.Duration(i) * 100 * time.Millisecond), IntervalMillis: 100}
			if err := protocol.WriteFrame(conn, b); err != nil {
				t.Fatal(err)
			}
		}
	}
	var generation uint64
	for i := range 4 {
		select {
		case b := <-out:
			if i == 0 {
				generation = b.ConnectionID
			}
			if b.ConnectionID == 0 || b.ConnectionID != generation {
				t.Fatal("missing authenticated connection generation")
			}
		case <-time.After(2 * time.Second):
			t.Fatal("same timestamp on independent interfaces rejected")
		}
	}
	if err := protocol.WriteFrame(conn, protocol.Batch{ProtocolVersion: protocol.Version, Interface: "unconfigured", SentAt: now.Add(200 * time.Millisecond), IntervalMillis: 100}); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	var response [1]byte
	if _, err := conn.Read(response[:]); err == nil {
		t.Fatal("unexpected response")
	} else if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
		t.Fatal("unconfigured stream was not closed")
	}
	if len(out) != 0 {
		t.Fatal("unconfigured interface admitted")
	}
}

func TestSensorAggregateCoverageRequiresEverySelectedInterface(t *testing.T) {
	a := eventTestApp(t)
	a.options.Config.Sensor.Interface = ""
	a.options.Config.Sensor.Interfaces = []string{"labA", "labB"}
	a.interfaceDiscoveryRequired = true
	ctx := context.Background()
	refs := []collector.InterfaceObservation{{Name: "labA", Index: 1}, {Name: "labB", Index: 2}}
	if err := a.updateInterfaces(ctx, refs); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	a.sensorByInterface = map[string]time.Time{"labA": now}
	if state := a.sensorCoverageState(now); state != "degraded" {
		t.Fatal("one link hid missing peer")
	}
	a.sensorByInterface["labB"] = now
	if state := a.sensorCoverageState(now); state != "running" {
		t.Fatal("two fresh links not healthy")
	}
	refs[1].Index = 9
	if err := a.updateInterfaces(ctx, refs); err != nil {
		t.Fatal(err)
	}
	if state := a.sensorCoverageState(time.Now()); state != "degraded" {
		t.Fatal("replacement reused old freshness")
	}
	gaps, err := a.options.Store.CoverageGaps(ctx, now.Add(-time.Hour), time.Now().Add(time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, gap := range gaps {
		if gap.Reason == "interface_selection_changed" && gap.Count == 0 {
			found = true
		}
	}
	if !found {
		t.Fatal("topology uncertainty not persisted")
	}
}
