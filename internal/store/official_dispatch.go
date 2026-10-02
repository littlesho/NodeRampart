// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/littlesho/NodeRampart/internal/api"
)

const (
	OfficialLINEWindow         = 24*time.Hour - 5*time.Minute
	MaxOfficialPollAttempts    = 8
	OfficialPollDeadline       = 24 * time.Hour
	maxOfficialChannelMessages = 750
	maxOfficialChannelBytes    = 2 << 20
	channelReservedMessages    = 64
	channelReservedBytes       = 256 << 10
)

type OfficialChannelPolicy struct {
	Enabled                   bool
	ConsentedAt               time.Time
	Purpose                   string
	EvidenceRef               string
	BasisID                   string
	NotificationTypes         []string
	Revoked                   bool
	CostConfirmed             bool
	DailyMessageLimit         int
	DailySegmentLimit         int
	MaxSegments               int
	PlatformRecoveryConfirmed bool
}

type OfficialDispatchPolicy struct{ MinimumInterval time.Duration }

type OfficialDispatchResolution struct {
	HTTPStatus       int
	APIErrorCode     int
	DispatchAttempt  int
	Outcome          string // accepted, not_accepted or unknown; never final delivery.
	ProviderID       string
	Reason           string
	RetryAfter       time.Duration
	Permanent        bool
	Suspend          bool
	OptOut           bool
	PollStatus       string
	PlatformSegments *int
	Price            *string
	PriceUnit        string
}

type OfficialPollResolution struct {
	HTTPStatus       int
	APIErrorCode     int
	PollAttempt      int
	Status           string
	Reason           string
	RetryAfter       time.Duration
	Suspend          bool
	OptOut           bool
	PlatformSegments *int
	Price            *string
	PriceUnit        string
}

func officialChannel(channel string) bool {
	return channel == "qqbot" || channel == "line" || paidOfficialChannel(channel)
}

func paidOfficialChannel(channel string) bool {
	return channel == "twilio_sms" || channel == "whatsapp_cloud"
}

func controlledOfficialReason(reason string) string {
	switch reason {
	case "provider_accepted", "delivery_unknown", "authentication_rejected", "permission_rejected", "payload_rejected", "rate_limited", "quota_exhausted", "recipient_unavailable", "recipient_opted_out", "template_rejected", "template_unavailable", "template_language_mismatch", "invalid_response", "retry_window_expired", "poll_unknown", "poll_complete", "retry_requires_resume", "transport_not_sent", "official_policy_changed", "official_delivery_error",
		"api_accepted", "carrier_accepted", "carrier_delivered", "delivery_failed", "payload_invalid", "frozen_request_invalid", "message_expired", "clock_rollback", "credential_or_permission_rejected", "provider_rejected", "request_rejected", "quality_or_policy_limited", "monthly_quota_exhausted", "token_unavailable", "token_response_invalid", "token_request_invalid", "token_rejected", "token_wait_canceled", "unconfirmed_response", "unconfirmed_provider_failure", "poll_response_invalid", "poll_receipt_invalid", "poll_request_invalid", "poll_intent_invalid", "durable_intent_required", "unsupported_channel", "template_parameter_count_invalid", "template_missing_or_unapproved", "template_text_too_long", "template_parameter_format_invalid", "template_policy_rejected":
		return reason
	}
	// Character filtering is insufficient: a reflected token can itself be
	// lower-case ASCII. Persist only fixed application-owned categories.
	return "official_delivery_error"
}

func safePolicyText(value string) bool {
	if len(value) > 256 || !utf8.ValidString(value) || strings.Contains(value, "://") {
		return false
	}
	for _, c := range value {
		if unicode.IsControl(c) {
			return false
		}
	}
	return true
}

func validOfficialPolicy(channel string, policy OfficialChannelPolicy, now time.Time) bool {
	if !officialChannel(channel) || now.IsZero() || now.UnixMilli() <= 0 || !safePolicyText(policy.Purpose) || !safePolicyText(policy.EvidenceRef) ||
		policy.DailyMessageLimit < 0 || policy.DailyMessageLimit > 1000 || policy.DailySegmentLimit < 0 || policy.DailySegmentLimit > 2000 || policy.MaxSegments < 0 || policy.MaxSegments > 2 {
		return false
	}
	if policy.BasisID != "" {
		if len(policy.BasisID) != 32 {
			return false
		}
		if _, err := hex.DecodeString(policy.BasisID); err != nil {
			return false
		}
	}
	seen := map[string]bool{}
	for _, kind := range policy.NotificationTypes {
		if kind != "event" && kind != "daily" && kind != "test" || seen[kind] {
			return false
		}
		seen[kind] = true
	}
	if policy.Enabled && !policy.Revoked {
		if policy.ConsentedAt.IsZero() || policy.ConsentedAt.After(now) || policy.Purpose == "" || policy.EvidenceRef == "" || policy.BasisID == "" || len(seen) == 0 || paidOfficialChannel(channel) && !policy.CostConfirmed || policy.DailyMessageLimit == 0 {
			return false
		}
		if channel == "twilio_sms" && (policy.DailySegmentLimit == 0 || policy.MaxSegments == 0) {
			return false
		}
	}
	return true
}

