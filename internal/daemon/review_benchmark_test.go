// SPDX-License-Identifier: MIT

//go:build linux

package daemon

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/api"
	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/enrich"
	"github.com/littlesho/NodeRampart/internal/ipc"
	"github.com/littlesho/NodeRampart/internal/privacy"
	"github.com/littlesho/NodeRampart/internal/protocol"
	"github.com/littlesho/NodeRampart/internal/sensor"
	"github.com/littlesho/NodeRampart/internal/store"
)

func reviewSocketApp(b *testing.B) (*App, context.Context, string) {
	b.Helper()
	// Keep the socket path shorter than sockaddr_un even when Go's test name
	// is long. No host collectors, credentials or external senders are started.
	dir, err := os.MkdirTemp("", "nr-bench-")
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = os.RemoveAll(dir) })
	s, err := store.OpenWithBudget(filepath.Join(dir, "state.db"), store.BudgetConfig{MaxBytes: 64 << 20})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = s.Close() })
	geo, err := enrich.Open("", "")
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = geo.Close() })
	transformer, err := privacy.New("prefix", "")
	if err != nil {
		b.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.Auth.Enabled = false
	cfg.Reports.Enabled = false
	cfg.Sensor.BatchInterval = config.Duration{Duration: 100 * time.Millisecond}
	cfg.Sensor.Interface = ""
	for i := range protocol.MaxInterfaces {
		cfg.Sensor.Interfaces = append(cfg.Sensor.Interfaces, fmt.Sprintf("lab%d", i))
	}
	uid := uint32(os.Geteuid())
	a, err := New(Options{Config: cfg, Store: s, Geo: geo, StorePrivacy: transformer, NotifyPrivacy: transformer, SensorUID: &uid, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		b.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	path := filepath.Join(dir, "control.sock")
	listener, err := ipc.ListenUnix(path, 0o600)
	if err != nil {
		cancel()
		b.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- a.serveControl(ctx, listener) }()
	b.Cleanup(func() {
		cancel()
		_ = listener.Close()
		select {
		case err := <-done:
			if err != nil {
				b.Error(err)
			}
		case <-time.After(time.Second):
			b.Error("control listener did not stop")
		}
	})
	return a, ctx, path
}

func reviewStatusRequest(path string) error {
	conn, err := ipc.DialUnix(path, time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		return err
	}
	if err := protocol.WriteFrame(conn, api.Request{Version: api.Version, Command: "status"}); err != nil {
		return err
	}
	var response api.Response
	if err := protocol.ReadFrame(bufio.NewReader(conn), &response); err != nil {
		return err
	}
	if !response.OK || response.Version != api.Version {
		return fmt.Errorf("status failed: %s", response.Error)
	}
	return nil
}

func BenchmarkReviewControlStatusUnix(b *testing.B) {
	_, _, path := reviewSocketApp(b)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if err := reviewStatusRequest(path); err != nil {
			b.Fatal(err)
		}
	}
}

// A real eight-interface round crosses both Unix sockets and the same bounded
// queue used by Run. Actual scheduling time and storage commits are measured;
// snapshot age includes IPC, queueing and earlier interfaces' processing.
func BenchmarkReviewSensorBackpressure(b *testing.B) {
	for _, flows := range []int{0, protocol.MaxFlowsPerBatch / protocol.MaxInterfaces} {
		b.Run(fmt.Sprintf("eight_interfaces_%d_flows", flows), func(b *testing.B) {
			a, ctx, controlPath := reviewSocketApp(b)
			listener, err := ipc.ListenUnix(filepath.Join(filepath.Dir(controlPath), "sensor.sock"), 0o600)
			if err != nil {
				b.Fatal(err)
			}
			output := make(chan protocol.Batch, a.options.Config.Sensor.InterfaceLimit())
			sensorCtx, stopSensor := context.WithCancel(ctx)
			serveDone := make(chan error, 1)
			go func() { serveDone <- a.serveSensor(sensorCtx, listener, output) }()
			sender := sensor.NewBatchSender(listener.Addr().String(), uint32(os.Geteuid()))
			b.Cleanup(func() {
				_ = sender.Close()
				stopSensor()
				_ = listener.Close()
				select {
				case err := <-serveDone:
					if err != nil {
						b.Error(err)
					}
				case <-time.After(time.Second):
					b.Error("sensor listener did not stop")
				}
			})
			type result struct {
				age, handle time.Duration
				queued      int
			}
			processed := make(chan result, protocol.MaxInterfaces)
			consumerCtx, stopConsumer := context.WithCancel(ctx)
			consumerDone := make(chan struct{})
			go func() {
				defer close(consumerDone)
				for {
					select {
					case batch := <-output:
						at := time.Now()
						queued := len(output)
						a.handleBatch(consumerCtx, batch)
						select {
						case processed <- result{at.Sub(batch.SentAt), time.Since(at), queued}:
						case <-consumerCtx.Done():
							return
						}
					case <-consumerCtx.Done():
						return
					}
				}
			}()
			b.Cleanup(func() { stopConsumer(); <-consumerDone })
			var ageTotal, ageMax, handleTotal, controlTotal, controlMax time.Duration
			var queueObservedMax int
			var nextRound time.Time
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				// Preserve the configured nominal period on the real clock;
				// ns/op therefore includes cadence, not only handler work.
				if delay := time.Until(nextRound); delay > 0 {
					time.Sleep(delay)
				}
				nextRound = time.Now().Add(a.options.Config.Sensor.BatchInterval.Duration)
				for _, name := range a.options.Config.Sensor.Interfaces {
					if err := sender.Send(socketSensorBatch(name, a.options.Config.Sensor.BatchInterval.Duration, flows)); err != nil {
						b.Fatal(err)
					}
				}
				controlAt := time.Now()
				if err := reviewStatusRequest(controlPath); err != nil {
					b.Fatal(err)
				}
				latency := time.Since(controlAt)
				controlTotal += latency
				controlMax = max(controlMax, latency)
				for range protocol.MaxInterfaces {
					select {
					case r := <-processed:
						ageTotal += r.age
						ageMax = max(ageMax, r.age)
						handleTotal += r.handle
						queueObservedMax = max(queueObservedMax, r.queued)
					case <-time.After(2 * time.Second):
						b.Fatal("batch did not finish within bounded wait")
					}
				}
			}
			b.StopTimer()
			summary, err := a.options.Store.Summary(ctx, time.Now().UTC().Add(-time.Hour), time.Now().UTC().Add(time.Hour), 10)
			if err != nil || summary.Batches != uint64(b.N*protocol.MaxInterfaces) || summary.IPCDroppedBatches != 0 {
				b.Fatalf("durable health mismatch: batches=%d losses=%d err=%v", summary.Batches, summary.IPCDroppedBatches, err)
			}
			if losses, _, _ := sender.PendingLoss(); losses != 0 {
				b.Fatalf("undelivered IPC loss estimate: %d", losses)
			}
			var bytes, packets uint64
			for _, row := range summary.Traffic {
				bytes += row.Bytes
				packets += row.Packets
			}
			status := a.Status(ctx)
			wantPackets := uint64(b.N * protocol.MaxInterfaces * flows)
			if summary.TrafficTruncated || packets != wantPackets || bytes != wantPackets*1200 || status.EventIngest.Dropped != 0 || status.Storage.Failures != 0 {
				b.Fatalf("incomplete commits: packets=%d bytes=%d event_losses=%d storage_failures=%d", packets, bytes, status.EventIngest.Dropped, status.Storage.Failures)
			}
			b.ReportMetric(float64(ageTotal.Nanoseconds())/float64(b.N*protocol.MaxInterfaces), "snapshot_age_ns/batch")
			b.ReportMetric(float64(ageMax.Nanoseconds()), "snapshot_age_max_ns")
			b.ReportMetric(float64(handleTotal.Nanoseconds())/float64(b.N*protocol.MaxInterfaces), "handler_ns/batch")
			b.ReportMetric(float64(controlTotal.Nanoseconds())/float64(b.N), "control_ns/round")
			b.ReportMetric(float64(controlMax.Nanoseconds()), "control_max_ns")
			b.ReportMetric(float64(summary.IPCDroppedBatches), "committed_ipc_loss")
			b.ReportMetric(float64(queueObservedMax), "queue_observed_max_batches")
		})
	}
}
