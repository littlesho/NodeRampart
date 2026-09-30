// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"errors"
	"time"
)

const MaxSSHHistoryLogins = 1000

// SSHHistoryLogin contains only identities already retained by event privacy.
// It never reconstructs an IP or adds a second baseline/retention dataset.
type SSHHistoryLogin struct {
	ObservedAt  time.Time
	SourceIP    string
	SourceRange string
}

func (s *Store) SSHLoginHistory(ctx context.Context, start, end time.Time) ([]SSHHistoryLogin, bool, error) {
	if !start.Before(end) || end.Sub(start) > 8*24*time.Hour {
		return nil, false, errors.New("invalid SSH history window")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT observed_at,source_ip,source_range FROM events WHERE kind='ssh_login_success' AND observed_at>=? AND observed_at<? ORDER BY observed_at,id LIMIT 1001`, start.UnixMilli(), end.UnixMilli())
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	result := make([]SSHHistoryLogin, 0)
	for rows.Next() {
		var entry SSHHistoryLogin
		var at int64
		if err := rows.Scan(&at, &entry.SourceIP, &entry.SourceRange); err != nil {
			return nil, false, err
		}
		entry.ObservedAt = time.UnixMilli(at).UTC()
		result = append(result, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	if len(result) > MaxSSHHistoryLogins {
		return result[:MaxSSHHistoryLogins], true, nil
	}
	return result, false, nil
}
