// SPDX-License-Identifier: MIT

package console

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

func TestServicesMenuCanStopWithInvalidConfiguration(t *testing.T) {
	b := newBackend()
	b.loadErr = errors.New("synthetic malformed JSON")
	s, _, _ := launch(t, false, b)
	awaitFrame(t, s, "Main menu")
	selectIndex(s, 2)
	awaitFrame(t, s, "Configuration unavailable")
	key(s, tcell.KeyEscape)
	awaitFrame(t, s, "Main menu")
	selectIndex(s, 9)
	awaitFrame(t, s, "Services and uninstall")
	selectIndex(s, 1)
	awaitFrame(t, s, "Stop observation now")
	key(s, tcell.KeyRight)
	key(s, tcell.KeyEnter)
	select {
	case call := <-b.calls:
		if call.id != "service_stop" {
			t.Fatalf("unexpected action: %s", call.id)
		}
	case <-time.After(time.Second):
		t.Fatal("invalid configuration prevented menu service stop")
	}
}

func TestTerminalCancellationWaitsForSupervisedOperation(t *testing.T) {
	b := newBackend()
	started, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	b.action = func(ctx context.Context, _ string, _ map[string]string) (string, error) {
		close(started)
		<-ctx.Done()
		<-release
		close(finished)
		return "settled", ctx.Err()
	}
	s, cancel, done := launch(t, false, b)
	awaitFrame(t, s, "Main menu")
	selectIndex(s, 0)
	<-started
	cancel()
	select {
	case err := <-done:
		t.Fatalf("terminal returned while supervised operation was pending: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("backend did not settle")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("terminal did not return after backend settled")
	}
}
