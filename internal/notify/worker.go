// SPDX-License-Identifier: MIT

package notify

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"log/slog"
	"time"

	"github.com/littlesho/NodeRampart/internal/store"
)

type Worker struct {
	Store  *store.Store
	Sender Sender
	Logger *slog.Logger
}

func (w *Worker) Run(ctx context.Context) error {
	if w.Store == nil || w.Sender == nil {
		return nil
	}
	if w.Logger == nil {
		w.Logger = slog.Default()
	}
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		if err := w.process(ctx); err != nil && !errors.Is(err, context.Canceled) {
			w.Logger.Warn("notification outbox pass failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (w *Worker) process(ctx context.Context) error {
	now := time.Now().UTC()
	messages, err := w.Store.Pending(ctx, now, 20)
	if err != nil {
		return err
	}
	// Pending excludes durable cooldowns. This bounded set also suppresses rows
	// already fetched before a destination was rate limited in this pass.
	blockedDestinations := make(map[string]bool, len(messages))
	for _, message := range messages {
		if err := ctx.Err(); err != nil {
			return err
		}
		if blockedDestinations[message.Destination] {
			continue
		}
		message, eligible, err := w.Store.ClaimNotification(ctx, message.ID, time.Now().UTC())
		if err != nil {
			return err
		}
		if !eligible {
			continue
		}
		deliveryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err = w.Sender.Send(deliveryCtx, message.Body)
		cancel()
		if err == nil {
			if err := w.Store.MarkSent(ctx, message.ID, time.Now().UTC()); err != nil {
				return err
			}
			continue
		}
		if err := ctx.Err(); err != nil {
			releaseCtx, stop := context.WithTimeout(context.Background(), 2*time.Second)
			_ = w.Store.ReleaseNotificationClaim(releaseCtx, message.ID)
			stop()
			return err
		}
		delay := retryDelay(message.Attempts)
		// Sender errors can contain request URLs or response bodies. Persist only
		// fixed categories and the numeric codes supplied by our Telegram client.
		reason := "notification delivery failed"
		var delivery *DeliveryError
		if errors.As(err, &delivery) {
			reason = delivery.Error()
			if delivery.RateLimited || delivery.RetryAfter > 0 || delivery.SuspendDestination {
				if delivery.RetryAfter > 0 {
					delay = delivery.RetryAfter
				} else if delay < 30*time.Second {
					delay = 30 * time.Second
				}
				until := time.Now().UTC().Add(delay)
				if delivery.SuspendDestination {
					until = time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)
				}
				if err := w.Store.MarkRateLimited(ctx, message.ID, message.Destination, until, reason); err != nil {
					return err
				}
				blockedDestinations[message.Destination] = true
				continue
			}
			if delivery.Permanent {
				if err := w.Store.MarkQuarantined(ctx, message.ID, time.Now().UTC(), reason); err != nil {
					return err
				}
				continue
			}
		}
		if err := w.Store.MarkFailed(ctx, message.ID, time.Now().UTC().Add(delay), reason); err != nil {
			return err
		}
	}
	return nil
}

func retryDelay(attempts int) time.Duration {
	if attempts < 0 {
		attempts = 0
	}
	if attempts > 8 {
		attempts = 8
	}
	base := time.Second * time.Duration(1<<attempts)
	var random [8]byte
	_, _ = rand.Read(random[:])
	jitter := time.Duration(binary.LittleEndian.Uint64(random[:]) % uint64(base/2+1))
	if base+jitter > time.Hour {
		return time.Hour
	}
	return base + jitter
}
