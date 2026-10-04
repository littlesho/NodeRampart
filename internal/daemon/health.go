// SPDX-License-Identifier: MIT

package daemon

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/littlesho/NodeRampart/internal/collector"
	"github.com/littlesho/NodeRampart/internal/store"
)

type StorageHealth struct {
	FailedOperations  []string  `json:"failed_operations"`
	Healthy           bool      `json:"healthy"`
	LastDurableIngest time.Time `json:"last_durable_ingest_utc,omitzero"`
	LastFailure       time.Time `json:"last_failure_utc,omitzero"`
	Failures          uint64    `json:"failures"`
	FailureOperation  string    `json:"failure_operation,omitempty"`
}

func (a *App) recordWrite(err error, operation string, ingest bool) {
	a.mu.Lock()
	if a.storageFailures == nil {
		a.storageFailures = make(map[string]bool)
	}
	if err != nil {
		a.storageFailures[operation] = true
		a.storageHealth.LastFailure = time.Now().UTC()
		if a.storageHealth.Failures < math.MaxUint64 {
			a.storageHealth.Failures++
		}
		a.storageHealth.FailureOperation = operation
	} else {
		delete(a.storageFailures, operation)
		if ingest {
			a.storageHealth.LastDurableIngest = time.Now().UTC()
		}
	}
	a.storageHealth.Healthy = len(a.storageFailures) == 0
	failures := make([]string, 0, len(a.storageFailures))
	for name := range a.storageFailures {
		failures = append(failures, name)
	}
	sort.Strings(failures)
	a.storageHealth.FailedOperations = failures
	a.mu.Unlock()
	if err != nil {
		a.options.Logger.Warn("storage operation unavailable", "operation", operation)
	}
}

type journalDelivery struct {
	entry collector.JournalEntry
	ack   chan error
}

func (a *App) handleJournal(ctx context.Context, entry collector.JournalEntry) error {
	// Keep the prepared result across storage retries: advancing the in-memory
	// detector a second time would count one journal record twice.
	if a.pendingJournal == nil || a.pendingJournal.Cursor != entry.Cursor {
		seen, err := a.options.Store.JournalSeen(ctx, entry.Cursor)
		if err != nil {
			a.recordWrite(err, "journal", false)
			return err
		}
		write := store.JournalWrite{Cursor: entry.Cursor, ReceivedAt: entry.ReceivedAt, ObservedAt: entry.ObservedAt, Trusted: entry.SkipReason == "" || entry.SkipReason == "unrecognized_message"}
		if entry.Observation != nil && !seen {
			observation := *entry.Observation
			write.Kind = string(observation.Kind)
			_, write.SourceRange = a.options.StorePrivacy.IP(observation.SourceIP.String())
			if event := a.auth.Observe(observation); event != nil {
				a.addAuthHistoryHints(ctx, observation, event)
				if event.Evidence == nil {
					event.Evidence = map[string]string{}
				}
				event.Evidence["detection_window_complete"] = fmt.Sprint(time.Since(a.started) >= a.options.Config.Auth.Window.Duration && a.auth.Stats().CoverageComplete)
				stored, message := a.prepareEvent(*event)
				write.Event = &stored
				write.Notification = message
			}
		}
		a.mu.Lock()
		a.authStats = a.auth.Stats()
		a.mu.Unlock()
		a.pendingJournal = &write
	}
	inserted, err := a.options.Store.CommitJournal(ctx, *a.pendingJournal)
	a.recordWrite(err, "journal", inserted && entry.Observation != nil)
	if err == nil {
		if inserted && a.pendingJournal.Trusted {
			a.recordWrite(nil, "journal_recovery", false)
		}
		a.pendingJournal = nil
		a.refreshAuthCoverage(ctx, time.Now().UTC())
		if !inserted {
			return collector.ErrJournalAlreadyAcknowledged
		}
	}
	return err
}

func (a *App) refreshAuthCoverage(ctx context.Context, now time.Time) {
	if a.auth == nil || !a.options.Config.Auth.Enabled {
		return
	}
	stats := a.auth.StatsAt(now)
	a.mu.Lock()
	a.authStats = stats
	previous := a.authCoverageState
	a.mu.Unlock()
	state := "running"
	if !stats.CoverageComplete {
		state = "degraded"
	}
	// Do not add a duplicate healthy ledger for deployments that have never
	// lost authentication detector coverage. After a refusal, preserve both
	// degradation and recovery using the existing component history.
	if state == previous || state == "running" && previous == "" {
		return
	}
	err := a.options.Store.SetComponentStatus(ctx, "auth_detection", state, now)
	a.recordWrite(err, "auth_detection_coverage", false)
	if err == nil {
		a.mu.Lock()
		a.authCoverageState = state
		a.mu.Unlock()
	}
}

func journalCoverageGap(status collector.JournalStatus) store.CoverageGap {
	since := status.Since
	if since.IsZero() || since.After(status.At) {
		since = status.At
	}
	return store.CoverageGap{Name: "ssh_journal", Reason: status.Reason, Start: since, End: status.At, Count: status.Count}
}

func (a *App) recordJournalDegradation(ctx context.Context, status collector.JournalStatus) error {
	err := a.options.Store.RecordJournalDegradation(ctx, journalCoverageGap(status))
	a.recordWrite(err, "journal_recovery", false)
	a.recordWrite(err, "coverage_gap", false)
	return err
}

func (a *App) recordJournalStatus(ctx context.Context, status collector.JournalStatus) {
	a.mu.Lock()
	previous := a.journalStatus
	a.journalStatus = status
	a.mu.Unlock()
	a.logJournalTransition(previous, status)
	state := "degraded"
	if status.State == "running" {
		state = "running"
	}
	a.recordWrite(a.options.Store.SetComponentStatus(ctx, "ssh_journal", state, time.Now().UTC()), "coverage", false)
	if status.State == "gap" && !status.QualityDegraded() {
		a.recordWrite(a.options.Store.RecordCoverageGap(ctx, journalCoverageGap(status)), "coverage_gap", false)
	}
}

// Log category changes so the underlying reason remains inspectable after a
// process restart. Never log journal messages, stderr, paths or raw errors.
func (a *App) logJournalTransition(previous, status collector.JournalStatus) {
	if a.options.Logger == nil || status.State == "starting" {
		return
	}
	diagnostic, old := status.SafeDiagnostic(), previous.SafeDiagnostic()
	if previous.State == status.State && previous.Reason == status.Reason && old.Cause == diagnostic.Cause && old.Scope == diagnostic.Scope && old.Signal == diagnostic.Signal && equalJournalExit(old.ExitCode, diagnostic.ExitCode) {
		return
	}
	attrs := []any{"state", collector.SafeJournalState(status.State), "reason", collector.SafeJournalReason(status.Reason)}
	if diagnostic.Cause != "" {
		attrs = append(attrs, "cause", diagnostic.Cause, "diagnostic_scope", diagnostic.Scope, "detail", diagnostic.Detail)
		if !diagnostic.At.IsZero() {
			attrs = append(attrs, "diagnostic_at_utc", diagnostic.At.Format(time.RFC3339Nano))
		}
		if diagnostic.ExitCode != nil {
			attrs = append(attrs, "exit_code", *diagnostic.ExitCode)
		}
		if diagnostic.Signal != "" {
			attrs = append(attrs, "signal", diagnostic.Signal)
		}
	}
	if status.State == "running" {
		a.options.Logger.Info("SSH journal reader state changed", attrs...)
	} else {
		a.options.Logger.Warn("SSH journal collection degraded", attrs...)
	}
}

func equalJournalExit(a, b *int) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}
