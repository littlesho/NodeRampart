// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"time"
)

// MonitorCoverage reads the evidence required by completed-day checks in one
// snapshot, without counting unrelated retained observations. The filtered
// lifetime totals remain available when detailed ledger entries were evicted.
func (s *Store) MonitorCoverage(ctx context.Context, start, end time.Time) (IntegrityView, RetentionView, error) {
	query := IntegrityQuery{Start: start, End: end, Limit: 1}
	if err := query.Validate(); err != nil {
		return IntegrityView{}, RetentionView{}, err
	}
	tx, asOf, err := s.beginReadSnapshot(ctx)
	if err != nil {
		return IntegrityView{}, RetentionView{}, err
	}
	defer tx.Rollback()
	integrity, err := readIntegrityCore(ctx, tx, query, asOf)
	if err != nil {
		return integrity, RetentionView{}, err
	}
	retention, err := readRetentionDetails(ctx, tx, RetentionQuery{Start: start, End: end, Dataset: "interface_hourly", Limit: 1}, asOf)
	if err != nil {
		return integrity, retention, err
	}
	return integrity, retention, tx.Commit()
}
