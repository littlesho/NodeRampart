// SPDX-License-Identifier: MIT

package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var nativeTestChannels = []string{"feishu", "wecom", "discord", "slack", "teams", "google_chat"}

func nativeTestTarget(channel, identity string) string {
	return fmt.Sprintf("%s:%x", channel, sha256.Sum256([]byte(identity)))
}

func nativeTestPolicy(t *testing.T, s *Store, channel, identity, privacy string, enabled bool) string {
	t.Helper()
	destination := nativeTestTarget(channel, identity)
	if err := s.ConfigureNotificationTarget(context.Background(), channel, destination, privacy, enabled, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	return destination
}

func nativeTestMessage(channel, identity, id string) OutboxMessage {
	return OutboxMessage{ID: "msg_" + id + "_" + channel, DedupeKey: "event:" + id + ":" + channel,
		Channel: channel, Destination: nativeTestTarget(channel, identity), PrivacyMode: "prefix", Body: "合成摘要 · " + id,
		Language: "zh", Timezone: "Asia/Shanghai|+08:00", NextAttempt: time.Now().Add(-time.Second)}
}

func nativeTestChain(channels []string, id string) *OutboxMessage {
	var first, tail *OutboxMessage
	for _, channel := range channels {
		message := nativeTestMessage(channel, "A", id)
		if channel == "webhook" {
			message.Language = "en"
		}
		if first == nil {
			first = &message
		} else {
			tail.Secondary = &message
		}
		tail = &message
	}
	return first
}

func TestEightChannelEventAdmissionDedupeAndPartialDelivery(t *testing.T) {
	s := budgetStore(t)
	ctx := context.Background()
	channels := []string{"telegram", "webhook", "feishu", "wecom", "discord", "slack", "teams", "google_chat"}
	for _, channel := range channels {
		nativeTestPolicy(t, s, channel, "A", "prefix", true)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	for index, kind := range []string{"syn_flood", "ssh_brute_force", "ssh_login_success", "budget_month_bytes", "budget_month_cost", "budget_day_bytes", "budget_day_growth", "health_sensor", "health_interface_counter", "health_ssh_journal", "health_storage", "health_geoip_update"} {
		id := fmt.Sprintf("all_%02d", index)
		event, _ := alertFixture(id, "inc_all", "recovery")
		event.ObservedAt, event.Kind = now, kind
		messages := nativeTestChain(channels, id)
		for range 2 {
			if err := s.InsertEventNotification(ctx, event, messages); err != nil {
				t.Fatal(kind, err)
			}
		}
	}
	var events, decisions, queued int
	if err := s.db.QueryRow(`SELECT (SELECT COUNT(*) FROM events),(SELECT COUNT(*) FROM event_notifications),(SELECT COUNT(*) FROM notification_outbox)`).Scan(&events, &decisions, &queued); err != nil || events != 12 || decisions != 96 || queued != 96 {
		t.Fatal("channel/event dedupe was not independent", events, decisions, queued, err)
	}
	if err := s.MarkSent(ctx, "msg_all_00_feishu", now); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkQuarantined(ctx, "msg_all_00_slack", now, "synthetic permission failure"); err != nil {
		t.Fatal(err)
	}
	page, err := s.Timeline(ctx, TimelineQuery{Start: now.Add(-time.Hour), End: now.Add(time.Hour), Limit: 1})
	if err != nil || !page.More || len(page.Events) != 1 || len(page.Events[0].Deliveries) != 8 || page.Events[0].Delivery.Channel != "telegram" {
		t.Fatal("eight-channel associations or pagination lost", page, err)
	}
	for _, delivery := range page.Events[0].Deliveries {
		want := "pending"
		if delivery.Channel == "feishu" {
			want = "sent"
		} else if delivery.Channel == "slack" {
			want = "quarantined"
		}
		if delivery.State != want {
			t.Fatal("one channel changed another channel's outcome", delivery)
		}
	}
	encoded, err := json.Marshal(page)
	if err != nil || strings.Contains(string(encoded), "合成摘要") || strings.Contains(string(encoded), nativeTestTarget("feishu", "A")) {
		t.Fatal("timeline exposed body or destination", err)
	}
	committed, decisionsCommitted, admitted, err := s.SensorEventOutcome(ctx, []string{"all_00"})
	if err != nil || !committed || !decisionsCommitted || !admitted {
		t.Fatal("remote delivery failure changed local commit meaning", committed, decisionsCommitted, admitted, err)
	}
}

func TestEventChannelChainRejectsCyclesDuplicatesAndAliasesAtomically(t *testing.T) {
	for _, invalid := range []string{"cycle", "same_channel", "same_dedupe", "same_id", "unknown", "legacy_pair"} {
		t.Run(invalid, func(t *testing.T) {
			s := budgetStore(t)
			first := nativeTestChain(nativeTestChannels, "bad")
			switch invalid {
			case "cycle":
				first.Secondary.Secondary = first
			case "same_channel":
				first.Secondary.Channel = first.Channel
			case "same_dedupe":
				first.Secondary.DedupeKey = first.DedupeKey
			case "same_id":
				first.Secondary.ID = first.ID
			case "unknown":
				first.Secondary.Channel = "other"
			case "legacy_pair":
				first.Channel = ""
			}
			event, _ := alertFixture("bad", "inc_bad", "start")
			if err := s.InsertEventNotification(context.Background(), event, first); err == nil {
				t.Fatal("invalid event chain accepted")
			}
			var count int
			if err := s.db.QueryRow(`SELECT (SELECT COUNT(*) FROM events)+(SELECT COUNT(*) FROM event_notifications)+(SELECT COUNT(*) FROM notification_outbox)`).Scan(&count); err != nil || count != 0 {
				t.Fatal("invalid chain partly committed", count, err)
			}
		})
	}
}

func TestNativeChannelsTargetChangeDisablePrivacyLeaseAndRestartMatrix(t *testing.T) {
	for _, channel := range nativeTestChannels {
		t.Run(channel, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "native.db")
			s, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = s.Close() }()
			target := nativeTestPolicy(t, s, channel, "A", "prefix", true)
			message := nativeTestMessage(channel, "A", "old")
			if ok, err := s.Enqueue(ctx, message); err != nil || !ok {
				t.Fatal(ok, err)
			}
			now := time.Now().UTC()
			claimed, ok, err := s.ClaimNotification(ctx, message.ID, now)
			if err != nil || !ok {
				t.Fatal(ok, err)
			}
			// Reapplying the same credential snapshot or language-only policy
			// keeps the active lease and original immutable presentation.
			nativeTestPolicy(t, s, channel, "A", "prefix", false)
			if allowed, err := s.NotificationDeliveryAllowed(ctx, message.ID, target); err != nil || allowed {
				t.Fatal("disabled target remained deliverable", allowed, err)
			}
			info, err := s.Notifications(ctx, "", 10)
			if err != nil || len(info) != 1 || info[0].Channel != channel || info[0].State != "paused" {
				t.Fatal("disabled queue state inaccurate", info, err)
			}
			nativeTestPolicy(t, s, channel, "A", "prefix", true)
			if _, ok, err := s.ClaimNotification(ctx, message.ID, now.Add(time.Second)); err != nil || ok {
				t.Fatal("enable revoked a live lease", ok, err)
			}
			if _, ok, err := s.ClaimNotification(ctx, message.ID, now.Add(3*time.Minute)); err != nil || !ok {
				t.Fatal("expired lease did not recover", ok, err)
			}
			if err := s.MarkRateLimited(ctx, message.ID, target, now.Add(time.Hour), "synthetic hold"); err != nil {
				t.Fatal(err)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			s, err = Open(path)
			if err != nil {
				t.Fatal(err)
			}
			nativeTestPolicy(t, s, channel, "A", "prefix", true)
			if pending, err := s.PendingDestination(ctx, now.Add(time.Minute), 20, target); err != nil || len(pending) != 0 {
				t.Fatal("restart reset native cooldown", pending, err)
			}
			nativeTestPolicy(t, s, channel, "B", "prefix", true)
			if allowed, err := s.NotificationDeliveryAllowed(ctx, claimed.ID, claimed.Destination); err != nil || allowed {
				t.Fatal("in-flight stale identity can be dispatched", allowed, err)
			}
			nativeTestPolicy(t, s, channel, "A", "prefix", true)
			if err := s.RetryNotification(ctx, message.ID, now); err == nil {
				t.Fatal("A-B-A revived isolated contents")
			}
			newMessage := nativeTestMessage(channel, "A", "new")
			if ok, err := s.Enqueue(ctx, newMessage); err != nil || !ok {
				t.Fatal(ok, err)
			}
			nativeTestPolicy(t, s, channel, "A", "hash", true)
			if pending, err := s.PendingDestination(ctx, now.Add(30*time.Minute), 20, target); err != nil || len(pending) != 0 {
				t.Fatal("incomparable privacy mode released old contents", pending, err)
			}
			var body, language, timezone string
			if err := s.db.QueryRow(`SELECT body,language,presentation_timezone FROM notification_outbox WHERE id=?`, message.ID).Scan(&body, &language, &timezone); err != nil || body != message.Body || language != message.Language || timezone != message.Timezone {
				t.Fatal("reconfiguration changed saved presentation", err)
			}
			if count, err := s.DiscardIsolatedNotifications(ctx, channel, now); err != nil || count != 2 {
				t.Fatal(count, err)
			}
			status, err := s.QueueStatus(ctx, now)
			if err != nil || status.Suppressed != 2 || len(status.Cooldowns) != 1 {
				t.Fatal("discard removed cooldown or history", status, err)
			}
		})
	}
}

