// SPDX-License-Identifier: MIT

package daemon

import (
	"context"
	"time"
)

func (a *App) refreshSensorState(ctx context.Context) error {
	return a.setSensorState(ctx, "")
}

// setSensorState serializes both startup states and runtime recomputation with
// their durable commit. An empty state means recompute after acquiring the
// gate, never reuse a health decision made while another transition ran.
func (a *App) setSensorState(ctx context.Context, state string) error {
	a.sensorStateGateOnce.Do(func() { a.sensorStateGate = make(chan struct{}, 1) })
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case a.sensorStateGate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-a.sensorStateGate }()
	if err := ctx.Err(); err != nil {
		return err
	}
	if state == "" {
		state = a.sensorCoverageState(time.Now().UTC())
	}
	a.mu.RLock()
	committed := a.sensorState
	a.mu.RUnlock()
	if committed == state {
		return nil
	}
	err := a.options.Store.SetComponentStatus(ctx, "sensor_feed", state, time.Now().UTC())
	a.mu.Lock()
	if err == nil {
		a.sensorState = state
	} else {
		// Invalidate the cache on failure so the next observation retries,
		// including a return to the previously committed state.
		a.sensorState = ""
	}
	a.mu.Unlock()
	if err != nil {
		a.options.Logger.Warn("record sensor coverage", "state", state, "error", err)
	}
	return err
}
