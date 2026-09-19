// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/model"
	"github.com/littlesho/NodeRampart/internal/protocol"
)

func TestStoreSummaryAndOutbox(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	event := model.Event{ID: "evt_test", ObservedAt: now, Kind: "ssh_brute_force", Severity: model.SeverityHigh, Summary: "test", SourceRange: "203.0.113.0/24"}
	if err := store.InsertEvent(ctx, event); err != nil {
		t.Fatal(err)
	}
	if err := store.AddTraffic(ctx, model.Traffic{HourUTC: now, Direction: model.DirectionInbound, Country: "JP", Bytes: 1000, Packets: 10, Attributed: true}); err != nil {
		t.Fatal(err)
	}
	if err := store.AddAuth(ctx, now, "failure", "203.0.113.0/24"); err != nil {
		t.Fatal(err)
	}
	if err := store.AddAuth(ctx, now, "failure", "203.0.113.0/24"); err != nil {
		t.Fatal(err)
	}
	if err := store.AddInterface(ctx, model.InterfaceTotals{HourUTC: now, RXBytes: 1200, RXPackets: 12}); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordBatchHealth(ctx, protocol.Batch{SentAt: now, OverflowBytes: 25, OverflowPackets: 1, KernelPackets: 100, KernelDrops: 2}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetComponentStatus(ctx, "ssh_journal", "running", now); err != nil {
		t.Fatal(err)
	}
	summary, err := store.Summary(ctx, now.Add(-time.Minute), now.Add(time.Minute), 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(summary.Events) != 1 || len(summary.Auth) != 1 || summary.Auth[0].Count != 2 || len(summary.Components) != 1 || summary.Interface.RXBytes != 1200 || summary.OverflowPackets != 1 || summary.KernelDrops != 2 {
		t.Fatalf("unexpected summary: %#v", summary)
	}
	message := OutboxMessage{ID: "msg_test", DedupeKey: "event:test", Destination: "telegram", Body: "hello"}
	inserted, err := store.Enqueue(ctx, message)
	if err != nil || !inserted {
		t.Fatalf("enqueue: inserted=%v err=%v", inserted, err)
	}
	inserted, err = store.Enqueue(ctx, message)
	if err != nil || inserted {
		t.Fatalf("dedupe failed: inserted=%v err=%v", inserted, err)
	}
	pending, err := store.Pending(ctx, now.Add(time.Minute), 10)
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending=%d err=%v", len(pending), err)
	}
}

func TestStoreTruncatesUTF8Safely(t *testing.T) {
	got := limit("安全安全", 7)
	if got != "安全" {
		t.Fatalf("unsafe truncation result %q", got)
	}
}

func TestOpenRejectsSymlinkedSQLiteSidecar(t *testing.T) {
	directory := t.TempDir()
	database := filepath.Join(directory, "state.db")
	target := filepath.Join(directory, "target")
	if err := os.WriteFile(target, []byte("do not touch"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, database+"-wal"); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(database); err == nil {
		t.Fatal("expected symlinked SQLite sidecar to be rejected")
	}
}

func TestOpenRejectsSQLiteURIDelimiters(t *testing.T) {
	if _, err := Open(filepath.Join(t.TempDir(), "state.db?mode=memory")); err == nil {
		t.Fatal("expected SQLite URI delimiters to be rejected")
	}
}

func TestHourlyCardinalityOverflowBuckets(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Hour)
	if _, err := database.db.ExecContext(ctx, `INSERT INTO traffic_cardinality_hourly(hour_utc, keys) VALUES (?, ?)`, now.Unix(), maxTrafficKeysPerHour); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `INSERT INTO auth_cardinality_hourly(hour_utc, keys) VALUES (?, ?)`, now.Unix(), maxAuthKeysPerHour); err != nil {
		t.Fatal(err)
	}
	if err := database.AddTraffic(ctx, model.Traffic{HourUTC: now, Direction: model.DirectionOutbound, Country: "US", Bytes: 500, Packets: 5, Attributed: true}); err != nil {
		t.Fatal(err)
	}
	if err := database.AddAuth(ctx, now, "failure", "203.0.113.0/24"); err != nil {
		t.Fatal(err)
	}
	summary, err := database.Summary(ctx, now, now.Add(time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(summary.Traffic) != 1 || summary.Traffic[0].Country != "_overflow" || summary.Traffic[0].Bytes != 500 {
		t.Fatalf("unexpected traffic overflow: %#v", summary.Traffic)
	}
	if len(summary.Auth) != 1 || summary.Auth[0].Count != 1 || len(summary.TopSources) != 1 || summary.TopSources[0].SourceRange != "_overflow" {
		t.Fatalf("unexpected auth overflow: auth=%#v sources=%#v", summary.Auth, summary.TopSources)
	}
}
