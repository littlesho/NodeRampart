// SPDX-License-Identifier: MIT

// Package replay processes bounded metadata offline, without daemon, database,
// journal access, packet capture, enrichment or notification side effects.
package replay

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/littlesho/NodeRampart/internal/collector"
	"github.com/littlesho/NodeRampart/internal/protocol"
)

const (
	Format         = "noderampart-replay"
	Version        = 1
	MaxBytes       = 64 << 20
	MaxRecords     = 100000
	MaxRecordBytes = 1 << 20
	MaxIdentities  = 65536
	MaxEvents      = 1000
)

var epoch = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

type Header struct {
	Format         string `json:"format"`
	Version        int    `json:"version"`
	Anonymized     *bool  `json:"anonymized"`
	InterfaceLimit int    `json:"interface_limit"`
}

type AuthRecord struct {
	ObservedAt  time.Time          `json:"observed_at_utc"`
	Kind        collector.AuthKind `json:"kind"`
	SourceIP    string             `json:"source_ip"`
	SourcePort  uint16             `json:"source_port,omitempty"`
	User        string             `json:"user"`
	Method      string             `json:"method"`
	Root        bool               `json:"root"`
	InvalidUser bool               `json:"invalid_user"`
}

type Record struct {
	Type       string          `json:"type"`
	Generation uint64          `json:"generation,omitempty"`
	Batch      *protocol.Batch `json:"batch,omitempty"`
	Auth       *AuthRecord     `json:"auth,omitempty"`
}

func (r Record) at() time.Time {
	if r.Batch != nil {
		return r.Batch.SentAt
	}
	return r.Auth.ObservedAt
}

type InputSummary struct {
	Records           int    `json:"records"`
	Batches           int    `json:"batches"`
	AuthObservations  int    `json:"auth_observations"`
	InterfaceLimit    int    `json:"interface_limit"`
	Interfaces        int    `json:"interfaces"`
	Identities        int    `json:"identities"`
	LossBatches       uint64 `json:"ipc_loss_batches"`
	LossPackets       uint64 `json:"ipc_loss_packets"`
	KernelDrops       uint64 `json:"kernel_drops"`
	OverflowPackets   uint64 `json:"overflow_packets"`
	ParseErrors       uint64 `json:"parse_errors"`
	KernelStatsErrors uint64 `json:"kernel_stats_errors"`
	Saturated         bool   `json:"health_counters_saturated"`
}

