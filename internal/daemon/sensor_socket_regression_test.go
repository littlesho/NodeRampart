// SPDX-License-Identifier: MIT

//go:build linux

package daemon

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/model"
	"github.com/littlesho/NodeRampart/internal/protocol"
	"github.com/littlesho/NodeRampart/internal/sensor"
)

func socketSensorApp(t *testing.T, cfg config.Config) (*App, *sensor.BatchSender, <-chan protocol.Batch) {
	t.Helper()
	dir, err := os.MkdirTemp("", "nr-sensor-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "sensor.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	uid := uint32(os.Geteuid())
	cfg.Paths.SensorSocket = path
	a := &App{options: Options{Config: cfg, SensorUID: &uid, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}}
	ctx, cancel := context.WithCancel(context.Background())
	output := make(chan protocol.Batch, protocol.MaxInterfaces)
	done := make(chan error, 1)
	go func() { done <- a.serveSensor(ctx, listener, output) }()
	sender := sensor.NewBatchSender(path, uid)
	t.Cleanup(func() {
		_ = sender.Close()
		cancel()
		_ = listener.Close()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(time.Second):
			t.Error("sensor listener did not stop")
		}
	})
	return a, sender, output
}

func socketSensorBatch(name string, interval time.Duration, flows int) protocol.Batch {
	b := protocol.Batch{ProtocolVersion: protocol.Version, SentAt: time.Now().UTC(), IntervalMillis: interval.Milliseconds(), Interface: name}
	for i := range flows {
		b.Flows = append(b.Flows, protocol.Flow{Direction: model.DirectionInbound, RemoteIP: "2001:db8:ffff:ffff:ffff:ffff:ffff:ffff", Protocol: "udp", LocalPort: uint16(i + 1), RemotePort: 65000, Packets: 1, Bytes: 1200})
		b.RXPackets++
		b.RXBytes += 1200
		b.InboundUDP++
	}
	return b
}

func TestSensorSocketAllowsConfiguredInterfaceBurst(t *testing.T) {
	for _, count := range []int{1, 2, 8} {
		for _, flows := range []int{0, protocol.MaxFlowsPerBatch / count} {
			for _, interval := range []time.Duration{2 * time.Second, time.Minute} {
				t.Run(fmt.Sprintf("interfaces%d/flows%d/interval%s", count, flows, interval), func(t *testing.T) {
					cfg := config.Defaults()
					cfg.Sensor.BatchInterval = config.Duration{Duration: interval}
					cfg.Sensor.Interface = ""
					for i := range count {
						cfg.Sensor.Interfaces = append(cfg.Sensor.Interfaces, fmt.Sprintf("lab%d", i))
					}
					_, sender, output := socketSensorApp(t, cfg)
					for _, name := range cfg.Sensor.Interfaces {
						if err := sender.Send(socketSensorBatch(name, interval, flows)); err != nil {
							t.Fatalf("valid interface burst failed: %v", err)
						}
					}
					for _, name := range cfg.Sensor.Interfaces {
						select {
						case b := <-output:
							if b.Interface != name || len(b.Flows) != flows || b.ConnectionID == 0 {
								t.Fatalf("decoded burst mismatch: %+v", b)
							}
						case <-time.After(time.Second):
							t.Fatal("valid burst was delayed by its own read gate")
						}
					}
					if batches, _, _ := sender.PendingLoss(); batches != 0 {
						t.Fatal("valid burst retained loss")
					}
				})
			}
		}
	}
}

func TestSensorSocketOneMinuteElapsedBoundary(t *testing.T) {
	for _, millis := range []int64{59999, 60000, 60001} {
		t.Run(fmt.Sprint(millis), func(t *testing.T) {
			cfg := config.Defaults()
			cfg.Sensor.Interface = "lab0"
			cfg.Sensor.BatchInterval = config.Duration{Duration: time.Minute}
			_, sender, output := socketSensorApp(t, cfg)
			b := socketSensorBatch("lab0", time.Duration(millis)*time.Millisecond, 1)
			if err := sender.Send(b); err != nil {
				t.Fatal(err)
			}
			select {
			case got := <-output:
				if got.IntervalMillis != millis || got.RXPackets != b.RXPackets || !got.SentAt.Equal(b.SentAt) {
					t.Fatal("actual elapsed interval or observation was changed")
				}
			case <-time.After(time.Second):
				t.Fatal("legal one-minute elapsed boundary was rejected")
			}
		})
	}
}
