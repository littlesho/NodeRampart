// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestOfficialFairHeadroomRetainsOldQueuesAndCountsFrozenBytes(t *testing.T) {
	s := budgetStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	for _, channel := range notificationChannels {
		nativeTestPolicy(t, s, channel, "A", "prefix", true)
		if officialChannel(channel) {
			if err := s.ConfigureOfficialPolicy(ctx, channel, officialTestPolicy(channel, now), now); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Preserve grandfathered legacy occupancy; no migration body is deleted.
	for _, channel := range notificationChannels[:7] {
		if _, err := s.db.Exec("WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<1250) INSERT INTO notification_outbox(id,dedupe_key,channel,destination,body,created_at,next_attempt,expires_at) SELECT ?||i,?||i,?,?,'retained',?,?,? FROM n", channel, channel, channel, nativeTestTarget(channel, "A"), now.UnixMilli(), now.UnixMilli(), now.Add(OutboxTTL).UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec("WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<993) INSERT INTO notification_outbox(id,dedupe_key,channel,destination,body,created_at,next_attempt,expires_at) SELECT 'g'||i,'g'||i,'google_chat',?,'retained',?,?,? FROM n", nativeTestTarget("google_chat", "A"), now.UnixMilli(), now.UnixMilli(), now.Add(OutboxTTL).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.Enqueue(ctx, nativeTestMessage("google_chat", "A", "last_reserved_boundary")); err != nil || !ok {
		t.Fatal("reserved boundary denied early", ok, err)
	}
	if ok, err := s.Enqueue(ctx, nativeTestMessage("google_chat", "A", "overflow")); ok || !errors.Is(err, ErrOutboxFull) {
		t.Fatal("failed targets consumed other channels headroom", ok, err)
	}
	for _, channel := range officialTestChannels {
		m := officialTestMessage(channel, "A", "reserved", now)
		if ok, err := s.Enqueue(ctx, m); err != nil || !ok {
			t.Fatal("new channel starved", channel, ok, err)
		}
	}
	status, err := s.QueueStatus(ctx, now)
	if err != nil || status.Pending != 9748 || status.Rejected != 1 {
		t.Fatal("fair admission not observable", status, err)
	}
	for _, entry := range status.Channels {
		if officialChannel(entry.Channel) {
			m := officialTestMessage(entry.Channel, "A", "reserved", now)
			if entry.PendingBytes != int64(len(m.Body)+len(m.FrozenPayload)) {
				t.Fatal("frozen bytes excluded", entry)
			}
		}
	}
	if _, ok, err := s.ClaimNotification(ctx, "telegram1", now); err != nil || !ok {
		t.Fatal("old queue stranded", ok, err)
	}
}

func TestOfficialLastRecheckRejectsRevokeTypesAndTargetChange(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	for _, change := range []string{"revoked", "types", "disabled", "destination"} {
		t.Run(change, func(t *testing.T) {
			s := budgetStore(t)
			dest := officialTestConfigure(t, s, "line", "A", now)
			m := officialTestMessage("line", "A", change, now)
			officialTestBegin(t, s, m, now)
			if allowed, err := s.NotificationDeliveryAllowed(ctx, m.ID, dest); err != nil || !allowed {
				t.Fatal("valid intent rejected", allowed, err)
			}
			p := officialTestPolicy("line", now)
			switch change {
			case "revoked":
				p.Revoked = true
			case "types":
				p.NotificationTypes = []string{"daily"}
			case "disabled":
				p.Enabled = false
			case "destination":
				if err := s.ConfigureNotificationTarget(ctx, "line", nativeTestTarget("line", "B"), "prefix", true, now); err != nil {
					t.Fatal(err)
				}
			}
			if change != "destination" {
				if err := s.ConfigureOfficialPolicy(ctx, "line", p, now); err != nil {
					t.Fatal(err)
				}
			}
			if allowed, err := s.NotificationDeliveryAllowed(ctx, m.ID, dest); err != nil || allowed {
				t.Fatal("changed policy passed final gate", allowed, err)
			}
		})
	}
}

func TestOfficialLateReceiptIsBoundToOriginalAttemptAndTarget(t *testing.T) {
	s := budgetStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	original := officialTestConfigure(t, s, "qqbot", "A", now)
	message := officialTestMessage("qqbot", "A", "late_receipt", now)
	officialTestBegin(t, s, message, now)
	next := nativeTestTarget("qqbot", "B")
	if err := s.ConfigureNotificationTarget(ctx, "qqbot", next, "prefix", true, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := s.ExpireNotifications(ctx, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if state := officialStoredState(t, s, message.ID); state != "delivery_unknown" {
		t.Fatal(state)
	}
	if err := s.ResolveOfficialDispatch(ctx, message.ID, now.Add(3*time.Second), OfficialDispatchResolution{DispatchAttempt: 1, Outcome: "accepted", ProviderID: "qq_receipt_synthetic"}); err != nil {
		t.Fatal(err)
	}
	var destination string
	if err := s.db.QueryRow("SELECT destination FROM notification_outbox WHERE id=?", message.ID).Scan(&destination); err != nil || destination != original {
		t.Fatal("late receipt redirected historical identity", destination, err)
	}
	if pending, err := s.PendingDestination(ctx, now.Add(time.Minute), 20, next); err != nil || len(pending) != 0 {
		t.Fatal("old message was forwarded", pending, err)
	}
}

func TestOfficialNumericDiagnosticsAndWholeDestinationSuspension(t *testing.T) {
	for _, outcome := range []string{"unknown", "not_accepted"} {
		t.Run(outcome, func(t *testing.T) {
			s := budgetStore(t)
			ctx := context.Background()
			now := time.Now().UTC().Truncate(time.Millisecond)
			dest := officialTestConfigure(t, s, "line", "A", now)
			message := officialTestMessage("line", "A", outcome, now)
			officialTestBegin(t, s, message, now)
			if err := s.ResolveOfficialDispatch(ctx, message.ID, now, OfficialDispatchResolution{DispatchAttempt: 1, Outcome: outcome, Permanent: outcome == "not_accepted", Suspend: true, HTTPStatus: 403, APIErrorCode: 11200, Reason: "permission_rejected"}); err != nil {
				t.Fatal(err)
			}
			next := officialTestMessage("line", "A", "next", now)
			if _, err := s.Enqueue(ctx, next); err != nil {
				t.Fatal(err)
			}
			if pending, err := s.PendingDestination(ctx, now.Add(time.Hour), 20, dest); err != nil || len(pending) != 0 {
				t.Fatal("destination credential fault failed to pause siblings", pending, err)
			}
			infos, err := s.Notifications(ctx, "", 20)
			if err != nil {
				t.Fatal(err)
			}
			for _, info := range infos {
				if info.ID == message.ID && (info.HTTPStatus != 403 || info.APIErrorCode != 11200) {
					t.Fatal("numeric response context lost", info)
				}
			}
			if err := s.ResumeDestination(ctx, dest); err != nil {
				t.Fatal(err)
			}
			if _, ok, err := s.ClaimNotification(ctx, message.ID, now.Add(time.Hour)); err != nil || ok {
				t.Fatal("resume reopened unresolved or permanent old POST", ok, err)
			}
		})
	}
}

func TestTwilioDelayedAcceptanceKeepsFirstIntentPollDeadline(t *testing.T) {
	s := budgetStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	dest := officialTestConfigure(t, s, "twilio_sms", "A", now)
	message := officialTestMessage("twilio_sms", "A", "delayed_acceptance", now)
	begun := officialTestBegin(t, s, message, now)
	accepted := now.Add(15 * time.Second)
	if err := s.ResolveOfficialDispatch(ctx, message.ID, accepted, OfficialDispatchResolution{DispatchAttempt: begun.DispatchAttempt, Outcome: "accepted", ProviderID: "SM" + strings.Repeat("2", 32), PollStatus: "queued", HTTPStatus: 201}); err != nil {
		t.Fatal(err)
	}
	poll, ok, err := s.ClaimOfficialPoll(ctx, message.ID, dest, accepted.Add(time.Minute))
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if !poll.PollDeadline.Equal(begun.FirstAttemptAt.Add(OfficialPollDeadline)) || poll.PollDeadline.After(poll.FirstAttemptAt.Add(24*time.Hour)) || poll.PollAttempts != 1 || poll.DispatchState != "accepted" || poll.FrozenPayload != begun.FrozenPayload || poll.ProviderID != "SM"+strings.Repeat("2", 32) {
		t.Fatal("delayed receipt broke persisted sender polling contract", poll)
	}
	if allowed, err := s.OfficialPollAllowed(ctx, message.ID, dest, poll.PollAttempts, accepted.Add(time.Minute)); err != nil || !allowed {
		t.Fatal("matching accepted SID GET was denied", allowed, err)
	}
}

func TestOfficialInFlightPresentationExcludesDispatchCredentials(t *testing.T) {
	s := budgetStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	officialTestConfigure(t, s, "line", "A", now)
	message := officialTestMessage("line", "A", "observable_intent", now)
	begun := officialTestBegin(t, s, message, now)
	infos, err := s.Notifications(ctx, "", 10)
	if err != nil || len(infos) != 1 || infos[0].State != "in_flight" || infos[0].DispatchState != "in_flight" || !infos[0].FirstAttemptAt.Equal(begun.FirstAttemptAt) {
		t.Fatal("durable network intent was indistinguishable from queued admission", infos, err)
	}
	encoded, err := json.Marshal(infos)
	if err != nil || strings.Contains(string(encoded), begun.RetryKey) || strings.Contains(string(encoded), message.FrozenPayload) || strings.Contains(string(encoded), message.Body) {
		t.Fatal("intent presentation exposed internal dispatch contents", err)
	}
	legacy := nativeTestMessage("telegram", "A", "legacy_pending")
	nativeTestPolicy(t, s, "telegram", "A", "prefix", true)
	if _, err := s.Enqueue(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	infos, err = s.Notifications(ctx, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, info := range infos {
		if info.ID == legacy.ID && (info.State != "pending" || info.DispatchState != "" || !info.FirstAttemptAt.IsZero()) {
			t.Fatal("legacy queued presentation changed", info)
		}
	}
}

func TestOfficialAcceptanceKeepsOnlyControlledProviderObservation(t *testing.T) {
	for _, channel := range []string{"qqbot", "line", "whatsapp_cloud"} {
		for _, observation := range []string{"api_accepted", "accepted", "held_for_quality_assessment", "delivered", "read", "syntheticlowercasesecrettoken"} {
			t.Run(channel+"/"+observation, func(t *testing.T) {
				s := budgetStore(t)
				ctx := context.Background()
				now := time.Now().UTC().Truncate(time.Millisecond)
				dest := officialTestConfigure(t, s, channel, "A", now)
				message := officialTestMessage(channel, "A", "receipt_observation", now)
				officialTestBegin(t, s, message, now)
				id := "synthetic_receipt"
				if channel == "whatsapp_cloud" {
					id = "wamid.synthetic_receipt"
				}
				if err := s.ResolveOfficialDispatch(ctx, message.ID, now, OfficialDispatchResolution{DispatchAttempt: 1, Outcome: "accepted", ProviderID: id, PollStatus: observation}); err != nil {
					t.Fatal(err)
				}
				want := "accepted"
				if observation == "api_accepted" || observation == "accepted" || channel == "whatsapp_cloud" && observation == "held_for_quality_assessment" {
					want = observation
				}
				infos, err := s.Notifications(ctx, "", 10)
				if err != nil || len(infos) != 1 || infos[0].State != "accepted" || infos[0].ProviderState != want {
					t.Fatal("API receipt was lost or changed into an unobserved delivery/read claim", infos, err)
				}
				data, _ := json.Marshal(infos)
				if strings.Contains(string(data), id) || strings.Contains(string(data), "syntheticlowercasesecrettoken") {
					t.Fatal("untrusted receipt observation leaked")
				}
				if pending, err := s.PendingOfficialPolls(ctx, dest, now.Add(time.Minute), 10); err != nil || len(pending) != 0 {
					t.Fatal("non-Twilio acceptance invented a status GET", pending, err)
				}
				if err := s.RetryNotification(ctx, message.ID, now.Add(time.Minute)); err == nil {
					t.Fatal("accepted or quality-held response reopened POST")
				}
			})
		}
	}
}

func TestLINEUnknownRetryAfterPausesTargetAcrossRestart(t *testing.T) {
	for _, wait := range []time.Duration{91*time.Second + time.Microsecond, 24 * time.Hour, OutboxTTL + time.Second} {
		t.Run(wait.String(), func(t *testing.T) {
			ctx := context.Background()
			now := time.Now().UTC().Truncate(time.Millisecond)
			path := filepath.Join(t.TempDir(), "line_cooldown.db")
			s, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			dest := officialTestConfigure(t, s, "line", "A", now)
			message := officialTestMessage("line", "A", "uncertain", now)
			begun := officialTestBegin(t, s, message, now)
			if err := s.ResolveOfficialDispatch(ctx, message.ID, now, OfficialDispatchResolution{DispatchAttempt: begun.DispatchAttempt, Outcome: "unknown", HTTPStatus: 503, RetryAfter: wait, Reason: "unconfirmed_provider_failure"}); err != nil {
				t.Fatal(err)
			}
			sibling := officialTestMessage("line", "A", "other_new_message", now)
			if _, err := s.Enqueue(ctx, sibling); err != nil {
				t.Fatal(err)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			s, err = Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			var until int64
			if err := s.db.QueryRow("SELECT until_at FROM notification_cooldowns WHERE destination=?", dest).Scan(&until); err != nil {
				t.Fatal(err)
			}
			minimum := now.Add(wait)
			if time.UnixMilli(until).Before(minimum) {
				t.Fatal("Retry-After was shortened", until, minimum)
			}
			if pending, err := s.PendingDestination(ctx, now.Add(time.Minute), 20, dest); err != nil || len(pending) != 0 {
				t.Fatal("another message bypassed the target-wide wait after restart", pending, err)
			}
			if _, ok, err := s.ClaimNotification(ctx, sibling.ID, now.Add(time.Minute)); err != nil || ok {
				t.Fatal("sibling POST claim bypassed provider waiting advice", ok, err)
			}
			if wait < OfficialLINEWindow {
				at := now.Add(wait).Truncate(time.Millisecond).Add(time.Millisecond)
				if _, ok, err := s.ClaimNotification(ctx, message.ID, at); err != nil || !ok {
					t.Fatal("same-key eligible retry was lost", ok, err)
				}
				retry, allowed, err := s.BeginOfficialDispatch(ctx, message.ID, dest, at, OfficialDispatchPolicy{})
				if err != nil || !allowed || retry.RetryKey != begun.RetryKey || retry.FrozenPayload != begun.FrozenPayload || !retry.FirstAttemptAt.Equal(begun.FirstAttemptAt) {
					t.Fatal("provider wait changed the idempotent request", retry, allowed, err)
				}
			} else {
				if got := officialStoredState(t, s, message.ID); got != "delivery_unknown" {
					t.Fatal("over-window advice resurrected an uncertain old request", got)
				}
				if wait > OutboxTTL && time.UnixMilli(until).Year() != 9999 {
					t.Fatal("unschedulable advice was shortened instead of requiring resume", until)
				}
			}
		})
	}
}

func TestTwilioCanceledReceiptIsTerminalForPOSTAndGET(t *testing.T) {
	for _, phase := range []string{"post", "get"} {
		t.Run(phase, func(t *testing.T) {
			s := budgetStore(t)
			ctx := context.Background()
			now := time.Now().UTC().Truncate(time.Millisecond)
			dest := officialTestConfigure(t, s, "twilio_sms", "A", now)
			message := officialTestMessage("twilio_sms", "A", "canceled_"+phase, now)
			officialTestBegin(t, s, message, now)
			status := "canceled"
			if phase == "get" {
				status = "queued"
			}
			if err := s.ResolveOfficialDispatch(ctx, message.ID, now, OfficialDispatchResolution{DispatchAttempt: 1, Outcome: "accepted", ProviderID: "SM" + strings.Repeat("3", 32), PollStatus: status}); err != nil {
				t.Fatal(err)
			}
			if phase == "get" {
				poll, ok, err := s.ClaimOfficialPoll(ctx, message.ID, dest, now.Add(time.Minute))
				if err != nil || !ok {
					t.Fatal(ok, err)
				}
				if err := s.ResolveOfficialPoll(ctx, message.ID, now.Add(time.Minute), OfficialPollResolution{PollAttempt: poll.PollAttempts, Status: "canceled", Reason: "delivery_failed"}); err != nil {
					t.Fatal("legitimate terminal receipt failed to resolve", err)
				}
			}
			infos, err := s.Notifications(ctx, "", 10)
			if err != nil || len(infos) != 1 || infos[0].State != "accepted" || infos[0].ProviderState != "canceled" {
				t.Fatal("canceled provider delivery was changed into queued or acceptance lost", infos, err)
			}
			if pending, err := s.PendingOfficialPolls(ctx, dest, now.Add(2*time.Minute), 10); err != nil || len(pending) != 0 {
				t.Fatal("canceled delivery remained pollable", pending, err)
			}
			if err := s.RetryNotification(ctx, message.ID, now.Add(2*time.Minute)); err == nil {
				t.Fatal("canceled asynchronous result reopened POST")
			}
		})
	}
}

func TestOfficialReasonCategoriesPreserveOnlyControlledVocabulary(t *testing.T) {
	for _, reason := range []string{"credential_or_permission_rejected", "monthly_quota_exhausted", "token_wait_canceled", "poll_receipt_invalid", "durable_intent_required", "template_parameter_count_invalid", "template_missing_or_unapproved", "template_text_too_long", "template_parameter_format_invalid", "template_policy_rejected", "official_policy_changed"} {
		if got := controlledOfficialReason(reason); got != reason {
			t.Fatalf("controlled category lost: %s became %s", reason, got)
		}
	}
	for _, untrusted := range []string{"https://secret.invalid/synthetic", "syntheticlowercasesecrettoken", "template_policy_rejected\nsynthetic"} {
		if got := controlledOfficialReason(untrusted); got != "official_delivery_error" {
			t.Fatal("untrusted response text became a category", got)
		}
	}
}

func TestOfficialUnknownRetainsControlledCauseWithoutGrantingPOSTRetry(t *testing.T) {
	for _, reason := range []string{"invalid_response", "credential_or_permission_rejected", "template_parameter_count_invalid", "https://secret.invalid/synthetic", "syntheticlowercasesecrettoken"} {
		t.Run(strings.ReplaceAll(reason, "/", "_"), func(t *testing.T) {
			s := budgetStore(t)
			ctx := context.Background()
			now := time.Now().UTC().Truncate(time.Millisecond)
			officialTestConfigure(t, s, "qqbot", "A", now)
			message := officialTestMessage("qqbot", "A", "unknown_cause", now)
			officialTestBegin(t, s, message, now)
			if err := s.ResolveOfficialDispatch(ctx, message.ID, now, OfficialDispatchResolution{DispatchAttempt: 1, Outcome: "unknown", Reason: reason}); err != nil {
				t.Fatal(err)
			}
			want := reason
			if controlledOfficialReason(reason) == "official_delivery_error" {
				want = "official_delivery_unknown"
			}
			infos, err := s.Notifications(ctx, "", 10)
			if err != nil || len(infos) != 1 || infos[0].State != "delivery_unknown" || infos[0].LastError != want {
				t.Fatal("uncertain request cause was lost or untrusted text persisted", infos, err)
			}
			if err := s.RetryNotification(ctx, message.ID, now.Add(time.Minute)); err == nil {
				t.Fatal("controlled error category reopened an uncertain POST")
			}
		})
	}
}

func TestTwilioPollCredentialFailurePausesPOSTAndGETWithoutErasingAcceptance(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	path := filepath.Join(t.TempDir(), "poll_auth_pause.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	dest := officialTestConfigure(t, s, "twilio_sms", "A", now)
	message := officialTestMessage("twilio_sms", "A", "accepted_before_auth_failure", now)
	officialTestBegin(t, s, message, now)
	sid := "SM" + strings.Repeat("4", 32)
	if err := s.ResolveOfficialDispatch(ctx, message.ID, now, OfficialDispatchResolution{DispatchAttempt: 1, Outcome: "accepted", ProviderID: sid, PollStatus: "queued"}); err != nil {
		t.Fatal(err)
	}
	at := now.Add(time.Minute)
	poll, ok, err := s.ClaimOfficialPoll(ctx, message.ID, dest, at)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if err := s.ResolveOfficialPoll(ctx, message.ID, at, OfficialPollResolution{PollAttempt: poll.PollAttempts, Suspend: true, HTTPStatus: 401, APIErrorCode: 20003, Reason: "credential_or_permission_rejected"}); err != nil {
		t.Fatal(err)
	}
	sibling := officialTestMessage("twilio_sms", "A", "new_post", at)
	if _, err := s.Enqueue(ctx, sibling); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	check := at.Add(time.Minute)
	if pending, err := s.PendingOfficialPolls(ctx, dest, check, 10); err != nil || len(pending) != 0 {
		t.Fatal("invalid authentication continued automatic GET after restart", pending, err)
	}
	if _, ok, err := s.ClaimOfficialPoll(ctx, message.ID, dest, check); err != nil || ok {
		t.Fatal("claim bypassed persistent authentication pause", ok, err)
	}
	if allowed, err := s.OfficialPollAllowed(ctx, message.ID, dest, poll.PollAttempts, check); err != nil || allowed {
		t.Fatal("last GET authorization ignored cooldown", allowed, err)
	}
	if _, ok, err := s.ClaimNotification(ctx, sibling.ID, check); err != nil || ok {
		t.Fatal("GET authentication rejection allowed a new POST", ok, err)
	}
	infos, err := s.Notifications(ctx, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, info := range infos {
		if info.ID == message.ID && (info.State != "accepted" || info.DispatchState != "accepted" || info.ProviderState != "unknown" || info.HTTPStatus != 401 || info.APIErrorCode != 20003) {
			t.Fatal("authentication pause erased or overstated prior receipt", info)
		}
	}
	var storedSID string
	var dispatchAttempt int
	if err := s.db.QueryRow("SELECT provider_id,dispatch_attempt FROM notification_dispatch WHERE notification_id=?", message.ID).Scan(&storedSID, &dispatchAttempt); err != nil || storedSID != sid || dispatchAttempt != 1 {
		t.Fatal("GET failure changed durable POST identity", storedSID, dispatchAttempt, err)
	}
	if err := s.ResumeDestination(ctx, dest); err != nil {
		t.Fatal(err)
	}
	if pending, err := s.PendingOfficialPolls(ctx, dest, check, 10); err != nil || len(pending) != 1 {
		t.Fatal("explicit resume failed to retain eligible GET intent", pending, err)
	}
	if err := s.RetryNotification(ctx, message.ID, check); err == nil {
		t.Fatal("credential resume reopened accepted POST")
	}
}

func TestOfficialBoundedLedgerAndZeroLimitsDoNotReset(t *testing.T) {
	s := budgetStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	officialTestConfigure(t, s, "whatsapp_cloud", "A", now)
	day := now.Unix() / 86400
	if _, err := s.db.Exec("WITH RECURSIVE n(i) AS (SELECT 0 UNION ALL SELECT i+1 FROM n WHERE i<405) INSERT INTO official_budget_usage(channel,utc_day,logical_messages,estimated_segments) SELECT 'whatsapp_cloud',?-i,1,0 FROM n", day); err != nil {
		t.Fatal(err)
	}
	m := officialTestMessage("whatsapp_cloud", "A", "ledger_bounded", now)
	officialTestBegin(t, s, m, now)
	var rows int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM official_budget_usage WHERE channel='whatsapp_cloud'").Scan(&rows); err != nil || rows != 400 {
		t.Fatal("unbounded ledger", rows, err)
	}
	status, err := s.OfficialChannelStatus(ctx, "whatsapp_cloud", now)
	if err != nil || status.PrunedBudgetDays != 6 || status.MessagesReserved != 2 {
		t.Fatal("budget pruning invisible", status, err)
	}
	p := officialTestPolicy("whatsapp_cloud", now)
	p.DailyMessageLimit = 0
	if err := s.ConfigureOfficialPolicy(ctx, "whatsapp_cloud", p, now); err == nil {
		t.Fatal("zero unlimited")
	}
	p.Enabled = false
	if err := s.ConfigureOfficialPolicy(ctx, "whatsapp_cloud", p, now); err != nil {
		t.Fatal(err)
	}
	status, err = s.OfficialChannelStatus(ctx, "whatsapp_cloud", now)
	if err != nil || status.MessagesReserved != 2 {
		t.Fatal("disable reset budget", status, err)
	}
}

var officialTestChannels = []string{"qqbot", "line", "twilio_sms", "whatsapp_cloud"}

func officialTestPolicy(channel string, now time.Time) OfficialChannelPolicy {
	p := OfficialChannelPolicy{Enabled: true}
	{
		p.ConsentedAt = now.Add(-time.Minute)
		p.Purpose = "synthetic observer alerts"
		p.EvidenceRef = "local:synthetic-consent-record"
		p.BasisID = strings.Repeat("a", 32)
		p.NotificationTypes = []string{"event", "daily", "test"}
		p.CostConfirmed = paidOfficialChannel(channel)
		p.DailyMessageLimit = 20
		if channel == "twilio_sms" {
			p.DailySegmentLimit = 40
			p.MaxSegments = 2
		}
	}
	return p
}

func officialTestConfigure(t *testing.T, s *Store, channel, identity string, now time.Time) string {
	t.Helper()
	destination := nativeTestTarget(channel, identity)
	if err := s.ConfigureNotificationTarget(context.Background(), channel, destination, "prefix", true, now); err != nil {
		t.Fatal(err)
	}
	if err := s.ConfigureOfficialPolicy(context.Background(), channel, officialTestPolicy(channel, now), now); err != nil {
		t.Fatal(err)
	}
	return destination
}

func officialTestMessage(channel, identity, id string, now time.Time) OutboxMessage {
	m := nativeTestMessage(channel, identity, id)
	m.NextAttempt = now
	m.LogicalKind = "event"
	m.FrozenPayload = `{"body":"合成摘要","snapshot_fingerprint":"synthetic"}`
	if channel == "twilio_sms" {
		m.EstimatedSegments = 2
		m.Encoding = "ucs2"
	}
	return m
}

func officialTestBegin(t *testing.T, s *Store, message OutboxMessage, now time.Time) OutboxMessage {
	t.Helper()
	ctx := context.Background()
	if ok, err := s.Enqueue(ctx, message); err != nil || !ok {
		t.Fatal("admission", ok, err)
	}
	pending, err := s.PendingDestination(ctx, now, 20, message.Destination)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range pending {
		if item.ID == message.ID {
			found = true
			if item.FrozenPayload != message.FrozenPayload || item.LogicalKind != message.LogicalKind || item.Language != message.Language || item.Timezone != message.Timezone || item.EstimatedSegments != message.EstimatedSegments || item.Encoding != message.Encoding || item.ExpiresAt.IsZero() {
				t.Fatal("pending lost immutable official context", item)
			}
		}
	}
	if !found {
		t.Fatal("new admitted official message missing from pending")
	}
	claimed, ok, err := s.ClaimNotification(ctx, message.ID, now)
	if err != nil || !ok {
		t.Fatal("claim", ok, err)
	}
	if claimed.FrozenPayload != message.FrozenPayload {
		t.Fatal("claim lost frozen request")
	}
	begun, ok, err := s.BeginOfficialDispatch(ctx, message.ID, message.Destination, now, OfficialDispatchPolicy{})
	if err != nil || !ok {
		t.Fatal("begin", ok, err)
	}
	return begun
}

func officialStoredState(t *testing.T, s *Store, id string) string {
	t.Helper()
	var state string
	if err := s.db.QueryRow(`SELECT state FROM notification_dispatch WHERE notification_id=?`, id).Scan(&state); err != nil {
		t.Fatal(err)
	}
	return state
}

func TestOfficialIntentPrecedesRequestAndLINEReusesFrozenUUID(t *testing.T) {
	s := budgetStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	dest := officialTestConfigure(t, s, "line", "A", now)
	m := officialTestMessage("line", "A", "line_fixed", now)
	begun := officialTestBegin(t, s, m, now)
	if len(begun.RetryKey) != 36 || begun.FirstAttemptAt != now || begun.DispatchAttempt != 1 || begun.DispatchState != "in_flight" {
		t.Fatal("missing durable LINE first-attempt contract", begun)
	}
	var key string
	var first, uncertain int64
	if err := s.db.QueryRow(`SELECT retry_key,first_attempt_at,uncertain_attempt FROM notification_dispatch WHERE notification_id=?`, m.ID).Scan(&key, &first, &uncertain); err != nil || key != begun.RetryKey || first != now.UnixMilli() || uncertain != 1 {
		t.Fatal("network could precede committed intent", err)
	}
	if _, ok, err := s.BeginOfficialDispatch(ctx, m.ID, dest, now, OfficialDispatchPolicy{}); err != nil || ok {
		t.Fatal("same committed intent authorized twice", ok, err)
	}
	if err := s.ResolveOfficialDispatch(ctx, m.ID, now, OfficialDispatchResolution{DispatchAttempt: 1, Outcome: "not_accepted", RetryAfter: 90 * time.Second, Reason: "line_rate_limited"}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.ClaimNotification(ctx, m.ID, now.Add(89*time.Second)); err != nil || ok {
		t.Fatal("ignored durable retry-after", ok, err)
	}
	if _, ok, err := s.ClaimNotification(ctx, m.ID, now.Add(91*time.Second)); err != nil || !ok {
		t.Fatal(ok, err)
	}
	retry, ok, err := s.BeginOfficialDispatch(ctx, m.ID, dest, now.Add(91*time.Second), OfficialDispatchPolicy{})
	if err != nil || !ok || retry.RetryKey != key || retry.FirstAttemptAt != begun.FirstAttemptAt || retry.FrozenPayload != m.FrozenPayload || retry.DispatchAttempt != 2 {
		t.Fatal("LINE retry changed request", retry, ok, err)
	}
	if err := s.ResolveOfficialDispatch(ctx, m.ID, now.Add(91*time.Second), OfficialDispatchResolution{DispatchAttempt: 1, Outcome: "accepted", ProviderID: key}); err == nil {
		t.Fatal("stale resolution accepted")
	}
	if err := s.ResolveOfficialDispatch(ctx, m.ID, now.Add(91*time.Second), OfficialDispatchResolution{DispatchAttempt: 2, Outcome: "accepted", ProviderID: key}); err != nil {
		t.Fatal(err)
	}
	infos, err := s.Notifications(ctx, "", 10)
	if err != nil || len(infos) != 1 || infos[0].State != "accepted" {
		t.Fatal("acceptance was misreported", infos, err)
	}
	encoded, _ := json.Marshal(infos)
	if strings.Contains(string(encoded), key) || strings.Contains(string(encoded), m.FrozenPayload) {
		t.Fatal("presentation leaked internal request/receipt")
	}
}

func TestOfficialInterruptedIntentRestartMatrix(t *testing.T) {
	for _, channel := range officialTestChannels {
		t.Run(channel, func(t *testing.T) {
			ctx := context.Background()
			now := time.Now().UTC().Truncate(time.Millisecond)
			path := filepath.Join(t.TempDir(), "restart.db")
			s, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			dest := officialTestConfigure(t, s, channel, "A", now)
			m := officialTestMessage(channel, "A", "interrupted", now)
			begun := officialTestBegin(t, s, m, now)
			if err := s.ReleaseNotificationClaim(ctx, m.ID); err != nil {
				t.Fatal(err)
			}
			var lease int64
			if err := s.db.QueryRow(`SELECT lease_until FROM notification_outbox WHERE id=?`, m.ID).Scan(&lease); err != nil || lease <= now.UnixMilli() {
				t.Fatal("cancellation prematurely released intent lease", lease, err)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			s, err = Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			pending, err := s.PendingDestination(ctx, now.Add(3*time.Minute), 20, dest)
			if err != nil {
				t.Fatal(err)
			}
			if channel == "line" {
				if len(pending) != 1 {
					t.Fatal("LINE bounded same-key retry lost", pending)
				}
				if _, ok, err := s.ClaimNotification(ctx, m.ID, now.Add(3*time.Minute)); err != nil || !ok {
					t.Fatal(ok, err)
				}
				retry, ok, err := s.BeginOfficialDispatch(ctx, m.ID, dest, now.Add(3*time.Minute), OfficialDispatchPolicy{})
				if err != nil || !ok || retry.RetryKey != begun.RetryKey {
					t.Fatal("LINE restart changed key", retry, ok, err)
				}
				if err := s.ExpireNotifications(ctx, now.Add(OfficialLINEWindow+time.Minute)); err != nil {
					t.Fatal(err)
				}
				if got := officialStoredState(t, s, m.ID); got != "delivery_unknown" {
					t.Fatal("expired uncertain LINE intent became retryable", got)
				}
			} else {
				if len(pending) != 0 || officialStoredState(t, s, m.ID) != "delivery_unknown" {
					t.Fatal("non-idempotent restart authorized duplicate", pending)
				}
				if err := s.RetryNotification(ctx, m.ID, now.Add(4*time.Minute)); err == nil {
					t.Fatal("manual retry bypassed delivery_unknown")
				}
				if err := s.ResumeDestination(ctx, dest); err != nil {
					t.Fatal(err)
				}
				if _, ok, err := s.ClaimNotification(ctx, m.ID, now.Add(4*time.Minute)); err != nil || ok {
					t.Fatal("resume revived unknown intent", ok, err)
				}
			}
		})
	}
}

func TestLINEUnknownWaitWindowAndBackwardClockFailClosed(t *testing.T) {
	for _, scenario := range []string{"long_wait", "clock_backwards", "expiry"} {
		t.Run(scenario, func(t *testing.T) {
			s := budgetStore(t)
			ctx := context.Background()
			now := time.Now().UTC().Truncate(time.Millisecond)
			dest := officialTestConfigure(t, s, "line", "A", now)
			m := officialTestMessage("line", "A", scenario, now)
			officialTestBegin(t, s, m, now)
			if scenario == "long_wait" {
				if err := s.ResolveOfficialDispatch(ctx, m.ID, now, OfficialDispatchResolution{DispatchAttempt: 1, Outcome: "unknown", RetryAfter: 24 * time.Hour, Reason: "line_transport_unknown"}); err != nil {
					t.Fatal(err)
				}
			} else {
				at := now
				if scenario == "clock_backwards" {
					at = now.Add(-time.Second)
				} else {
					at = now.Add(OfficialLINEWindow)
				}
				if err := s.ResolveOfficialDispatch(ctx, m.ID, at, OfficialDispatchResolution{DispatchAttempt: 1, Outcome: "unknown"}); err != nil {
					t.Fatal(err)
				}
			}
			if got := officialStoredState(t, s, m.ID); got != "delivery_unknown" {
				t.Fatal("uncertain LINE request was reopened", got)
			}
			if pending, err := s.PendingDestination(ctx, now.Add(time.Hour), 20, dest); err != nil || len(pending) != 0 {
				t.Fatal(pending, err)
			}
		})
	}
}

func TestPaidBudgetUnknownRotationUTCAndTransactionalFailure(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	for _, channel := range []string{"twilio_sms", "whatsapp_cloud"} {
		t.Run(channel, func(t *testing.T) {
			s := budgetStore(t)
			officialTestConfigure(t, s, channel, "A", now)
			policy := officialTestPolicy(channel, now)
			policy.DailyMessageLimit = 1
			if err := s.ConfigureOfficialPolicy(ctx, channel, policy, now); err != nil {
				t.Fatal(err)
			}
			first := officialTestMessage(channel, "A", "one", now)
			officialTestBegin(t, s, first, now)
			if err := s.ResolveOfficialDispatch(ctx, first.ID, now, OfficialDispatchResolution{DispatchAttempt: 1, Outcome: "unknown", Reason: "transport_unknown"}); err != nil {
				t.Fatal(err)
			}
			if err := s.ConfigureNotificationTarget(ctx, channel, nativeTestTarget(channel, "B"), "prefix", true, now.Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			second := officialTestMessage(channel, "B", "two", now.Add(time.Second))
			if ok, err := s.Enqueue(ctx, second); err != nil || !ok {
				t.Fatal(ok, err)
			}
			if _, ok, err := s.ClaimNotification(ctx, second.ID, now.Add(time.Second)); err != nil || !ok {
				t.Fatal(ok, err)
			}
			if _, ok, err := s.BeginOfficialDispatch(ctx, second.ID, second.Destination, now.Add(time.Second), OfficialDispatchPolicy{}); err != nil || ok {
				t.Fatal("rotation reset paid budget", ok, err)
			}
			status, err := s.OfficialChannelStatus(ctx, channel, now)
			if err != nil || status.MessagesReserved != 1 || status.UnknownDeliveries != 1 {
				t.Fatal("unknown reservation refunded", status, err)
			}
			third := officialTestMessage(channel, "B", "tomorrow", now.Add(24*time.Hour))
			if ok, err := s.Enqueue(ctx, third); err != nil || !ok {
				t.Fatal(ok, err)
			}
			if _, ok, err := s.ClaimNotification(ctx, third.ID, now.Add(24*time.Hour)); err != nil || !ok {
				t.Fatal(ok, err)
			}
			if _, ok, err := s.BeginOfficialDispatch(ctx, third.ID, third.Destination, now.Add(24*time.Hour), OfficialDispatchPolicy{}); err != nil || !ok {
				t.Fatal("new UTC day denied", ok, err)
			}
			fourth := officialTestMessage(channel, "B", "clockback", now.Add(2*time.Second))
			if ok, err := s.Enqueue(ctx, fourth); err != nil || !ok {
				t.Fatal(ok, err)
			}
			if _, ok, err := s.ClaimNotification(ctx, fourth.ID, now.Add(2*time.Second)); err != nil || !ok {
				t.Fatal(ok, err)
			}
			if _, ok, err := s.BeginOfficialDispatch(ctx, fourth.ID, fourth.Destination, now.Add(2*time.Second), OfficialDispatchPolicy{}); err != nil || ok {
				t.Fatal("backward day reopened paid budget", ok, err)
			}
		})
	}
	s := budgetStore(t)
	dest := officialTestConfigure(t, s, "twilio_sms", "A", now)
	m := officialTestMessage("twilio_sms", "A", "failedcommit", now)
	if _, err := s.Enqueue(ctx, m); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.ClaimNotification(ctx, m.ID, now); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER fail_dispatch BEFORE UPDATE ON notification_dispatch WHEN NEW.state='in_flight' BEGIN SELECT RAISE(ABORT,'synthetic transaction failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.BeginOfficialDispatch(ctx, m.ID, dest, now, OfficialDispatchPolicy{MinimumInterval: time.Second}); err == nil || ok {
		t.Fatal("failed write authorized external request", ok, err)
	}
	var ledger, cooldowns int
	if err := s.db.QueryRow(`SELECT (SELECT COUNT(*) FROM official_budget_usage),(SELECT COUNT(*) FROM notification_cooldowns)`).Scan(&ledger, &cooldowns); err != nil || ledger != 0 || cooldowns != 0 || officialStoredState(t, s, m.ID) != "prepared" {
		t.Fatal("intent/budget/cooldown transaction partially committed", ledger, cooldowns, err)
	}
}

func TestPaidConfirmedRejectionConsumesOnceAndCrossDayHolds(t *testing.T) {
	s := budgetStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	dest := officialTestConfigure(t, s, "twilio_sms", "A", now)
	m := officialTestMessage("twilio_sms", "A", "retryday", now)
	officialTestBegin(t, s, m, now)
	if err := s.ResolveOfficialDispatch(ctx, m.ID, now, OfficialDispatchResolution{DispatchAttempt: 1, Outcome: "not_accepted", RetryAfter: time.Second, Reason: "twilio_not_accepted"}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.ClaimNotification(ctx, m.ID, now.Add(2*time.Second)); err != nil || !ok {
		t.Fatal(ok, err)
	}
	retry, ok, err := s.BeginOfficialDispatch(ctx, m.ID, dest, now.Add(2*time.Second), OfficialDispatchPolicy{})
	if err != nil || !ok {
		t.Fatal(retry, ok, err)
	}
	status, err := s.OfficialChannelStatus(ctx, "twilio_sms", now)
	if err != nil || status.MessagesReserved != 1 || status.SegmentsReserved != 2 {
		t.Fatal("logical retry double charged", status, err)
	}
	if err := s.ResolveOfficialDispatch(ctx, m.ID, now.Add(2*time.Second), OfficialDispatchResolution{DispatchAttempt: 2, Outcome: "not_accepted", RetryAfter: 24 * time.Hour, Reason: "twilio_not_accepted"}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.ClaimNotification(ctx, m.ID, now.Add(24*time.Hour+3*time.Second)); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if _, ok, err := s.BeginOfficialDispatch(ctx, m.ID, dest, now.Add(24*time.Hour+3*time.Second), OfficialDispatchPolicy{}); err != nil || ok {
		t.Fatal("previous day's debit authorized next-day send", ok, err)
	}
	var reason string
	if err := s.db.QueryRow(`SELECT reason FROM notification_dispatch WHERE notification_id=?`, m.ID).Scan(&reason); err != nil || reason != "official_budget_day_changed" {
		t.Fatal(reason, err)
	}
}

func TestOfficialOptoutSurvivesRotationDisableAndOnlyExplicitReconsent(t *testing.T) {
	s := budgetStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	officialTestConfigure(t, s, "twilio_sms", "A", now)
	m := officialTestMessage("twilio_sms", "A", "optout", now)
	officialTestBegin(t, s, m, now)
	queued := officialTestMessage("twilio_sms", "A", "pending_before_revoke", now)
	if _, err := s.Enqueue(ctx, queued); err != nil {
		t.Fatal(err)
	}
	if err := s.ResolveOfficialDispatch(ctx, m.ID, now, OfficialDispatchResolution{DispatchAttempt: 1, Outcome: "not_accepted", Permanent: true, OptOut: true, Reason: "twilio_recipient_opted_out"}); err != nil {
		t.Fatal(err)
	}
	for _, enabled := range []bool{false, true} {
		p := officialTestPolicy("twilio_sms", now)
		p.Enabled = enabled
		if err := s.ConfigureOfficialPolicy(ctx, "twilio_sms", p, now.Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		if err := s.ConfigureNotificationTarget(ctx, "twilio_sms", nativeTestTarget("twilio_sms", "B"), "prefix", enabled, now.Add(time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.ResumeDestination(ctx, nativeTestTarget("twilio_sms", "B")); err != nil {
		t.Fatal(err)
	}
	status, err := s.OfficialChannelStatus(ctx, "twilio_sms", now)
	if err != nil || !status.OptedOut {
		t.Fatal("ordinary config cleared 21610 lock", status, err)
	}
	p := officialTestPolicy("twilio_sms", now)
	p.BasisID = strings.Repeat("b", 32)
	p.ConsentedAt = now.Add(time.Second)
	if err := s.ConfigureOfficialPolicy(ctx, "twilio_sms", p, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	status, _ = s.OfficialChannelStatus(ctx, "twilio_sms", now)
	if !status.OptedOut {
		t.Fatal("local checkbox substituted for platform permission")
	}
	// A distinct later record plus explicit platform recovery evidence is needed.
	p.BasisID = strings.Repeat("c", 32)
	p.ConsentedAt = now.Add(3 * time.Second)
	p.PlatformRecoveryConfirmed = true
	if err := s.ConfigureOfficialPolicy(ctx, "twilio_sms", p, now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	status, _ = s.OfficialChannelStatus(ctx, "twilio_sms", now)
	if status.OptedOut {
		t.Fatal("valid explicit reconsent remained locked")
	}
	if err := s.RetryNotification(ctx, queued.ID, now.Add(4*time.Second)); err == nil {
		t.Fatal("reconsent resurrected revoked backlog")
	}
}

func TestTwilioAcceptedBoundedGETPollingAndNullableCharges(t *testing.T) {
	s := budgetStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	dest := officialTestConfigure(t, s, "twilio_sms", "A", now)
	m := officialTestMessage("twilio_sms", "A", "poll", now)
	officialTestBegin(t, s, m, now)
	sid := "SM" + strings.Repeat("1", 32)
	if err := s.ResolveOfficialDispatch(ctx, m.ID, now, OfficialDispatchResolution{DispatchAttempt: 1, Outcome: "accepted", ProviderID: sid, PollStatus: "queued"}); err != nil {
		t.Fatal(err)
	}
	infos, err := s.Notifications(ctx, "", 10)
	if err != nil || len(infos) != 1 || infos[0].State != "accepted" || infos[0].PlatformSegments != nil || infos[0].PlatformPrice != nil {
		t.Fatal("unknown provider charges became zero", infos, err)
	}
	if pending, err := s.PendingDestination(ctx, now.Add(time.Minute), 20, dest); err != nil || len(pending) != 0 {
		t.Fatal("accepted SID reopened POST", pending, err)
	}
	for attempt := 1; attempt <= MaxOfficialPollAttempts; attempt++ {
		at := now.Add(time.Duration(attempt) * time.Minute)
		poll, ok, err := s.ClaimOfficialPoll(ctx, m.ID, dest, at)
		if err != nil || !ok || poll.PollAttempts != attempt || poll.ProviderID != sid {
			t.Fatal("GET claim lost same SID or count", poll, ok, err)
		}
		if _, ok, err := s.ClaimOfficialPoll(ctx, m.ID, dest, at); err != nil || ok {
			t.Fatal("GET lease double claimed", ok, err)
		}
		if err := s.ResolveOfficialPoll(ctx, m.ID, at, OfficialPollResolution{PollAttempt: attempt, Reason: "twilio_poll_transport_error"}); err != nil {
			t.Fatal(err)
		}
	}
	if polls, err := s.PendingOfficialPolls(ctx, dest, now.Add(time.Hour), 20); err != nil || len(polls) != 0 {
		t.Fatal("unbounded GET loop", polls, err)
	}
	infos, err = s.Notifications(ctx, "", 10)
	if err != nil || infos[0].State != "accepted" || infos[0].ProviderState != "unknown" {
		t.Fatal("GET limit erased prior acceptance", infos, err)
	}
	status, _ := s.OfficialChannelStatus(ctx, "twilio_sms", now)
	if status.MessagesReserved != 1 || status.SegmentsReserved != 2 {
		t.Fatal("polling changed logical budget", status)
	}
	if err := s.RetryNotification(ctx, m.ID, now.Add(time.Hour)); err == nil {
		t.Fatal("asynchronous state allowed POST retry")
	}
	second := officialTestMessage("twilio_sms", "A", "delivered", now.Add(time.Hour))
	officialTestBegin(t, s, second, now.Add(time.Hour))
	segments := 1
	price := "-0.0125"
	if err := s.ResolveOfficialDispatch(ctx, second.ID, now.Add(time.Hour), OfficialDispatchResolution{DispatchAttempt: 1, Outcome: "accepted", ProviderID: sid, PollStatus: "sent"}); err != nil {
		t.Fatal(err)
	}
	poll, ok, err := s.ClaimOfficialPoll(ctx, second.ID, dest, now.Add(time.Hour+time.Minute))
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if err := s.ResolveOfficialPoll(ctx, second.ID, now.Add(time.Hour+time.Minute), OfficialPollResolution{PollAttempt: poll.PollAttempts, Status: "delivered", PlatformSegments: &segments, Price: &price, PriceUnit: "USD"}); err != nil {
		t.Fatal(err)
	}
	infos, err = s.Notifications(ctx, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, info := range infos {
		if info.ID == second.ID && (info.ProviderState != "delivered" || info.PlatformSegments == nil || *info.PlatformSegments != 1 || info.PlatformPrice == nil || *info.PlatformPrice != price) {
			t.Fatal("actual charge not distinct from reservation", info)
		}
	}
}

func TestOfficialRestoreHoldsPaidOnlyAndConsumesReconciledDay(t *testing.T) {
	s := budgetStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	for _, channel := range officialTestChannels {
		officialTestConfigure(t, s, channel, "A", now)
		m := officialTestMessage(channel, "A", "restored", now)
		if _, err := s.Enqueue(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	dir := t.TempDir()
	backup := filepath.Join(dir, "original.db")
	restoredPath := filepath.Join(dir, "restored.db")
	if _, err := s.Backup(ctx, backup); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(backup)
	if err != nil {
		t.Fatal(err)
	}
	if info, err := RestoreBackup(ctx, backup, restoredPath); err != nil || info.SchemaVersion != 14 {
		t.Fatal(info, err)
	}
	after, _ := os.ReadFile(backup)
	if string(original) != string(after) {
		t.Fatal("restore modified original backup")
	}
	restored, err := Open(restoredPath)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	for _, channel := range officialTestChannels {
		status, err := restored.OfficialChannelStatus(ctx, channel, now)
		if err != nil || status.RestoreHold != paidOfficialChannel(channel) {
			t.Fatal("restore hold scope", status, err)
		}
	}
	for _, channel := range []string{"twilio_sms", "whatsapp_cloud"} {
		if err := restored.ReconcileOfficialPaidChannel(ctx, channel, "local:synthetic-reconciliation", now); err != nil {
			t.Fatal(err)
		}
		status, err := restored.OfficialChannelStatus(ctx, channel, now)
		if err != nil || status.RestoreHold || status.MessagesReserved != status.DailyMessageLimit {
			t.Fatal("reconciliation invented unspent current-day budget", status, err)
		}
		if err := restored.RetryNotification(ctx, "msg_restored_"+channel, now.Add(24*time.Hour)); err == nil {
			t.Fatal("reconciliation resurrected backup bodies")
		}
		fresh := officialTestMessage(channel, "A", "fresh", now.Add(24*time.Hour))
		officialTestBegin(t, restored, fresh, now.Add(24*time.Hour))
	}
	// Restoring an old schema preserves its DDL for the old binary. The fixed
	// marker is consumed on upgrade and only the new paid channels are held.
	old := filepath.Join(t.TempDir(), "thirteen.db")
	if _, err := s.Backup(ctx, old); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", old)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{"DELETE FROM notification_outbox WHERE channel IN ('qqbot','line','twilio_sms','whatsapp_cloud')", "DELETE FROM notification_targets WHERE channel IN ('qqbot','line','twilio_sms','whatsapp_cloud')"} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range schemaThirteenDowngradeFixture() {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`DELETE FROM schema_migrations WHERE version=14`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	oldRestore := filepath.Join(t.TempDir(), "old-restored.db")
	if info, err := RestoreBackup(ctx, old, oldRestore); err != nil || info.SchemaVersion != 13 {
		t.Fatal("old restore silently changed schema", info, err)
	}
	upgraded, err := Open(oldRestore)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	status, err := upgraded.OfficialChannelStatus(ctx, "twilio_sms", now)
	if err != nil || !status.RestoreHold || status.Enabled {
		t.Fatal("old backup upgrade permitted paid dispatch", status, err)
	}
}

func TestOfficialAdmissionFailureDoesNotBreakSensorCommitAndClearsSelectedFrozenBodies(t *testing.T) {
	s := budgetStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	var chain, tail *OutboxMessage
	for _, channel := range notificationChannels {
		nativeTestPolicy(t, s, channel, "A", "prefix", true)
		message := nativeTestMessage(channel, "A", "twelve")
		if channel == "webhook" {
			message.Language = "en"
		}
		if officialChannel(channel) {
			if err := s.ConfigureOfficialPolicy(ctx, channel, officialTestPolicy(channel, now), now); err != nil {
				t.Fatal(err)
			}
			message = officialTestMessage(channel, "A", "twelve", now)
			if channel == "twilio_sms" {
				message.AdmissionFailure = "official_render_rejected"
				message.FrozenPayload = ""
				message.EstimatedSegments = 0
				message.Encoding = ""
			}
		}
		if chain == nil {
			chain = &message
		} else {
			tail.Secondary = &message
		}
		tail = &message
	}
	event, _ := alertFixture("twelve", "incident_twelve", "recovery")
	event.ObservedAt = now
	if err := s.InsertEventNotification(ctx, event, chain); err != nil {
		t.Fatal(err)
	}
	committed, decided, admitted, err := s.SensorEventOutcome(ctx, []string{event.ID})
	if err != nil || !committed || !decided || !admitted {
		t.Fatal("optional render rejection broke local ACK", committed, decided, admitted, err)
	}
	page, err := s.Timeline(ctx, TimelineQuery{Start: now.Add(-time.Minute), End: now.Add(time.Minute), Limit: 10})
	if err != nil || len(page.Events) != 1 || len(page.Events[0].Deliveries) != 12 {
		t.Fatal(page, err)
	}
	for _, delivery := range page.Events[0].Deliveries {
		if delivery.Channel == "twilio_sms" && delivery.State != "blocked" {
			t.Fatal("render failure hidden", delivery)
		}
	}
	if pending, err := s.PendingDestination(ctx, now.Add(time.Second), 20, nativeTestTarget("twilio_sms", "A")); err != nil || len(pending) != 0 {
		t.Fatal("render rejection was dispatchable", pending, err)
	}
	for _, channel := range []string{"qqbot", "line"} {
		if err := s.ConfigureNotificationTarget(ctx, channel, nativeTestTarget(channel, "B"), "prefix", true, now.Add(time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	if count, err := s.DiscardIsolatedNotifications(ctx, "qqbot", now.Add(2*time.Second)); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	var qq, line string
	if err := s.db.QueryRow(`SELECT (SELECT frozen_payload FROM notification_dispatch WHERE notification_id='msg_twelve_qqbot'),(SELECT frozen_payload FROM notification_dispatch WHERE notification_id='msg_twelve_line')`).Scan(&qq, &line); err != nil || qq != "" || line == "" {
		t.Fatal("selected discard leaked or crossed channels", qq, line, err)
	}
}

func TestOfficialBudgetConcurrentCapAndPayloadQuota(t *testing.T) {
	s := budgetStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	officialTestConfigure(t, s, "whatsapp_cloud", "A", now)
	p := officialTestPolicy("whatsapp_cloud", now)
	p.DailyMessageLimit = 3
	if err := s.ConfigureOfficialPolicy(ctx, "whatsapp_cloud", p, now); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	allowed := 0
	for i := range 20 {
		m := officialTestMessage("whatsapp_cloud", "A", fmt.Sprintf("parallel_%d", i), now)
		if _, err := s.Enqueue(ctx, m); err != nil {
			t.Fatal(err)
		}
		if _, ok, err := s.ClaimNotification(ctx, m.ID, now); err != nil || !ok {
			t.Fatal(ok, err)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, ok, err := s.BeginOfficialDispatch(ctx, m.ID, m.Destination, now, OfficialDispatchPolicy{})
			if err != nil {
				t.Error(err)
			}
			if ok {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	status, err := s.OfficialChannelStatus(ctx, "whatsapp_cloud", now)
	if err != nil || allowed != 3 || status.MessagesReserved != 3 {
		t.Fatal("concurrent dispatch exceeded cap", allowed, status, err)
	}
	queue, err := s.QueueStatus(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, channel := range queue.Channels {
		if channel.Channel == "whatsapp_cloud" && (channel.MaxMessages != 750 || channel.MaxBytes != 2<<20 || channel.PendingBytes <= int64(20*len("合成摘要"))) {
			t.Fatal("frozen bytes or bounded share missing", channel)
		}
		if channel.Channel == "telegram" && (channel.MaxMessages != 1250 || channel.MaxBytes != 4<<20) {
			t.Fatal("legacy share regressed", channel)
		}
	}
}

func TestOfficialRejectsRoutingSecretsAndUnsafeResponseCategories(t *testing.T) {
	s := budgetStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	officialTestConfigure(t, s, "qqbot", "A", now)
	for index, payload := range []string{`{"to":"secret-recipient"}`, `{"nested":{"access_token":"secret-token"}}`, `{"url":"https://secret.invalid"}`, `{bad}`, `{"items":[[[[[[[[[[[[[["deep"]]]]]]]]]]]]]]}`} {
		m := officialTestMessage("qqbot", "A", fmt.Sprintf("bad_%d", index), now)
		m.FrozenPayload = payload
		if _, err := s.Enqueue(ctx, m); err == nil {
			t.Fatal("unsafe frozen routing/payload accepted", payload)
		}
	}
	m := officialTestMessage("qqbot", "A", "unsafe_reason", now)
	officialTestBegin(t, s, m, now)
	secret := "https://secret.invalid/token-synthetic"
	if err := s.ResolveOfficialDispatch(ctx, m.ID, now, OfficialDispatchResolution{DispatchAttempt: 1, Outcome: "not_accepted", Permanent: true, Reason: secret}); err != nil {
		t.Fatal(err)
	}
	infos, err := s.Notifications(ctx, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(infos)
	if strings.Contains(string(data), secret) {
		t.Fatal("response reason leaked secret")
	}
	var reason string
	if err := s.db.QueryRow(`SELECT reason FROM notification_dispatch WHERE notification_id=?`, m.ID).Scan(&reason); err != nil || reason != "official_delivery_error" {
		t.Fatal("uncontrolled vendor response retained", reason, err)
	}
	lowercaseSecret := "syntheticlowercasesecretcredential"
	m = officialTestMessage("qqbot", "A", "unsafe_lowercase_reason", now)
	officialTestBegin(t, s, m, now)
	if err := s.ResolveOfficialDispatch(ctx, m.ID, now, OfficialDispatchResolution{DispatchAttempt: 1, Outcome: "not_accepted", Permanent: true, Reason: lowercaseSecret}); err != nil {
		t.Fatal(err)
	}
	infos, err = s.Notifications(ctx, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	data, _ = json.Marshal(infos)
	if strings.Contains(string(data), lowercaseSecret) {
		t.Fatal("ASCII token masqueraded as a reason category")
	}
}

func TestMigration14AtomicFailureKeepsExactHistorical13AndForeignKeys(t *testing.T) {
	ctx := context.Background()
	s := budgetStore(t)
	path := filepath.Join(t.TempDir(), "thirteen.db")
	if _, err := s.Backup(ctx, path); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range schemaThirteenDowngradeFixture() {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range []string{`DELETE FROM schema_migrations WHERE version=14`, `CREATE TRIGGER fail_fourteen BEFORE INSERT ON schema_migrations WHEN NEW.version=14 BEGIN SELECT RAISE(ABORT,'synthetic migration14 failure'); END`} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	if migrated, err := Open(path); err == nil {
		migrated.Close()
		t.Fatal("migration failure accepted")
	}
	db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DROP TRIGGER fail_fourteen`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if info, err := VerifyBackup(ctx, path); err != nil || info.SchemaVersion != 13 {
		t.Fatal("atomic rollback changed published13 schema", info, err)
	}
	migrated, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer migrated.Close()
	if fk, err := migrated.ForeignKeys(ctx); err != nil || len(fk.Violations) != 0 {
		t.Fatal(fk, err)
	}
	if _, err := migrated.db.Exec(`INSERT INTO notification_dispatch(notification_id,logical_kind,frozen_payload) VALUES('absent','event','{}')`); err == nil {
		t.Fatal("dispatch lost outbox FK")
	}
	if _, err := migrated.db.Exec(`INSERT INTO schema_migrations(version,applied_at) VALUES(15,0)`); err != nil {
		t.Fatal(err)
	}
	migrated.Close()
	if _, err := Open(path); err == nil || !strings.Contains(err.Error(), "unsupported database schema version 15") {
		t.Fatal("future database not refused", err)
	}
}
