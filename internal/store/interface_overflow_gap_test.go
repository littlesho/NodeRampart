// SPDX-License-Identifier: MIT
package store

import (
	"context"
	"fmt"
	"github.com/littlesho/NodeRampart/internal/model"
	"testing"
	"time"
)

func TestInterfaceOverflowGapIsOncePerHourAndPreservesOtherEvidence(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	hour := time.Now().UTC().Truncate(time.Hour)
	if err := s.RecordCoverageGap(ctx, CoverageGap{Name: "ssh_journal", Reason: "cursor_invalid", Start: hour.Add(-time.Hour), End: hour, Count: 7}); err != nil {
		t.Fatal(err)
	}
	add := func(name string, at time.Time) {
		t.Helper()
		if err := s.AddInterface(ctx, model.InterfaceTotals{Interface: name, HourUTC: at, RXBytes: 1}); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 33 {
		add(fmt.Sprintf("lab%d", i), hour)
	}
	for range 1000 {
		add("lab32", hour)
	}
	var total, cardinality, ssh int
	if err := s.db.QueryRow(`SELECT COUNT(*),SUM(reason='interface_detail_cardinality'),SUM(reason='cursor_invalid') FROM coverage_gaps`).Scan(&total, &cardinality, &ssh); err != nil {
		t.Fatal(err)
	}
	if total != 2 || cardinality != 1 || ssh != 1 {
		t.Fatalf("gaps total=%d overflow=%d prior SSH=%d", total, cardinality, ssh)
	}
	history, err := s.InterfaceHistory(ctx, hour, hour.Add(time.Hour), 100)
	if err != nil || !history.TotalsConsistent || history.Total.RXBytes != 1033 || history.Unattributed.RXBytes != 1001 {
		t.Fatalf("overflow did not conserve totals: %+v error=%v", history, err)
	}
	// Existing schema-4 duplicate evidence remains untouched; new writes dedupe.
	if _, err := s.db.Exec(`INSERT INTO coverage_gaps(name,reason,started_at,ended_at,count) VALUES ('interface_counter','interface_detail_cardinality',?,?,7)`, hour.UnixMilli(), hour.Add(time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	add("lab32", hour)
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM coverage_gaps WHERE reason='interface_detail_cardinality'`).Scan(&cardinality); err != nil || cardinality != 2 {
		t.Fatal("historical duplicates were rewritten or another was added", err)
	}
	for i := range 33 {
		add(fmt.Sprintf("lab%d", i), hour.Add(time.Hour))
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM coverage_gaps WHERE reason='interface_detail_cardinality'`).Scan(&cardinality); err != nil || cardinality != 3 {
		t.Fatal("next-hour overflow evidence missing", err)
	}
}
