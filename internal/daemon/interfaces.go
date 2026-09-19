// SPDX-License-Identifier: MIT

package daemon

import (
	"context"
	"time"

	"github.com/littlesho/NodeRampart/internal/collector"
	"github.com/littlesho/NodeRampart/internal/protocol"
	"github.com/littlesho/NodeRampart/internal/store"
)

func (a *App) allowedSensorInterface(name string) bool {
	if !protocol.ValidInterfaceName(name) {
		return false
	}
	names := a.options.Config.Sensor.InterfaceNames()
	if len(names) == 0 {
		return true
	}
	for _, allowed := range names {
		if name == allowed {
			return true
		}
	}
	return false
}

func (a *App) sensorCoverageState(now time.Time) string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	fresh := func(at time.Time) bool {
		return !at.IsZero() && now.Sub(at) <= a.sensorStaleAfter() && at.Sub(now) <= a.sensorStaleAfter()
	}
	if a.interfaceDiscoveryRequired {
		if a.discoveryDegraded || !a.interfacesObserved || len(a.interfaces) == 0 {
			return "degraded"
		}
		for _, ref := range a.interfaces {
			if ref.Index <= 0 || !fresh(a.sensorByInterface[ref.Name]) {
				return "degraded"
			}
		}
		return "running"
	}
	// Direct, pre-Run users retain the single-feed status behavior. Run always
	// requires independently resolved interface coverage before declaring ready.
	if fresh(a.lastSensor) {
		return "running"
	}
	return "degraded"
}

func (a *App) updateDiscovery(ctx context.Context, discoveryErr error) error {
	child, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	a.mu.Lock()
	a.latestDiscoveryDegraded = discoveryErr != nil
	// A failed discovery immediately invalidates coverage. A successful one
	// becomes authoritative together with its selected interfaces below, so an
	// old fresh interface cannot create a spurious recovery before replacement.
	if a.latestDiscoveryDegraded {
		a.discoveryDegraded = true
	}
	a.mu.Unlock()
	if !a.options.Config.Sensor.Enabled {
		return nil
	}
	return a.refreshSensorState(child)
}

func (a *App) updateInterfaces(ctx context.Context, values []collector.InterfaceObservation) error {
	child, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	now := time.Now().UTC()
	a.mu.Lock()
	old := make(map[string]int, len(a.interfaces))
	for _, ref := range a.interfaces {
		old[ref.Name] = ref.Index
	}
	changed := len(old) != len(values)
	for _, ref := range values {
		if index, ok := old[ref.Name]; !ok || index != ref.Index {
			changed = true
			delete(a.sensorByInterface, ref.Name)
		}
	}
	if changed && a.interfacesObserved {
		start := a.interfaceSelectionAt
		if a.interfaceGap != nil {
			start = a.interfaceGap.Start
		}
		if start.IsZero() || start.After(now) {
			start = now
		}
		a.interfaceGap = &store.CoverageGap{Name: "sensor_feed", Reason: "interface_selection_changed", Start: start, End: now, Count: 0}
	}
	a.interfaceSelectionAt = now
	a.interfaces = append([]collector.InterfaceObservation(nil), values...)
	a.interfacesObserved = true
	a.discoveryDegraded = a.latestDiscoveryDegraded
	pending := a.interfaceGap
	a.mu.Unlock()
	// A topology change must not leave a previously healthy aggregate in place.
	if a.options.Config.Sensor.Enabled {
		if err := a.refreshSensorState(child); err != nil {
			return err
		}
	}
	if pending == nil {
		return nil
	}
	err := a.options.Store.RecordCoverageGap(child, *pending)
	a.recordWrite(err, "interface_coverage_gap", false)
	if err == nil {
		a.mu.Lock()
		if a.interfaceGap == pending {
			a.interfaceGap = nil
		}
		a.mu.Unlock()
	}
	return err
}
