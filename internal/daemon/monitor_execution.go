// SPDX-License-Identifier: MIT

package daemon

import (
	"context"
	"strings"
	"time"
)

// Check execution is live process state. Success means known observations, not
// healthy components, durable notifications or delivery. It resets on restart.
type MonitorCheckStatus struct {
	Group            string    `json:"group"`
	State            string    `json:"state"`
	LastAttemptAt    time.Time `json:"last_attempt_at_utc,omitzero"`
	LastCompletedAt  time.Time `json:"last_completed_at_utc,omitzero"`
	LastSuccessfulAt time.Time `json:"last_successful_check_utc,omitzero"`
	DurationMS       int64     `json:"duration_milliseconds"`
	DelayMS          int64     `json:"delay_milliseconds"`
	InProgress       bool      `json:"in_progress"`
}

func updateMonitorAges(status *MonitorStatus, now time.Time) {
	for i := range status.Rules {
		r := &status.Rules[i]
		if r.Pending && !r.PendingSince.IsZero() {
			r.PendingAgeMS = max(0, now.Sub(r.PendingSince).Milliseconds())
		}
	}
	for i := range status.Checks {
		c := &status.Checks[i]
		if !c.LastAttemptAt.IsZero() && c.State != "disabled" {
			c.DelayMS = max(0, now.Sub(c.LastAttemptAt).Milliseconds()-time.Minute.Milliseconds())
			if c.InProgress {
				c.DurationMS = max(0, now.Sub(c.LastAttemptAt).Milliseconds())
			}
		}
	}
}

func (m *monitorRuntime) pass(ctx context.Context, now time.Time, supplied []monitorObservation) {
	passStarted := time.Now()
	templates := supplied
	if templates == nil {
		for _, key := range monitorKeys {
			enabled := monitorEnabled(key, m.app.options.Config.Alerts)
			templates = append(templates, monitorObservation{Now: now, Status: MonitorRuleStatus{Key: key, Enabled: enabled, Available: !enabled, State: "disabled", Reason: "disabled"}, Condition: "disabled"})
		}
	}
	// Keep query order (including the bounded injected observations used in tests)
	// while publishing the independent health phase before historical analysis.
	m.app.monitorMu.Lock()
	status := m.app.monitorStatus
	if status.Rules == nil {
		status = initialMonitorStatus(m.app.options.Config.Alerts)
	}
	previous := map[string]MonitorRuleStatus{}
	for _, r := range status.Rules {
		previous[r.Key] = r
	}
	status.Rules = nil
	for _, in := range templates {
		r, ok := previous[in.Status.Key]
		if !ok {
			r = in.Status
		}
		status.Rules = append(status.Rules, r)
	}
	status.CheckedAt = now
	m.app.monitorStatus = status
	m.app.monitorMu.Unlock()
	for phase, group := range []string{"health", "budget"} {
		var observations []monitorObservation
		for _, in := range templates {
			if strings.HasPrefix(in.Status.Key, group+"_") {
				observations = append(observations, in)
			}
		}
		if len(observations) == 0 {
			continue
		}
		enabled := false
		for _, in := range observations {
			enabled = enabled || in.Status.Enabled
		}
		started := time.Now()
		phaseNow := now.Add(time.Since(passStarted))
		if supplied == nil {
			for i := range observations {
				observations[i].Now = phaseNow
			}
		}
		m.app.monitorMu.Lock()
		check := m.app.monitorStatus.Checks[phase]
		check.LastAttemptAt, check.InProgress, check.State = phaseNow, true, "running"
		check.DurationMS, check.DelayMS = 0, 0
		m.app.monitorStatus.Checks[phase] = check
		m.app.monitorMu.Unlock()
		bounded, cancel := context.WithTimeout(ctx, 15*time.Second)
		readErr := m.reload(bounded)
		if supplied == nil && enabled {
			if group == "health" {
				m.app.observeHealth(bounded, phaseNow, observations, m.states)
			} else {
				m.budgetObserver(bounded, phaseNow, observations, m.states)
			}
		}
		views := m.applyObservations(bounded, observations, readErr)
		ended := phaseNow.Add(time.Since(started))
		check.LastCompletedAt, check.DurationMS, check.InProgress = ended, time.Since(started).Milliseconds(), false
		check.State = "completed"
		known := readErr == nil
		for _, view := range views {
			if view.Enabled && !view.Available {
				known = false
			}
		}
		if !enabled {
			check.State = "disabled"
		} else if bounded.Err() != nil {
			check.State = "timeout"
		} else if !known {
			check.State = "partial"
		} else {
			check.LastSuccessfulAt = ended
		}
		cancel()
		m.app.monitorMu.Lock()
		status = m.app.monitorStatus
		status.Checks[phase] = check
		for _, view := range views {
			for i := range status.Rules {
				if status.Rules[i].Key == view.Key {
					status.Rules[i] = view
					break
				}
			}
		}
		status.Pending = 0
		status.Available = true
		for _, r := range status.Rules {
			if r.Pending {
				status.Pending++
			}
			if r.Enabled && !r.Available {
				status.Available = false
			}
		}
		m.app.monitorStatus = status
		m.app.monitorMu.Unlock()
	}
}
