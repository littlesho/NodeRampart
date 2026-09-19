// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/littlesho/NodeRampart/internal/api"
	"github.com/littlesho/NodeRampart/internal/model"
)

type InterfaceBreakdown struct {
	Start            time.Time               `json:"start_utc"`
	End              time.Time               `json:"end_utc"`
	Interfaces       []model.InterfaceTotals `json:"interfaces"`
	Total            model.InterfaceTotals   `json:"total"`
	Unattributed     model.InterfaceTotals   `json:"interface_identity_unavailable"`
	Truncated        bool                    `json:"interfaces_truncated"`
	TotalsConsistent bool                    `json:"totals_consistent"`
	Notes            []string                `json:"notes"`
}

// InterfaceHistory conserves the original aggregate, including legacy and
// churn-overflow totals whose interface identities cannot be reconstructed.
func (s *Store) InterfaceHistory(ctx context.Context, start, end time.Time, limit int) (InterfaceBreakdown, error) {
	result := InterfaceBreakdown{Start: start.UTC(), End: end.UTC(), Interfaces: []model.InterfaceTotals{}, Notes: []string{"Counters include every overlapping UTC hour. Legacy and cardinality-overflow identities are unavailable.", "Selected interfaces are summed, without packet deduplication across bridges, tunnels or overlapping paths."}}
	if !api.ValidTimeRange(start, end, 400) || !api.ValidLimit(limit) {
		return result, errors.New("invalid interface history bounds")
	}
	first := start.UTC().Truncate(time.Hour).Unix()
	last := end.UTC().Truncate(time.Hour).Unix()
	if end.After(end.Truncate(time.Hour)) {
		last += 3600
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	readTotals := func(query string, target *model.InterfaceTotals) error {
		return tx.QueryRowContext(ctx, query, first, last).Scan(&target.RXBytes, &target.TXBytes, &target.RXPackets, &target.TXPackets)
	}
	if err := readTotals(`SELECT COALESCE(SUM(rx_bytes),0),COALESCE(SUM(tx_bytes),0),COALESCE(SUM(rx_packets),0),COALESCE(SUM(tx_packets),0) FROM interface_hourly WHERE hour_utc>=? AND hour_utc<?`, &result.Total); err != nil {
		return result, err
	}
	var named model.InterfaceTotals
	if err := readTotals(`SELECT COALESCE(SUM(rx_bytes),0),COALESCE(SUM(tx_bytes),0),COALESCE(SUM(rx_packets),0),COALESCE(SUM(tx_packets),0) FROM interface_detail_hourly WHERE hour_utc>=? AND hour_utc<? AND interface<>'_overflow/'`, &named); err != nil {
		return result, err
	}
	result.TotalsConsistent = named.RXBytes <= result.Total.RXBytes && named.TXBytes <= result.Total.TXBytes && named.RXPackets <= result.Total.RXPackets && named.TXPackets <= result.Total.TXPackets
	difference := func(total, part uint64) uint64 {
		if part >= total {
			return 0
		}
		return total - part
	}
	result.Unattributed = model.InterfaceTotals{RXBytes: difference(result.Total.RXBytes, named.RXBytes), TXBytes: difference(result.Total.TXBytes, named.TXBytes), RXPackets: difference(result.Total.RXPackets, named.RXPackets), TXPackets: difference(result.Total.TXPackets, named.TXPackets)}
	rows, err := tx.QueryContext(ctx, `SELECT interface,SUM(rx_bytes),SUM(tx_bytes),SUM(rx_packets),SUM(tx_packets) FROM interface_detail_hourly WHERE hour_utc>=? AND hour_utc<? AND interface<>'_overflow/' GROUP BY interface ORDER BY SUM(rx_bytes) DESC,interface LIMIT ?`, first, last, limit+1)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		if len(result.Interfaces) >= limit {
			result.Truncated = true
			break
		}
		var item model.InterfaceTotals
		if err := rows.Scan(&item.Interface, &item.RXBytes, &item.TXBytes, &item.RXPackets, &item.TXPackets); err != nil {
			return result, err
		}
		result.Interfaces = append(result.Interfaces, item)
	}
	return result, rows.Err()
}
