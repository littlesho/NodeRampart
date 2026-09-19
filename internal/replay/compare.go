// SPDX-License-Identifier: MIT

package replay

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"sort"
	"time"

	"github.com/littlesho/NodeRampart/internal/collector"
	"github.com/littlesho/NodeRampart/internal/config"
	"github.com/littlesho/NodeRampart/internal/detect"
	"github.com/littlesho/NodeRampart/internal/model"
)

type Rules struct {
	NetworkEnabled bool                   `json:"network_enabled"`
	Detection      config.DetectionConfig `json:"detection"`
	Auth           config.AuthConfig      `json:"auth"`
}

func LoadRules(path string) (Rules, error) {
	file, err := openInput(path, 1<<20)
	if err != nil {
		return Rules{}, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	cfg := config.Defaults()
	if err != nil || strictJSON(data, &cfg) != nil || cfg.Validate() != nil {
		return Rules{}, errors.New("invalid replay threshold configuration")
	}
	// Journalctl is a fixed validated config path, but replay never uses it.
	cfg.Auth.Journalctl = ""
	return Rules{NetworkEnabled: cfg.Sensor.Enabled, Detection: cfg.Detection, Auth: cfg.Auth}, nil
}

type EventSummary struct {
	ID         string         `json:"id"`
	IncidentID string         `json:"incident_id"`
	At         time.Time      `json:"observed_at_utc"`
	Kind       string         `json:"kind"`
	Phase      string         `json:"phase"`
	Severity   model.Severity `json:"severity"`
	Interface  string         `json:"interface,omitempty"`
	Source     string         `json:"source,omitempty"`
	Count      uint64         `json:"count"`
}

type Side struct {
	Rules       Rules               `json:"rules"`
	TotalEvents uint64              `json:"total_events"`
	ByKindPhase map[string]uint64   `json:"by_kind_phase"`
	Events      []EventSummary      `json:"events"`
	Truncated   bool                `json:"events_truncated"`
	Network     detect.NetworkStats `json:"network_state"`
	Auth        detect.AuthStats    `json:"auth_state"`
}

type Comparison struct {
	FormatVersion    int              `json:"format_version"`
	InputSHA256      string           `json:"input_sha256"`
	Input            InputSummary     `json:"input"`
	Baseline         Side             `json:"baseline"`
	Candidate        Side             `json:"candidate"`
	DeltaByKindPhase map[string]int64 `json:"candidate_minus_baseline"`
	Limits           []string         `json:"limitations"`
}

type engine struct {
	side      Side
	network   *detect.Fleet
	auth      *detect.Auth
	incidents map[string]string
}

func newEngine(rules Rules, limit int) *engine {
	return &engine{side: Side{Rules: rules, ByKindPhase: map[string]uint64{}, Events: []EventSummary{}}, network: detect.NewFleet(rules.Detection, limit), auth: detect.NewAuth(rules.Auth), incidents: map[string]string{}}
}

func (e *engine) observe(r Record) {
	var events []model.Event
	if r.Batch != nil && e.side.Rules.NetworkEnabled {
		b := *r.Batch
		b.ConnectionID = r.Generation
		events = e.network.Observe(b)
	}
	if r.Auth != nil && e.side.Rules.Auth.Enabled {
		a := r.Auth
		if event := e.auth.Observe(collector.AuthObservation{ObservedAt: a.ObservedAt, Kind: a.Kind, SourceIP: netip.MustParseAddr(a.SourceIP), SourcePort: a.SourcePort, User: a.User, Method: a.Method, Root: a.Root, InvalidUser: a.InvalidUser}); event != nil {
			events = append(events, *event)
		}
	}
	// Detector maps and random IDs must not affect a replay comparison.
	sort.Slice(events, func(i, j int) bool {
		a, b := events[i], events[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Phase != b.Phase {
			return a.Phase < b.Phase
		}
		return a.SourceIP < b.SourceIP
	})
	for _, event := range events {
		e.side.TotalEvents++
		e.side.ByKindPhase[event.Kind+"/"+event.Phase]++
		if len(e.side.Events) >= MaxEvents {
			e.side.Truncated = true
			continue
		}
		if e.incidents[event.IncidentID] == "" {
			e.incidents[event.IncidentID] = fmt.Sprintf("inc_%d", len(e.incidents)+1)
		}
		e.side.Events = append(e.side.Events, EventSummary{ID: fmt.Sprintf("evt_%d", e.side.TotalEvents), IncidentID: e.incidents[event.IncidentID], At: event.ObservedAt, Kind: event.Kind, Phase: event.Phase, Severity: event.Severity, Interface: event.Evidence["interface"], Source: event.SourceIP, Count: event.Count})
	}
}

func Compare(ctx context.Context, inputPath string, baseline, candidate Rules) (Comparison, error) {
	file, err := openInput(inputPath, MaxBytes)
	if err != nil {
		return Comparison{}, err
	}
	defer file.Close()
	return compare(ctx, file, baseline, candidate)
}

func compare(ctx context.Context, input io.Reader, baseline, candidate Rules) (Comparison, error) {
	result := Comparison{FormatVersion: Version, DeltaByKindPhase: map[string]int64{}, Limits: []string{
		"Production detectors only; no database, socket, journal, capture, GeoIP lookup or notification delivery is used.",
		"Metadata omits payloads and within-batch ordering. Missing batches, startup UDP warmup and reported loss limit conclusions.",
		"Thresholds apply per interface using the file's fixed state budget. Configured live interface selection and notification rules are not simulated.",
		"Event IDs are normalized per side, not cross-rule identities. Counts do not measure true/false positives; at most 1000 summaries per side are retained.",
		"Pseudonymized timing, ports, traffic volume and behavior remain linkable. Hourly exports cannot reconstruct detection windows.",
	}}
	var a, b *engine
	hash := sha256.New()
	summary, err := read(ctx, io.TeeReader(input, hash), true, func(h Header) error {
		a = newEngine(baseline, h.InterfaceLimit)
		b = newEngine(candidate, h.InterfaceLimit)
		return nil
	}, func(r Record) error { a.observe(r); b.observe(r); return nil })
	if err != nil {
		return Comparison{}, err
	}
	a.side.Network, a.side.Auth = a.network.Stats(), a.auth.Stats()
	b.side.Network, b.side.Auth = b.network.Stats(), b.auth.Stats()
	result.Input, result.Baseline, result.Candidate = summary, a.side, b.side
	result.InputSHA256 = hex.EncodeToString(hash.Sum(nil))
	for key, value := range a.side.ByKindPhase {
		result.DeltaByKindPhase[key] -= int64(value)
	}
	for key, value := range b.side.ByKindPhase {
		result.DeltaByKindPhase[key] += int64(value)
	}
	return result, nil
}
