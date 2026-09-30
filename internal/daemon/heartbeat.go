// SPDX-License-Identifier: MIT

package daemon

import (
	"context"
	"errors"
	"time"

	"github.com/littlesho/NodeRampart/internal/notify"
	"github.com/littlesho/NodeRampart/internal/version"
)

// No durable heartbeat queue: each attempt describes a fresh observation. One
// retry is bounded by the configured HTTP deadline and never blocks collectors.
func (a *App) runHeartbeat(ctx context.Context) error {
	for {
		if ctx.Err() != nil {
			return nil
		}
		err := a.sendHeartbeat(ctx)
		if err != nil && ctx.Err() == nil && heartbeatRetryable(err) {
			timer := time.NewTimer(time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil
			case <-timer.C:
			}
			err = a.sendHeartbeat(ctx)
		}
		if ctx.Err() != nil {
			return nil
		}
		state := "running"
		if err != nil {
			state = "degraded"
		}
		if err := a.options.Store.SetComponentStatus(ctx, "heartbeat", state, time.Now().UTC()); err != nil {
			a.options.Logger.Warn("record heartbeat state failed")
		}
		wait := a.options.Config.Heartbeat.Interval.Duration
		var delivery *notify.DeliveryError
		if errors.As(err, &delivery) {
			if delivery.SuspendDestination {
				<-ctx.Done()
				return nil
			}
			if delivery.RetryAfter > wait {
				wait = delivery.RetryAfter
			}
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}

func (a *App) sendHeartbeat(ctx context.Context) error {
	status := a.Status(ctx)
	diagnosis := Diagnose(status, false)
	reasons := []string{}
	for _, check := range diagnosis.Checks {
		if (check.State == "unknown" || check.State == "degraded") && len(reasons) < 16 {
			reasons = append(reasons, check.Reason)
		}
	}
	work, cancel := context.WithTimeout(ctx, a.options.Config.Heartbeat.Timeout.Duration)
	defer cancel()
	return a.options.HeartbeatSender.Send(work, notify.HeartbeatPayload{InstanceID: a.options.Config.Heartbeat.InstanceID, At: time.Now().UTC(), Version: version.Version, Alive: true, Health: diagnosis.State, Reasons: reasons})
}

func heartbeatRetryable(err error) bool {
	var delivery *notify.DeliveryError
	if errors.As(err, &delivery) {
		return !delivery.Permanent && !delivery.RateLimited && delivery.RetryAfter == 0 && !delivery.SuspendDestination
	}
	return true
}
