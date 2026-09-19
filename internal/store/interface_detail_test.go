// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/model"
)

func TestInterfaceDetailConservesTotalsAndBoundsChurn(t *testing.T) {
	s := budgetStore(t)
	ctx := context.Background()
	hour := time.Now().UTC().Truncate(time.Hour)
	for i := range 35 {
		name := fmt.Sprintf("lab%d", i)
		if i == 34 {
			name = ""
		} // Legacy totals retain unknown attribution.
		if err := s.AddInterface(ctx, model.InterfaceTotals{HourUTC: hour, Interface: name, RXBytes: 100, TXBytes: 10, RXPackets: 2, TXPackets: 1}); err != nil {
			t.Fatal(err)
		}
	}
	var global, detail, overflow, keys, gaps int
	for _, q := range []struct {
		sql   string
		value *int
	}{
		{`SELECT SUM(rx_bytes) FROM interface_hourly`, &global},
		{`SELECT SUM(rx_bytes) FROM interface_detail_hourly`, &detail},
		{`SELECT COUNT(*) FROM interface_detail_hourly`, &keys},
		{`SELECT SUM(rx_bytes) FROM interface_detail_hourly WHERE interface='_overflow/'`, &overflow},
		{`SELECT COUNT(*) FROM coverage_gaps WHERE reason='interface_detail_cardinality'`, &gaps},
	} {
		if err := s.db.QueryRowContext(ctx, q.sql).Scan(q.value); err != nil {
			t.Fatal(err)
		}
	}
	// All overflow identities in one UTC hour share one coverage marker.
	if global != 3500 || detail != 3400 || keys != 33 || overflow != 200 || gaps != 1 {
		t.Fatalf("detail conservation failed: global=%d detail=%d keys=%d overflow=%d gaps=%d", global, detail, keys, overflow, gaps)
	}
	history, err := s.InterfaceHistory(ctx, hour, hour.Add(time.Hour), 100)
	if err != nil || len(history.Interfaces) != 32 || history.Unattributed.RXBytes != 300 || history.Total.RXBytes != 3500 {
		t.Fatalf("history invented identity: %+v %v", history, err)
	}
	page, err := s.InterfaceHistory(ctx, hour, hour.Add(time.Hour), 2)
	if err != nil || len(page.Interfaces) != 2 || !page.Truncated || page.Total != history.Total {
		t.Fatal("bounded history changed totals")
	}
}

func TestReportAttributionDisplayBoundPreservesReconciliationTotals(t *testing.T) {
	s := budgetStore(t)
	ctx := context.Background()
	hour := time.Now().UTC().Truncate(time.Hour)
	if _, err := s.db.ExecContext(ctx, `WITH RECURSIVE n(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<4097) INSERT INTO traffic_hourly(hour_utc,direction,country,region,asn,asn_org,attributed,bytes,packets) SELECT ?,'inbound','ZZ',CAST(i AS TEXT),0,'',1,10,1 FROM n`, hour.Unix()); err != nil {
		t.Fatal(err)
	}
	summary, err := s.Summary(ctx, hour, hour.Add(time.Hour), 10)
	if err != nil || !summary.TrafficTruncated || len(summary.Traffic) != 4096 || summary.AttributedRXBytes != 40970 {
		t.Fatalf("display cap changed totals: %d %d %v", len(summary.Traffic), summary.AttributedRXBytes, err)
	}
}
