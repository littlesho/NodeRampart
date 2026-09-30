// SPDX-License-Identifier: MIT

package detect

import (
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/littlesho/NodeRampart/internal/collector"
	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/model"
)

type Auth struct {
	mu                            sync.Mutex
	config                        config.AuthConfig
	failures                      map[string][]authTimestamp
	lastAlert                     map[string]time.Time
	lastSeen                      map[string]time.Time
	observed                      uint64
	auxiliary                     uint64
	rejected                      uint64
	refused                       uint64
	truncated                     uint64
	entries, capacity, entryLimit int
	lastPrune, incompleteUntil    time.Time
}

// UTC wall time from journal records; unlike UnixNano this preserves the full
// supported year range and nanosecond boundaries in sixteen bytes.
type authTimestamp struct {
	seconds int64
	nanos   uint32
}

func compactAuthTime(at time.Time) authTimestamp {
	return authTimestamp{at.Unix(), uint32(at.Nanosecond())}
}
func (at authTimestamp) before(other time.Time) bool {
	return at.seconds < other.Unix() || at.seconds == other.Unix() && at.nanos < uint32(other.Nanosecond())
}
func (at authTimestamp) after(other time.Time) bool {
	return at.seconds > other.Unix() || at.seconds == other.Unix() && at.nanos > uint32(other.Nanosecond())
}

type AuthStats struct {
	CanonicalFailures       uint64    `json:"canonical_failures"`
	AuxiliaryObservations   uint64    `json:"auxiliary_observations"`
	RejectedSources         uint64    `json:"rejected_sources"`
	RejectedFailures        uint64    `json:"rejected_failures"`
	HistoryTruncations      uint64    `json:"history_truncations"`
	FailureEntries          int       `json:"failure_entries"`
	FailureCapacity         int       `json:"failure_capacity"`
	FailureEntryLimit       int       `json:"failure_entry_limit"`
	Sources                 int       `json:"sources"`
	SourceLimit             int       `json:"source_limit"`
	StateSaturated          bool      `json:"state_saturated"`
	CoverageIncompleteUntil time.Time `json:"coverage_incomplete_until_utc,omitzero"`
	CoverageComplete        bool      `json:"coverage_complete"`
}

func (a *Auth) Stats() AuthStats {
	return a.StatsAt(time.Now().UTC())
}

func (a *Auth) StatsAt(now time.Time) AuthStats {
	a.mu.Lock()
	defer a.mu.Unlock()
	return AuthStats{CanonicalFailures: a.observed, AuxiliaryObservations: a.auxiliary, RejectedSources: a.rejected,
		RejectedFailures: a.refused, HistoryTruncations: a.truncated, FailureEntries: a.entries, FailureCapacity: a.capacity,
		FailureEntryLimit: a.entryLimit, Sources: len(a.failures), SourceLimit: maxAuthSources,
		StateSaturated:          a.capacity >= a.entryLimit || len(a.failures) >= maxAuthSources,
		CoverageIncompleteUntil: a.incompleteUntil, CoverageComplete: !now.Before(a.incompleteUntil)}
}

const (
	maxAuthSources           = 65_536
	maxFailuresPerAuthSource = 4_096
	// One Auth is owned by the daemon. Bound allocated timestamp slots across
	// all its sources, not only live entries; separate detector instances have
	// independent budgets. Maps/strings and allocator overhead are additional.
	maxAuthFailureEntries = 131_072
)

func NewAuth(cfg config.AuthConfig) *Auth {
	return &Auth{config: cfg, failures: make(map[string][]authTimestamp), lastAlert: make(map[string]time.Time), lastSeen: make(map[string]time.Time), entryLimit: maxAuthFailureEntries}
}