// ConfigureOfficialPolicy never treats ordinary enable/rotation as new consent.
// A platform optout lock survives every ordinary configuration transaction.
func (s *Store) ConfigureOfficialPolicy(ctx context.Context, channel string, policy OfficialChannelPolicy, now time.Time) error {
	if !validOfficialPolicy(channel, policy, now) {
		return errors.New("invalid official notification policy")
	}
	release, err := s.beginWrite(ctx, writeCritical)
	if err != nil {
		return err
	}
	defer release()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var basis string
	var consent, optout int64
	if err := tx.QueryRowContext(ctx, `SELECT basis_id,consented_at,optout_at FROM official_channel_policy WHERE channel=?`, channel).Scan(&basis, &consent, &optout); err != nil {
		return err
	}
	newConsent := policy.BasisID != "" && policy.BasisID != basis && policy.ConsentedAt.UnixMilli() > consent && !policy.Revoked
	completeConsent := !policy.ConsentedAt.IsZero() && !policy.ConsentedAt.After(now) && policy.Purpose != "" && policy.EvidenceRef != "" && len(policy.NotificationTypes) > 0 && policy.DailyMessageLimit > 0 && (!paidOfficialChannel(channel) || policy.CostConfirmed)
	if channel == "twilio_sms" {
		completeConsent = completeConsent && policy.DailySegmentLimit > 0 && policy.MaxSegments > 0
	}
	clearOptout := newConsent && completeConsent && policy.PlatformRecoveryConfirmed && policy.ConsentedAt.UnixMilli() > optout
	types, _ := json.Marshal(policy.NotificationTypes)
	if string(types) == "null" {
		types = []byte("[]")
	}
	consented := int64(0)
	if !policy.ConsentedAt.IsZero() {
		consented = policy.ConsentedAt.UnixMilli()
	}
	_, err = tx.ExecContext(ctx, `UPDATE official_channel_policy SET enabled=?,consented_at=?,purpose=?,evidence_ref=?,basis_id=?,notification_types=?,revoked=?,cost_confirmed=?,daily_message_limit=?,daily_segment_limit=?,max_segments=?,
 optout_at=CASE WHEN ? THEN 0 ELSE optout_at END,optout_basis_id=CASE WHEN ? THEN '' ELSE optout_basis_id END WHERE channel=?`, boolInt(policy.Enabled), consented, policy.Purpose, policy.EvidenceRef, policy.BasisID, string(types), boolInt(policy.Revoked), boolInt(policy.CostConfirmed), policy.DailyMessageLimit, policy.DailySegmentLimit, policy.MaxSegments, boolInt(clearOptout), boolInt(clearOptout), channel)
	if err != nil {
		return err
	}
	if policy.Revoked {
		if _, err = tx.ExecContext(ctx, `UPDATE notification_outbox SET suppressed_at=?,lease_until=NULL,last_error='official_subscription_revoked' WHERE channel=? AND sent_at IS NULL AND suppressed_at IS NULL`, now.UnixMilli(), channel); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func validateOfficialMessage(message OutboxMessage) error {
	if !officialChannel(message.Channel) {
		if message.FrozenPayload != "" || message.LogicalKind != "" || message.EstimatedSegments != 0 || message.Encoding != "" {
			return errors.New("official metadata on legacy channel")
		}
		return nil
	}
	if message.LogicalKind != "event" && message.LogicalKind != "daily" && message.LogicalKind != "test" {
		return errors.New("official notification kind is invalid")
	}
	if message.AdmissionFailure != "" {
		if message.AdmissionFailure != "official_render_rejected" || message.FrozenPayload != "" || message.EstimatedSegments != 0 || message.Encoding != "" {
			return errors.New("invalid official render rejection")
		}
		return nil
	}
	if len(message.FrozenPayload) == 0 || len(message.FrozenPayload) > 4096 || !json.Valid([]byte(message.FrozenPayload)) || !utf8.ValidString(message.FrozenPayload) {
		return errors.New("official frozen payload is invalid")
	}
	// Frozen request content is provided by a platform-specific pure renderer.
	// Reject credential/recipient field names at every object depth as defense
	// in depth; authentication and receiver selection belong to bound snapshots.
	var payload any
	if json.Unmarshal([]byte(message.FrozenPayload), &payload) != nil {
		return errors.New("official frozen payload is invalid")
	}
	if _, ok := payload.(map[string]any); !ok {
		return errors.New("official frozen payload must be a typed object")
	}
	var inspect func(any, int) bool
	inspect = func(value any, depth int) bool {
		if depth > 12 {
			return false
		}
		switch v := value.(type) {
		case map[string]any:
			for key, item := range v {
				normalizedKey := strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(key))
				switch normalizedKey {
				case "recipient", "to", "token", "accesstoken", "secret", "authorization", "credential", "url", "webhookurl", "accountsid", "authtoken", "phonenumberid":
					return false
				}
				if !inspect(item, depth+1) {
					return false
				}
			}
		case []any:
			if len(v) > 64 {
				return false
			}
			for _, item := range v {
				if !inspect(item, depth+1) {
					return false
				}
			}
		case string:
			if strings.ContainsAny(v, "\x00\x1b") {
				return false
			}
		}
		return true
	}
	if !inspect(payload, 0) {
		return errors.New("official frozen payload contains unsafe routing metadata")
	}
	if message.Channel == "twilio_sms" {
		if message.EstimatedSegments < 1 || message.EstimatedSegments > 2 || message.Encoding != "gsm7" && message.Encoding != "ucs2" {
			return errors.New("invalid frozen SMS segment estimate")
		}
	} else if message.EstimatedSegments != 0 || message.Encoding != "" {
		return errors.New("SMS metadata on another channel")
	}
	return nil
}

func newLINERetryKey() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	bytes[6] = bytes[6]&15 | 64
	bytes[8] = bytes[8]&63 | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", bytes[:4], bytes[4:6], bytes[6:8], bytes[8:10], bytes[10:]), nil
}

