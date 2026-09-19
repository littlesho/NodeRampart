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
	var totals model.InterfaceTotals
	if !start.Before(end) {
		return totals, fmt.Errorf("interface period must have a positive duration")
	}
	startHour := start.UTC().Truncate(time.Hour).Unix()
	endHour := end.UTC().Truncate(time.Hour).Unix()
	if end.UTC().After(end.UTC().Truncate(time.Hour)) {
		endHour += 3600
	}
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(rx_bytes),0), COALESCE(SUM(tx_bytes),0), COALESCE(SUM(rx_packets),0), COALESCE(SUM(tx_packets),0) FROM interface_hourly WHERE hour_utc>=? AND hour_utc<?`, startHour, endHour).Scan(&totals.RXBytes, &totals.TXBytes, &totals.RXPackets, &totals.TXPackets)
	if err != nil {
		return totals, fmt.Errorf("read interface summary: %w", err)
	}
	return totals, nil
}
