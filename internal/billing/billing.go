// SPDX-License-Identifier: MIT

package billing

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	"strings"
	"time"
)

const maxProfileSize = 256 << 10

// MaxPricePerGB keeps all uint64 byte totals safely within float64 arithmetic.
const MaxPricePerGB = 1e100

type Tier struct {
	UpToGB     float64 `json:"up_to_gb"`
	PricePerGB float64 `json:"price_per_gb"`
}

type Profile struct {
	SchemaVersion  int     `json:"schema_version"`
	Name           string  `json:"name"`
	Provider       string  `json:"provider"`
	SourceRegion   string  `json:"source_region"`
	Currency       string  `json:"currency"`
	EffectiveDate  string  `json:"effective_date"`
	SourceURL      string  `json:"source_url"`
	FreeGB         float64 `json:"free_gb"`
	UnitBytes      uint64  `json:"unit_bytes,omitempty"`
	InternetEgress []Tier  `json:"internet_egress"`
}

type Estimate struct {
	Unavailable                 bool `json:"unavailable,omitempty"`
	BillableGB, Cost            float64
	Currency, Profile, Provider string
	SourceRegion, EffectiveDate string
}

func Load(path string) (*Profile, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, errors.New("billing profile must be a regular file, not a symlink")
	}
	if info.Mode().Perm()&0o022 != 0 {
		return nil, errors.New("billing profile must not be writable by group or others")
	}
	if info.Size() > maxProfileSize {
		return nil, errors.New("billing profile exceeds 256 KiB")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maxProfileSize+1))
	decoder.DisallowUnknownFields()
	var profile Profile
	if err := decoder.Decode(&profile); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, errors.New("billing profile has trailing JSON data")
	}
	if err := profile.Validate(); err != nil {
		return nil, err
	}
	return &profile, nil
}

func (p *Profile) Validate() error {
	if p.UnitBytes != 0 && p.UnitBytes != 1_000_000_000 && p.UnitBytes != 1<<30 {
		return errors.New("billing unit_bytes must be 0, 1000000000 or 1073741824")
	}
	if p.SchemaVersion != 1 || !safeText(p.Name, 128) || !safeText(p.Provider, 64) || !safeText(p.SourceRegion, 64) || len(p.Currency) != 3 {
		return errors.New("billing profile identity fields are invalid")
	}
	for _, character := range p.Currency {
		if character < 'A' || character > 'Z' {
			return errors.New("billing currency must be a three-letter uppercase code")
		}
	}
	if _, err := time.Parse("2006-01-02", p.EffectiveDate); err != nil {
		return errors.New("billing effective_date must use YYYY-MM-DD")
	}
	parsed, err := url.Parse(p.SourceURL)
	if err != nil || len(p.SourceURL) > 4096 || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return errors.New("billing source_url must be HTTPS")
	}
	return p.validateAmounts()
}

func (p *Profile) validateAmounts() error {
	if p.UnitBytes != 0 && p.UnitBytes != 1_000_000_000 && p.UnitBytes != 1<<30 {
		return errors.New("invalid billing byte unit")
	}
	if p.FreeGB < 0 || math.IsNaN(p.FreeGB) || math.IsInf(p.FreeGB, 0) || len(p.InternetEgress) == 0 || len(p.InternetEgress) > 32 {
		return errors.New("billing tiers are invalid")
	}
	previous := float64(0)
	for index, tier := range p.InternetEgress {
		if tier.UpToGB < 0 || math.IsNaN(tier.UpToGB) || math.IsInf(tier.UpToGB, 0) {
			return errors.New("billing tier boundary must be finite and non-negative")
		}
		if tier.PricePerGB < 0 || tier.PricePerGB > MaxPricePerGB || math.IsNaN(tier.PricePerGB) || math.IsInf(tier.PricePerGB, 0) {
			return errors.New("billing price must be finite and between 0 and 1e100")
		}
		if index < len(p.InternetEgress)-1 && tier.UpToGB <= previous {
			return errors.New("bounded billing tiers must be strictly increasing")
		}
		if index == len(p.InternetEgress)-1 && tier.UpToGB != 0 {
			return errors.New("last billing tier must be unbounded (up_to_gb 0)")
		}
		if tier.UpToGB > 0 {
			previous = tier.UpToGB
		}
	}
	return nil
}

func safeText(value string, maximum int) bool {
	return strings.TrimSpace(value) != "" && len(value) <= maximum && strings.IndexFunc(value, func(r rune) bool { return r < 0x20 || r == 0x7f }) < 0
}

func (p *Profile) Estimate(outboundBytes uint64) Estimate {
	result := Estimate{Currency: p.Currency, Profile: p.Name, Provider: p.Provider, SourceRegion: p.SourceRegion, EffectiveDate: p.EffectiveDate}
	if p.validateAmounts() != nil {
		result.Unavailable = true
		return result
	}
	unit := p.UnitBytes
	if unit == 0 {
		unit = 1_000_000_000
	}
	totalGB := float64(outboundBytes) / float64(unit)
	billable := math.Max(0, totalGB-p.FreeGB)
	remaining := billable
	lower := float64(0)
	cost := float64(0)
	for _, tier := range p.InternetEgress {
		if remaining <= 0 {
			break
		}
		amount := remaining
		if tier.UpToGB > 0 {
			capacity := tier.UpToGB - lower
			if amount > capacity {
				amount = capacity
			}
		}
		cost += amount * tier.PricePerGB
		remaining -= amount
		if tier.UpToGB > 0 {
			lower = tier.UpToGB
		}
	}
	if math.IsNaN(cost) || math.IsInf(cost, 0) || cost < 0 || remaining > 0 {
		result.Unavailable = true
		return result
	}
	result.BillableGB, result.Cost = billable, cost
	return result
}

func (e Estimate) String() string {
	if e.Unavailable || math.IsNaN(e.Cost) || math.IsInf(e.Cost, 0) || math.IsNaN(e.BillableGB) || math.IsInf(e.BillableGB, 0) {
		return "Billing estimate unavailable: unsupported numeric inputs."
	}
	return fmt.Sprintf("%.2f %s for %.3f billable GB (%s; %s/%s; effective %s)", e.Cost, e.Currency, e.BillableGB, e.Profile, e.Provider, e.SourceRegion, e.EffectiveDate)
}
