// SPDX-License-Identifier: MIT

package collector

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/littlesho/NodeRampart/internal/model"
)

func TestNetDevSetRebasesReplacementsAndMissingCounters(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ticks := make(chan time.Time)
	out := make(chan model.InterfaceTotals, 32)
	observed := make(chan []InterfaceObservation, 1)
	states := make(chan bool, 16)
	frames := []struct {
		refs   []InterfaceRef
		values map[string]uint64
	}{
		{[]InterfaceRef{{"labA", 1}, {"labB", 2}}, map[string]uint64{"labA": 100, "labB": 200}},
		{[]InterfaceRef{{"labA", 1}, {"labB", 2}}, map[string]uint64{"labA": 200, "labB": 400}},
		{[]InterfaceRef{{"labA", 1}, {"labB", 9}}, map[string]uint64{"labA": 300, "labB": 9000}},
		{[]InterfaceRef{{"labC", 3}, {"labB", 9}}, map[string]uint64{"labC": 1000, "labB": 9400}},
		{[]InterfaceRef{{"labC", 3}, {"labB", 9}}, map[string]uint64{"labC": 1050}},
		{[]InterfaceRef{{"labC", 3}, {"labB", 9}}, map[string]uint64{"labC": 1100, "labB": 10000}},
		{[]InterfaceRef{{"labC", 3}, {"labB", 9}}, map[string]uint64{"labC": 1150, "labB": 10100}},
	}
	round := -1
	n := NetDev{OnState: func(err error) error { states <- err == nil; return nil }, OnInterfaces: func(v []InterfaceObservation) error { observed <- v; return nil }}
	done := make(chan error, 1)
	go func() {
		done <- n.runSet(ctx, out, ticks, func(context.Context) ([]InterfaceRef, error) {
			round++
			return frames[round].refs, nil
		}, func(_, name string) (netCounters, error) {
			v, ok := frames[round].values[name]
			if !ok {
				return netCounters{}, errors.New("missing counters")
			}
			return netCounters{rxBytes: v, txBytes: v, rxPackets: v, txPackets: v}, nil
		})
	}()
	for i := range frames {
		if i > 0 {
			netDevTick(t, ticks, time.Now().Add(time.Duration(i)*time.Second))
		}
		select {
		case v := <-observed:
			if len(v) != 2 {
				t.Fatalf("observations=%v", v)
			}
			if (i == 2 || i == 4 || i == 5) && v[1].State != "degraded" {
				t.Fatalf("replacement/missing counters became healthy: %+v", v)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("collector did not complete poll")
		}
	}
	cancel()
	netDevDone(t, done)
	totals := map[string]uint64{}
	for len(out) > 0 {
		delta := <-out
		totals[delta.Interface] += delta.RXBytes
	}
	if !reflect.DeepEqual(totals, map[string]uint64{"labA": 200, "labB": 700, "labC": 150}) {
		t.Fatalf("invented or lost deltas: %v", totals)
	}
	if got := netDevStates(states); !reflect.DeepEqual(got, []bool{true, false, true}) {
		t.Fatalf("aggregate coverage=%v", got)
	}
}
