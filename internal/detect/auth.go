// SPDX-License-Identifier: MIT

package detect

import (
	"fmt"
	"strconv"
	"time"

	"github.com/littlesho/NodeRampart/internal/collector"
	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/model"
)

type Auth struct {
	config    config.AuthConfig
	failures  map[string][]time.Time
	lastAlert map[string]time.Time
	lastSeen  map[string]time.Time
	observed  uint64
	auxiliary uint64
	rejected  uint64
}

type AuthStats struct {
	CanonicalFailures     uint64 `json:"canonical_failures"`
	AuxiliaryObservations uint64 `json:"auxiliary_observations"`
	RejectedSources       uint64 `json:"rejected_sources"`
}

func (a *Auth) Stats() AuthStats {
	return AuthStats{CanonicalFailures: a.observed, AuxiliaryObservations: a.auxiliary, RejectedSources: a.rejected}
}

const (
	maxAuthSources           = 65_536
	maxFailuresPerAuthSource = 4_096
)

func NewAuth(cfg config.AuthConfig) *Auth {
	return &Auth{config: cfg, failures: make(map[string][]time.Time), lastAlert: make(map[string]time.Time), lastSeen: make(map[string]time.Time)}
}

func (a *Auth) Observe(observation collector.AuthObservation) *model.Event {
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
			if !failure.Before(cutoff) && !failure.After(observation.ObservedAt) {
				prior++
			}
		}
		if prior > 0 {
			evidence = map[string]string{"preceding_source_failures": strconv.Itoa(prior), "window": a.config.Window.Duration.String(), "count_basis": "openssh_final_failure"}
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
	a.observed++
	if a.observed%1_024 == 0 {
		a.prune(observation.ObservedAt)
	}
	if _, exists := a.failures[source]; !exists && len(a.failures) >= maxAuthSources {
		a.prune(observation.ObservedAt)
		if len(a.failures) >= maxAuthSources {
			a.rejected++
			return nil
		}
	}
	cutoff := observation.ObservedAt.Add(-a.config.Window.Duration)
	times := a.failures[source]
	kept := times[:0]
	for _, value := range times {
		if !value.Before(cutoff) {
			kept = append(kept, value)
		}
	}
	if len(kept) >= maxFailuresPerAuthSource {
		kept = kept[len(kept)-maxFailuresPerAuthSource+1:]
	}
	kept = append(kept, observation.ObservedAt)
	a.failures[source] = kept
	a.lastSeen[source] = observation.ObservedAt
	if len(kept) < a.config.Threshold || observation.ObservedAt.Sub(a.lastAlert[source]) < a.config.Cooldown.Duration {
		return nil
	}
	a.lastAlert[source] = observation.ObservedAt
	return &model.Event{ID: model.NewID("evt"), IncidentID: model.NewID("inc"), ObservedAt: observation.ObservedAt, Kind: "ssh_brute_force", Phase: "start", Severity: model.SeverityHigh, SourceIP: source, SourceRange: PrefixString(source), Target: "ssh user=" + observation.User, Count: uint64(len(kept)), Summary: fmt.Sprintf("%d SSH authentication failures in %s", len(kept), a.config.Window.Duration), Evidence: map[string]string{"method": observation.Method, "invalid_user": strconv.FormatBool(observation.InvalidUser), "count_basis": "openssh_final_failure"}}
}

func (a *Auth) prune(now time.Time) {
	cutoff := now.Add(-a.config.Window.Duration - a.config.Cooldown.Duration)
	for source, seen := range a.lastSeen {
		if seen.Before(cutoff) {
			delete(a.failures, source)
			delete(a.lastAlert, source)
			delete(a.lastSeen, source)
		}
	}
}