func insertOfficialIntent(ctx context.Context, tx *sql.Tx, message OutboxMessage) error {
	if !officialChannel(message.Channel) {
		return nil
	}
	key := ""
	if message.Channel == "line" {
		var err error
		key, err = newLINERetryKey()
		if err != nil {
			return err
		}
	}
	state, reason := "prepared", ""
	if message.AdmissionFailure != "" {
		state, reason = "blocked", message.AdmissionFailure
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO notification_dispatch(notification_id,logical_kind,frozen_payload,estimated_segments,encoding,retry_key,state,reason) VALUES(?,?,?,?,?,?,?,?)`, message.ID, message.LogicalKind, message.FrozenPayload, message.EstimatedSegments, message.Encoding, key, state, reason)
	if err == nil && reason != "" {
		_, err = tx.ExecContext(ctx, `UPDATE notification_outbox SET quarantined_at=?,last_error=? WHERE id=?`, time.Now().UTC().UnixMilli(), reason, message.ID)
	}
	return err
}

func channelOutboxLimits(channel string) (int, int) {
	if officialChannel(channel) {
		return maxOfficialChannelMessages, maxOfficialChannelBytes
	}
	return maxChannelOutboxMessages, maxChannelOutboxBytes
}

type admissionUsage struct{ count, bytes, channelCount, channelBytes, reservedCount, reservedBytes int64 }

func outboxAdmissionUsage(ctx context.Context, tx *sql.Tx, channel string) (admissionUsage, error) {
	var usage admissionUsage
	// Materialize one bounded outbox pass. Legacy rows need no auxiliary lookup;
	// the twelve fixed active targets then reserve only their unused headroom.
	err := tx.QueryRowContext(ctx, `WITH usage AS MATERIALIZED (
 SELECT o.channel,COUNT(*) AS n,SUM(length(CAST(o.body AS BLOB))+CASE WHEN o.channel IN ('qqbot','line','twilio_sms','whatsapp_cloud') THEN COALESCE((SELECT length(CAST(d.frozen_payload AS BLOB)) FROM notification_dispatch d WHERE d.notification_id=o.id),0) ELSE 0 END) AS bytes
 FROM notification_outbox o WHERE o.sent_at IS NULL AND o.suppressed_at IS NULL GROUP BY o.channel)
 SELECT COALESCE(SUM(n),0),COALESCE(SUM(bytes),0),COALESCE(SUM(CASE WHEN channel=? THEN n ELSE 0 END),0),COALESCE(SUM(CASE WHEN channel=? THEN bytes ELSE 0 END),0),
 (SELECT COALESCE(SUM(MAX(0,?-COALESCE(u.n,0))),0) FROM notification_targets t LEFT JOIN usage u ON u.channel=t.channel WHERE t.enabled=1 AND t.channel<>? AND t.channel IN (`+notificationChannelsSQL+`)),
 (SELECT COALESCE(SUM(MAX(0,?-COALESCE(u.bytes,0))),0) FROM notification_targets t LEFT JOIN usage u ON u.channel=t.channel WHERE t.enabled=1 AND t.channel<>? AND t.channel IN (`+notificationChannelsSQL+`))
 FROM usage`, channel, channel, channelReservedMessages, channel, channelReservedBytes, channel).Scan(&usage.count, &usage.bytes, &usage.channelCount, &usage.channelBytes, &usage.reservedCount, &usage.reservedBytes)
	return usage, err
}

type officialRow struct {
	uncertain                                                      bool
	message                                                        OutboxMessage
	first, last, accepted, budgetDay, pollNext, pollEnd, pollLease int64
	providerState, reason                                          string
}

func readOfficialRow(ctx context.Context, tx *sql.Tx, id string) (officialRow, error) {
	var row officialRow
	var next, expires int64
	err := tx.QueryRowContext(ctx, `SELECT o.id,o.dedupe_key,o.channel,o.destination,o.body,o.attempts,o.next_attempt,o.language,o.presentation_timezone,o.expires_at,
 d.logical_kind,d.frozen_payload,d.estimated_segments,d.encoding,d.state,d.retry_key,d.first_attempt_at,d.last_attempt_at,d.dispatch_attempt,d.provider_id,d.accepted_at,d.budget_day,d.provider_state,d.poll_attempts,d.next_poll,d.poll_deadline,d.poll_lease_until,d.reason,d.uncertain_attempt
 FROM notification_outbox o JOIN notification_dispatch d ON d.notification_id=o.id WHERE o.id=?`, id).Scan(&row.message.ID, &row.message.DedupeKey, &row.message.Channel, &row.message.Destination, &row.message.Body, &row.message.Attempts, &next, &row.message.Language, &row.message.Timezone, &expires, &row.message.LogicalKind, &row.message.FrozenPayload, &row.message.EstimatedSegments, &row.message.Encoding, &row.message.DispatchState, &row.message.RetryKey, &row.first, &row.last, &row.message.DispatchAttempt, &row.message.ProviderID, &row.accepted, &row.budgetDay, &row.providerState, &row.message.PollAttempts, &row.pollNext, &row.pollEnd, &row.pollLease, &row.reason, &row.uncertain)
	row.message.NextAttempt = time.UnixMilli(next).UTC()
	row.message.ExpiresAt = time.UnixMilli(expires).UTC()
	if row.first > 0 {
		row.message.FirstAttemptAt = time.UnixMilli(row.first).UTC()
	}
	if row.pollNext > 0 {
		row.message.NextPoll = time.UnixMilli(row.pollNext).UTC()
	}
	if row.pollEnd > 0 {
		row.message.PollDeadline = time.UnixMilli(row.pollEnd).UTC()
	}
	return row, err
}

func blockOfficial(ctx context.Context, tx *sql.Tx, id, reason string, now time.Time, unknown bool) error {
	state := "blocked"
	if unknown {
		state = "delivery_unknown"
	}
	if _, err := tx.ExecContext(ctx, `UPDATE notification_dispatch SET state=?,reason=? WHERE notification_id=? AND state<>'accepted'`, state, reason, id); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE notification_outbox SET lease_until=NULL,quarantined_at=?,last_error=? WHERE id=? AND sent_at IS NULL`, now.UnixMilli(), reason, id)
	return err
}

