// SPDX-License-Identifier: MIT
package daemon

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/collector"
)

type pauseCoverageContext struct {
	context.Context
	calls   atomic.Int32
	at      int32
	entered chan struct{}
	release chan struct{}
}

func (c *pauseCoverageContext) Err() error {
	if c.calls.Add(1) == c.at {
		close(c.entered)
		select {
		case <-c.release:
		case <-c.Context.Done():
		}
	}
	return c.Context.Err()
}
func pauseCoverage(t *testing.T, at int32) *pauseCoverageContext {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	t.Cleanup(cancel)
	return &pauseCoverageContext{Context: ctx, at: at, entered: make(chan struct{}), release: make(chan struct{})}
}
func waitCoverage(t *testing.T, ctx context.Context, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-ctx.Done():
		t.Fatal("coverage operation did not reach finite scheduling seam")
	}
}
func sensorComponentState(t *testing.T, a *App) string {
	t.Helper()
	states, err := a.options.Store.ComponentStatuses(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range states {
		if s.Name == "sensor_feed" {
			return s.State
		}
	}
	return ""
}

func TestSensorCoverageRecomputesAfterPendingNewerObservation(t *testing.T) {
	a := eventTestApp(t)
	ctx := context.Background()
	if err := a.setSensorState(ctx, "degraded"); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	a.lastSensor = time.Now().UTC()
	a.mu.Unlock()
	slow := pauseCoverage(t, 1)
	done := make(chan struct{})
	var olderErr error
	go func() { olderErr = a.refreshSensorState(slow); close(done) }()
	waitCoverage(t, slow, slow.entered)
	// The old refresh has not acquired the transition gate yet. It must not
	// commit the running decision that preceded this newer loss observation.
	a.mu.Lock()
	a.lastSensor = time.Time{}
	a.mu.Unlock()
	if err := a.refreshSensorState(ctx); err != nil {
		t.Fatal(err)
	}
	close(slow.release)
	waitCoverage(t, slow, done)
	if olderErr != nil {
		t.Fatal(olderErr)
	}
	for range 3 {
		if err := a.refreshSensorState(ctx); err != nil {
			t.Fatal(err)
		}
	}
	a.mu.RLock()
	cached := a.sensorState
	a.mu.RUnlock()
	if cached != "degraded" || sensorComponentState(t, a) != "degraded" {
		t.Fatal("an earlier refresh overwrote the newer degraded evidence")
	}
}

func TestSensorTransitionGateCancelsWaitAndCachesOnlyCommittedState(t *testing.T) {
	a := eventTestApp(t)
	ctx := context.Background()
	if err := a.setSensorState(ctx, "degraded"); err != nil {
		t.Fatal(err)
	}
	// First two Err checks are the transition gate; the third is Store admission.
	slow := pauseCoverage(t, 3)
	done := make(chan struct{})
	var writeErr error
	go func() { writeErr = a.setSensorState(slow, "running"); close(done) }()
	waitCoverage(t, slow, slow.entered)
	a.mu.RLock()
	cached := a.sensorState
	a.mu.RUnlock()
	if cached != "degraded" || sensorComponentState(t, a) != "degraded" {
		t.Fatal("uncommitted state entered the cache or global mutex blocked reads")
	}
	short, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	if err := a.refreshSensorState(short); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("queued transition ignored cancellation: %v", err)
	}
	close(slow.release)
	waitCoverage(t, slow, done)
	if writeErr != nil || sensorComponentState(t, a) != "running" {
		t.Fatal("serialized startup transition failed", writeErr)
	}
	if err := a.refreshSensorState(ctx); err != nil || sensorComponentState(t, a) != "degraded" {
		t.Fatal("new runtime state was not committed after releasing gate", err)
	}
}

