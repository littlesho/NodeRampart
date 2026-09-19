// SPDX-License-Identifier: MIT

package collector

import (
	"context"
	"errors"
	"time"

	"github.com/littlesho/NodeRampart/internal/model"
)

type InterfaceObservation struct {
	Name        string    `json:"name"`
	Index       int       `json:"index"`
	State       string    `json:"state"`
	LastDeltaAt time.Time `json:"last_delta_at_utc,omitzero"`
}

type netBaseline struct {
	index     int
	counters  netCounters
	valid     bool
	lastDelta time.Time
}

func (n NetDev) runSet(ctx context.Context, output chan<- model.InterfaceTotals, ticks <-chan time.Time, resolve func(context.Context) ([]InterfaceRef, error), read func(string, string) (netCounters, error)) error {
	if n.Path == "" {
		n.Path = "/proc/net/dev"
	}
	baselines := make(map[string]netBaseline)
	reported, healthy := false, false
	report := func(err error) {
		if !reported || healthy != (err == nil) {
			if n.OnState != nil && n.OnState(err) != nil {
				return
			}
			reported, healthy = true, err == nil
		}
	}
	now := time.Now().UTC()
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		refs, err := resolve(ctx)
		var partial *PartialDiscoveryError
		usable := (err == nil || errors.As(err, &partial)) && len(refs) > 0 && len(refs) <= MaxInterfaces
		if !usable && err == nil {
			err = errors.New("interface selection unavailable or exceeds limit")
		}
		if n.OnDiscovery != nil {
			if callbackErr := n.OnDiscovery(err); callbackErr != nil {
				report(callbackErr)
			}
		}
		observations := []InterfaceObservation{}
		if !usable {
			baselines = make(map[string]netBaseline)
			report(err)
		} else {
			next := make(map[string]netBaseline, len(refs))
			allHealthy := err == nil
			readFailed := false
			for _, ref := range refs {
				if !ValidInterfaceName(ref.Name) {
					return errors.New("invalid resolved interface")
				}
				if _, duplicate := next[ref.Name]; duplicate {
					return errors.New("duplicate resolved interface")
				}
				previous := baselines[ref.Name]
				observation := InterfaceObservation{Name: ref.Name, Index: ref.Index, State: "degraded", LastDeltaAt: previous.lastDelta}
				current, readErr := read(n.Path, ref.Name)
				if ref.Index <= 0 {
					readErr = errors.New("interface unavailable")
				}
				if readErr != nil {
					readFailed = true
					previous.valid = false
					next[ref.Name] = previous
					allHealthy = false
					observations = append(observations, observation)
					continue
				}
				if previous.valid && previous.index == ref.Index && current.rxBytes >= previous.counters.rxBytes && current.txBytes >= previous.counters.txBytes && current.rxPackets >= previous.counters.rxPackets && current.txPackets >= previous.counters.txPackets {
					delta := model.InterfaceTotals{Interface: ref.Name, HourUTC: now.UTC(), RXBytes: current.rxBytes - previous.counters.rxBytes, TXBytes: current.txBytes - previous.counters.txBytes, RXPackets: current.rxPackets - previous.counters.rxPackets, TXPackets: current.txPackets - previous.counters.txPackets}
					select {
					case output <- delta:
						observation.State = "running"
						observation.LastDeltaAt = now.UTC()
					case <-ctx.Done():
						return nil
					}
				} else {
					allHealthy = false
				}
				next[ref.Name] = netBaseline{index: ref.Index, counters: current, valid: true, lastDelta: observation.LastDeltaAt}
				observations = append(observations, observation)
			}
			baselines = next
			if allHealthy {
				report(nil)
			} else if err != nil {
				report(err)
			} else if reported || readFailed {
				report(errors.New("interface counters incomplete; collecting valid baselines"))
			}
		}
		if n.OnInterfaces != nil {
			if err := n.OnInterfaces(observations); err != nil {
				report(err)
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case tick, ok := <-ticks:
			if !ok {
				return nil
			}
			now = tick
		}
	}
}
