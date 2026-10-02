// SPDX-License-Identifier: MIT

package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/store"
)

// Exercise the real worker POST, persisted delayed receipt and real adapter
// GET. The explicit later poll time avoids sleeps for the production interval;
// neither the store authorization nor the sender's deadline is bypassed.
func TestOfficialRealTwilioDelayedReceiptPollsOnlyKnownSID(t *testing.T) {
	ctx := context.Background()
	db := openNotifyStore(t)
	sender, message := officialFixture(t, "twilio_sms")
	sender.now = time.Now
	now := time.Now().UTC()
	destination := sender.Destination()
	if err := db.ConfigureNotificationTarget(ctx, "twilio_sms", destination, "prefix", true, now); err != nil {
		t.Fatal(err)
	}
	policy := store.OfficialChannelPolicy{Enabled: true, ConsentedAt: now.Add(-time.Hour), Purpose: "synthetic real adapter test", EvidenceRef: "synthetic-poll", BasisID: sender.cfg.Subscription.BasisID, NotificationTypes: []string{"test"}, CostConfirmed: true, DailyMessageLimit: 20, DailySegmentLimit: 40, MaxSegments: 2}
	if err := db.ConfigureOfficialPolicy(ctx, "twilio_sms", policy, now); err != nil {
		t.Fatal(err)
	}
	message.ID, message.DedupeKey, message.PrivacyMode, message.NextAttempt = "real-adapter-poll", "real-adapter-poll", "prefix", now.Add(-time.Second)
	if inserted, err := db.Enqueue(ctx, message); err != nil || !inserted {
		t.Fatal("admit real frozen SMS", inserted, err)
	}
	posts, gets := 0, 0
	sender.client.Transport = nativeRoundTrip(func(request *http.Request) (*http.Response, error) {
		state, status := "queued", http.StatusCreated
		if request.Method == http.MethodPost {
			posts++
			p, err := db.OfficialChannelStatus(ctx, "twilio_sms", time.Now())
			if err != nil || p.MessagesReserved != 1 || p.SegmentsReserved != message.EstimatedSegments {
				t.Fatal("network before durable debit", p, err)
			}
			// A measurable response delay exposes receipt-time+24h mistakes.
			time.Sleep(15 * time.Millisecond)
		} else if request.Method == http.MethodGet {
			gets++
			state, status = "delivered", http.StatusOK
			if request.URL.Path != "/2010-04-01/Accounts/"+sender.credential.AccountSID+"/Messages/SM"+strings.Repeat("a", 32)+".json" {
				t.Fatal("GET did not use the original receipt")
			}
		} else {
			t.Fatal("unexpected provider method")
		}
		body, _ := json.Marshal(map[string]any{"sid": "SM" + strings.Repeat("a", 32), "account_sid": sender.credential.AccountSID, "to": sender.credential.To, "body": message.Body, "status": state, "num_segments": "1", "price": nil, "price_unit": nil, "error_code": nil})
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(body))), Request: request}, nil
	})
	worker := Worker{Store: db, Sender: sender, Destination: destination}
	if err := worker.process(ctx); err != nil {
		t.Fatal(err)
	}
	if posts != 1 || gets != 0 {
		t.Fatal("acceptance did not submit exactly once")
	}
	pollAt := time.Now().UTC().Add(time.Minute)
	pending, err := db.PendingOfficialPolls(ctx, destination, pollAt, 8)
	if err != nil || len(pending) != 1 {
		t.Fatal("delayed receipt lost pending GET", err)
	}
	if pending[0].PollDeadline != pending[0].FirstAttemptAt.Add(24*time.Hour) {
		t.Fatal("receipt latency extended the first intent's poll deadline")
	}
	claimed, allowed, err := db.ClaimOfficialPoll(ctx, message.ID, destination, pollAt)
	if err != nil || !allowed {
		t.Fatal("durable poll claim failed", allowed, err)
	}
	if allowed, err := db.OfficialPollAllowed(ctx, message.ID, destination, claimed.PollAttempts, pollAt); err != nil || !allowed {
		t.Fatal("poll revalidation failed", allowed, err)
	}
	sender.now = func() time.Time { return pollAt }
	out, err := sender.Poll(ctx, claimed)
	if err != nil || out.State != DispatchAccepted || out.ProviderState != "delivered" || posts != 1 || gets != 1 {
		t.Fatal("real accepted resource was not polled", out.State, out.ProviderState, err)
	}
	if err := db.ResolveOfficialPoll(ctx, message.ID, pollAt, store.OfficialPollResolution{PollAttempt: claimed.PollAttempts, Status: out.ProviderState, Reason: "poll_complete", HTTPStatus: out.StatusCode, PlatformSegments: out.PlatformSegments}); err != nil {
		t.Fatal(err)
	}
	if err := worker.process(ctx); err != nil {
		t.Fatal(err)
	}
	if posts != 1 || gets != 1 {
		t.Fatal("terminal GET reopened the paid POST path")
	}
	rows, err := db.Notifications(ctx, "", 20)
	if err != nil || len(rows) != 1 || rows[0].State != "accepted" || rows[0].ProviderState != "delivered" {
		t.Fatal("receipt state was not preserved", err)
	}
}

