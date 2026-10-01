// SPDX-License-Identifier: MIT

package notify

import (
	"context"
	"errors"
	"testing"
	"time"
)

type pacedSender struct{ senderFunc }

func (p pacedSender) MinimumInterval() time.Duration { return 3 * time.Second }

func TestNativeAttemptPacingIncludesFailuresAndFreshPrefetch(t *testing.T) {
	for _, result := range []string{"success", "transient", "permanent"} {
		t.Run(result, func(t *testing.T) {
			db := openNotifyStore(t)
			now := time.Now().UTC()
			for _, id := range []string{"paced_first", "paced_second", "paced_third"} {
				enqueueSynthetic(t, db, id, "native_paced", now.Add(-time.Second))
			}
			enqueueSynthetic(t, db, "independent", "other", now)
			calls := 0
			sender := pacedSender{senderFunc(func(context.Context, string) error {
				calls++
				switch result {
				case "transient":
					return errors.New("synthetic failure")
				case "permanent":
					return &DeliveryError{Channel: "wecom", Permanent: true, InvalidPayload: true}
				}
				return nil
			})}
			worker := &Worker{Store: db, Destination: "native_paced", Sender: sender}
			if err := worker.process(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err := worker.process(context.Background()); err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatal("fresh or retried rows bypassed durable attempt floor", calls)
			}
			status := queueStatus(t, db)
			if len(status.Cooldowns) != 1 || status.Cooldowns[0].Until.Before(now.Add(3*time.Second)) {
				t.Fatal("attempt floor not persisted")
			}
			otherCalls := 0
			other := &Worker{Store: db, Destination: "other", Sender: senderFunc(func(context.Context, string) error { otherCalls++; return nil })}
			if err := other.process(context.Background()); err != nil || otherCalls != 1 {
				t.Fatal("paced failure blocked independent destination", err)
			}
		})
	}
}
