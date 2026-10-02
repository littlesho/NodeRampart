// SPDX-License-Identifier: MIT

package notify

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/store"
)

type officialWorkerFixture struct {
	calls, polls int
	outcome      DispatchOutcome
	err          error
	onDispatch   func(store.OutboxMessage)
}

func (s *officialWorkerFixture) Send(context.Context, string) error {
	return errors.New("unsafe direct send")
}
func (s *officialWorkerFixture) Dispatch(_ context.Context, m store.OutboxMessage) (DispatchOutcome, error) {
	s.calls++
	if s.onDispatch != nil {
		s.onDispatch(m)
	}
	return s.outcome, s.err
}
func (s *officialWorkerFixture) Poll(_ context.Context, m store.OutboxMessage) (DispatchOutcome, error) {
	s.polls++
	return DispatchOutcome{State: DispatchAccepted, ProviderID: m.ProviderID, ProviderState: "delivered", Reason: "poll_complete"}, nil
}

func enqueueOfficialWorker(t *testing.T, db *store.Store, channel, id string) string {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	dest := channel + ":" + strings.Repeat("a", 64)
	if err := db.ConfigureNotificationTarget(ctx, channel, dest, "prefix", true, now); err != nil {
		t.Fatal(err)
	}
	p := store.OfficialChannelPolicy{Enabled: true, ConsentedAt: now.Add(-time.Hour), Purpose: "synthetic test", EvidenceRef: "synthetic-consent", BasisID: strings.Repeat("b", 32), NotificationTypes: []string{"test", "event", "daily"}, CostConfirmed: true, DailyMessageLimit: 20}
	if channel == "twilio_sms" {
		p.DailySegmentLimit = 40
		p.MaxSegments = 2
	}
	if err := db.ConfigureOfficialPolicy(ctx, channel, p, now); err != nil {
		t.Fatal(err)
	}
	m := store.OutboxMessage{ID: id, DedupeKey: id, Channel: channel, Destination: dest, PrivacyMode: "prefix", Body: "synthetic", Language: "en", LogicalKind: "test", FrozenPayload: `{"body":"synthetic"}`, NextAttempt: now.Add(-time.Second)}
	if channel == "twilio_sms" {
		m.EstimatedSegments = 1
		m.Encoding = "gsm7"
	}
	if inserted, err := db.Enqueue(ctx, m); err != nil || !inserted {
		t.Fatal(inserted, err)
	}
	return dest
}

