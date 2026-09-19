// SPDX-License-Identifier: MIT

package daemon

import (
	"context"
	"sync"
	"time"
)

// sensorReadGate shapes reads instead of rejecting buffered legitimate data.
// The gate belongs to the listener, so reconnecting does not reset the rate.
// One frame is decoded at a time, with no additional unbounded burst buffer.
type sensorReadGate struct {
	mu       sync.Mutex
	interval time.Duration
	next     time.Time
}

func (g *sensorReadGate) wait(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	g.mu.Lock()
	now := time.Now()
	ready := g.next
	if ready.Before(now) {
		ready = now
	}
	g.next = ready.Add(g.interval)
	g.mu.Unlock()
	if delay := ready.Sub(now); delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return ctx.Err()
}
