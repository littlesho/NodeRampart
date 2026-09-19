// SPDX-License-Identifier: MIT
package collector

import (
	"context"
	"errors"
	"github.com/littlesho/NodeRampart/internal/model"
	"reflect"
	"testing"
	"time"
)

func TestPartialDiscoveryPreservesHealthyCounterBaselineAndReportsDegraded(t *testing.T) {
	for _, partial := range []bool{true, false} {
		t.Run(map[bool]string{true: "typed partial", false: "ordinary error fails closed"}[partial], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ticks := make(chan time.Time)
			out := make(chan model.InterfaceTotals, 8)
			observed := make(chan []InterfaceObservation, 1)
			states := make(chan bool, 8)
			discoveries := make(chan bool, 8)
			round := -1
			n := NetDev{OnDiscovery: func(err error) error { discoveries <- err == nil; return nil }, OnState: func(err error) error { states <- err == nil; return nil }, OnInterfaces: func(v []InterfaceObservation) error { observed <- v; return nil }}
			done := make(chan error, 1)
			go func() {
				done <- n.runSet(ctx, out, ticks, func(context.Context) ([]InterfaceRef, error) {
					round++
					refs := []InterfaceRef{{Name: "labA", Index: 1}}
					if round == 1 {
						if partial {
							return refs, &PartialDiscoveryError{}
						}
						return refs, errors.New("incomplete arbitrary resolver failure")
					}
					return refs, nil
				}, func(_, _ string) (netCounters, error) {
					v := uint64(100 * (round + 1))
					return netCounters{rxBytes: v, txBytes: v, rxPackets: v, txPackets: v}, nil
				})
			}()
			for i := range 4 {
				if i > 0 {
					netDevTick(t, ticks, time.Now().Add(time.Duration(i)*time.Second))
				}
				select {
				case values := <-observed:
					if len(discoveries) != i+1 {
						t.Fatal("discovery callback did not precede observations")
					}
					if i == 1 && partial && (len(values) != 1 || values[0].State != "running") {
						t.Fatal("healthy partial-link delta was lost")
					}
					if i == 1 && !partial && len(values) != 0 {
						t.Fatal("ordinary error was treated as trusted partial discovery")
					}
				case <-time.After(2 * time.Second):
					t.Fatal("collector did not poll")
				}
			}
			cancel()
			netDevDone(t, done)
			var got uint64
			for len(out) > 0 {
				got += (<-out).RXBytes
			}
			want := uint64(300)
			if !partial {
				want = 100
			}
			if got != want {
				t.Fatalf("counter baseline continuity=%d want=%d", got, want)
			}
			if got := netDevStates(states); !reflect.DeepEqual(got, []bool{false, true}) {
				t.Fatalf("degraded/recovered states=%v", got)
			}
		})
	}
}