func TestOfficialWorkerUncertainDoesNotAutomaticallyResubmit(t *testing.T) {
	for _, channel := range []string{"qqbot", "twilio_sms", "whatsapp_cloud"} {
		t.Run(channel, func(t *testing.T) {
			db := openNotifyStore(t)
			dest := enqueueOfficialWorker(t, db, channel, "unknown-case")
			sender := &officialWorkerFixture{outcome: DispatchOutcome{State: DispatchUnknown, Reason: "secret-synthetic-token"}, err: errors.New("https://untrusted.example/secret-synthetic-token")}
			sender.onDispatch = func(m store.OutboxMessage) {
				if m.DispatchState != "in_flight" || m.DispatchAttempt != 1 || m.FirstAttemptAt.IsZero() {
					t.Fatal("side effect before durable intent")
				}
				if channel == "twilio_sms" {
					p, e := db.OfficialChannelStatus(context.Background(), channel, time.Now())
					if e != nil || p.MessagesReserved != 1 || p.SegmentsReserved != 1 {
						t.Fatal("side effect before budget reservation", p, e)
					}
				}
			}
			w := Worker{Store: db, Sender: sender, Destination: dest}
			for i := 0; i < 3; i++ {
				if err := w.process(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			rows, e := db.Notifications(context.Background(), "", 20)
			if e != nil || len(rows) != 1 || rows[0].State != "delivery_unknown" || strings.Contains(rows[0].LastError, "secret") {
				t.Fatal("uncertainty or secret classification lost", rows, e)
			}
			if sender.calls != 1 {
				t.Fatal("unknown side effect was submitted again", sender.calls)
			}
			if err := db.RetryNotification(context.Background(), "unknown-case", time.Now()); err == nil {
				t.Fatal("ordinary retry resurrected unknown request")
			}
		})
	}
}

func TestOfficialWorkerAcceptedTwilioPollNeverPostsAgain(t *testing.T) {
	db := openNotifyStore(t)
	dest := enqueueOfficialWorker(t, db, "twilio_sms", "accepted-case")
	sender := &officialWorkerFixture{outcome: DispatchOutcome{State: DispatchAccepted, ProviderID: "SM" + strings.Repeat("c", 32), ProviderState: "delivered"}}
	w := Worker{Store: db, Sender: sender, Destination: dest}
	for i := 0; i < 2; i++ {
		if err := w.process(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	rows, e := db.Notifications(context.Background(), "", 20)
	if e != nil || len(rows) != 1 || rows[0].State != "accepted" || rows[0].ProviderState != "delivered" {
		t.Fatal(rows, e)
	}
	if sender.calls != 1 || sender.polls != 0 {
		t.Fatal("terminal receipt was polled or reposted")
	}
}

func TestOfficialWorkerCancellationPersistsUncertainty(t *testing.T) {
	db := openNotifyStore(t)
	dest := enqueueOfficialWorker(t, db, "whatsapp_cloud", "cancel-case")
	ctx, cancel := context.WithCancel(context.Background())
	sender := &officialWorkerFixture{outcome: DispatchOutcome{State: DispatchUnknown}, err: context.Canceled, onDispatch: func(store.OutboxMessage) { cancel() }}
	w := Worker{Store: db, Sender: sender, Destination: dest}
	if err := w.process(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	rows, e := db.Notifications(context.Background(), "", 20)
	if e != nil || len(rows) != 1 || rows[0].State != "delivery_unknown" {
		t.Fatal("cancel released uncertain message", rows, e)
	}
}

func TestOfficialWorkerResolvedRefusalIsDistinctFromUnknown(t *testing.T) {
	for _, state := range []string{DispatchRejected, DispatchOptedOut} {
		t.Run(state, func(t *testing.T) {
			db := openNotifyStore(t)
			dest := enqueueOfficialWorker(t, db, "twilio_sms", "refused")
			sender := &officialWorkerFixture{outcome: DispatchOutcome{State: state, Reason: "recipient_opted_out"}}
			w := Worker{Store: db, Sender: sender, Destination: dest}
			if err := w.process(context.Background()); err != nil {
				t.Fatal(err)
			}
			p, e := db.OfficialChannelStatus(context.Background(), "twilio_sms", time.Now())
			if e != nil || p.OptedOut != (state == DispatchOptedOut) || p.MessagesReserved != 1 {
				t.Fatal(p, e)
			}
			if err := w.process(context.Background()); err != nil {
				t.Fatal(err)
			}
			if sender.calls != 1 {
				t.Fatal("rejected request retried")
			}
		})
	}
}

func TestOfficialWorkerPersistsSafeNumericAndReasonDiagnostics(t *testing.T) {
	for _, invalid := range []bool{false, true} {
		db := openNotifyStore(t)
		dest := enqueueOfficialWorker(t, db, "whatsapp_cloud", "numeric-diagnostics")
		out := DispatchOutcome{State: DispatchRejected, Reason: "template_parameter_count_invalid", StatusCode: 400, APIErrorCode: 132000}
		if invalid {
			out.StatusCode, out.APIErrorCode = 600, -1
		}
		sender := &officialWorkerFixture{outcome: out}
		w := Worker{Store: db, Sender: sender, Destination: dest}
		if err := w.process(context.Background()); err != nil {
			t.Fatal(err)
		}
		rows, err := db.Notifications(context.Background(), "", 20)
		if err != nil || len(rows) != 1 {
			t.Fatal(err)
		}
		if invalid {
			if rows[0].State != "delivery_unknown" || rows[0].HTTPStatus != 0 || rows[0].APIErrorCode != 0 || rows[0].LastError != "invalid_response" {
				t.Fatal("invalid diagnostics authorized a classified refusal", rows[0])
			}
		} else if rows[0].HTTPStatus != 400 || rows[0].APIErrorCode != 132000 || rows[0].LastError != "template_parameter_count_invalid" {
			t.Fatal("safe provider diagnostics lost", rows[0])
		}
	}
}