// An authentication refusal on a GET must pause the same receiver's POST and
// GET paths without turning an already accepted resource back into a POST.
func TestOfficialRealTwilioPollAuthenticationSuspendsBothPaths(t *testing.T) {
	ctx := context.Background()
	db := openNotifyStore(t)
	sender, message := officialFixture(t, "twilio_sms")
	sender.now = time.Now
	now := time.Now().UTC().Truncate(time.Millisecond)
	intentAt := now.Add(-90 * time.Second)
	destination := sender.Destination()
	if err := db.ConfigureNotificationTarget(ctx, "twilio_sms", destination, "prefix", true, intentAt); err != nil {
		t.Fatal(err)
	}
	policy := store.OfficialChannelPolicy{Enabled: true, ConsentedAt: intentAt.Add(-time.Hour), Purpose: "synthetic poll authentication test", EvidenceRef: "synthetic-poll-auth", BasisID: sender.cfg.Subscription.BasisID, NotificationTypes: []string{"test"}, CostConfirmed: true, DailyMessageLimit: 20, DailySegmentLimit: 40, MaxSegments: 2}
	if err := db.ConfigureOfficialPolicy(ctx, "twilio_sms", policy, intentAt); err != nil {
		t.Fatal(err)
	}
	message.ID, message.DedupeKey, message.PrivacyMode, message.NextAttempt = "real-poll-auth", "real-poll-auth", "prefix", intentAt.Add(-time.Second)
	if inserted, err := db.Enqueue(ctx, message); err != nil || !inserted {
		t.Fatal("admit SMS", inserted, err)
	}
	claimed, allowed, err := db.ClaimNotification(ctx, message.ID, intentAt)
	if err != nil || !allowed {
		t.Fatal("claim SMS", allowed, err)
	}
	intent, allowed, err := db.BeginOfficialDispatch(ctx, claimed.ID, destination, intentAt, store.OfficialDispatchPolicy{MinimumInterval: sender.MinimumInterval()})
	if err != nil || !allowed {
		t.Fatal("durable intent", allowed, err)
	}
	posts, gets, refusePoll := 0, 0, true
	sid := "SM" + strings.Repeat("a", 32)
	sender.client.Transport = nativeRoundTrip(func(request *http.Request) (*http.Response, error) {
		status, body := officialSuccess(sender, message)
		switch request.Method {
		case http.MethodPost:
			posts++
		case http.MethodGet:
			gets++
			if request.URL.Path != "/2010-04-01/Accounts/"+sender.credential.AccountSID+"/Messages/"+sid+".json" {
				t.Fatal("poll changed the accepted SID")
			}
			if refusePoll {
				status, body = http.StatusUnauthorized, `{"code":20003,"message":"synthetic refusal"}`
			} else {
				status = http.StatusOK
				body = strings.Replace(body, `"status":"queued"`, `"status":"delivered"`, 1)
			}
		default:
			t.Fatal("unexpected provider method")
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})
	accepted, err := sender.Dispatch(ctx, intent)
	if err != nil || accepted.State != DispatchAccepted {
		t.Fatal("initial API acceptance", accepted.State, err)
	}
	if err := db.ResolveOfficialDispatch(ctx, message.ID, intentAt.Add(time.Second), store.OfficialDispatchResolution{DispatchAttempt: intent.DispatchAttempt, Outcome: "accepted", ProviderID: accepted.ProviderID, PollStatus: accepted.ProviderState, HTTPStatus: accepted.StatusCode}); err != nil {
		t.Fatal(err)
	}
	sibling := message
	sibling.ID, sibling.DedupeKey, sibling.NextAttempt = "real-poll-auth-sibling", "real-poll-auth-sibling", now.Add(-time.Second)
	if inserted, err := db.Enqueue(ctx, sibling); err != nil || !inserted {
		t.Fatal("admit queued sibling", inserted, err)
	}
	worker := Worker{Store: db, Sender: sender, Destination: destination}
	for i := 0; i < 2; i++ {
		if err := worker.process(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if posts != 1 || gets != 1 {
		t.Fatal("poll authentication refusal did not pause POST and GET", posts, gets)
	}
	pollAt := now.Add(2 * time.Minute)
	if pending, err := db.PendingOfficialPolls(ctx, destination, pollAt, 8); err != nil || len(pending) != 0 {
		t.Fatal("suspended target still offered a GET", len(pending), err)
	}
	if _, allowed, err := db.ClaimOfficialPoll(ctx, message.ID, destination, pollAt); err != nil || allowed {
		t.Fatal("poll claim bypassed suspension", allowed, err)
	}
	policyStatus, err := db.OfficialChannelStatus(ctx, "twilio_sms", intentAt)
	if err != nil || policyStatus.OptedOut || policyStatus.MessagesReserved != 1 {
		t.Fatal("auth refusal changed consent or charged the sibling", policyStatus, err)
	}
	if err := db.ResumeDestination(ctx, destination); err != nil {
		t.Fatal(err)
	}
	poll, allowed, err := db.ClaimOfficialPoll(ctx, message.ID, destination, pollAt)
	if err != nil || !allowed || poll.ProviderID != sid || poll.DispatchState != "accepted" {
		t.Fatal("explicit resume lost the original accepted SID", allowed, err)
	}
	if allowed, err := db.OfficialPollAllowed(ctx, message.ID, destination, poll.PollAttempts, pollAt); err != nil || !allowed {
		t.Fatal("resumed accepted GET failed revalidation", allowed, err)
	}
	refusePoll = false
	sender.now = func() time.Time { return pollAt }
	result, err := sender.Poll(ctx, poll)
	if err != nil || result.State != DispatchAccepted || result.ProviderState != "delivered" || posts != 1 || gets != 2 {
		t.Fatal("resume reopened POST or lost the known resource", result.State, result.ProviderState, err)
	}
	if err := db.ResolveOfficialPoll(ctx, message.ID, pollAt, store.OfficialPollResolution{PollAttempt: poll.PollAttempts, Status: result.ProviderState, HTTPStatus: result.StatusCode}); err != nil {
		t.Fatal(err)
	}
}

func TestOfficialQQFreshAccessTokenCannotBecomeStoredReceipt(t *testing.T) {
	ctx := context.Background()
	db := openNotifyStore(t)
	sender, message := officialFixture(t, "qqbot")
	sender.now = time.Now
	now := time.Now().UTC()
	destination := sender.Destination()
	if err := db.ConfigureNotificationTarget(ctx, "qqbot", destination, "prefix", true, now); err != nil {
		t.Fatal(err)
	}
	policy := store.OfficialChannelPolicy{Enabled: true, ConsentedAt: now.Add(-time.Hour), Purpose: "synthetic reflected receipt test", EvidenceRef: "synthetic-token-echo", BasisID: sender.cfg.Subscription.BasisID, NotificationTypes: []string{"test"}, DailyMessageLimit: 20}
	if err := db.ConfigureOfficialPolicy(ctx, "qqbot", policy, now); err != nil {
		t.Fatal(err)
	}
	message.ID, message.DedupeKey, message.PrivacyMode, message.NextAttempt = "qq-token-echo", "qq-token-echo", "prefix", now.Add(-time.Second)
	message.FirstAttemptAt, message.ExpiresAt = now, now.Add(7*24*time.Hour)
	if inserted, err := db.Enqueue(ctx, message); err != nil || !inserted {
		t.Fatal("admit QQ notification", inserted, err)
	}
	token := "synthetic-fresh-access-token"
	tokens, posts := 0, 0
	sender.client.Transport = nativeRoundTrip(func(request *http.Request) (*http.Response, error) {
		var body []byte
		if request.URL.Path == "/app/getAppAccessToken" {
			tokens++
			body, _ = json.Marshal(map[string]any{"access_token": token, "expires_in": 7200})
		} else {
			posts++
			if request.Header.Get("Authorization") != "QQBot "+token {
				t.Fatal("request did not use its minted token")
			}
			body, _ = json.Marshal(map[string]string{"id": "ROBOT_" + token + "_ACK", "timestamp": now.Format(time.RFC3339Nano)})
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(body))), Request: request}, nil
	})
	// Verify the adapter itself removes the echo before any caller can persist
	// it, then exercise that same cached token through the real outbox worker.
	result, err := sender.Dispatch(ctx, message)
	if err != nil || result.State != DispatchUnknown || result.ProviderID != "" || strings.Contains(result.Reason, token) {
		t.Fatal("fresh access token escaped in the receipt outcome")
	}
	worker := Worker{Store: db, Sender: sender, Destination: destination}
	if err := worker.process(ctx); err != nil {
		t.Fatal(err)
	}
	if tokens != 1 || posts != 2 {
		t.Fatal("synthetic token or message request count changed", tokens, posts)
	}
	rows, err := db.Notifications(ctx, "", 20)
	if err != nil || len(rows) != 1 || rows[0].State != "delivery_unknown" {
		t.Fatal("reflected token was treated as API acceptance", err)
	}
	encoded, _ := json.Marshal(rows)
	if strings.Contains(string(encoded), token) {
		t.Fatal("reflected access token persisted in notification diagnostics")
	}
	if err := db.RetryNotification(ctx, message.ID, time.Now()); err == nil {
		t.Fatal("unconfirmed reflected receipt reopened the non-idempotent POST")
	}
}
