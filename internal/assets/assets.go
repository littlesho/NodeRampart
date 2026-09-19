// SPDX-License-Identifier: MIT

// Package assets downloads data from fixed official endpoints. It never changes
// the installed configuration or executes downloaded content.
package assets

import (
	"context"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/littlesho/NodeRampart/internal/billing"
)

type Credentials struct {
	AccountID  string
	LicenseKey string
}

func (Credentials) String() string   { return "<MaxMind credentials redacted>" }
func (Credentials) GoString() string { return "<MaxMind credentials redacted>" }

// Client permits an injected transport for tests; endpoint selection and redirect
// restrictions remain enforced independently of the supplied HTTP client.
type Client struct{ HTTP *http.Client }

type GeoBundle struct {
	Directory string
	CityPath  string
	ASNPath   string
	// Notices names are relative to Directory and are selected by the client.
	Notices   []string
	CityBuild time.Time
	ASNBuild  time.Time
	mu        sync.Mutex
	root      *os.Root
	ownedDir  string
	identity  os.FileInfo
}

type PriceSnapshot struct {
	SchemaVersion   int             `json:"schema_version"`
	Profile         billing.Profile `json:"profile"`
	Provider        string          `json:"provider"`
	Region          string          `json:"region"`
	SourceURL       string          `json:"source_url"`
	PublishedAt     time.Time       `json:"published_at"`
	EffectiveAt     time.Time       `json:"effective_at"`
	FetchedAt       time.Time       `json:"fetched_at"`
	Digest          string          `json:"digest"`
	PublishedFreeGB float64         `json:"published_free_gb"`
	Notes           []string        `json:"notes"`
}

func DownloadGeo(ctx context.Context, credentials Credentials) (*GeoBundle, error) {
	return (&Client{}).DownloadGeo(ctx, credentials)
}

func Regions(ctx context.Context) ([]string, error) { return (&Client{}).Regions(ctx) }

func FetchPrice(ctx context.Context, provider, region string) (PriceSnapshot, error) {
	return (&Client{}).FetchPrice(ctx, provider, region)
}