func TestNativeChannelQuotaIsolationAndConcurrentAdmission(t *testing.T) {
	s := budgetStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	for _, channel := range nativeTestChannels {
		nativeTestPolicy(t, s, channel, "A", "prefix", true)
	}
	// Fill a single quarantined receiver to one less than its message share.
	if _, err := s.db.Exec(`WITH RECURSIVE n(v) AS (SELECT 1 UNION ALL SELECT v+1 FROM n WHERE v<?)
 INSERT INTO notification_outbox(id,dedupe_key,channel,destination,body,created_at,next_attempt,expires_at,quarantined_at)
 SELECT 'full_'||v,'full_'||v,'feishu',?,'synthetic',?,?,?,? FROM n`, maxChannelOutboxMessages-1,
		nativeTestTarget("feishu", "A"), now.UnixMilli(), now.UnixMilli(), now.Add(OutboxTTL).UnixMilli(), now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	var accepted atomic.Int32
	var wg sync.WaitGroup
	for index := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			message := nativeTestMessage("feishu", "A", fmt.Sprintf("concurrent_%d", index))
			ok, err := s.Enqueue(ctx, message)
			if ok {
				accepted.Add(1)
			}
			if err != nil && !errors.Is(err, ErrOutboxFull) {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if accepted.Load() != 1 {
		t.Fatal("concurrent admissions exceeded channel share", accepted.Load())
	}
	// Rotation and isolation cannot evade the channel's aggregate share.
	nativeTestPolicy(t, s, "feishu", "B", "prefix", true)
	if ok, err := s.Enqueue(ctx, nativeTestMessage("feishu", "B", "rotated")); ok || !errors.Is(err, ErrOutboxFull) {
		t.Fatal("target rotation evaded quota", ok, err)
	}
	event, _ := alertFixture("partial", "inc_partial", "start")
	messages := nativeTestChain(nativeTestChannels, event.ID)
	// Use the same fixture clock for scheduling and the later pending query.
	// Concurrent admission may take longer than the query's one-second offset.
	for message := messages; message != nil; message = message.Secondary {
		message.NextAttempt = now
	}
	if err := s.InsertEventNotification(ctx, event, messages); err != nil {
		t.Fatal(err)
	}
	var rejected, queued int
	if err := s.db.QueryRow(`SELECT SUM(decision='rejected'),SUM(decision='queued') FROM event_notifications WHERE event_id='partial'`).Scan(&rejected, &queued); err != nil || rejected != 1 || queued != 5 {
		t.Fatal("one receiver exhausted unrelated admission", rejected, queued, err)
	}
	_, decisions, admitted, err := s.SensorEventOutcome(ctx, []string{event.ID})
	if err != nil || !decisions || admitted {
		t.Fatal("partial output reported admitted", decisions, admitted, err)
	}
	status, err := s.QueueStatus(ctx, now.Add(time.Second))
	if err != nil || status.Pending != maxChannelOutboxMessages+5 || status.Rejected != 9 || len(status.Channels) != MaxNotificationChannels {
		t.Fatal("channel rejection accounting inaccurate", status, err)
	}
	for _, entry := range status.Channels {
		if entry.Channel == "feishu" && (entry.Pending != maxChannelOutboxMessages || entry.Isolated != maxChannelOutboxMessages || entry.MaxMessages != 1250 || entry.MaxBytes != 4<<20) {
			t.Fatal("isolated quota not observable", entry)
		}
	}
	if count, err := s.DiscardIsolatedNotifications(ctx, "feishu", now); err != nil || count != maxChannelOutboxMessages {
		t.Fatal(count, err)
	}
	if ok, err := s.Enqueue(ctx, nativeTestMessage("feishu", "B", "after_discard")); err != nil || !ok {
		t.Fatal("explicit selected-channel discard failed to free capacity", ok, err)
	}
	for _, channel := range nativeTestChannels[1:] {
		pending, err := s.PendingDestination(ctx, now.Add(time.Second), 20, nativeTestTarget(channel, "A"))
		if err != nil || len(pending) != 1 {
			t.Fatal("selected-channel discard affected another channel", channel, pending, err)
		}
	}
}

func TestNativeChannelDiscardPreservesOtherChannelsSchedule(t *testing.T) {
	s := budgetStore(t)
	ctx := context.Background()
	for _, channel := range nativeTestChannels {
		nativeTestPolicy(t, s, channel, "A", "prefix", true)
	}
	scheduledAt := time.Now().UTC().Truncate(time.Millisecond).Add(time.Hour)
	event, _ := alertFixture("scheduled", "inc_scheduled", "start")
	messages := nativeTestChain(nativeTestChannels, event.ID)
	for message := messages; message != nil; message = message.Secondary {
		message.NextAttempt = scheduledAt
	}
	if err := s.InsertEventNotification(ctx, event, messages); err != nil {
		t.Fatal(err)
	}
	nativeTestPolicy(t, s, "feishu", "B", "prefix", true)
	if count, err := s.DiscardIsolatedNotifications(ctx, "feishu", time.Now().UTC()); err != nil || count != 1 {
		t.Fatal("selected-channel discard failed", count, err)
	}
	for message := messages.Secondary; message != nil; message = message.Secondary {
		destination := nativeTestTarget(message.Channel, "A")
		if pending, err := s.PendingDestination(ctx, scheduledAt.Add(-time.Millisecond), 20, destination); err != nil || len(pending) != 0 {
			t.Fatal("discard released another channel before its schedule", message.Channel, pending, err)
		}
		pending, err := s.PendingDestination(ctx, scheduledAt, 20, destination)
		if err != nil || len(pending) != 1 {
			t.Fatal("discard lost another channel at its schedule", message.Channel, pending, err)
		}
		if pending[0].ID != message.ID || pending[0].Body != message.Body || pending[0].Language != message.Language || pending[0].Timezone != message.Timezone || !pending[0].NextAttempt.Equal(scheduledAt) {
			t.Fatal("discard changed another channel's saved message", message.Channel, pending[0])
		}
	}
}

func TestNativeChannelUTF8BodyQuotaAndNoAutomaticMerging(t *testing.T) {
	for _, channel := range nativeTestChannels {
		t.Run(channel, func(t *testing.T) {
			s := budgetStore(t)
			ctx := context.Background()
			nativeTestPolicy(t, s, channel, "A", "prefix", true)
			if err := s.ConfigureNotifications(time.Hour); err != nil {
				t.Fatal(err)
			}
			for _, id := range []string{"first", "second"} {
				event, _ := alertFixture(id, "inc_native", "update")
				message := nativeTestMessage(channel, "A", id)
				if err := s.InsertEventNotification(ctx, event, &message); err != nil {
					t.Fatal(err)
				}
			}
			pending, err := s.PendingDestination(ctx, time.Now().Add(time.Second), 20, nativeTestTarget(channel, "A"))
			if err != nil || len(pending) != 2 {
				t.Fatal("native bodies merged or unnecessarily delayed", len(pending), err)
			}
			for _, message := range pending {
				if strings.Contains(message.Body, "<b>") || strings.Contains(message.Body, "incident updates") || message.Timezone != "Asia/Shanghai|+08:00" || message.Language != "zh" {
					t.Fatal("platform-ready body changed", message)
				}
			}
			body := strings.Repeat("界", 1333)
			now := time.Now().UTC()
			if _, err := s.db.Exec(`WITH RECURSIVE n(v) AS (SELECT 1 UNION ALL SELECT v+1 FROM n WHERE v<1048)
 INSERT INTO notification_outbox(id,dedupe_key,channel,destination,body,created_at,next_attempt,expires_at)
 SELECT 'bytes_'||v,'bytes_'||v,?,?,?,?,?,? FROM n`, channel, nativeTestTarget(channel, "A"), body, now.UnixMilli(), now.UnixMilli(), now.Add(OutboxTTL).UnixMilli()); err != nil {
				t.Fatal(err)
			}
			message := nativeTestMessage(channel, "A", "over_bytes")
			message.Body = body
			if ok, err := s.Enqueue(ctx, message); ok || !errors.Is(err, ErrOutboxFull) {
				t.Fatal("UTF-8 bytes bypassed native quota", ok, err)
			}
		})
	}
}

func TestNativeChannelSilenceAndReportDedupeRemainIndependent(t *testing.T) {
	s := budgetStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	for _, channel := range nativeTestChannels {
		nativeTestPolicy(t, s, channel, "A", "prefix", true)
		message := nativeTestMessage(channel, "A", "report")
		message.DedupeKey = "daily:2026-09-30:" + message.Destination
		if ok, err := s.Enqueue(ctx, message); err != nil || !ok {
			t.Fatal(ok, err)
		}
		if err := s.MarkReportGenerated(ctx, "2026-09-30", message.Destination, now); err != nil {
			t.Fatal(err)
		}
	}
	event, _ := alertFixture("silence_all", "inc_silence_all", "start")
	if err := s.InsertEventNotification(ctx, event, nativeTestChain(nativeTestChannels, event.ID)); err != nil {
		t.Fatal(err)
	}
	result, err := s.AddSilence(ctx, Silence{IncidentID: event.IncidentID, ExpiresAt: now.Add(time.Hour)}, now)
	if err != nil || result.Suppressed != 6 {
		t.Fatal("silence missed native event bodies or erased reports", result, err)
	}
	for _, channel := range nativeTestChannels {
		generated, err := s.ReportGenerated(ctx, "2026-09-30", nativeTestTarget(channel, "A"))
		if err != nil || !generated {
			t.Fatal("report destination dedupe lost", channel, generated, err)
		}
		nativeTestPolicy(t, s, channel, "B", "prefix", true)
		generated, err = s.ReportGenerated(ctx, "2026-09-30", nativeTestTarget(channel, "B"))
		if err != nil || generated {
			t.Fatal("new target adopted old report receipt", channel, generated, err)
		}
		pending, err := s.PendingDestination(ctx, now.Add(time.Second), 20, nativeTestTarget(channel, "B"))
		if err != nil || len(pending) != 0 {
			t.Fatal("new target received historic report", channel, pending, err)
		}
	}
	page, err := s.Timeline(ctx, TimelineQuery{Start: now.Add(-time.Hour), End: now.Add(time.Hour), Limit: 10})
	if err != nil || len(page.Events) != 1 || len(page.Events[0].Deliveries) != 6 {
		t.Fatal(page, err)
	}
	for _, delivery := range page.Events[0].Deliveries {
		if delivery.State != "silenced" {
			t.Fatal("silenced body revived during target change", delivery)
		}
	}
}

func TestNativeSuccessfulSendCooldownIsAtomicPersistentAndTargetSpecific(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "success_hold.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	target := nativeTestPolicy(t, s, "wecom", "A", "prefix", true)
	nativeTestPolicy(t, s, "slack", "A", "prefix", true)
	now := time.Now().UTC().Truncate(time.Millisecond)
	for _, id := range []string{"sent", "pending"} {
		if ok, err := s.Enqueue(ctx, nativeTestMessage("wecom", "A", id)); err != nil || !ok {
			t.Fatal(ok, err)
		}
	}
	if _, err := s.db.Exec(`CREATE TRIGGER reject_cooldown BEFORE INSERT ON notification_cooldowns BEGIN SELECT RAISE(ABORT,'synthetic failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkSentWithCooldown(ctx, "msg_sent_wecom", now, 3*time.Second); err == nil {
		t.Fatal("cooldown failure was ignored")
	}
	var sent bool
	if err := s.db.QueryRow(`SELECT sent_at IS NOT NULL FROM notification_outbox WHERE id='msg_sent_wecom'`).Scan(&sent); err != nil || sent {
		t.Fatal("success committed without durable interval", sent, err)
	}
	if _, err := s.db.Exec(`DROP TRIGGER reject_cooldown`); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkSentWithCooldown(ctx, "msg_sent_wecom", now, 3*time.Second+time.Nanosecond); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	nativeTestPolicy(t, s, "wecom", "A", "prefix", true)
	if ok, err := s.Enqueue(ctx, nativeTestMessage("wecom", "A", "new")); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if pending, err := s.PendingDestination(ctx, now.Add(3*time.Second), 20, target); err != nil || len(pending) != 0 {
		t.Fatal("success interval rounded earlier or was lost on restart", pending, err)
	}
	if pending, err := s.PendingDestination(ctx, now.Add(3*time.Second+time.Millisecond), 20, target); err != nil || len(pending) != 2 {
		t.Fatal("success interval never resumed", pending, err)
	}
	// A duplicate local acknowledgment cannot keep extending an old hold.
	if err := s.MarkSentWithCooldown(ctx, "msg_sent_wecom", now.Add(time.Hour), 3*time.Second); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.Enqueue(ctx, nativeTestMessage("slack", "A", "independent")); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if pending, err := s.PendingDestination(ctx, now.Add(time.Second), 20, nativeTestTarget("slack", "A")); err != nil || len(pending) != 1 {
		t.Fatal("success interval held another receiver", pending, err)
	}
	if err := s.MarkSentWithCooldown(ctx, "msg_new_wecom", now, -time.Second); err == nil {
		t.Fatal("negative cooldown accepted")
	}
}

func TestNativeTargetActivationBoundaryPersistsWithoutChangingOldBodies(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "activation.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	now := time.Now().UTC().Truncate(time.Millisecond)
	for _, channel := range nativeTestChannels {
		target := nativeTestTarget(channel, "A")
		configure := func(enabled bool, at time.Time) {
			t.Helper()
			if err := s.ConfigureNotificationTarget(ctx, channel, target, "prefix", enabled, at); err != nil {
				t.Fatal(err)
			}
		}
		configure(false, now)
		configure(true, now.Add(time.Second))
		message := nativeTestMessage(channel, "A", "retained")
		if ok, err := s.Enqueue(ctx, message); err != nil || !ok {
			t.Fatal(ok, err)
		}
		// Normal reapplication includes language/timezone changes and restarts.
		configure(true, now.Add(2*time.Second))
		activated, err := s.NotificationTargetActivatedAt(ctx, channel, target)
		if err != nil || !activated.Equal(now.Add(time.Second)) {
			t.Fatal("same active target moved historical report boundary", channel, activated, err)
		}
		configure(false, now.Add(3*time.Second))
		configure(true, now.Add(4*time.Second))
		activated, err = s.NotificationTargetActivatedAt(ctx, channel, target)
		if err != nil || !activated.Equal(now.Add(4*time.Second)) {
			t.Fatal("reenabled target retained old automatic report boundary", channel, activated, err)
		}
		pending, err := s.PendingDestination(ctx, now.Add(5*time.Second), 20, target)
		if err != nil || len(pending) != 1 || pending[0].Body != message.Body || pending[0].Language != message.Language || pending[0].Timezone != message.Timezone {
			t.Fatal("activation gate erased or changed previously admitted bodies", channel, pending, err)
		}
		if _, err := s.NotificationTargetActivatedAt(ctx, channel, nativeTestTarget(channel, "B")); err == nil {
			t.Fatal("unknown target received a historical report boundary")
		}
	}
	for _, channel := range []string{"telegram", "webhook"} {
		target := nativeTestPolicy(t, s, channel, "A", "prefix", true)
		activated, err := s.NotificationTargetActivatedAt(ctx, channel, target)
		if err != nil || !activated.IsZero() {
			t.Fatal("legacy daily schedule changed", channel, activated, err)
		}
	}
	backup := filepath.Join(t.TempDir(), "backup.db")
	if _, err := s.Backup(ctx, backup); err != nil {
		t.Fatal(err)
	}
	restoredPath := filepath.Join(t.TempDir(), "restored.db")
	if _, err := RestoreBackup(ctx, backup, restoredPath); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(restoredPath)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	for _, channel := range nativeTestChannels {
		target := nativeTestTarget(channel, "A")
		activated, err := restored.NotificationTargetActivatedAt(ctx, channel, target)
		if err != nil || !activated.Equal(now.Add(4*time.Second)) {
			t.Fatal("backup changed native activation boundary", channel, activated, err)
		}
		if err := restored.ConfigureNotificationTarget(ctx, channel, nativeTestTarget(channel, "B"), "prefix", true, now.Add(6*time.Second)); err != nil {
			t.Fatal(err)
		}
		activated, err = restored.NotificationTargetActivatedAt(ctx, channel, nativeTestTarget(channel, "B"))
		if err != nil || !activated.Equal(now.Add(6*time.Second)) {
			t.Fatal("target rotation retained previous report boundary", channel, activated, err)
		}
	}
}

func TestTeamsAcknowledgementIsAcceptedNotFinalPublication(t *testing.T) {
	s := budgetStore(t)
	ctx := context.Background()
	nativeTestPolicy(t, s, "teams", "A", "prefix", true)
	event, _ := alertFixture("workflow", "inc_workflow", "start")
	message := nativeTestMessage("teams", "A", event.ID)
	if err := s.InsertEventNotification(ctx, event, &message); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkSentWithCooldown(ctx, message.ID, time.Now().UTC(), time.Second); err != nil {
		t.Fatal(err)
	}
	info, err := s.Notifications(ctx, "", 10)
	if err != nil || len(info) != 1 || info[0].State != "accepted" {
		t.Fatal("workflow request acceptance reported as publication", info, err)
	}
	page, err := s.Timeline(ctx, TimelineQuery{Start: event.ObservedAt.Add(-time.Hour), End: event.ObservedAt.Add(time.Hour), Limit: 10})
	if err != nil || len(page.Events) != 1 || len(page.Events[0].Deliveries) != 1 || page.Events[0].Delivery.State != "accepted" || page.Events[0].Delivery.SentAt.IsZero() {
		t.Fatal("workflow timeline acknowledgement inaccurate", page, err)
	}
}

func TestNativePrivacyTighteningIsolatesOldAndStaleAdmission(t *testing.T) {
	for _, channel := range nativeTestChannels {
		for _, privacy := range []string{"prefix", "hash"} {
			t.Run(channel+"_"+privacy, func(t *testing.T) {
				s := budgetStore(t)
				ctx := context.Background()
				target := nativeTestPolicy(t, s, channel, "A", "full", true)
				old := nativeTestMessage(channel, "A", "full")
				old.PrivacyMode, old.Body = "full", "synthetic full details"
				if ok, err := s.Enqueue(ctx, old); err != nil || !ok {
					t.Fatal(ok, err)
				}
				nativeTestPolicy(t, s, channel, "A", privacy, true)
				stale := old
				stale.ID, stale.DedupeKey = "stale", "stale"
				if ok, err := s.Enqueue(ctx, stale); err != nil || !ok {
					t.Fatal(ok, err)
				}
				fresh := nativeTestMessage(channel, "A", "private")
				fresh.PrivacyMode = privacy
				if ok, err := s.Enqueue(ctx, fresh); err != nil || !ok {
					t.Fatal(ok, err)
				}
				pending, err := s.PendingDestination(ctx, time.Now().Add(time.Second), 20, target)
				if err != nil || len(pending) != 1 || pending[0].ID != fresh.ID {
					t.Fatal("old privacy body admitted for tightened target", pending, err)
				}
				var body string
				var isolated bool
				if err := s.db.QueryRow(`SELECT body,isolated_at IS NOT NULL FROM notification_outbox WHERE id=?`, old.ID).Scan(&body, &isolated); err != nil || body != old.Body || !isolated {
					t.Fatal("privacy tightening rewrote saved body", body, isolated, err)
				}
			})
		}
	}
}

func TestNativeAttemptReservationPersistsThroughCrashAndBoundsConcurrentClaims(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "reserved_attempt.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	target := nativeTestPolicy(t, s, "wecom", "A", "prefix", true)
	now := time.Now().UTC().Truncate(time.Millisecond)
	for index := range 8 {
		message := nativeTestMessage("wecom", "A", fmt.Sprintf("attempt_%d", index))
		if ok, err := s.Enqueue(ctx, message); err != nil || !ok {
			t.Fatal(ok, err)
		}
		if _, ok, err := s.ClaimNotification(ctx, message.ID, now); err != nil || !ok {
			t.Fatal(ok, err)
		}
	}
	var reserved atomic.Int32
	var wg sync.WaitGroup
	for index := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := s.ReserveNotificationAttempt(ctx, fmt.Sprintf("msg_attempt_%d_wecom", index), target, now, 3*time.Second+time.Nanosecond)
			if err != nil {
				t.Error(err)
			}
			if ok {
				reserved.Add(1)
			}
		}()
	}
	wg.Wait()
	if reserved.Load() != 1 {
		t.Fatal("concurrent claimed rows exceeded paced target interval", reserved.Load())
	}
	var unchanged int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM notification_outbox WHERE attempts=0 AND sent_at IS NULL AND body LIKE '合成摘要%'`).Scan(&unchanged); err != nil || unchanged != 8 {
		t.Fatal("reservation changed body, attempt history or success", unchanged, err)
	}
	// Simulate interruption before any network request or acknowledgment.
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := s.ReserveNotificationAttempt(ctx, "msg_attempt_0_wecom", target, now.Add(3*time.Second), time.Second); err != nil || ok {
		t.Fatal("restart or rounding bypassed consumed request interval", ok, err)
	}
	if pending, err := s.PendingDestination(ctx, now.Add(3*time.Second), 20, target); err != nil || len(pending) != 0 {
		t.Fatal("prefetched queue bypassed reserved interval", pending, err)
	}
	if ok, err := s.ReserveNotificationAttempt(ctx, "msg_attempt_0_wecom", target, now.Add(3*time.Second+time.Millisecond), time.Second); err != nil || !ok {
		t.Fatal("bounded interval did not resume", ok, err)
	}
	nativeTestPolicy(t, s, "wecom", "B", "prefix", true)
	if ok, err := s.ReserveNotificationAttempt(ctx, "msg_attempt_0_wecom", nativeTestTarget("wecom", "B"), now.Add(5*time.Second), time.Second); err != nil || ok {
		t.Fatal("old lease was redirected to a new target", ok, err)
	}
	if allowed, err := s.NotificationDeliveryAllowed(ctx, "msg_attempt_0_wecom", target); err != nil || allowed {
		t.Fatal("reserved old request survived target revalidation", allowed, err)
	}
}

func TestNativeAttemptReservationRejectsIneligibleRowsAndRollsBackFailure(t *testing.T) {
	for _, state := range []string{"unclaimed", "expired_lease", "sent", "quarantined", "isolated", "suppressed", "disabled", "wrong_target", "cooldown", "write_failure"} {
		t.Run(state, func(t *testing.T) {
			s := budgetStore(t)
			ctx := context.Background()
			target := nativeTestPolicy(t, s, "feishu", "A", "prefix", true)
			message := nativeTestMessage("feishu", "A", "reservation")
			if ok, err := s.Enqueue(ctx, message); err != nil || !ok {
				t.Fatal(ok, err)
			}
			now := time.Now().UTC().Truncate(time.Millisecond)
			if state != "unclaimed" {
				if _, ok, err := s.ClaimNotification(ctx, message.ID, now); err != nil || !ok {
					t.Fatal(ok, err)
				}
			}
			switch state {
			case "expired_lease":
				now = now.Add(3 * time.Minute)
			case "sent":
				if err := s.MarkSent(ctx, message.ID, now); err != nil {
					t.Fatal(err)
				}
			case "quarantined":
				if err := s.MarkQuarantined(ctx, message.ID, now, "synthetic payload failure"); err != nil {
					t.Fatal(err)
				}
			case "isolated":
				nativeTestPolicy(t, s, "feishu", "B", "prefix", true)
			case "suppressed":
				if _, err := s.db.Exec(`UPDATE notification_outbox SET suppressed_at=?,body='' WHERE id=?`, now.UnixMilli(), message.ID); err != nil {
					t.Fatal(err)
				}
			case "disabled":
				nativeTestPolicy(t, s, "feishu", "A", "prefix", false)
			case "wrong_target":
				target = nativeTestTarget("feishu", "B")
			case "cooldown":
				if _, err := s.db.Exec(`INSERT INTO notification_cooldowns(destination,until_at) VALUES (?,?)`, target, now.Add(time.Hour).UnixMilli()); err != nil {
					t.Fatal(err)
				}
			case "write_failure":
				if _, err := s.db.Exec(`CREATE TRIGGER reject_reservation BEFORE INSERT ON notification_cooldowns BEGIN SELECT RAISE(ABORT,'synthetic failure'); END`); err != nil {
					t.Fatal(err)
				}
			}
			ok, err := s.ReserveNotificationAttempt(ctx, message.ID, target, now, time.Second)
			if ok || state == "write_failure" && err == nil || state != "write_failure" && err != nil {
				t.Fatal("ineligible/failing reservation accepted", state, ok, err)
			}
			var cooldowns int
			if err := s.db.QueryRow(`SELECT COUNT(*) FROM notification_cooldowns`).Scan(&cooldowns); err != nil || state != "cooldown" && cooldowns != 0 || state == "cooldown" && cooldowns != 1 {
				t.Fatal("failed reservation changed persisted cooldown", cooldowns, err)
			}
		})
	}
}

func TestPacedSyntheticLegacyTargetAndReservationArgumentBounds(t *testing.T) {
	s := budgetStore(t)
	ctx := context.Background()
	message := OutboxMessage{ID: "synthetic_legacy", DedupeKey: "synthetic_legacy", Destination: "synthetic-target", Body: "synthetic", NextAttempt: time.Now().Add(-time.Second)}
	if ok, err := s.Enqueue(ctx, message); err != nil || !ok {
		t.Fatal(ok, err)
	}
	now := time.Now().UTC()
	if _, ok, err := s.ClaimNotification(ctx, message.ID, now); err != nil || !ok {
		t.Fatal(ok, err)
	}
	for _, interval := range []time.Duration{0, time.Second - time.Nanosecond, 30*time.Second + time.Nanosecond} {
		if ok, err := s.ReserveNotificationAttempt(ctx, message.ID, message.Destination, now, interval); ok || err == nil {
			t.Fatal("out-of-range interval accepted", ok, err)
		}
	}
	if ok, err := s.ReserveNotificationAttempt(ctx, message.ID, message.Destination, now, time.Second); err != nil || !ok {
		t.Fatal("explicit paced synthetic sender lost legacy compatibility", ok, err)
	}
}
