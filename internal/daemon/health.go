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
				if event.Evidence == nil {
					event.Evidence = map[string]string{}
				}
				event.Evidence["detection_window_complete"] = fmt.Sprint(time.Since(a.started) >= a.options.Config.Auth.Window.Duration)
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
		if !inserted {
			return collector.ErrJournalAlreadyAcknowledged
		}
	}
	return err
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
