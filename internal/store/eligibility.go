// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"errors"
	"time"
)

// NotificationEligible rechecks a prefetched row immediately before delivery.
// An operator quarantine cannot recall a request that is already in flight.
func (s *Store) NotificationEligible(ctx context.Context, id string, now time.Time) (bool, error) {
	if id == "" || len(id) > 128 || now.IsZero() {
		return false, errors.New("invalid notification eligibility query")
	}
	var eligible bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(
 SELECT 1 FROM notification_outbox AS o WHERE id=? AND sent_at IS NULL
 AND quarantined_at IS NULL AND suppressed_at IS NULL AND expires_at>? AND next_attempt<=?
 AND (lease_until IS NULL OR lease_until<=?)
 AND NOT EXISTS (SELECT 1 FROM notification_cooldowns c WHERE c.destination=o.destination AND c.until_at>?)
)`, id, now.UnixMilli(), now.UnixMilli(), now.UnixMilli(), now.UnixMilli()).Scan(&eligible)
	return eligible, err
}
