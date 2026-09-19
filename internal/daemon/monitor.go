// SPDX-License-Identifier: MIT

package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/littlesho/NodeRampart/internal/model"
	"github.com/littlesho/NodeRampart/internal/store"
)

type monitorRepository interface {
	MonitorStates(context.Context) ([]store.MonitorState, error)
	CommitMonitorState(context.Context, store.MonitorState, int64, []model.Event, []*store.OutboxMessage) error
}

type monitorPending struct {
	observation monitorObservation
	next        monitorData
	record      store.MonitorState
	expected    int64
	events      []model.Event
	messages    []*store.OutboxMessage
	since       time.Time
}

type monitorRuntime struct {
	app            *App
	repository     monitorRepository
	records        map[string]store.MonitorState
	states         map[string]monitorData
	invalid        map[string]bool
	pending        map[string]*monitorPending
	loaded         bool
	budgetObserver func(context.Context, time.Time, []monitorObservation, map[string]monitorData)
}

func newMonitorRuntime(a *App) *monitorRuntime {
	return &monitorRuntime{app: a, repository: a.options.Store, records: map[string]store.MonitorState{}, states: map[string]monitorData{}, invalid: map[string]bool{}, pending: map[string]*monitorPending{}, budgetObserver: a.observeBudgets}
}