// BeginOfficialDispatch is the sole authorization for a new official POST.
// Its committed intent, consumed interval and (paid) debit precede the network.
func (s *Store) BeginOfficialDispatch(ctx context.Context, id, destination string, now time.Time, policy OfficialDispatchPolicy) (OutboxMessage, bool, error) {
	if !api.ValidID(id) || len(destination) > 128 || destination == "" || now.IsZero() || now.UnixMilli() <= 0 || policy.MinimumInterval < 0 || policy.MinimumInterval > 30*time.Second || policy.MinimumInterval > 0 && policy.MinimumInterval < time.Second {
		return OutboxMessage{}, false, errors.New("invalid official dispatch intent")
	}
	release, err := s.beginWrite(ctx, writeCritical)
	if err != nil {
		return OutboxMessage{}, false, err
	}
	defer release()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return OutboxMessage{}, false, err
	}
	defer tx.Rollback()
	row, err := readOfficialRow(ctx, tx, id)
	if err != nil {
		return OutboxMessage{}, false, err
	}
	m := row.message
	if m.Destination != destination || !officialChannel(m.Channel) {
		return OutboxMessage{}, false, errors.New("official intent target mismatch")
	}
	var eligible bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM notification_outbox o WHERE id=? AND sent_at IS NULL AND suppressed_at IS NULL AND isolated_at IS NULL AND quarantined_at IS NULL AND lease_until>? AND expires_at>? AND EXISTS(SELECT 1 FROM notification_targets t WHERE t.channel=o.channel AND t.destination=o.destination AND t.enabled=1) AND NOT EXISTS(SELECT 1 FROM notification_cooldowns c WHERE c.destination=o.destination AND c.until_at>?))`, id, now.UnixMilli(), now.UnixMilli(), now.UnixMilli()).Scan(&eligible); err != nil {
		return OutboxMessage{}, false, err
	}
	if !eligible || m.DispatchState != "prepared" && m.DispatchState != "retry_ready" {
		return m, false, nil
	}
	var enabled, revoked, cost, restore bool
	var consent, optout, clock, dayHigh int64
	var types, basis, purpose, evidence string
	var capMessages, capSegments, maxSegments int
	if err := tx.QueryRowContext(ctx, `SELECT enabled,consented_at,purpose,evidence_ref,basis_id,notification_types,revoked,cost_confirmed,daily_message_limit,daily_segment_limit,max_segments,optout_at,restore_hold,clock_highwater,budget_day_highwater FROM official_channel_policy WHERE channel=?`, m.Channel).Scan(&enabled, &consent, &purpose, &evidence, &basis, &types, &revoked, &cost, &capMessages, &capSegments, &maxSegments, &optout, &restore, &clock, &dayHigh); err != nil {
		return OutboxMessage{}, false, err
	}
	reason := ""
	if !enabled || revoked {
		reason = "official_subscription_paused"
	}
	if optout > 0 {
		reason = "official_subscription_opted_out"
	}
	if restore {
		reason = "official_restore_reconciliation_required"
	}
	if now.UnixMilli() < clock || row.last > now.UnixMilli() {
		reason = "official_clock_moved_backwards"
	}
	if m.Channel == "line" && row.first > 0 && now.Sub(time.UnixMilli(row.first)) >= OfficialLINEWindow {
		reason = "line_retry_window_expired"
	}
	day := now.UTC().Unix() / 86400
	if officialChannel(m.Channel) {
		var allowedTypes []string
		_ = json.Unmarshal([]byte(types), &allowedTypes)
		allowedKind := false
		for _, kind := range allowedTypes {
			allowedKind = allowedKind || kind == m.LogicalKind
		}
		if consent <= 0 || consent > now.UnixMilli() || purpose == "" || evidence == "" || basis == "" || paidOfficialChannel(m.Channel) && !cost || capMessages == 0 || !allowedKind {
			reason = "official_subscription_required"
		}
		if day < dayHigh {
			reason = "official_clock_moved_backwards"
		}
		if m.Channel == "twilio_sms" && (capSegments == 0 || maxSegments == 0 || m.EstimatedSegments > maxSegments) {
			reason = "official_segment_limit"
		}
		if paidOfficialChannel(m.Channel) && row.budgetDay >= 0 && row.budgetDay != day {
			reason = "official_budget_day_changed"
		}
	}
	if reason != "" {
		if err := blockOfficial(ctx, tx, id, reason, now, row.uncertain); err != nil {
			return m, false, err
		}
		return m, false, tx.Commit()
	}
	if officialChannel(m.Channel) && row.budgetDay < 0 {
		var usedMessages, usedSegments int
		err := tx.QueryRowContext(ctx, `SELECT logical_messages,estimated_segments FROM official_budget_usage WHERE channel=? AND utc_day=?`, m.Channel, day).Scan(&usedMessages, &usedSegments)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return m, false, err
		}
		if usedMessages >= capMessages || m.Channel == "twilio_sms" && usedSegments+m.EstimatedSegments > capSegments {
			if err := blockOfficial(ctx, tx, id, "official_daily_budget_exhausted", now, false); err != nil {
				return m, false, err
			}
			return m, false, tx.Commit()
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO official_budget_usage(channel,utc_day,logical_messages,estimated_segments) VALUES(?,?,1,?) ON CONFLICT(channel,utc_day) DO UPDATE SET logical_messages=logical_messages+1,estimated_segments=estimated_segments+excluded.estimated_segments`, m.Channel, day, m.EstimatedSegments); err != nil {
			return m, false, err
		}
		row.budgetDay = day
		// At most 400 day rows per fixed channel, independent of outbox pruning.
		if _, err := tx.ExecContext(ctx, `UPDATE official_channel_policy SET pruned_budget_days=pruned_budget_days+(SELECT COUNT(*) FROM official_budget_usage WHERE channel=? AND utc_day<?) WHERE channel=?`, m.Channel, day-399, m.Channel); err != nil {
			return m, false, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM official_budget_usage WHERE channel=? AND utc_day<?`, m.Channel, day-399); err != nil {
			return m, false, err
		}
	}
	if m.DispatchAttempt >= MaxDeliveryAttempts {
		if err := blockOfficial(ctx, tx, id, "official_attempt_limit", now, row.uncertain); err != nil {
			return m, false, err
		}
		return m, false, tx.Commit()
	}
	if row.first == 0 {
		row.first = now.UnixMilli()
	}
	if _, err := tx.ExecContext(ctx, `UPDATE notification_dispatch SET state='in_flight',uncertain_attempt=1,first_attempt_at=?,last_attempt_at=?,dispatch_attempt=dispatch_attempt+1,budget_day=?,reason='' WHERE notification_id=?`, row.first, now.UnixMilli(), row.budgetDay, id); err != nil {
		return m, false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE official_channel_policy SET clock_highwater=MAX(clock_highwater,?),budget_day_highwater=MAX(budget_day_highwater,?) WHERE channel=?`, now.UnixMilli(), day, m.Channel); err != nil {
		return m, false, err
	}
	if policy.MinimumInterval > 0 {
		until := now.Add(policy.MinimumInterval)
		if !until.Equal(until.Truncate(time.Millisecond)) {
			until = until.Truncate(time.Millisecond).Add(time.Millisecond)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO notification_cooldowns(destination,until_at) VALUES(?,?) ON CONFLICT(destination) DO UPDATE SET until_at=MAX(until_at,excluded.until_at)`, destination, until.UnixMilli()); err != nil {
			return m, false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return m, false, err
	}
	m.DispatchState = "in_flight"
	m.DispatchAttempt++
	m.FirstAttemptAt = time.UnixMilli(row.first).UTC()
	return m, true, nil
}

func validProviderID(channel, id string) bool {
	if id == "" || len(id) > 256 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-_.:+/=", c)) {
			return false
		}
	}
	if channel == "twilio_sms" {
		if len(id) != 34 || !strings.HasPrefix(id, "SM") {
			return false
		}
		_, err := hex.DecodeString(id[2:])
		return err == nil
	}
	if channel == "whatsapp_cloud" && !strings.HasPrefix(id, "wamid.") {
		return false
	}
	if strings.Contains(id, "://") {
		return false
	}
	return true
}

func validPlatformCharge(segments *int, price *string, unit string) bool {
	if segments != nil && (*segments < 0 || *segments > 100) {
		return false
	}
	if len(unit) != 0 && len(unit) != 3 {
		return false
	}
	for _, c := range unit {
		if c < 'A' || c > 'Z' {
			return false
		}
	}
	if price != nil {
		if len(*price) == 0 || len(*price) > 32 || unit == "" {
			return false
		}
		digits, dots := 0, 0
		for i, c := range *price {
			if c >= '0' && c <= '9' {
				digits++
				continue
			}
			if c == '.' {
				dots++
				if dots > 1 {
					return false
				}
				continue
			}
			if c == '-' && i == 0 {
				continue
			}
			return false
		}
		if digits == 0 {
			return false
		}
	}
	return true
}

func savePlatformCharge(ctx context.Context, tx *sql.Tx, id string, segments *int, price *string, unit string) error {
	_, err := tx.ExecContext(ctx, `UPDATE notification_dispatch SET platform_segments=COALESCE(?,platform_segments),platform_price=COALESCE(?,platform_price),price_unit=CASE WHEN ?<>'' THEN ? ELSE price_unit END WHERE notification_id=?`, segments, price, unit, unit, id)
	return err
}

func persistOfficialOptout(ctx context.Context, tx *sql.Tx, channel string, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `UPDATE official_channel_policy SET optout_at=MAX(optout_at,?),optout_basis_id=basis_id WHERE channel=?`, now.UnixMilli(), channel); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE notification_outbox SET suppressed_at=?,lease_until=NULL,last_error='official_subscription_opted_out' WHERE channel=? AND sent_at IS NULL AND suppressed_at IS NULL`, now.UnixMilli(), channel)
	return err
}

