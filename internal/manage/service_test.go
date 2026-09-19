// SPDX-License-Identifier: MIT

package manage

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestNativeStartLimitWaitIsBoundedAndSpecific(t *testing.T) {
	for _, tc := range []struct {
		name, result, interval string
		cancel, success        bool
		attempts               int
	}{
		{"native limit", "start-limit-hit", "5ms", false, true, 2},
		{"ordinary crash", "exit-code", "5ms", false, false, 1},
		{"long policy", "start-limit-hit", "1min", false, false, 1},
		{"cancellation", "start-limit-hit", "1s", true, false, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := fixtureManager(t)
			attempts := 0
			m.runner = func(ctx context.Context, program string, args ...string) (string, error) {
				if err := ctx.Err(); err != nil {
					return "", err
				}
				if strings.Contains(strings.Join(args, " "), "reset-failed") {
					t.Fatal("native start limit bypassed")
				}
				if args[0] == "restart" {
					attempts++
					if attempts == 1 {
						return "", errors.New("synthetic failed start")
					}
					return "", nil
				}
				switch args[1] {
				case "--property=Result":
					return tc.result, nil
				case "--property=StartLimitIntervalUSec":
					return tc.interval, nil
				}
				t.Fatal("unexpected service operation")
				return "", nil
			}
			ctx := context.Background()
			if tc.cancel {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 10*time.Millisecond)
				defer cancel()
			}
			err := m.serviceOperation(ctx, "restart", "noderampartd.service")
			if (err == nil) != tc.success || attempts != tc.attempts {
				t.Fatalf("wrong bounded retry: attempts=%d err=%v", attempts, err)
			}
			if tc.cancel && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("native limit wait ignored cancellation")
			}
		})
	}
}
