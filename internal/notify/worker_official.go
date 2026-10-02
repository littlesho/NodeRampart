// SPDX-License-Identifier: MIT

package notify

import (
	"context"
	"time"

	"github.com/littlesho/NodeRampart/internal/store"
)

type officialDispatchSender interface {
	Dispatch(context.Context, store.OutboxMessage) (DispatchOutcome, error)
	Poll(context.Context, store.OutboxMessage) (DispatchOutcome, error)
}

func (w *Worker) processOfficialMessage(ctx context.Context, claimed store.OutboxMessage, interval time.Duration) error {
	message, allowed, err := w.Store.BeginOfficialDispatch(ctx, claimed.ID, claimed.Destination, time.Now().UTC(), store.OfficialDispatchPolicy{MinimumInterval: interval})
	if err != nil {
		return err
	}
	if !allowed {
		return w.Store.ReleaseNotificationClaim(ctx, claimed.ID)
	}
	allowed, err = w.Store.NotificationDeliveryAllowed(ctx, message.ID, message.Destination)
	if err != nil {
		return err
	}
	if !allowed {
		return w.Store.ResolveOfficialDispatch(ctx, message.ID, time.Now().UTC(), store.OfficialDispatchResolution{DispatchAttempt: message.DispatchAttempt, Outcome: "not_accepted", RetryAfter: time.Second, Reason: "official_policy_changed"})
	}
	sender := w.Sender.(officialDispatchSender)
	// The committed intent survives cancellation, lease expiry and a failed
	// receipt write. For non-idempotent APIs recovery holds it as unknown.
	deliveryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	result, sendErr := sender.Dispatch(deliveryCtx, message)
	cancel()
	resolution := store.OfficialDispatchResolution{
		DispatchAttempt: message.DispatchAttempt, Outcome: "unknown",
		Reason: "delivery_unknown", ProviderID: result.ProviderID,
		PollStatus: result.ProviderState, PlatformSegments: result.PlatformSegments,
		Price: result.Price, PriceUnit: result.PriceUnit,
		Suspend: result.Suspend, HTTPStatus: result.StatusCode, APIErrorCode: result.APIErrorCode,
	}
	// A sender must prove non-acceptance; arbitrary errors cannot grant a
	// retry. Provider bodies/URLs and Go transport errors are never persisted.
	switch result.State {
	case DispatchAccepted:
		if sendErr == nil {
			resolution.Outcome, resolution.Reason = "accepted", "provider_accepted"
		}
	case DispatchNotAccepted:
		resolution.Outcome, resolution.Reason = "not_accepted", officialWorkerReason(result.Reason)
		resolution.RetryAfter = result.RetryAfterDuration
		if resolution.RetryAfter == 0 {
			resolution.RetryAfter = retryDelay(message.Attempts)
		}
		if resolution.RetryAfter < interval {
			resolution.RetryAfter = interval
		}
	case DispatchRejected, DispatchOptedOut:
		resolution.Outcome, resolution.Reason = "not_accepted", officialWorkerReason(result.Reason)
		resolution.Permanent = true
		resolution.OptOut = result.State == DispatchOptedOut
	case DispatchUnknown:
		resolution.Reason = officialWorkerReason(result.Reason)
		resolution.RetryAfter = result.RetryAfterDuration
	}
	if result.StatusCode < 0 || result.StatusCode > 599 || result.APIErrorCode < 0 || result.APIErrorCode > 2147483647 {
		resolution.Outcome, resolution.Reason = "unknown", "invalid_response"
		resolution.HTTPStatus, resolution.APIErrorCode = 0, 0
	}
	writeCtx, stop := context.WithTimeout(context.Background(), 2*time.Second)
	err = w.Store.ResolveOfficialDispatch(writeCtx, message.ID, time.Now().UTC(), resolution)
	stop()
	if err != nil {
		return err
	}
	return ctx.Err()
}

func (w *Worker) processOfficialPolls(ctx context.Context, sender officialDispatchSender, destination string, now time.Time) error {
	// Polling reads only an already accepted Twilio resource. Its persistent
	// attempt/deadline cannot reopen the POST dispatch path.
	messages, err := w.Store.PendingOfficialPolls(ctx, destination, now, 8)
	if err != nil {
		return err
	}
	for _, pending := range messages {
		if err := ctx.Err(); err != nil {
			return err
		}
		message, allowed, err := w.Store.ClaimOfficialPoll(ctx, pending.ID, destination, time.Now().UTC())
		if err != nil {
			return err
		}
		if !allowed {
			continue
		}
		allowed, err = w.Store.OfficialPollAllowed(ctx, message.ID, destination, message.PollAttempts, time.Now().UTC())
		if err != nil {
			return err
		}
		if !allowed {
			continue
		}
		pollCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		result, pollErr := sender.Poll(pollCtx, message)
		cancel()
		status, reason := result.ProviderState, officialWorkerReason(result.Reason)
		if pollErr != nil || result.State != DispatchAccepted && result.State != DispatchOptedOut {
			status = "unknown"
		}
		switch status {
		case "accepted", "queued", "sending", "sent", "delivered", "undelivered", "failed", "canceled", "unknown":
		default:
			status, reason = "unknown", "invalid_response"
		}
		if result.StatusCode < 0 || result.StatusCode > 599 || result.APIErrorCode < 0 || result.APIErrorCode > 2147483647 {
			status, reason = "unknown", "invalid_response"
			result.StatusCode, result.APIErrorCode = 0, 0
		}
		writeCtx, stop := context.WithTimeout(context.Background(), 2*time.Second)
		err = w.Store.ResolveOfficialPoll(writeCtx, message.ID, time.Now().UTC(), store.OfficialPollResolution{
			PollAttempt: message.PollAttempts, Status: status, Reason: reason,
			RetryAfter: result.RetryAfterDuration, OptOut: result.State == DispatchOptedOut,
			Suspend:          result.Suspend,
			PlatformSegments: result.PlatformSegments, Price: result.Price, PriceUnit: result.PriceUnit,
			HTTPStatus: result.StatusCode, APIErrorCode: result.APIErrorCode,
		})
		stop()
		if err != nil {
			return err
		}
	}
	return nil
}

func officialWorkerReason(reason string) string {
	// This allowlist prevents an injected/third-party sender from persisting
	// credentials, recipient identifiers, response text or request URLs.
	switch reason {
	case "provider_accepted", "delivery_unknown", "authentication_rejected", "permission_rejected", "payload_rejected", "rate_limited", "quota_exhausted", "recipient_unavailable", "recipient_opted_out", "template_rejected", "template_unavailable", "template_language_mismatch", "invalid_response", "retry_window_expired", "poll_unknown", "poll_complete", "retry_requires_resume", "transport_not_sent",
		"api_accepted", "carrier_accepted", "carrier_delivered", "delivery_failed", "payload_invalid", "frozen_request_invalid", "message_expired", "clock_rollback", "credential_or_permission_rejected", "provider_rejected", "request_rejected", "quality_or_policy_limited", "monthly_quota_exhausted", "token_unavailable", "token_response_invalid", "token_request_invalid", "token_rejected", "token_wait_canceled", "unconfirmed_response", "unconfirmed_provider_failure", "poll_response_invalid", "poll_receipt_invalid", "poll_request_invalid", "poll_intent_invalid", "durable_intent_required", "unsupported_channel", "template_parameter_count_invalid", "template_missing_or_unapproved", "template_text_too_long", "template_parameter_format_invalid", "template_policy_rejected":
		return reason
	default:
		return "delivery_unknown"
	}
}