// ResolveOfficialDispatch cannot authorize a second network attempt itself.
// A stale resolver may not overwrite a newer intent's outcome.
func (s *Store) ResolveOfficialDispatch(ctx context.Context, id string, now time.Time, result OfficialDispatchResolution) error {
	if !api.ValidID(id) || now.IsZero() || result.DispatchAttempt < 1 || result.DispatchAttempt > MaxDeliveryAttempts || result.Outcome != "accepted" && result.Outcome != "not_accepted" && result.Outcome != "unknown" || result.RetryAfter < 0 || !validPlatformCharge(result.PlatformSegments, result.Price, result.PriceUnit) || result.HTTPStatus < 0 || result.HTTPStatus > 599 || result.APIErrorCode < 0 || result.APIErrorCode > 2147483647 {
		return errors.New("invalid official dispatch resolution")
	}
	release, err := s.beginWrite(ctx, writeCritical)
	if err != nil {
		return err
	}
	defer release()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	row, err := readOfficialRow(ctx, tx, id)
	if err != nil {
		return err
	}
	lateAcceptance := result.Outcome == "accepted" && (row.message.DispatchState == "delivery_unknown" || row.message.DispatchState == "retry_ready")
	if row.message.DispatchState != "in_flight" && !lateAcceptance || row.message.DispatchAttempt != result.DispatchAttempt {
		return errors.New("official dispatch resolution does not match active intent")
	}
	reason := controlledOfficialReason(result.Reason)
	if result.Outcome == "accepted" {
		if !validProviderID(row.message.Channel, result.ProviderID) {
			return errors.New("invalid official acceptance receipt")
		}
		pollNext, pollEnd := int64(0), int64(0)
		providerState := "accepted"
		if result.PollStatus == "api_accepted" || result.PollStatus == "accepted" || row.message.Channel == "whatsapp_cloud" && result.PollStatus == "held_for_quality_assessment" {
			providerState = result.PollStatus
		}
		if row.message.Channel == "twilio_sms" {
			providerState = result.PollStatus
			if !validPollStatus(providerState) {
				providerState = "queued"
			}
			pollNext = now.Add(30 * time.Second).UnixMilli()
			// The total polling window is anchored to the durable first POST
			// intent. A delayed acceptance cannot extend it, and the sender's
			// persisted-window validation uses this same boundary.
			pollEnd = time.UnixMilli(row.first).Add(OfficialPollDeadline).UnixMilli()
			if terminalPollStatus(providerState) {
				pollNext = 0
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE notification_dispatch SET state='accepted',uncertain_attempt=0,provider_id=?,accepted_at=?,provider_state=?,next_poll=?,poll_deadline=?,reason='' WHERE notification_id=?`, result.ProviderID, now.UnixMilli(), providerState, pollNext, pollEnd, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE notification_outbox SET sent_at=?,lease_until=NULL,last_error='' WHERE id=?`, now.UnixMilli(), id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE notification_counters SET last_sent_at=MAX(last_sent_at,?) WHERE id=1`, now.UnixMilli()); err != nil {
			return err
		}
	} else if result.Outcome == "unknown" {
		wait := result.RetryAfter
		if wait < 30*time.Second {
			wait = 30 * time.Second
		}
		// An uncertain request still consumes the provider's target-wide
		// waiting advice. Other queued messages cannot bypass Retry-After.
		if result.RetryAfter > 0 || row.message.Channel == "line" {
			until := now.Add(wait)
			if wait > OutboxTTL || result.Suspend {
				until = time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)
			}
			if !until.Equal(until.Truncate(time.Millisecond)) {
				until = until.Truncate(time.Millisecond).Add(time.Millisecond)
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO notification_cooldowns(destination,until_at) VALUES(?,?) ON CONFLICT(destination) DO UPDATE SET until_at=MAX(until_at,excluded.until_at)`, row.message.Destination, until.UnixMilli()); err != nil {
				return err
			}
		}

		if row.message.Channel == "line" && !result.Suspend && now.UnixMilli() >= row.last && wait < OfficialLINEWindow && now.Add(wait).Sub(time.UnixMilli(row.first)) < OfficialLINEWindow {
			if _, err := tx.ExecContext(ctx, `UPDATE notification_dispatch SET state='retry_ready',reason=? WHERE notification_id=?`, reason, id); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE notification_outbox SET lease_until=NULL,attempts=attempts+1,next_attempt=?,last_error=? WHERE id=?`, now.Add(wait).UnixMilli(), reason, id); err != nil {
				return err
			}
		} else {
			unknownReason := reason
			if unknownReason == "official_delivery_error" {
				unknownReason = "official_delivery_unknown"
			}
			if err := blockOfficial(ctx, tx, id, unknownReason, now, true); err != nil {
				return err
			}
		}
	} else {
		if _, err := tx.ExecContext(ctx, `UPDATE notification_dispatch SET uncertain_attempt=0 WHERE notification_id=?`, id); err != nil {
			return err
		}
		if result.Permanent || result.Suspend || result.OptOut || row.message.DispatchAttempt >= MaxDeliveryAttempts || result.RetryAfter > OutboxTTL {
			if err := blockOfficial(ctx, tx, id, reason, now, false); err != nil {
				return err
			}
		} else {
			wait := result.RetryAfter
			if wait < time.Second {
				wait = time.Second
			}
			if _, err := tx.ExecContext(ctx, `UPDATE notification_dispatch SET state='retry_ready',reason=? WHERE notification_id=?`, reason, id); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE notification_outbox SET lease_until=NULL,attempts=attempts+1,next_attempt=?,last_error=? WHERE id=?`, now.Add(wait).UnixMilli(), reason, id); err != nil {
				return err
			}
			if result.RetryAfter > 0 {
				if _, err := tx.ExecContext(ctx, `INSERT INTO notification_cooldowns(destination,until_at) VALUES(?,?) ON CONFLICT(destination) DO UPDATE SET until_at=MAX(until_at,excluded.until_at)`, row.message.Destination, now.Add(wait).UnixMilli()); err != nil {
					return err
				}
			}
		}
	}
	if result.OptOut {
		if err := persistOfficialOptout(ctx, tx, row.message.Channel, now); err != nil {
			return err
		}
	}
	if result.Suspend {
		until := time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC).UnixMilli()
		if _, err := tx.ExecContext(ctx, `INSERT INTO notification_cooldowns(destination,until_at) VALUES(?,?) ON CONFLICT(destination) DO UPDATE SET until_at=MAX(until_at,excluded.until_at)`, row.message.Destination, until); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE notification_dispatch SET http_status=?,api_error_code=? WHERE notification_id=?`, result.HTTPStatus, result.APIErrorCode, id); err != nil {
		return err
	}
	if err := savePlatformCharge(ctx, tx, id, result.PlatformSegments, result.Price, result.PriceUnit); err != nil {
		return err
	}
	return tx.Commit()
}

func validPollStatus(status string) bool {
	switch status {
	case "queued", "accepted", "sending", "sent", "delivered", "undelivered", "failed", "canceled", "unknown":
		return true
	}
	return false
}
func terminalPollStatus(status string) bool {
	return status == "delivered" || status == "undelivered" || status == "failed" || status == "canceled" || status == "unknown"
}

// Recover only expired intents, never one whose original network lease is live.
func recoverOfficialIntents(ctx context.Context, tx *sql.Tx, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `UPDATE notification_dispatch SET state=CASE WHEN (SELECT channel FROM notification_outbox WHERE id=notification_id)='line' AND first_attempt_at>0 AND last_attempt_at<=? AND first_attempt_at>? THEN 'retry_ready' ELSE 'delivery_unknown' END,reason='official_interrupted_dispatch' WHERE state='in_flight' AND EXISTS(SELECT 1 FROM notification_outbox o WHERE o.id=notification_id AND (o.lease_until IS NULL OR o.lease_until<=?))`, now.UnixMilli(), now.Add(-OfficialLINEWindow).UnixMilli(), now.UnixMilli()); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE notification_dispatch SET state=CASE WHEN uncertain_attempt=1 THEN 'delivery_unknown' ELSE 'blocked' END,reason='line_retry_window_expired' WHERE state IN ('prepared','retry_ready') AND first_attempt_at>0 AND first_attempt_at<=? AND EXISTS(SELECT 1 FROM notification_outbox o WHERE o.id=notification_id AND o.channel='line')`, now.Add(-OfficialLINEWindow).UnixMilli()); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE notification_outbox SET lease_until=NULL,quarantined_at=COALESCE(quarantined_at,?),last_error=COALESCE((SELECT reason FROM notification_dispatch d WHERE d.notification_id=id),'official_delivery_unknown') WHERE sent_at IS NULL AND suppressed_at IS NULL AND EXISTS(SELECT 1 FROM notification_dispatch d WHERE d.notification_id=id AND d.state IN ('delivery_unknown','blocked'))`, now.UnixMilli())
	return err
}

func expireOfficialPolls(ctx context.Context, tx *sql.Tx, now time.Time) error {
	_, err := tx.ExecContext(ctx, `UPDATE notification_dispatch SET provider_state='unknown',next_poll=0,poll_lease_until=0,reason='official_poll_limit' WHERE state='accepted' AND next_poll>0 AND (poll_deadline<=? OR (poll_attempts>=? AND poll_lease_until<=?))`, now.UnixMilli(), MaxOfficialPollAttempts, now.UnixMilli())
	return err
}

func (s *Store) PendingOfficialPolls(ctx context.Context, destination string, now time.Time, count int) ([]OutboxMessage, error) {
	if destination == "" || len(destination) > 128 || now.IsZero() || count < 1 || count > 100 {
		return nil, errors.New("invalid official poll query")
	}
	release, err := s.beginWrite(ctx, writeCritical)
	if err != nil {
		return nil, err
	}
	defer release()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := expireOfficialPolls(ctx, tx, now); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT o.id FROM notification_outbox o JOIN notification_dispatch d ON d.notification_id=o.id WHERE o.channel='twilio_sms' AND o.destination=? AND d.state='accepted' AND d.provider_id<>'' AND d.next_poll>0 AND d.next_poll<=? AND d.poll_deadline>? AND d.poll_attempts<? AND d.poll_lease_until<=? AND EXISTS(SELECT 1 FROM notification_targets t WHERE t.channel=o.channel AND t.destination=o.destination AND t.enabled=1) AND EXISTS(SELECT 1 FROM official_channel_policy p WHERE p.channel=o.channel AND p.enabled=1 AND p.revoked=0 AND p.optout_at=0 AND p.restore_hold=0) AND NOT EXISTS(SELECT 1 FROM notification_cooldowns c WHERE c.destination=o.destination AND c.until_at>?) ORDER BY d.next_poll,o.id LIMIT ?`, destination, now.UnixMilli(), now.UnixMilli(), MaxOfficialPollAttempts, now.UnixMilli(), now.UnixMilli(), count)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	result := make([]OutboxMessage, 0, len(ids))
	for _, id := range ids {
		row, err := readOfficialRow(ctx, tx, id)
		if err != nil {
			return nil, err
		}
		result = append(result, row.message)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Store) ClaimOfficialPoll(ctx context.Context, id, destination string, now time.Time) (OutboxMessage, bool, error) {
	if !api.ValidID(id) || destination == "" || len(destination) > 128 || now.IsZero() {
		return OutboxMessage{}, false, errors.New("invalid official poll claim")
	}
	release, err := s.beginWrite(ctx, writeCritical)
	if err != nil {
		return OutboxMessage{}, false, err
	}
	defer release()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return OutboxMessage{}, false, err
	}
	defer tx.Rollback()
	if err := expireOfficialPolls(ctx, tx, now); err != nil {
		return OutboxMessage{}, false, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE notification_dispatch SET poll_attempts=poll_attempts+1,poll_lease_until=? WHERE notification_id=? AND state='accepted' AND provider_id<>'' AND next_poll>0 AND next_poll<=? AND poll_deadline>? AND poll_attempts<? AND poll_lease_until<=? AND EXISTS(SELECT 1 FROM notification_outbox o JOIN notification_targets t ON t.channel=o.channel AND t.destination=o.destination JOIN official_channel_policy p ON p.channel=o.channel WHERE o.id=notification_id AND o.channel='twilio_sms' AND o.destination=? AND t.enabled=1 AND p.enabled=1 AND p.revoked=0 AND p.optout_at=0 AND p.restore_hold=0 AND NOT EXISTS(SELECT 1 FROM notification_cooldowns c WHERE c.destination=o.destination AND c.until_at>?))`, now.Add(2*time.Minute).UnixMilli(), id, now.UnixMilli(), now.UnixMilli(), MaxOfficialPollAttempts, now.UnixMilli(), destination, now.UnixMilli())
	if err != nil {
		return OutboxMessage{}, false, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return OutboxMessage{}, false, err
	}
	if n == 0 {
		return OutboxMessage{}, false, tx.Commit()
	}
	row, err := readOfficialRow(ctx, tx, id)
	if err != nil {
		return OutboxMessage{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return OutboxMessage{}, false, err
	}
	return row.message, true, nil
}

func (s *Store) OfficialPollAllowed(ctx context.Context, id, destination string, attempt int, now time.Time) (bool, error) {
	if !api.ValidID(id) || destination == "" || len(destination) > 128 || attempt < 1 || attempt > MaxOfficialPollAttempts || now.IsZero() {
		return false, errors.New("invalid official poll authorization")
	}
	var allowed bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM notification_dispatch d JOIN notification_outbox o ON o.id=d.notification_id JOIN notification_targets t ON t.channel=o.channel AND t.destination=o.destination JOIN official_channel_policy p ON p.channel=o.channel WHERE o.id=? AND o.channel='twilio_sms' AND o.destination=? AND d.state='accepted' AND d.provider_id<>'' AND d.poll_attempts=? AND d.poll_lease_until>? AND d.poll_deadline>? AND t.enabled=1 AND p.enabled=1 AND p.revoked=0 AND p.optout_at=0 AND p.restore_hold=0 AND NOT EXISTS(SELECT 1 FROM notification_cooldowns c WHERE c.destination=o.destination AND c.until_at>?))`, id, destination, attempt, now.UnixMilli(), now.UnixMilli(), now.UnixMilli()).Scan(&allowed)
	return allowed, err
}