func TestSensorCoverageFailedWriteRemainsRetryable(t *testing.T) {
	a := eventTestApp(t)
	ctx := context.Background()
	if err := a.setSensorState(ctx, "running"); err != nil {
		t.Fatal(err)
	}
	eventTestBudget(t, a, true)
	if err := a.refreshSensorState(ctx); err == nil {
		t.Fatal("synthetic storage failure was not reported")
	}
	a.mu.RLock()
	cached := a.sensorState
	a.mu.RUnlock()
	if cached != "" {
		t.Fatal("failed commit left a suppressing cache entry")
	}
	eventTestBudget(t, a, false)
	if err := a.refreshSensorState(ctx); err != nil || sensorComponentState(t, a) != "degraded" {
		t.Fatal("recovered writer did not retry desired state", err)
	}
}

func TestPartialDiscoveryKeepsSensorCoverageDegradedUntilComplete(t *testing.T) {
	a := eventTestApp(t)
	ctx := context.Background()
	a.interfaceDiscoveryRequired = true
	a.sensorByInterface = map[string]time.Time{"labA": time.Now().UTC()}
	if err := a.updateInterfaces(ctx, []collector.InterfaceObservation{{Name: "labA", Index: 1}}); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	a.sensorByInterface["labA"] = time.Now().UTC()
	a.mu.Unlock()
	if err := a.updateDiscovery(ctx, nil); err != nil || sensorComponentState(t, a) != "running" {
		t.Fatal("complete fresh selection not healthy", err)
	}
	if err := a.updateDiscovery(ctx, &collector.PartialDiscoveryError{}); err != nil {
		t.Fatal(err)
	}
	if err := a.refreshSensorState(ctx); err != nil || a.sensorCoverageState(time.Now()) != "degraded" || sensorComponentState(t, a) != "degraded" {
		t.Fatal("healthy retained family concealed incomplete discovery", err)
	}
	if err := a.updateDiscovery(ctx, nil); err != nil || sensorComponentState(t, a) != "degraded" {
		t.Fatal("discovery recovered before its new selection was observed", err)
	}
	if err := a.updateInterfaces(ctx, []collector.InterfaceObservation{{Name: "labA", Index: 1}}); err != nil || sensorComponentState(t, a) != "running" {
		t.Fatal("completed unchanged selection did not recover", err)
	}
}

func TestDiscoveryRecoveryWaitsForNewSelectionAndFreshBatch(t *testing.T) {
	a := eventTestApp(t)
	ctx := context.Background()
	a.interfaceDiscoveryRequired = true
	a.sensorByInterface = make(map[string]time.Time)
	if err := a.updateInterfaces(ctx, []collector.InterfaceObservation{{Name: "labA", Index: 1}}); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	a.sensorByInterface["labA"] = time.Now().UTC()
	a.mu.Unlock()
	if err := a.refreshSensorState(ctx); err != nil || sensorComponentState(t, a) != "running" {
		t.Fatal("initial selection did not become healthy", err)
	}
	assertDegraded := func(stage string) {
		t.Helper()
		// Other sensor observations and heartbeat refreshes must also keep the
		// previous partial discovery authoritative until selection is replaced.
		if err := a.refreshSensorState(ctx); err != nil || sensorComponentState(t, a) != "degraded" {
			t.Fatalf("%s concealed incomplete coverage: %v", stage, err)
		}
	}
	if err := a.updateDiscovery(ctx, &collector.PartialDiscoveryError{}); err != nil {
		t.Fatal(err)
	}
	assertDegraded("partial discovery")
	if err := a.updateDiscovery(ctx, nil); err != nil {
		t.Fatal(err)
	}
	assertDegraded("successful discovery before new selection")
	if err := a.updateInterfaces(ctx, []collector.InterfaceObservation{{Name: "labB", Index: 2}}); err != nil {
		t.Fatal(err)
	}
	assertDegraded("new selection before first batch")
	a.mu.Lock()
	a.sensorByInterface["labB"] = time.Now().UTC()
	a.mu.Unlock()
	if err := a.refreshSensorState(ctx); err != nil || sensorComponentState(t, a) != "running" {
		t.Fatal("fresh replacement selection did not recover", err)
	}
}
