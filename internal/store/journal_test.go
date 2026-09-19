// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/model"
)

func TestJournalCommitIsAtomicAndReplayIdempotent(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second).Add(123456 * time.Microsecond)
	event := model.Event{ID: "event", ObservedAt: now, Kind: "ssh_login_success", Severity: model.SeverityMedium, Evidence: map[string]string{"oversize": strings.Repeat("x", 70<<10)}}
	write := JournalWrite{Cursor: "s=synthetic;i=1", ReceivedAt: now, ObservedAt: now, Kind: "success", SourceRange: "192.0.2.0/24", Event: &event, Notification: &OutboxMessage{ID: "message", DedupeKey: "event:event:telegram", Destination: "telegram", Body: "synthetic"}}
	if _, err := db.CommitJournal(ctx, write); err == nil {
		t.Fatal("invalid event committed")
	}
	seen, err := db.JournalSeen(ctx, write.Cursor)
	if err != nil || seen {
		t.Fatalf("cursor advanced on failed transaction: %t %v", seen, err)
	}
	checkpoint, err := db.JournalCheckpoint(ctx)
	if err != nil || checkpoint.Cursor != "" {
		t.Fatalf("checkpoint survived rollback: %+v %v", checkpoint, err)
	}
	event.Evidence = nil
	if inserted, err := db.CommitJournal(ctx, write); err != nil || !inserted {
		t.Fatalf("retry failed: %v %v", inserted, err)
	}
	db.Close()
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if inserted, err := db.CommitJournal(ctx, write); err != nil || inserted {
		t.Fatalf("replay duplicated: %v %v", inserted, err)
	}
	summary, err := db.Summary(ctx, now.Add(-time.Hour), now.Add(time.Hour), 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(summary.Auth) != 1 || summary.Auth[0].Count != 1 || len(summary.Events) != 1 || summary.Events[0].Count != 1 {
		t.Fatalf("atomic replay totals wrong: %+v", summary)
	}
	queue, err := db.QueueStatus(ctx, time.Now().UTC())
	if err != nil || queue.Pending != 1 {
		t.Fatalf("notification replay wrong: %+v %v", queue, err)
	}
	checkpoint, err = db.JournalCheckpoint(ctx)
	if err != nil || !checkpoint.ObservedAt.Equal(now) {
		t.Fatalf("checkpoint lost microseconds: %+v %v", checkpoint, err)
	}
}

func TestCoverageRestartPreservesUnknownInterval(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	start := time.Now().UTC().Truncate(time.Hour).Add(-time.Hour)
	if err := db.SetComponentStatus(ctx, "ssh_journal", "running", start); err != nil {
		t.Fatal(err)
	}
	if err := db.HeartbeatCoverage(ctx, start.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := db.resetCoverage(ctx, start.Add(20*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := db.SetComponentStatus(ctx, "ssh_journal", "running", start.Add(20*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := db.HeartbeatCoverage(ctx, start.Add(30*time.Minute)); err != nil {
		t.Fatal(err)
	}
	values, err := db.Coverage(ctx, start, start.Add(40*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	totals := map[string]int64{}
	for _, v := range values {
		totals[v.State] += v.Milliseconds
	}
	if totals["running"] != int64(20*time.Minute/time.Millisecond) || totals["unknown"] != int64(10*time.Minute/time.Millisecond) {
		t.Fatalf("restart invented coverage: %+v", totals)
	}
	// The ten minutes since the last heartbeat remain unrecorded, not running.
	if totals["running"]+totals["unknown"] != int64(30*time.Minute/time.Millisecond) {
		t.Fatal("unobserved tail hidden")
	}
}