// ResolveOfficialPoll stores a GET observation; it never makes POST ready.
// Empty/unknown observations may retry GET within the persisted limits.
func (s *Store) ResolveOfficialPoll(ctx context.Context, id string, now time.Time, result OfficialPollResolution) error {
	if !api.ValidID(id) || now.IsZero() || result.PollAttempt < 1 || result.PollAttempt > MaxOfficialPollAttempts || result.Status != "" && !validPollStatus(result.Status) || result.RetryAfter < 0 || !validPlatformCharge(result.PlatformSegments, result.Price, result.PriceUnit) || result.HTTPStatus < 0 || result.HTTPStatus > 599 || result.APIErrorCode < 0 || result.APIErrorCode > 2147483647 {
		return errors.New("invalid official poll resolution")
	}
	release, err := s.beginWrite(ctx, writeCritical)
	if err != nil {
		return err
	}
	defer release()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	row, err := readOfficialRow(ctx, tx, id)
	if err != nil {
		return err
	}
	if row.message.Channel != "twilio_sms" || row.message.DispatchState != "accepted" || row.message.PollAttempts != result.PollAttempt || row.pollLease == 0 {
		return errors.New("official poll resolution does not match active lease")
	}
	status := result.Status
	if status == "" {
		status = row.providerState
	}
	if result.Suspend {
		status = "unknown"
	}
	next := int64(0)
	reason := ""
	terminal := status != "unknown" && terminalPollStatus(status)
	if !terminal && !result.OptOut && result.PollAttempt < MaxOfficialPollAttempts && now.UnixMilli() < row.pollEnd {
		wait := result.RetryAfter
		if wait < 30*time.Second {
			wait = 30 * time.Second
		}
		if wait <= OfficialPollDeadline && now.Add(wait).UnixMilli() < row.pollEnd {
			next = now.Add(wait).UnixMilli()
		} else {
			status = "unknown"
			reason = "official_poll_deadline"
		}
	} else if !terminal && !result.OptOut {
		status = "unknown"
		reason = "official_poll_limit"
	}
	if result.OptOut {
		status = "failed"
		reason = "official_subscription_opted_out"
		if err := persistOfficialOptout(ctx, tx, "twilio_sms", now); err != nil {
			return err
		}
	}
	if result.Suspend || result.RetryAfter > 0 {
		wait := result.RetryAfter
		if wait < 30*time.Second {
			wait = 30 * time.Second
		}
		until := now.Add(wait)
		if result.Suspend || wait > OutboxTTL {
			until = time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)
		}
		if !until.Equal(until.Truncate(time.Millisecond)) {
			until = until.Truncate(time.Millisecond).Add(time.Millisecond)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO notification_cooldowns(destination,until_at) VALUES(?,?) ON CONFLICT(destination) DO UPDATE SET until_at=MAX(until_at,excluded.until_at)`, row.message.Destination, until.UnixMilli()); err != nil {
			return err
		}
	}
	if reason == "" && result.Reason != "" {
		reason = controlledOfficialReason(result.Reason)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE notification_dispatch SET provider_state=?,next_poll=?,poll_lease_until=0,reason=? WHERE notification_id=?`, status, next, reason, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE notification_dispatch SET http_status=?,api_error_code=? WHERE notification_id=?`, result.HTTPStatus, result.APIErrorCode, id); err != nil {
		return err
	}
	if err := savePlatformCharge(ctx, tx, id, result.PlatformSegments, result.Price, result.PriceUnit); err != nil {
		return err
	}
	return tx.Commit()
}

// Reconcile explicitly consumes the current UTC day's full configured budget.
// Backup history cannot establish other post-backup spending. Old unsent paid
// bodies stay quarantined; a following UTC day permits new admissions only.
func (s *Store) ReconcileOfficialPaidChannel(ctx context.Context, channel, reference string, now time.Time) error {
	if !paidOfficialChannel(channel) || reference == "" || !safePolicyText(reference) || now.IsZero() || now.UnixMilli() <= 0 {
		return errors.New("invalid paid reconciliation")
	}
	release, err := s.beginWrite(ctx, writeCritical)
	if err != nil {
		return err
	}
	defer release()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var enabled, revoked, cost, hold bool
	var consent, optout, clock int64
	var basis, purpose, evidence string
	var messages, segments int
	if err := tx.QueryRowContext(ctx, `SELECT enabled,consented_at,purpose,evidence_ref,basis_id,revoked,cost_confirmed,daily_message_limit,daily_segment_limit,optout_at,restore_hold,clock_highwater FROM official_channel_policy WHERE channel=?`, channel).Scan(&enabled, &consent, &purpose, &evidence, &basis, &revoked, &cost, &messages, &segments, &optout, &hold, &clock); err != nil {
		return err
	}
	if !hold || !enabled || revoked || !cost || consent <= 0 || consent > now.UnixMilli() || basis == "" || purpose == "" || evidence == "" || messages <= 0 || optout > 0 || now.UnixMilli() < clock {
		return errors.New("paid reconciliation requires active consent without optout or clock rollback")
	}
	day := now.UTC().Unix() / 86400
	if _, err := tx.ExecContext(ctx, `INSERT INTO official_budget_usage(channel,utc_day,logical_messages,estimated_segments) VALUES(?,?,?,?) ON CONFLICT(channel,utc_day) DO UPDATE SET logical_messages=MAX(logical_messages,excluded.logical_messages),estimated_segments=MAX(estimated_segments,excluded.estimated_segments)`, channel, day, messages, segments); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE official_channel_policy SET restore_hold=0,reconciliation_ref=?,clock_highwater=MAX(clock_highwater,?),budget_day_highwater=MAX(budget_day_highwater,?) WHERE channel=?`, reference, now.UnixMilli(), day, channel); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE notification_dispatch SET state='blocked',reason='official_restored_body_requires_reconciliation' WHERE notification_id IN (SELECT id FROM notification_outbox WHERE channel=? AND sent_at IS NULL)`, channel); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE notification_outbox SET quarantined_at=?,lease_until=NULL,last_error='official_restored_body_requires_reconciliation' WHERE channel=? AND sent_at IS NULL`, now.UnixMilli(), channel); err != nil {
		return err
	}
	return tx.Commit()
}

