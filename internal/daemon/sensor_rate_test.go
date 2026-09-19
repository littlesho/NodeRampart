// SPDX-License-Identifier: MIT

package daemon

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSensorReadGateBoundsSustainedBurst(t *testing.T) {
	gate := sensorReadGate{interval: 20 * time.Millisecond}
	start := time.Now()
	for i := 0; i < 6; i++ {
		if err := gate.wait(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if time.Since(start) < 5*gate.interval {
		t.Fatal("buffered frames bypassed the sustained read-rate bound")
	}
}

func TestSensorReadGateWaitIsCancelable(t *testing.T) {
	gate := sensorReadGate{interval: time.Hour}
	if err := gate.wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- gate.wait(ctx) }()
	time.Sleep(10 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("unexpected wait result: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not interrupt read shaping")
	}
}
