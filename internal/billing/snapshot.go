// SPDX-License-Identifier: MIT

package billing

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"time"
)

const MaxSnapshotBytes = 16 << 10
const SnapshotBasis = "configured_tariff_at_generation"

// Snapshot preserves calculation inputs independently of the notification body.
// It is a guest-counter estimate under the configured tariff, not a provider bill.
type Snapshot struct {
	SchemaVersion      int       `json:"schema_version"`
	CalculationVersion int       `json:"calculation_version"`
	Basis              string    `json:"basis"`
	Profile            Profile   `json:"profile"`
	ProfileSHA256      string    `json:"profile_sha256"`
	UnitBytes          uint64    `json:"unit_bytes"`
	OutboundBytes      uint64    `json:"outbound_bytes"`
	Estimate           Estimate  `json:"estimate"`
	PeriodStart        time.Time `json:"period_start_utc"`
	PeriodEnd          time.Time `json:"period_end_utc"`
	Timezone           string    `json:"timezone"`
	GeneratedAt        time.Time `json:"generated_at_utc"`
}

func NewSnapshot(p Profile, outbound uint64, start, end, generated time.Time, timezone string) (*Snapshot, error) {
	p.InternetEgress = append([]Tier(nil), p.InternetEgress...)
	unit := p.UnitBytes
	if unit == 0 {
		unit = 1_000_000_000
	}
	s := &Snapshot{SchemaVersion: 1, CalculationVersion: 1, Basis: SnapshotBasis,
		Profile: p, UnitBytes: unit, OutboundBytes: outbound, Estimate: p.Estimate(outbound),
		PeriodStart: start.UTC(), PeriodEnd: end.UTC(), Timezone: timezone, GeneratedAt: generated.UTC()}
	data, err := json.Marshal(p)
	if err != nil {
		return nil, errors.New("pricing profile cannot be encoded")
	}
	hash := sha256.Sum256(data)
	s.ProfileSHA256 = hex.EncodeToString(hash[:])
	if err := s.Validate(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Snapshot) Validate() error {
	if s == nil || s.SchemaVersion != 1 || s.CalculationVersion != 1 || s.Basis != SnapshotBasis ||
		!s.PeriodStart.Before(s.PeriodEnd) || s.GeneratedAt.IsZero() || !safeText(s.Timezone, 128) {
		return errors.New("invalid pricing snapshot metadata")
	}
	if _, err := time.LoadLocation(s.Timezone); err != nil {
		return errors.New("invalid pricing snapshot timezone")
	}
	if err := s.Profile.Validate(); err != nil {
		return errors.New("invalid pricing snapshot profile")
	}
	unit := s.Profile.UnitBytes
	if unit == 0 {
		unit = 1_000_000_000
	}
	data, err := json.Marshal(s.Profile)
	if err != nil {
		return errors.New("invalid pricing snapshot profile")
	}
	hash := sha256.Sum256(data)
	if s.ProfileSHA256 != hex.EncodeToString(hash[:]) || s.UnitBytes != unit || s.Estimate.Unavailable || s.Estimate != s.Profile.Estimate(s.OutboundBytes) {
		return errors.New("pricing snapshot calculation does not match its inputs")
	}
	return nil
}

func EncodeSnapshot(s *Snapshot) (string, error) {
	if s == nil {
		return "", nil
	}
	if err := s.Validate(); err != nil {
		return "", err
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	err := encoder.Encode(s)
	data := bytes.TrimSuffix(buffer.Bytes(), []byte("\n"))
	if err != nil || len(data) > MaxSnapshotBytes {
		return "", errors.New("pricing snapshot exceeds encoding bounds")
	}
	return string(data), nil
}

func DecodeSnapshot(data string) (*Snapshot, error) {
	if data == "" {
		return nil, nil
	}
	if len(data) > MaxSnapshotBytes {
		return nil, errors.New("pricing snapshot exceeds encoding bounds")
	}
	decoder := json.NewDecoder(bytes.NewBufferString(data))
	decoder.DisallowUnknownFields()
	var s Snapshot
	if err := decoder.Decode(&s); err != nil {
		return nil, errors.New("invalid pricing snapshot encoding")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, errors.New("pricing snapshot has trailing data")
	}
	if err := s.Validate(); err != nil {
		return nil, err
	}
	return &s, nil
}