// markRestoredOfficialHold modifies only the unpublished private restore
// output. Historical schemas keep their shape for a matching older binary;
// migration14 consumes the fixed component marker before starting dispatch.
func markRestoredOfficialHold(ctx context.Context, path string) error {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, `PRAGMA trusted_schema=OFF`); err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
		return err
	}
	now := time.Now().UTC().UnixMilli()
	if version < 14 {
		if _, err := tx.ExecContext(ctx, `INSERT INTO component_status(name,state,updated_at) VALUES('official_paid_restore_hold','reconciliation_required',?) ON CONFLICT(name) DO UPDATE SET state=excluded.state,updated_at=excluded.updated_at`, now); err != nil {
			return err
		}
	} else {
		if _, err := tx.ExecContext(ctx, `UPDATE official_channel_policy SET restore_hold=1 WHERE channel IN ('twilio_sms','whatsapp_cloud')`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE notification_dispatch SET state=CASE WHEN state='in_flight' THEN 'delivery_unknown' ELSE 'blocked' END,reason='official_restored_body_requires_reconciliation' WHERE state<>'accepted' AND notification_id IN(SELECT id FROM notification_outbox WHERE channel IN ('twilio_sms','whatsapp_cloud') AND sent_at IS NULL)`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE notification_outbox SET isolated_at=COALESCE(isolated_at,?),quarantined_at=COALESCE(quarantined_at,?),lease_until=NULL,last_error='official_restored_body_requires_reconciliation' WHERE channel IN ('twilio_sms','whatsapp_cloud') AND sent_at IS NULL`, now, now); err != nil {
			return err
		}
		// A restored polling receipt may be stale; it is not fresh delivery proof.
		if _, err := tx.ExecContext(ctx, `UPDATE notification_dispatch SET provider_state=CASE WHEN next_poll>0 THEN 'unknown' ELSE provider_state END,next_poll=0,poll_lease_until=0 WHERE notification_id IN(SELECT id FROM notification_outbox WHERE channel='twilio_sms')`); err != nil {
			return err
		}
	}
	return tx.Commit()
}

