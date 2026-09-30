// SPDX-License-Identifier: MIT

package daemon

import (
	"context"
	"sync"
	"time"
)

// sensorReadGate shapes reads instead of rejecting buffered legitimate data.
// The gate belongs to the listener, so reconnecting does not reset the rate.
// At most one configured round of interface frames may arrive together. One
// frame is decoded at a time, with no additional burst buffer.
type sensorReadGate struct {
	mu       sync.Mutex
	interval time.Duration
	burst    int
	next     time.Time
}

func (g *sensorReadGate) wait(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	g.mu.Lock()
	now := time.Now()
	if g.next.Before(now) {
		g.next = now
	}
	// The virtual schedule permits only burst-1 intervals of lead. The next
	// frame then waits at the same sustained rate used before burst allowance.
	ready := g.next.Add(-time.Duration(max(g.burst, 1)-1) * g.interval)
	g.next = g.next.Add(g.interval)
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

// readComplete charges an idle read at its actual completion time. Waiting for
// the first frame must not replenish that frame's token and create a burst+1
// allowance when the sensor finally emits its configured round.
func (g *sensorReadGate) readComplete() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if next := time.Now().Add(g.interval); g.next.Before(next) {
		g.next = next
	}
}
