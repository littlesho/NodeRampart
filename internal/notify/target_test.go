// SPDX-License-Identifier: MIT

package notify

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/store"
)

func TestTelegramTargetIdentityCredentialRotationAndUncertainty(t *testing.T) {
	first, err := telegramDestination(fakeToken(), "-1001234567890")
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := telegramDestination("123456789:"+strings.Repeat("B", 35), "-1001234567890")
	if err != nil || first != rotated {
		t.Fatal("same bot/chat credential rotation changed identity", err)
	}
	for _, change := range []struct{ token, chat string }{
		{fakeToken(), "-1001234567891"},
		{"123456788:" + strings.Repeat("A", 35), "-1001234567890"},
	} {
		identity, err := telegramDestination(change.token, change.chat)
		if err != nil || identity == first {
			t.Fatal("different receiver reused identity", err)
		}
	}
	for _, chat := range []string{"@mutable_name", "0", "", " 12", "12\n", "9223372036854775808"} {
		if _, err := telegramDestination(fakeToken(), chat); err == nil {
			t.Fatal("uncertain identity accepted")
		}
	}
	if strings.Contains(first, fakeToken()) || strings.Contains(first, "123456789") || len(first) != 73 {
		t.Fatal("unsafe identity representation")
	}
	path := filepath.Join(t.TempDir(), "credential")
	if err := os.WriteFile(path, []byte(fakeToken()), 0o600); err != nil {
		t.Fatal(err)
	}
	fromFile, err := TelegramDestination(path, "-1001234567890")
	if err != nil || fromFile != first {
		t.Fatal("file-derived identity", err)
	}
	link := filepath.Join(filepath.Dir(path), "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := TelegramDestination(link, "-1001234567890"); err == nil {
		t.Fatal("symlink credential read accepted")
	}
}

func TestWorkerTargetSwitchDuringSendNeverReroutesPrefetchedBacklog(t *testing.T) {
	targetA, _ := telegramDestination(fakeToken(), "1")
	targetB, _ := telegramDestination(fakeToken(), "2")
	db := openNotifyStore(t)
	ctx := context.Background()
	configure := func(destination string) {
		t.Helper()
		if err := db.ConfigureNotificationTarget(ctx, "telegram", destination, "prefix", true, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	enqueue := func(id, destination string) {
		t.Helper()
		if ok, err := db.Enqueue(ctx, store.OutboxMessage{ID: id, DedupeKey: id, Channel: "telegram", PrivacyMode: "prefix", Destination: destination, Body: id, NextAttempt: time.Now().Add(-time.Second)}); err != nil || !ok {
			t.Fatal("enqueue", ok, err)
		}
	}
	configure(targetA)
	enqueue("first_A", targetA)
	enqueue("second_A", targetA)
	var deliveredA, deliveredB []string
	workerA := &Worker{Store: db, Destination: targetA, Sender: senderFunc(func(_ context.Context, body string) error {
		deliveredA = append(deliveredA, body)
		// The first request may finish at A. B must not receive its body or the
		// second row already prefetched by this old sender.
		configure(targetB)
		enqueue("only_B", targetB)
		return nil
	})}
	if err := workerA.process(ctx); err != nil {
		t.Fatal(err)
	}
	workerB := &Worker{Store: db, Destination: targetB, Sender: senderFunc(func(_ context.Context, body string) error { deliveredB = append(deliveredB, body); return nil })}
	if err := workerB.process(ctx); err != nil {
		t.Fatal(err)
	}
	if len(deliveredA) != 1 || deliveredA[0] != "first_A" || len(deliveredB) != 1 || deliveredB[0] != "only_B" {
		t.Fatal("receiver isolation failed", deliveredA, deliveredB)
	}
	if err := db.RetryNotification(ctx, "second_A", time.Now().UTC()); err == nil {
		t.Fatal("isolated prefetch revived by manual retry")
	}
}

func TestWorkerUnidentifiedSenderFailsClosed(t *testing.T) {
	db := openNotifyStore(t)
	enqueueSynthetic(t, db, "pending", "telegram", time.Now().UTC())
	called := false
	w := &Worker{Store: db, Sender: senderFunc(func(context.Context, string) error { called = true; return nil })}
	if err := w.process(context.Background()); err == nil || called {
		t.Fatal("unidentified sender delivered a message")
	}
}