type OfficialChannelStatus struct {
	Channel           string    `json:"channel"`
	Enabled           bool      `json:"enabled"`
	Revoked           bool      `json:"revoked"`
	OptedOut          bool      `json:"opted_out"`
	RestoreHold       bool      `json:"restore_reconciliation_required"`
	ConsentedAt       time.Time `json:"consented_at_utc,omitzero"`
	ClockHighwater    time.Time `json:"clock_highwater_utc,omitzero"`
	DailyMessageLimit int       `json:"daily_message_limit"`
	DailySegmentLimit int       `json:"daily_segment_limit"`
	MessagesReserved  int       `json:"logical_messages_reserved"`
	SegmentsReserved  int       `json:"estimated_segments_reserved"`
	UnknownDeliveries int       `json:"delivery_unknown"`
	PrunedBudgetDays  int64     `json:"pruned_budget_days"`
}

func (s *Store) OfficialChannelStatus(ctx context.Context, channel string, now time.Time) (OfficialChannelStatus, error) {
	result := OfficialChannelStatus{Channel: channel}
	if !officialChannel(channel) || now.IsZero() {
		return result, errors.New("invalid official channel status")
	}
	var consent, clock, optout int64
	err := s.db.QueryRowContext(ctx, `SELECT p.enabled,p.revoked,p.optout_at,p.restore_hold,p.consented_at,p.clock_highwater,p.daily_message_limit,p.daily_segment_limit,COALESCE(b.logical_messages,0),COALESCE(b.estimated_segments,0),p.pruned_budget_days,(SELECT COUNT(*) FROM notification_dispatch d JOIN notification_outbox o ON o.id=d.notification_id WHERE o.channel=p.channel AND d.state='delivery_unknown') FROM official_channel_policy p LEFT JOIN official_budget_usage b ON b.channel=p.channel AND b.utc_day=? WHERE p.channel=?`, now.UTC().Unix()/86400, channel).Scan(&result.Enabled, &result.Revoked, &optout, &result.RestoreHold, &consent, &clock, &result.DailyMessageLimit, &result.DailySegmentLimit, &result.MessagesReserved, &result.SegmentsReserved, &result.PrunedBudgetDays, &result.UnknownDeliveries)
	result.OptedOut = optout > 0
	if consent > 0 {
		result.ConsentedAt = time.UnixMilli(consent).UTC()
	}
	if clock > 0 {
		result.ClockHighwater = time.UnixMilli(clock).UTC()
	}
	return result, err
}
