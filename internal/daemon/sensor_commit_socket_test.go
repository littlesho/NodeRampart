// SPDX-License-Identifier: MIT

//go:build linux

package daemon

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/detect"
	"github.com/littlesho/NodeRampart/internal/ipc"
	"github.com/littlesho/NodeRampart/internal/model"
	"github.com/littlesho/NodeRampart/internal/protocol"
	"github.com/littlesho/NodeRampart/internal/store"
)

func receiveCommitBatch(t *testing.T, output <-chan protocol.Batch) protocol.Batch {
	t.Helper()
	select {
	case b := <-output:
		return b
	case <-time.After(2 * time.Second):
		t.Fatal("sensor frame unavailable")
		return protocol.Batch{}
	}
}

func waitSenderCommit(t *testing.T, status func() bool) {
	t.Helper()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		if status() {
			return
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatal("commit acknowledgement unavailable")
		}
	}
}

func rawSensorConnection(t *testing.T, receiver *App) *net.UnixConn {
	t.Helper()
	conn, err := ipc.DialUnixPeer(receiver.options.Config.Paths.SensorSocket, time.Second, uint32(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func readCommitACK(t *testing.T, conn *net.UnixConn) protocol.CommitACK {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	var ack protocol.CommitACK
	if err := protocol.ReadFrame(bufio.NewReader(conn), &ack); err != nil {
		t.Fatal(err)
	}
	return ack
}

func reconnectSensorBatch(t *testing.T, receiver *App, output <-chan protocol.Batch, b protocol.Batch) (*net.UnixConn, protocol.Batch) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn := rawSensorConnection(t, receiver)
		if err := protocol.WriteFrame(conn, b); err != nil {
			conn.Close()
			continue
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case received := <-output:
			timer.Stop()
			return conn, received
		case <-timer.C:
			conn.Close()
		}
	}
	t.Fatal("sensor reconnect never admitted an actual frame")
	return nil, protocol.Batch{}
}

func TestSensorSocketWriteIsNotCommitAndCumulativeACKLossDoesNotRepeatHealth(t *testing.T) {
	cfg := config.Defaults()
	cfg.Sensor.Interface = "lab0"
	cfg.Sensor.BatchInterval.Duration = 100 * time.Millisecond
	_, sender, output := socketSensorApp(t, cfg)
	a := eventTestApp(t)
	a.options.Config.Sensor = cfg.Sensor
	first := socketSensorBatch("lab0", 100*time.Millisecond, 1)
	first.KernelDrops = 3
	if err := sender.Send(first); err != nil {
		t.Fatal(err)
	}
	batch := receiveCommitBatch(t, output)
	state := sender.DeliveryStatus()[0]
	if state.SentSequence != 1 || state.CommittedSequence != 0 || state.Complete {
		t.Fatal("socket success claimed commit", state)
	}
	// Deliberately lose only the ACK while performing the real transaction.
	batch.Acknowledge = nil
	a.handleBatch(context.Background(), batch)
	if sender.DeliveryStatus()[0].CommittedSequence != 0 {
		t.Fatal("sender fabricated missing ACK")
	}
	time.Sleep(100 * time.Millisecond)
	second := socketSensorBatch("lab0", 100*time.Millisecond, 1)
	second.KernelDrops = 4
	if err := sender.Send(second); err != nil {
		t.Fatal(err)
	}
	batch = receiveCommitBatch(t, output)
	if batch.KernelDrops != 7 || batch.Sequence != 2 {
		t.Fatal("cumulative health did not retain unconfirmed observations", batch.KernelDrops, batch.Sequence)
	}
	a.handleBatch(context.Background(), batch)
	waitSenderCommit(t, func() bool { return sender.DeliveryStatus()[0].CommittedSequence == 2 })
	if state := sender.DeliveryStatus()[0]; !state.Complete {
		t.Fatal(state)
	}
	summary, err := a.options.Store.Summary(context.Background(), first.SentAt.Add(-time.Hour), second.SentAt.Add(time.Hour), 10)
	if err != nil || summary.KernelDrops != 7 || summary.Batches != 2 {
		t.Fatal("ACK loss repeated cumulative health", summary.KernelDrops, summary.Batches, err)
	}
}

func TestSensorSocketDuplicateReconnectAndDaemonRestart(t *testing.T) {
	cfg := config.Defaults()
	cfg.Sensor.Interface = "lab0"
	cfg.Sensor.BatchInterval.Duration = 100 * time.Millisecond
	receiver, _, output := socketSensorApp(t, cfg)
	a := eventTestApp(t)
	a.options.Config.Sensor = cfg.Sensor
	conn := rawSensorConnection(t, receiver)
	b := socketSensorBatch("lab0", 100*time.Millisecond, 1)
	b.SessionID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	b.Sequence = 1
	if err := protocol.WriteFrame(conn, b); err != nil {
		t.Fatal(err)
	}
	firstReceived := receiveCommitBatch(t, output)
	a.handleBatch(context.Background(), firstReceived)
	if ack := readCommitACK(t, conn); !ack.Complete || ack.Duplicate {
		t.Fatal(ack)
	}
	if err := protocol.WriteFrame(conn, b); err != nil {
		t.Fatal(err)
	}
	a.handleBatch(context.Background(), receiveCommitBatch(t, output))
	if ack := readCommitACK(t, conn); !ack.Complete || !ack.Duplicate {
		t.Fatal(ack)
	}
	conn.Close()
	// Close/reopen the same physical file and construct a new daemon App.
	options := a.options
	if err := options.Store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.OpenWithBudget(options.Config.Paths.Database, store.BudgetConfig{MaxBytes: 64 << 20})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reopened.Close() })
	options.Store = reopened
	a, err = New(options)
	if err != nil {
		t.Fatal(err)
	}
	var reconnected protocol.Batch
	conn, reconnected = reconnectSensorBatch(t, receiver, output, b)
	if reconnected.ConnectionID <= firstReceived.ConnectionID {
		t.Fatal("fixture did not replace actual connection")
	}
	a.handleBatch(context.Background(), reconnected)
	if ack := readCommitACK(t, conn); !ack.Complete || !ack.Duplicate || ack.CommittedSequence != 1 {
		t.Fatal("restart lost durable duplicate state", ack)
	}
	summary, err := reopened.Summary(context.Background(), b.SentAt.Add(-time.Hour), b.SentAt.Add(time.Hour), 10)
	if err != nil || summary.Batches != 1 || len(summary.Traffic) != 1 || summary.Traffic[0].Bytes != 1200 {
		t.Fatal("duplicate replay accumulated data", summary.Batches, summary.Traffic, err)
	}
}

