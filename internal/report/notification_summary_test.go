// SPDX-License-Identifier: MIT

package report

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/model"
	"github.com/littlesho/NodeRampart/internal/store"
)

func TestReportTwoTargetsUseShortSummaryAfterPrivacyTightening(t *testing.T) {
	b, _, now := completeUsageBuilder(t)
	ctx := context.Background()
	date, start, _ := PreviousDay(now, time.UTC)
	source := "2001:db8:1234::/48"
	if err := b.Store.InsertEvent(ctx, model.Event{ID: "evt_local_source", ObservedAt: start.Add(time.Hour), Kind: "tcp_port_scan", Severity: model.SeverityInfo, SourceRange: source}); err != nil {
		t.Fatal(err)
	}
	archive, _, err := b.archiveDate(ctx, date, now)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := DecodeDocument(archive.Document)
	if err != nil || !strings.Contains(doc.Body, source) {
		t.Fatal("local source fixture missing", err)
	}
	telegram, webhook := "telegram:"+strings.Repeat("a", 64), "webhook:"+strings.Repeat("b", 64)
	if err := b.Store.ConfigureNotificationTarget(ctx, "telegram", telegram, "full", true, now); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Store.Enqueue(ctx, store.OutboxMessage{ID: "msg_old_full", DedupeKey: "old_full", Channel: "telegram", PrivacyMode: "full", Destination: telegram, Body: source}); err != nil {
		t.Fatal(err)
	}
	if err := b.Store.ConfigureNotificationTarget(ctx, "telegram", telegram, "hash", true, now); err != nil {
		t.Fatal(err)
	}
	if err := b.Store.ConfigureNotificationTarget(ctx, "webhook", webhook, "hash", true, now); err != nil {
		t.Fatal(err)
	}
	scheduler := Scheduler{Store: b.Store, Builder: b, DailyAt: "00:00", Destinations: []string{telegram, webhook}, NotificationPrivacy: "hash"}
	if err := scheduler.checkPrevious(ctx, now); err != nil {
		t.Fatal(err)
	}
	messages, err := b.Store.Pending(ctx, time.Now().UTC().Add(time.Second), 10)
	if err != nil || len(messages) != 2 {
		t.Fatal("missing report channel", err, len(messages))
	}
	channels := map[string]bool{}
	for _, message := range messages {
		channels[message.Channel] = true
		if strings.Contains(message.Body, source) || len(message.Body) > 4096 {
			t.Fatal("short summary leaked old source identity or exceeded limit")
		}
	}
	if !channels["telegram"] || !channels["webhook"] {
		t.Fatal("report targets collapsed into one channel")
	}
	queue, err := b.Store.QueueStatus(ctx, time.Now().UTC())
	if err != nil || queue.Isolated != 1 {
		t.Fatal("old backlog isolation lost", err)
	}
	again, err := b.Store.Report(ctx, date)
	if err != nil || string(again.Document) != string(archive.Document) || again.Body != archive.Body {
		t.Fatal("delivery rewrote local archive", err)
	}
	if err := scheduler.checkPrevious(ctx, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if pending, err := b.Store.Pending(ctx, time.Now().UTC().Add(time.Second), 10); err != nil || len(pending) != 2 {
		t.Fatal("report duplicated after retry", err)
	}
}