func (a *App) runMonitor(ctx context.Context) {
	runtime := newMonitorRuntime(a)
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		runtime.pass(ctx, time.Now().UTC(), nil)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (m *monitorRuntime) reload(ctx context.Context) error {
	query, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	records, err := m.repository.MonitorStates(query)
	if err != nil {
		return err
	}
	for _, record := range records {
		data, err := decodeMonitorData(record.Data, record.Key)
		m.invalid[record.Key] = err != nil || !data.ObservedAt.UTC().Truncate(time.Millisecond).Equal(record.UpdatedAt)
		if !m.invalid[record.Key] {
			m.records[record.Key] = record
			m.states[record.Key] = data
		}
	}
	m.loaded = true
	return nil
}

// applyObservations is called only by the monitor worker, after each bounded
// group's observation phase. A pending event or month-close transition is frozen.
func (m *monitorRuntime) applyObservations(ctx context.Context, observations []monitorObservation, readErr error) []MonitorRuleStatus {
	views := make([]MonitorRuleStatus, 0, len(observations))
	for _, observation := range observations {
		key := observation.Status.Key
		view := observation.Status
		if !view.Enabled && strings.HasPrefix(key, "budget_") {
			view.State, view.Reason, view.Available = "disabled", "disabled", true
		} else if !m.loaded || m.invalid[key] {
			view.State, view.Reason, view.Available = "unknown", "read_failed", false
			if m.invalid[key] {
				view.Reason = "state_invalid"
			}
		} else {
			if pending := m.pending[key]; pending != nil {
				if len(pending.events) == 0 && !pending.observation.Closing && !needsMonthClose(pending.next, observation) && !observation.Now.Before(pending.next.ObservedAt) {
					if observation.Condition == "failed" && !pending.next.ConditionSince.IsZero() {
						observation.Since = pending.next.ConditionSince
					}
					next, events := evaluateMonitor(pending.next, observation, m.app.options.Config.Alerts, m.app.started)
					if data, err := json.Marshal(next); err == nil && validateMonitorStatus(next.Status) == nil && validateMonthData(next, key) == nil {
						pending.next = next
						pending.record.Data = data
						pending.record.UpdatedAt = next.ObservedAt
						pending.observation = observation
						pending.events = events
						pending.messages = make([]*store.OutboxMessage, len(events))
						for i := range events {
							pending.events[i], pending.messages[i] = m.app.prepareEvent(events[i])
						}
					}
				}
				if !m.commit(ctx, key) {
					view = pending.next.Status
					view.Pending = true
					view.Available = false
				}
			}
			if m.pending[key] == nil {
				previous := m.states[key]
				if observation.Now.Before(previous.ObservedAt) || observation.Status.Period != "" && previous.Period != "" && observation.Status.Period < previous.Period {
					view = previous.Status
					view.State, view.Reason, view.Available = "unknown", "clock_rollback", false
				} else {
					closed := true
					if needsMonthClose(previous, observation) {
						closeObservation, err := m.app.closingObservation(ctx, observation.Now, previous)
						if err != nil {
							closed = false
							view = previous.Status
							view.State, view.Reason, view.Available = "unknown", "period_close_pending", false
						} else {
							view = m.storeObservation(ctx, closeObservation)
							closed = m.pending[key] == nil && m.states[key].MonthFinalized
						}
					}
					if closed {
						view = m.storeObservation(ctx, observation)
					}
				}
			}
		}
		if readErr != nil && !view.Pending && view.Enabled {
			view.Available = false
			view.State, view.Reason = "unknown", "read_failed"
		}
		if pending := m.pending[key]; pending != nil {
			view.Pending = true
			view.PendingSince = pending.since
		}
		if record := m.records[key]; record.Revision > 0 {
			view.LastPersistedObservation = record.UpdatedAt
		}
		if view.Pending {
			view.Milestone = 0
			view.LastNotification = time.Time{}
			durable := m.states[key]
			if view.Period == durable.Period {
				view.Milestone = durable.Milestone
				view.LastNotification = durable.LastNotification
			}
		}
		views = append(views, view)
	}
	return views
}

func (m *monitorRuntime) storeObservation(ctx context.Context, observation monitorObservation) MonitorRuleStatus {
	key := observation.Status.Key
	next, events := evaluateMonitor(m.states[key], observation, m.app.options.Config.Alerts, m.app.started)
	data, err := json.Marshal(next)
	if err != nil || len(data) > 16384 || validateMonitorStatus(next.Status) != nil || validateMonthData(next, key) != nil {
		view := observation.Status
		view.State, view.Reason, view.Available = "unknown", "state_invalid", false
		return view
	}
	if !observation.Status.Enabled && m.records[key].Revision == 0 || bytes.Equal(m.records[key].Data, data) {
		return next.Status
	}
	messages := make([]*store.OutboxMessage, len(events))
	for i := range events {
		events[i], messages[i] = m.app.prepareEvent(events[i])
	}
	m.pending[key] = &monitorPending{observation: observation, next: next, record: store.MonitorState{Key: key, UpdatedAt: next.ObservedAt, Data: data}, expected: m.records[key].Revision, events: events, messages: messages, since: observation.Now}
	if m.commit(ctx, key) {
		return m.states[key].Status
	}
	view := next.Status
	view.Pending = true
	view.Available = false
	return view
}

func (m *monitorRuntime) commit(ctx context.Context, key string) bool {
	pending := m.pending[key]
	if pending == nil {
		return true
	}
	// A canceled commit can have an unknown outcome. A subsequent authoritative
	// read of the identical state acknowledges it without duplicating events.
	if record := m.records[key]; record.Revision > pending.expected && bytes.Equal(record.Data, pending.record.Data) {
		delete(m.pending, key)
		return true
	}
	write, cancel := context.WithTimeout(ctx, 2*time.Second)
	err := m.repository.CommitMonitorState(write, pending.record, pending.expected, pending.events, pending.messages)
	cancel()
	if errors.Is(err, store.ErrMonitorConflict) {
		if m.reload(ctx) != nil || m.invalid[key] {
			return false
		}
		previous := m.states[key]
		if previous.ObservedAt.After(pending.observation.Now) {
			delete(m.pending, key)
			return true
		}
		if bytes.Equal(m.records[key].Data, pending.record.Data) {
			delete(m.pending, key)
			return true
		}
		next, events := evaluateMonitor(previous, pending.observation, m.app.options.Config.Alerts, m.app.started)
		data, marshalErr := json.Marshal(next)
		if marshalErr != nil {
			return false
		}
		messages := make([]*store.OutboxMessage, len(events))
		for i := range events {
			events[i], messages[i] = m.app.prepareEvent(events[i])
		}
		pending.next = next
		pending.record.Data = data
		pending.record.UpdatedAt = next.ObservedAt
		pending.expected = m.records[key].Revision
		pending.events = events
		pending.messages = messages
		// One bounded retry; another conflict remains pending until the next pass.
		write, cancel = context.WithTimeout(ctx, 2*time.Second)
		err = m.repository.CommitMonitorState(write, pending.record, pending.expected, pending.events, pending.messages)
		cancel()
	}
	if err != nil {
		return false
	}
	pending.record.Revision = pending.expected + 1
	m.records[key] = pending.record
	m.states[key] = pending.next
	delete(m.pending, key)
	return true
}