func TestSensorSocketStorageFailureAndDerivedEventsPartialACK(t *testing.T) {
	cfg := config.Defaults()
	cfg.Sensor.Interface = "lab0"
	cfg.Sensor.BatchInterval.Duration = 100 * time.Millisecond
	receiver, _, output := socketSensorApp(t, cfg)
	a := eventTestApp(t)
	a.options.Config.Sensor = cfg.Sensor
	conn := rawSensorConnection(t, receiver)
	b := socketSensorBatch("lab0", 100*time.Millisecond, 1)
	b.SessionID = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	b.Sequence = 1
	fixtureSentAt := b.SentAt
	eventTestBudget(t, a, true)
	if err := protocol.WriteFrame(conn, b); err != nil {
		t.Fatal(err)
	}
	a.handleBatch(context.Background(), receiveCommitBatch(t, output))
	if ack := readCommitACK(t, conn); ack.Complete || ack.HealthCommitted || ack.TrafficCommitted || ack.CommittedSequence != 0 {
		t.Fatal("rejected transaction claimed commit", ack)
	}
	eventTestBudget(t, a, false)
	// Derive more immutable events than one bounded flush can persist; the
	// health/traffic transaction commits while this existing queue is partial.
	detection := cfg.Detection
	detection.ScanUniquePorts = 2
	detection.SYNPacketsPerSecond, detection.UDPPacketsPerSecond, detection.ICMPPacketsPerSecond, detection.BytesPerSecond = 0, 0, 0, 0
	a.network = detect.NewFleet(detection, 1)
	time.Sleep(100 * time.Millisecond)
	b = socketSensorBatch("lab0", 100*time.Millisecond, 0)
	b.SessionID = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	b.Sequence = 2
	// The synthetic measurement timeline is independent of database scheduling.
	// Match the declared 100ms interval even when the rejected commit takes longer.
	b.SentAt = fixtureSentAt.Add(100 * time.Millisecond)
	for source := 0; source < 128; source++ {
		for port := 1; port <= 2; port++ {
			b.Flows = append(b.Flows, protocol.Flow{Direction: model.DirectionInbound, RemoteIP: fmt.Sprintf("198.18.0.%d", source), Protocol: "tcp", LocalPort: uint16(port), RemotePort: 4242, TCPFlags: 2, Packets: 1, Bytes: 60})
			b.RXPackets++
			b.RXBytes += 60
			b.InboundSYN++
		}
	}
	if err := protocol.WriteFrame(conn, b); err != nil {
		t.Fatal(err)
	}
	a.handleBatch(context.Background(), receiveCommitBatch(t, output))
	ack := readCommitACK(t, conn)
	if !ack.HealthCommitted || !ack.TrafficCommitted || ack.CommittedSequence != 2 || ack.Complete || ack.EventsCommitted || ack.Reason != "derived_events_pending" {
		t.Fatal(ack)
	}
	watermark, ok, err := a.options.Store.SensorWatermark(context.Background(), b.SessionID, b.Interface)
	if err != nil || !ok || watermark.SequenceGaps != 1 {
		t.Fatal(watermark, ok, err)
	}
}

