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

func TestNetDevRetriesMissingDefaultRoute(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ticks := make(chan time.Time)
	output := make(chan model.InterfaceTotals, 8)
	states := make(chan bool, 8)
	resolutions, reads := 0, 0
	n := NetDev{OnState: func(err error) error { states <- err == nil; return nil }}
	done := make(chan error, 1)
	go func() {
		done <- n.run(ctx, output, ticks, func() (string, error) {
			resolutions++
			if resolutions < 3 {
				return "", errors.New("default route not ready")
			}
			return "lab0", nil
		}, func(path, iface string) (netCounters, error) {
			if path != "/proc/net/dev" || iface != "lab0" {
				t.Errorf("unexpected source: %s %s", path, iface)
			}
			reads++
			return netCounters{rxBytes: 1_000_000 + uint64(reads)*42, rxPackets: 100 + uint64(reads)}, nil
		})
	}()
	now := time.Now().UTC()
	for i := 1; i <= 3; i++ {
		netDevTick(t, ticks, now.Add(time.Duration(i)*time.Second))
	}
	got := netDevTotals(t, output)
	if got.RXBytes != 42 || got.RXPackets != 1 || !got.HourUTC.Equal(now.Add(3*time.Second)) {
		t.Fatalf("recovery included pre-baseline counters: %+v", got)
	}
	cancel()
	netDevDone(t, done)
	if resolutions != 3 || reads != 2 {
		t.Fatalf("unexpected retry counts: resolutions=%d reads=%d", resolutions, reads)
	}
	if got := netDevStates(states); !reflect.DeepEqual(got, []bool{false, true}) {
		t.Fatalf("coverage transitions = %v", got)
	}
}

func TestNetDevReadFailureAndResetRebaseCounters(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ticks := make(chan time.Time)
	output := make(chan model.InterfaceTotals, 8)
	states := make(chan bool, 8)
	values := []uint64{100, 120, 0, 0, 1000, 1030, 5, 12}
	reads := 0
	n := NetDev{Interface: "fixed0", Path: "fixture", OnState: func(err error) error { states <- err == nil; return nil }}
	done := make(chan error, 1)
	go func() {
		done <- n.run(ctx, output, ticks, func() (string, error) {
			t.Error("configured interface must not resolve the default route")
			return "", errors.New("unexpected lookup")
		}, func(path, iface string) (netCounters, error) {
			if path != "fixture" || iface != "fixed0" {
				t.Errorf("collector changed the selected interface: %s %s", path, iface)
			}
			i := reads
			reads++
			if i == 2 || i == 3 {
				return netCounters{}, errors.New("interface temporarily unavailable")
			}
			v := values[i]
			return netCounters{rxBytes: v, rxPackets: v, txBytes: v, txPackets: v}, nil
		})
	}()
	now := time.Now().UTC()
	for i := 1; i < len(values); i++ {
		netDevTick(t, ticks, now.Add(time.Duration(i)*time.Second))
	}
	for _, want := range []uint64{20, 30, 7} {
		got := netDevTotals(t, output)
		if got.RXBytes != want || got.TXBytes != want || got.RXPackets != want || got.TXPackets != want {
			t.Fatalf("unexpected delta across gap or reset: %+v, want %d", got, want)
		}
	}
	cancel()
	netDevDone(t, done)
	if reads != len(values) || len(output) != 0 {
		t.Fatalf("unexpected samples: reads=%d extra totals=%d", reads, len(output))
	}
	if got := netDevStates(states); !reflect.DeepEqual(got, []bool{true, false, true, false, true}) {
		t.Fatalf("coverage transitions = %v", got)
	}
}

func TestNetDevCancellationDuringRetryAndBlockedOutput(t *testing.T) {
	for _, blockedOutput := range []bool{false, true} {
		t.Run(map[bool]string{false: "retry", true: "blocked output"}[blockedOutput], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ticks := make(chan time.Time)
			readCalled := make(chan struct{}, 2)
			done := make(chan error, 1)
			go func() {
				done <- (NetDev{Interface: "fixed0"}).run(ctx, make(chan model.InterfaceTotals), ticks, nil, func(string, string) (netCounters, error) {
					readCalled <- struct{}{}
					if !blockedOutput {
						return netCounters{}, errors.New("read failure")
					}
					return netCounters{}, nil
				})
			}()
			select {
			case <-readCalled:
			case <-time.After(2 * time.Second):
				t.Fatal("collector did not attempt initial read")
			}
			if blockedOutput {
				netDevTick(t, ticks, time.Now())
				select {
				case <-readCalled:
				case <-time.After(2 * time.Second):
					t.Fatal("collector did not attempt delta read")
				}
			}
			cancel()
			netDevDone(t, done)
		})
	}
}

func TestNetDevRetriesFailedCoveragePersistence(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ticks := make(chan time.Time)
	output := make(chan model.InterfaceTotals, 8)
	values := []uint64{100, 120, 0, 0, 0, 1000, 1030, 1050, 1060}
	reads := 0
	var attempted, persisted []bool
	n := NetDev{Interface: "fixed0", OnState: func(err error) error {
		healthy := err == nil
		attempted = append(attempted, healthy)
		// Fail the first degraded transition and the first recovery update.
		if len(attempted) == 2 || len(attempted) == 4 {
			return errors.New("coverage database temporarily unavailable")
		}
		persisted = append(persisted, healthy)
		return nil
	}}
	done := make(chan error, 1)
	go func() {
		done <- n.run(ctx, output, ticks, nil, func(string, string) (netCounters, error) {
			i := reads
			reads++
			if i >= 2 && i <= 4 {
				return netCounters{}, errors.New("interface temporarily unavailable")
			}
			return netCounters{rxBytes: values[i]}, nil
		})
	}()
	now := time.Now().UTC()
	for i := 1; i < len(values); i++ {
		netDevTick(t, ticks, now.Add(time.Duration(i)*time.Second))
	}
	for _, want := range []uint64{20, 30, 20, 10} {
		if got := netDevTotals(t, output); got.RXBytes != want {
			t.Fatalf("coverage retry changed traffic delta: got %d, want %d", got.RXBytes, want)
		}
	}
	cancel()
	netDevDone(t, done)
	if !reflect.DeepEqual(attempted, []bool{true, false, false, true, true}) {
		t.Fatalf("coverage attempts = %v; failed transitions must retry and successful ones deduplicate", attempted)
	}
	if !reflect.DeepEqual(persisted, []bool{true, false, true}) {
		t.Fatalf("persisted coverage transitions = %v", persisted)
	}
	if reads != len(values) || len(output) != 0 {
		t.Fatalf("unexpected samples: reads=%d extra totals=%d", reads, len(output))
	}
}

func netDevTick(t *testing.T, ticks chan<- time.Time, now time.Time) {
	t.Helper()
	select {
	case ticks <- now:
	case <-time.After(2 * time.Second):
		t.Fatal("collector stopped retrying")
	}
}

func netDevTotals(t *testing.T, output <-chan model.InterfaceTotals) model.InterfaceTotals {
	t.Helper()
	select {
	case value := <-output:
		return value
	case <-time.After(2 * time.Second):
		t.Fatal("collector did not recover")
		return model.InterfaceTotals{}
	}
}

func netDevDone(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("collector did not stop on cancellation")
	}
}

func netDevStates(states <-chan bool) []bool {
	var result []bool
	for len(states) > 0 {
		result = append(result, <-states)
	}
	return result
}
