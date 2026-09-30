// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"fmt"
	"github.com/littlesho/NodeRampart/internal/model"
	"time"
)

// InterfaceSummary reads only interface counters. Aggregates include every hour
// overlapping [start,end), matching the documented hourly storage precision.
func (s *Store) InterfaceSummary(ctx context.Context, start, end time.Time) (model.InterfaceTotals, error) {
	totals, _, err := s.InterfaceSummaryWithRecords(ctx, start, end)
	return totals, err
}

// InterfaceSummaryWithRecords distinguishes retained zero observations from
// absent hourly rows without a separate inventory scan.
func (s *Store) InterfaceSummaryWithRecords(ctx context.Context, start, end time.Time) (model.InterfaceTotals, int64, error) {
	var totals model.InterfaceTotals
	var records int64
	if !start.Before(end) {
		return totals, records, fmt.Errorf("interface period must have a positive duration")
	}
	startHour := start.UTC().Truncate(time.Hour).Unix()
	endHour := end.UTC().Truncate(time.Hour).Unix()
	if end.UTC().After(end.UTC().Truncate(time.Hour)) {
		endHour += 3600
	}
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(rx_bytes),0), COALESCE(SUM(tx_bytes),0), COALESCE(SUM(rx_packets),0), COALESCE(SUM(tx_packets),0),COUNT(*) FROM interface_hourly WHERE hour_utc>=? AND hour_utc<?`, startHour, endHour).Scan(&totals.RXBytes, &totals.TXBytes, &totals.RXPackets, &totals.TXPackets, &records)
	if err != nil {
		return totals, records, fmt.Errorf("read interface summary: %w", err)
	}
	return totals, records, nil
}
