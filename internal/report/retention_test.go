// SPDX-License-Identifier: MIT

package report

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/model"
	"github.com/littlesho/NodeRampart/internal/store"
)

func TestReportExplainsActualPruningWithoutInferringContinuousLoss(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	now := time.Now().UTC()
	old := now.Add(-7*24*time.Hour - time.Hour)
	if err := db.InsertEvent(ctx, model.Event{ID: "evt_pruned", ObservedAt: old, Kind: "syn_flood", Phase: "start", Summary: "synthetic"}); err != nil {
		t.Fatal(err)
	}
	if err := db.Prune(ctx, now); err != nil {
		t.Fatal(err)
	}
	body, err := (&Builder{Store: db, Hostname: "fixture", Location: time.UTC, TopN: 5}).Range(ctx, "test", old.Add(-time.Hour), old.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"events / time_expiry: 1 affected rows", "bounding ranges", "not lost packets", "Earlier removal is unknown"} {
		if !strings.Contains(body, want) {
			t.Fatal("report omitted retention qualification", want)
		}
	}
	if len(body) > 4096 {
		t.Fatal("report exceeded message bound")
	}
}