func TestSensorSocketTwoNotificationChannelsOneQuotaRejectionIsPartial(t *testing.T) {
	cfg := config.Defaults()
	cfg.Sensor.Interface = "lab0"
	cfg.Sensor.BatchInterval.Duration = 100 * time.Millisecond
	receiver, _, output := socketSensorApp(t, cfg)
	a := eventTestApp(t)
	a.options.Config.Sensor = cfg.Sensor
	a.options.Config.Notifications.Webhook.Enabled = true
	a.options.WebhookDestination = "webhook:" + strings.Repeat("c", 64)
	if err := a.options.Store.ConfigureNotificationTarget(context.Background(), "webhook", a.options.WebhookDestination, "prefix", true, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	// Use a real shared SQLite transaction to fill exactly 9999 rows, leaving
	// one global slot for the two-channel event. No worker is started.
	db, err := sql.Open("sqlite", a.options.Config.Paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for i := 0; i < 9999; i++ {
		if _, err := tx.Exec(`INSERT INTO notification_outbox(id,dedupe_key,channel,destination,body,next_attempt,created_at,expires_at) VALUES(?,?,'telegram',?,'synthetic quota fixture',?,?,?)`, fmt.Sprintf("quota-%d", i), fmt.Sprintf("quota-key-%d", i), a.options.NotificationDestination, now.UnixMilli(), now.UnixMilli(), now.Add(24*time.Hour).UnixMilli()); err != nil {
			tx.Rollback()
			db.Close()
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()
	conn := rawSensorConnection(t, receiver)
	b := socketSensorBatch("lab0", 100*time.Millisecond, 1)
	b.SessionID = strings.Repeat("c", 32)
	b.Sequence = 1
	b.InboundUDP = 0
	b.InboundSYN = 1
	b.Flows[0].Protocol = "tcp"
	b.Flows[0].TCPFlags = 2
	if err := protocol.WriteFrame(conn, b); err != nil {
		t.Fatal(err)
	}
	a.handleBatch(context.Background(), receiveCommitBatch(t, output))
	ack := readCommitACK(t, conn)
	if !ack.HealthCommitted || !ack.TrafficCommitted || !ack.EventsCommitted || !ack.NotificationsCommitted || ack.Complete || ack.Reason != "notification_rejected" {
		t.Fatal("one-channel rejection was hidden", ack)
	}
}
