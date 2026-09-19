// SPDX-License-Identifier: MIT
package sensor

import (
	"context"
	"errors"
	"github.com/littlesho/NodeRampart/internal/collector"
	"io"
	"log/slog"
	"testing"
	"time"
)

func TestPartialCaptureDiscoveryRetainsHealthySlotButOrdinaryErrorRetiresIt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	refs := []collector.InterfaceRef{{Name: "labA", Index: 1}, {Name: "labB", Index: 2}}
	var discoveryErr error
	f := &Fleet{Limit: 2, MaxFlows: 64, ReceiveBuffer: 1 << 20, Sender: &BatchSender{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), slots: map[string]*captureSlot{}}
	f.resolve = func(context.Context, []string) ([]collector.InterfaceRef, error) { return refs, discoveryErr }
	f.open = func(string, int) (captureSource, error) { return &fakeCapture{}, nil }
	defer func() {
		for name := range f.slots {
			if err := f.retire(name); err != nil {
				t.Error(err)
			}
		}
	}()
	if err := f.reconcile(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	healthy := f.slots["labA"]
	lost := f.slots["labB"].capture.(*fakeCapture)
	refs = refs[:1]
	discoveryErr = &collector.PartialDiscoveryError{}
	if err := f.reconcile(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	if len(f.slots) != 1 || f.slots["labA"] != healthy || healthy.capture.(*fakeCapture).closed.Load() || !lost.closed.Load() || f.lastError == "" {
		t.Fatal("partial discovery disrupted healthy capture or hid degradation")
	}
	discoveryErr = errors.New("untrusted partial result")
	if err := f.reconcile(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	if len(f.slots) != 0 || !healthy.capture.(*fakeCapture).closed.Load() {
		t.Fatal("ordinary discovery failure did not fail closed")
	}
}
