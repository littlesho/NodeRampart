// SPDX-License-Identifier: MIT

package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/config"
)

func TestNativeRapidInPlaceReturnNeverRevivesIsolatedQueue(t *testing.T) {
	for _, channel := range config.NativeChannelNames() {
		t.Run(channel, func(t *testing.T) {
			db := openHTTPSOutbox(t)
			a, path := nativeFixture(t, channel)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := bindHTTPSOutbox(db, channel, a.Destination(), "prefix"); err != nil {
				t.Fatal(err)
			}
			if err := enqueueHTTPSOutbox(db, "old_A", channel, a.Destination(), "prefix", "retained old A body", time.Now().Add(-time.Second)); err != nil {
				t.Fatal(err)
			}
			// Same inode, no sleeps or forced metadata movement. The filesystem
			// may coalesce timestamps. Queue isolation must hold independently of
			// whether returning A can reproduce the previous snapshot digest.
			var credential config.NativeCredential
			if json.Unmarshal(data, &credential) != nil {
				t.Fatal("fixture")
			}
			credential.URL = strings.Replace(strings.Replace(credential.URL, "synthetic", "different", 1), "Synthetic", "Different", 1)
			changed, _ := json.Marshal(credential)
			if err := os.WriteFile(path, changed, 0o600); err != nil {
				t.Fatal(err)
			}
			b, err := NewNative(channel, config.NativeChannelConfig{CredentialFile: path, Timeout: config.Duration{Duration: time.Second}})
			if err != nil || b.Destination() == a.Destination() {
				t.Fatal("changed snapshot was not distinguished", err)
			}
			if err := bindHTTPSOutbox(db, channel, b.Destination(), "prefix"); err != nil {
				t.Fatal(err)
			}
			if err := enqueueHTTPSOutbox(db, "old_B", channel, b.Destination(), "prefix", "retained old B body", time.Now().Add(-time.Second)); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			back, err := NewNative(channel, config.NativeChannelConfig{CredentialFile: path, Timeout: config.Duration{Duration: time.Second}})
			if err != nil {
				t.Fatal(err)
			}
			if err := bindHTTPSOutbox(db, channel, back.Destination(), "prefix"); err != nil {
				t.Fatal(err)
			}
			if err := enqueueHTTPSOutbox(db, "new_A", channel, back.Destination(), "prefix", "new A summary only", time.Now().Add(-time.Second)); err != nil {
				t.Fatal(err)
			}
			calls := 0
			back.client.Transport = nativeRoundTrip(func(r *http.Request) (*http.Response, error) {
				calls++
				payload, _ := io.ReadAll(r.Body)
				if strings.Contains(string(payload), "retained old") {
					t.Fatal("returning target received isolated historical body")
				}
				status, body := nativeSuccess(channel)
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})
			if err := (&Worker{Store: db, Sender: back}).process(context.Background()); err != nil {
				t.Fatal(err)
			}
			if calls != 1 || queueStatus(t, db).Isolated != 2 {
				t.Fatal("rapid A -> B -> A revived old queue")
			}
			rows, err := db.Notifications(context.Background(), "", 10)
			if err != nil {
				t.Fatal(err)
			}
			encoded, _ := json.Marshal(rows)
			for _, secret := range []string{nativeFixtureURLs[channel], "synthetic-signing-secret", "synthetic-key-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "synthetic-token-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"} {
				if strings.Contains(string(encoded), secret) {
					t.Fatal("queue/API snapshot leaked credential")
				}
			}
		})
	}
}

func TestSixNativeWorkersIsolateFailuresAndCancel(t *testing.T) {
	db := openHTTPSOutbox(t)
	var workers []*Worker
	for _, channel := range config.NativeChannelNames() {
		n, _ := nativeFixture(t, channel)
		if err := bindHTTPSOutbox(db, channel, n.Destination(), "prefix"); err != nil {
			t.Fatal(err)
		}
		if err := enqueueHTTPSOutbox(db, channel+"_summary", channel, n.Destination(), "prefix", "synthetic safe summary", time.Now().Add(-time.Second)); err != nil {
			t.Fatal(err)
		}
		status, body := nativeSuccess(channel)
		if channel == "feishu" {
			status, body = 200, `{"code":11232,"msg":"secret-synthetic-response"}`
		}
		nativeMock(n, status, body, "")
		workers = append(workers, &Worker{Store: db, Sender: n})
	}
	var wg sync.WaitGroup
	errors := make(chan error, len(workers))
	for _, worker := range workers {
		wg.Add(1)
		go func() { defer wg.Done(); errors <- worker.process(context.Background()) }()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	status := queueStatus(t, db)
	if status.Pending != 1 || status.LastSent.IsZero() {
		t.Fatalf("a failed target affected other channels: %+v", status)
	}
	for _, channel := range status.Channels {
		if channel.Channel == "feishu" && channel.Pending != 1 || channel.Channel != "feishu" && channel.Pending != 0 {
			t.Fatal("partial success was marked as whole success")
		}
	}
	rows, err := db.Notifications(context.Background(), "", 10)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(rows)
	if strings.Contains(string(encoded), "secret-synthetic-response") {
		t.Fatal("vendor error response leaked into persistent state")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, worker := range workers {
		done := make(chan error, 1)
		go func() { done <- worker.Run(ctx) }()
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("native worker did not exit on cancellation")
		}
	}
}