func (a *Auth) Observe(observation collector.AuthObservation) *model.Event {
	a.mu.Lock()
	defer a.mu.Unlock()
	source := observation.SourceIP.String()
	if observation.Kind == collector.AuthSuccess {
		severity := model.SeverityInfo
		if observation.Root {
			severity = model.SeverityMedium
		}
		var evidence map[string]string
		prior := 0
		cutoff := observation.ObservedAt.Add(-a.config.Window.Duration)
		for _, failure := range a.failures[source] {
			if !failure.before(cutoff) && !failure.after(observation.ObservedAt) {
				prior++
			}
		}
		if prior > 0 {
			evidence = map[string]string{"preceding_source_failures": strconv.Itoa(prior), "window": a.config.Window.Duration.String(), "count_basis": "openssh_final_failure"}
		}
		if observation.ObservedAt.Before(a.incompleteUntil) {
			if evidence == nil {
				evidence = make(map[string]string)
			}
			evidence["preceding_source_failures_complete"] = "false"
		}
		return &model.Event{ID: model.NewID("evt"), IncidentID: model.NewID("inc"), ObservedAt: observation.ObservedAt, Kind: "ssh_login_success", Phase: "observed", Severity: severity, SourceIP: source, SourceRange: PrefixString(source), Target: "ssh user=" + observation.User, Count: 1, Summary: "successful SSH " + observation.Method + " authentication", Evidence: evidence}
	}
	// OpenSSH's final Failed record is the canonical authentication outcome.
	// PAM and Invalid user records describe the same attempt (or merely a
	// username lookup), so keep them out of the attempt threshold. The
	// collector preserves those separate kinds for observation reporting.
	if observation.Kind != collector.AuthFailure || observation.Method == "pam" {
		a.auxiliary++
		return nil
	}
	if a.observed < ^uint64(0) {
		a.observed++
	}
	if a.observed%1_024 == 0 {
		a.prune(observation.ObservedAt)
	}
	if _, exists := a.failures[source]; !exists && len(a.failures) >= maxAuthSources {
		a.pruneIfDue(observation.ObservedAt)
		if len(a.failures) >= maxAuthSources {
			if a.rejected < ^uint64(0) {
				a.rejected++
			}
			a.refuse(observation.ObservedAt)
			return nil
		}
	}
	cutoff := observation.ObservedAt.Add(-a.config.Window.Duration)
	kept := a.trim(source, cutoff)
	if len(kept) >= maxFailuresPerAuthSource {
		copy(kept, kept[1:])
		kept = kept[:len(kept)-1]
		a.entries--
		if a.truncated < ^uint64(0) {
			a.truncated++
		}
		a.markIncomplete(observation.ObservedAt)
	}
	if len(kept) == cap(kept) && a.capacity >= a.entryLimit {
		a.pruneIfDue(observation.ObservedAt)
		kept = a.failures[source]
	}
	if len(kept) == cap(kept) {
		available := a.entryLimit - a.capacity
		if available <= 0 {
			a.refuse(observation.ObservedAt)
			return nil
		}
		capacity := min(max(1, 2*cap(kept)), maxFailuresPerAuthSource, cap(kept)+available)
		grown := make([]authTimestamp, len(kept), capacity)
		copy(grown, kept)
		a.capacity += capacity - cap(kept)
		kept = grown
	}
	kept = append(kept, compactAuthTime(observation.ObservedAt))
	a.entries++
	a.failures[source] = kept
	a.lastSeen[source] = observation.ObservedAt
	if len(kept) < a.config.Threshold || observation.ObservedAt.Sub(a.lastAlert[source]) < a.config.Cooldown.Duration {
		return nil
	}
	a.lastAlert[source] = observation.ObservedAt
	return &model.Event{ID: model.NewID("evt"), IncidentID: model.NewID("inc"), ObservedAt: observation.ObservedAt, Kind: "ssh_brute_force", Phase: "start", Severity: model.SeverityHigh, SourceIP: source, SourceRange: PrefixString(source), Target: "ssh user=" + observation.User, Count: uint64(len(kept)), Summary: fmt.Sprintf("%d SSH authentication failures in %s", len(kept), a.config.Window.Duration), Evidence: map[string]string{"method": observation.Method, "invalid_user": strconv.FormatBool(observation.InvalidUser), "count_basis": "openssh_final_failure", "detection_window_complete": strconv.FormatBool(!observation.ObservedAt.Before(a.incompleteUntil))}}
}

func (a *Auth) markIncomplete(at time.Time) {
	// Failure windows include the exact cutoff timestamp.
	if until := at.Add(a.config.Window.Duration + time.Nanosecond); until.After(a.incompleteUntil) {
		a.incompleteUntil = until
	}
}

func (a *Auth) refuse(at time.Time) {
	if a.refused < ^uint64(0) {
		a.refused++
	}
	a.markIncomplete(at)
}

func (a *Auth) trim(source string, cutoff time.Time) []authTimestamp {
	times := a.failures[source]
	kept := times[:0]
	for _, at := range times {
		if !at.before(cutoff) {
			kept = append(kept, at)
		}
	}
	a.entries -= len(times) - len(kept)
	if len(kept) == 0 || len(kept)*2 < cap(kept) {
		compact := make([]authTimestamp, len(kept))
		copy(compact, kept)
		a.capacity -= cap(kept) - cap(compact)
		kept = compact
	}
	if _, exists := a.failures[source]; exists {
		a.failures[source] = kept
	}
	return kept
}

func (a *Auth) pruneIfDue(now time.Time) {
	// Refused streams cannot force an O(source-count) sweep for every record.
	if a.lastPrune.IsZero() || now.Sub(a.lastPrune) >= min(time.Second, a.config.Window.Duration) {
		a.prune(now)
	}
}

func (a *Auth) prune(now time.Time) {
	cutoff := now.Add(-a.config.Window.Duration - a.config.Cooldown.Duration)
	for source, seen := range a.lastSeen {
		if seen.Before(cutoff) {
			a.entries -= len(a.failures[source])
			a.capacity -= cap(a.failures[source])
			delete(a.failures, source)
			delete(a.lastAlert, source)
			delete(a.lastSeen, source)
		} else {
			a.trim(source, now.Add(-a.config.Window.Duration))
		}
	}
	a.lastPrune = now
}