// strictJSON also rejects duplicate keys and excessive nesting. Diagnostics
// never include input fields, usernames, addresses or raw decoder errors.
func strictJSON(data []byte, target any) error {
	invalid := errors.New("invalid replay JSON object")
	data = bytes.TrimSpace(data)
	if len(data) < 2 || len(data) > MaxRecordBytes || data[0] != '{' || data[len(data)-1] != '}' || !utf8.Valid(data) {
		return invalid
	}
	check := json.NewDecoder(bytes.NewReader(data))
	check.UseNumber()
	if walkJSON(check, 0) != nil {
		return invalid
	}
	if _, err := check.Token(); !errors.Is(err, io.EOF) {
		return invalid
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil {
		return invalid
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return invalid
	}
	return nil
}

func walkJSON(d *json.Decoder, depth int) error {
	if depth > 16 {
		return errors.New("excessive JSON depth")
	}
	token, err := d.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		keys := map[string]bool{}
		for d.More() {
			token, err := d.Token()
			if err != nil {
				return err
			}
			key, ok := token.(string)
			key = strings.ToLower(key)
			if !ok || keys[key] {
				return errors.New("invalid object keys")
			}
			keys[key] = true
			if err := walkJSON(d, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for d.More() {
			if err := walkJSON(d, depth+1); err != nil {
				return err
			}
		}
	default:
		return errors.New("unexpected delimiter")
	}
	_, err = d.Token()
	return err
}

func syntheticAddress(value string) bool {
	address, err := netip.ParseAddr(value)
	if err != nil || address.Zone() != "" || value != address.String() {
		return false
	}
	var id uint32
	if address.Is4() {
		bytes := address.As4()
		if bytes[0] != 198 || bytes[1] < 18 || bytes[1] > 19 {
			return false
		}
		id = uint32(bytes[1]-18)<<16 | uint32(bytes[2])<<8 | uint32(bytes[3])
	} else {
		if !netip.MustParsePrefix("2001:db8::/96").Contains(address) {
			return false
		}
		bytes := address.As16()
		id = uint32(bytes[12])<<24 | uint32(bytes[13])<<16 | uint32(bytes[14])<<8 | uint32(bytes[15])
	}
	return id > 0 && id <= MaxIdentities
}

func syntheticName(value, prefix string, maximum int) bool {
	if !strings.HasPrefix(value, prefix) {
		return false
	}
	number, err := strconv.Atoi(strings.TrimPrefix(value, prefix))
	return err == nil && number > 0 && number <= maximum && value == fmt.Sprintf("%s%d", prefix, number)
}

func read(ctx context.Context, input io.Reader, requireAnonymized bool, onHeader func(Header) error, visit func(Record) error) (InputSummary, error) {
	summary := InputSummary{}
	scanner := bufio.NewScanner(io.LimitReader(input, MaxBytes+1))
	scanner.Buffer(make([]byte, 64<<10), MaxRecordBytes+2)
	if !scanner.Scan() {
		return summary, errors.New("missing replay header")
	}
	consumed := len(scanner.Bytes()) + 1
	var header Header
	if strictJSON(scanner.Bytes(), &header) != nil || header.Format != Format || header.Version != Version || header.Anonymized == nil || header.InterfaceLimit < 1 || header.InterfaceLimit > protocol.MaxInterfaces || requireAnonymized && !*header.Anonymized {
		return summary, errors.New("invalid or non-anonymized replay header")
	}
	summary.InterfaceLimit = header.InterfaceLimit
	if err := onHeader(header); err != nil {
		return summary, err
	}
	var first, last time.Time
	var generation uint64
	previous := map[string]time.Time{}
	identities := map[string]bool{}
	addIdentity := func(key string) bool {
		if identities[key] {
			return true
		}
		if len(identities) >= MaxIdentities {
			return false
		}
		identities[key] = true
		return true
	}
	validIP := func(value string) bool {
		address, err := netip.ParseAddr(value)
		return err == nil && address.Zone() == "" && value == address.String() && (!*header.Anonymized || syntheticAddress(value)) && addIdentity("ip:"+value)
	}
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return summary, errors.New("replay cancelled")
		}
		consumed += len(scanner.Bytes()) + 1
		summary.Records++
		invalid := func() (InputSummary, error) {
			return summary, fmt.Errorf("invalid or out-of-bounds replay record %d", summary.Records)
		}
		if consumed > MaxBytes || summary.Records > MaxRecords {
			return invalid()
		}
		var r Record
		if strictJSON(scanner.Bytes(), &r) != nil {
			return invalid()
		}
		switch r.Type {
		case "batch":
			if r.Batch == nil || r.Auth != nil || r.Generation == 0 || r.Generation > MaxRecords || r.Generation < generation || r.Batch.ValidateAt(r.Batch.SentAt) != nil {
				return invalid()
			}
			b := r.Batch
			if *header.Anonymized && !syntheticName(b.Interface, "if", protocol.MaxInterfaces) {
				return invalid()
			}
			prior, exists := previous[b.Interface]
			if !exists && len(previous) >= header.InterfaceLimit || exists && !b.SentAt.After(prior) {
				return invalid()
			}
			previous[b.Interface] = b.SentAt
			generation = r.Generation
			for _, flow := range b.Flows {
				if !validIP(flow.RemoteIP) {
					return invalid()
				}
			}
			summary.Batches++
			summary.LossBatches += b.IPCDroppedBatches
			summary.LossPackets += b.IPCDroppedPackets
			summary.KernelDrops += b.KernelDrops
			summary.OverflowPackets += b.OverflowPackets
			summary.ParseErrors += b.ParseErrors
			summary.KernelStatsErrors += b.KernelStatsErrors
			summary.Saturated = summary.Saturated || b.HealthCountersSaturated
		case "auth":
			if r.Auth == nil || r.Batch != nil || r.Generation != 0 {
				return invalid()
			}
			a := r.Auth
			if !validIP(a.SourceIP) || len(a.User) > 64 || a.User == "" || strings.IndexFunc(a.User, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 || !addIdentity("user:"+a.User) {
				return invalid()
			}
			if *header.Anonymized && !syntheticName(a.User, "user", MaxIdentities) {
				return invalid()
			}
			switch a.Kind {
			case collector.AuthFailure, collector.AuthSuccess, collector.AuthInvalidUser, collector.AuthPAMFailure:
			default:
				return invalid()
			}
			switch a.Method {
			case "password", "publickey", "keyboard-interactive", "keyboard-interactive/pam", "hostbased", "gssapi-with-mic", "pam", "unknown":
			default:
				return invalid()
			}
			summary.AuthObservations++
		default:
			return invalid()
		}
		at := r.at()
		if at.IsZero() || at.Year() < 1970 || at.Year() > 9999 || !last.IsZero() && at.Before(last) {
			return invalid()
		}
		if first.IsZero() {
			first = at
			if *header.Anonymized && !first.Equal(epoch) {
				return invalid()
			}
		}
		if at.Sub(first) > 7*24*time.Hour {
			return invalid()
		}
		last = at
		if err := visit(r); err != nil {
			return summary, err
		}
	}
	if scanner.Err() != nil || consumed > MaxBytes {
		return summary, errors.New("replay exceeds byte/record bounds or cannot be read")
	}
	if summary.Records == 0 {
		return summary, errors.New("replay contains no observations")
	}
	summary.Interfaces = len(previous)
	summary.Identities = len(identities)
	return summary, nil
}
