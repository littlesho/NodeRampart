// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"database/sql"
	"fmt"
)

// Official dispatches share the existing outbox admission transaction. The
// separate intent and ledger survive worker interruption without authorizing
// an ambiguous non-idempotent request again. No legacy body is rewritten.
func migrateV14(ctx context.Context, tx *sql.Tx) error {
	for _, statement := range []string{
		`DROP INDEX event_notifications_message_idx`,
		`ALTER TABLE event_notifications RENAME TO event_notifications_v13`,
		`CREATE TABLE event_notifications (event_id TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
 channel TEXT NOT NULL DEFAULT 'telegram' CHECK(channel IN (` + notificationChannelsSQL + `)),
 notification_id TEXT NOT NULL DEFAULT '', decision TEXT NOT NULL CHECK(decision IN ('queued','merged','silenced','ineligible','rejected')),
 silence_id TEXT NOT NULL DEFAULT '', recorded_at INTEGER NOT NULL, PRIMARY KEY(event_id,channel))`,
		`INSERT INTO event_notifications SELECT event_id,channel,notification_id,decision,silence_id,recorded_at FROM event_notifications_v13`,
		`DROP TABLE event_notifications_v13`,
		`CREATE INDEX event_notifications_message_idx ON event_notifications(notification_id)`,
		`CREATE TABLE notification_dispatch (
 notification_id TEXT PRIMARY KEY REFERENCES notification_outbox(id) ON DELETE CASCADE,
 logical_kind TEXT NOT NULL CHECK(logical_kind IN ('event','daily','test')),
 frozen_payload TEXT NOT NULL CHECK(length(CAST(frozen_payload AS BLOB))<=4096),
 estimated_segments INTEGER NOT NULL DEFAULT 0 CHECK(typeof(estimated_segments)='integer' AND estimated_segments BETWEEN 0 AND 2),
 encoding TEXT NOT NULL DEFAULT '' CHECK(encoding IN ('','gsm7','ucs2')),
 state TEXT NOT NULL DEFAULT 'prepared' CHECK(state IN ('prepared','in_flight','retry_ready','accepted','delivery_unknown','blocked')),
 retry_key TEXT NOT NULL DEFAULT '' CHECK(length(retry_key)<=36),
 first_attempt_at INTEGER NOT NULL DEFAULT 0, last_attempt_at INTEGER NOT NULL DEFAULT 0, uncertain_attempt INTEGER NOT NULL DEFAULT 0 CHECK(uncertain_attempt IN (0,1)),
 dispatch_attempt INTEGER NOT NULL DEFAULT 0 CHECK(typeof(dispatch_attempt)='integer' AND dispatch_attempt BETWEEN 0 AND 10),
 provider_id TEXT NOT NULL DEFAULT '' CHECK(length(provider_id)<=256), accepted_at INTEGER NOT NULL DEFAULT 0,
 budget_day INTEGER NOT NULL DEFAULT -1,
 provider_state TEXT NOT NULL DEFAULT '', platform_segments INTEGER CHECK(platform_segments IS NULL OR (typeof(platform_segments)='integer' AND platform_segments BETWEEN 0 AND 100)),
 platform_price TEXT CHECK(platform_price IS NULL OR length(platform_price)<=32), price_unit TEXT NOT NULL DEFAULT '' CHECK(length(price_unit)<=3),
 poll_attempts INTEGER NOT NULL DEFAULT 0 CHECK(typeof(poll_attempts)='integer' AND poll_attempts BETWEEN 0 AND 8),
 next_poll INTEGER NOT NULL DEFAULT 0, poll_deadline INTEGER NOT NULL DEFAULT 0, poll_lease_until INTEGER NOT NULL DEFAULT 0,
 reason TEXT NOT NULL DEFAULT '' CHECK(length(reason)<=96), http_status INTEGER NOT NULL DEFAULT 0 CHECK(typeof(http_status)='integer' AND http_status BETWEEN 0 AND 599), api_error_code INTEGER NOT NULL DEFAULT 0 CHECK(typeof(api_error_code)='integer' AND api_error_code BETWEEN 0 AND 2147483647))`,
		`CREATE INDEX notification_dispatch_poll_idx ON notification_dispatch(next_poll,poll_deadline)`,
		`CREATE TABLE official_channel_policy (
 channel TEXT PRIMARY KEY CHECK(channel IN ('qqbot','line','twilio_sms','whatsapp_cloud')),
 enabled INTEGER NOT NULL DEFAULT 0 CHECK(enabled IN (0,1)), consented_at INTEGER NOT NULL DEFAULT 0,
 purpose TEXT NOT NULL DEFAULT '' CHECK(length(CAST(purpose AS BLOB))<=256), evidence_ref TEXT NOT NULL DEFAULT '' CHECK(length(CAST(evidence_ref AS BLOB))<=256),
 basis_id TEXT NOT NULL DEFAULT '' CHECK(length(basis_id)<=64), notification_types TEXT NOT NULL DEFAULT '[]' CHECK(length(notification_types)<=128),
 revoked INTEGER NOT NULL DEFAULT 0 CHECK(revoked IN (0,1)), cost_confirmed INTEGER NOT NULL DEFAULT 0 CHECK(cost_confirmed IN (0,1)),
 daily_message_limit INTEGER NOT NULL DEFAULT 0 CHECK(daily_message_limit BETWEEN 0 AND 1000),
 daily_segment_limit INTEGER NOT NULL DEFAULT 0 CHECK(daily_segment_limit BETWEEN 0 AND 2000),
 max_segments INTEGER NOT NULL DEFAULT 0 CHECK(max_segments BETWEEN 0 AND 2),
 optout_at INTEGER NOT NULL DEFAULT 0, optout_basis_id TEXT NOT NULL DEFAULT '' CHECK(length(optout_basis_id)<=64),
 restore_hold INTEGER NOT NULL DEFAULT 0 CHECK(restore_hold IN (0,1)),
 reconciliation_ref TEXT NOT NULL DEFAULT '' CHECK(length(CAST(reconciliation_ref AS BLOB))<=256), clock_highwater INTEGER NOT NULL DEFAULT 0, budget_day_highwater INTEGER NOT NULL DEFAULT -1,
 pruned_budget_days INTEGER NOT NULL DEFAULT 0 CHECK(pruned_budget_days>=0))`,
		`INSERT INTO official_channel_policy(channel,restore_hold) SELECT 'qqbot',0 UNION ALL SELECT 'line',0 UNION ALL
 SELECT 'twilio_sms',EXISTS(SELECT 1 FROM component_status WHERE name='official_paid_restore_hold') UNION ALL
 SELECT 'whatsapp_cloud',EXISTS(SELECT 1 FROM component_status WHERE name='official_paid_restore_hold')`,
		`DELETE FROM component_status WHERE name='official_paid_restore_hold'`,
		`CREATE TABLE official_budget_usage (channel TEXT NOT NULL REFERENCES official_channel_policy(channel), utc_day INTEGER NOT NULL CHECK(utc_day>=0),
 logical_messages INTEGER NOT NULL CHECK(typeof(logical_messages)='integer' AND logical_messages BETWEEN 0 AND 1000),
 estimated_segments INTEGER NOT NULL CHECK(typeof(estimated_segments)='integer' AND estimated_segments BETWEEN 0 AND 2000),
 PRIMARY KEY(channel,utc_day))`,
		`INSERT INTO schema_migrations(version,applied_at) VALUES(14,unixepoch())`,
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("apply database migration 14: %w", err)
		}
	}
	return nil
}
