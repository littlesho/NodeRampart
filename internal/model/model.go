// SPDX-License-Identifier: MIT

package model

import "time"

type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityLow      Severity = "low"
	SeverityMedium   Severity = "medium"
	SeverityHigh     Severity = "high"
	SeverityCritical Severity = "critical"
)

type Event struct {
	ID          string            `json:"id"`
	IncidentID  string            `json:"incident_id,omitempty"`
	ObservedAt  time.Time         `json:"observed_at_utc"`
	Kind        string            `json:"kind"`
	Phase       string            `json:"phase,omitempty"`
	Severity    Severity          `json:"severity"`
	Summary     string            `json:"summary"`
	SourceIP    string            `json:"source_ip,omitempty"`
	SourceRange string            `json:"source_range,omitempty"`
	Target      string            `json:"target,omitempty"`
	Count       uint64            `json:"count,omitempty"`
	Geo         Geo               `json:"geo"`
	Evidence    map[string]string `json:"evidence,omitempty"`
}

type Geo struct {
	CountryCode string `json:"country_code,omitempty"`
	Country     string `json:"country,omitempty"`
	Region      string `json:"region,omitempty"`
	City        string `json:"city,omitempty"`
	ASN         uint   `json:"asn,omitempty"`
	ASNOrg      string `json:"asn_org,omitempty"`
	ASNNetwork  string `json:"asn_network,omitempty"`
	DatabaseAge string `json:"database_age,omitempty"`
	Estimate    bool   `json:"estimate"`
}

type Direction string

const (
	DirectionInbound  Direction = "inbound"
	DirectionOutbound Direction = "outbound"
)

type Traffic struct {
	HourUTC    time.Time `json:"hour_utc"`
	Direction  Direction `json:"direction"`
	Country    string    `json:"country"`
	Region     string    `json:"region"`
	ASN        uint      `json:"asn"`
	ASNOrg     string    `json:"asn_org"`
	Bytes      uint64    `json:"bytes"`
	Packets    uint64    `json:"packets"`
	Attributed bool      `json:"attributed"`
}

type InterfaceTotals struct {
	Interface string    `json:"interface,omitempty"`
	HourUTC   time.Time `json:"hour_utc"`
	RXBytes   uint64    `json:"rx_bytes"`
	TXBytes   uint64    `json:"tx_bytes"`
	RXPackets uint64    `json:"rx_packets"`
	TXPackets uint64    `json:"tx_packets"`
}
