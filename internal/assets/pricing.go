// SPDX-License-Identifier: MIT

package assets

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/littlesho/NodeRampart/internal/billing"
)

const awsIndexURL = "https://" + awsHost + "/offers/v1.0/aws/AWSDataTransfer/current/region_index.json"

var regionPattern = regexp.MustCompile(`^[a-z]{2}(?:-[a-z0-9]+){2,7}$`)
var versionPattern = regexp.MustCompile(`^[0-9]{14}$`)
var ociSKUs = map[string]string{
	"north-america-europe-uk":          "B88327",
	"asia-pacific-japan-south-america": "B93455",
	"middle-east-africa":               "B93456",
}

type awsIndex struct {
	FormatVersion   string `json:"formatVersion"`
	PublicationDate string `json:"publicationDate"`
	Regions         map[string]struct {
		RegionCode string `json:"regionCode"`
		URL        string `json:"currentVersionUrl"`
	} `json:"regions"`
}

func (c *Client) regionIndex(ctx context.Context) (awsIndex, error) {
	var index awsIndex
	raw, err := c.request(ctx, awsIndexURL, nil, 256<<10)
	if err != nil {
		return index, err
	}
	if err := decodeCatalog(raw, &index); err != nil {
		return index, err
	}
	if index.FormatVersion != "v1.0" || len(index.Regions) == 0 || len(index.Regions) > 1024 {
		return index, errors.New("AWS region index format is unsupported")
	}
	if _, err := sourceTime(index.PublicationDate); err != nil {
		return index, err
	}
	for code, region := range index.Regions {
		if len(code) > 64 || !regionPattern.MatchString(code) || code != region.RegionCode {
			return index, errors.New("AWS region identity is invalid")
		}
		parts := strings.Split(region.URL, "/")
		if len(parts) != 8 || parts[0] != "" || strings.Join(parts[1:5], "/") != "offers/v1.0/aws/AWSDataTransfer" || !versionPattern.MatchString(parts[5]) || parts[6] != code || parts[7] != "index.json" {
			return index, errors.New("AWS region catalog path is invalid")
		}
	}
	return index, nil
}

func (c *Client) Regions(ctx context.Context) ([]string, error) {
	index, err := c.regionIndex(ctx)
	if err != nil {
		return nil, err
	}
	regions := make([]string, 0, len(index.Regions))
	for region := range index.Regions {
		regions = append(regions, region)
	}
	sort.Strings(regions)
	return regions, nil
}

func (c *Client) FetchPrice(ctx context.Context, provider, region string) (PriceSnapshot, error) {
	switch provider {
	case "aws":
		return c.fetchAWS(ctx, region)
	case "oci":
		return c.fetchOCI(ctx, region)
	default:
		return PriceSnapshot{}, errors.New("price provider must be aws or oci")
	}
}

type awsProduct struct {
	SKU        string            `json:"sku"`
	Family     string            `json:"productFamily"`
	Attributes map[string]string `json:"attributes"`
}
type awsDimension struct {
	Begin     string            `json:"beginRange"`
	End       string            `json:"endRange"`
	Unit      string            `json:"unit"`
	Price     map[string]string `json:"pricePerUnit"`
	AppliesTo []string          `json:"appliesTo"`
}
type awsTerm struct {
	SKU        string                  `json:"sku"`
	Effective  string                  `json:"effectiveDate"`
	Dimensions map[string]awsDimension `json:"priceDimensions"`
}
type awsCatalog struct {
	FormatVersion string                `json:"formatVersion"`
	OfferCode     string                `json:"offerCode"`
	Version       string                `json:"version"`
	Published     string                `json:"publicationDate"`
	Products      map[string]awsProduct `json:"products"`
	Terms         struct {
		OnDemand map[string]map[string]awsTerm `json:"OnDemand"`
	} `json:"terms"`
}
type rateTier struct {
	start, end, price float64
	infinite          bool
}

func (c *Client) fetchAWS(ctx context.Context, region string) (PriceSnapshot, error) {
	if len(region) > 64 || !regionPattern.MatchString(region) {
		return PriceSnapshot{}, errors.New("AWS region is invalid")
	}
	index, err := c.regionIndex(ctx)
	if err != nil {
		return PriceSnapshot{}, err
	}
	entry, exists := index.Regions[region]
	if !exists {
		return PriceSnapshot{}, errors.New("AWS region is not in the current catalog")
	}
	endpoint := "https://" + awsHost + entry.URL
	raw, err := c.request(ctx, endpoint, nil, 8<<20)
	if err != nil {
		return PriceSnapshot{}, err
	}
	var catalog awsCatalog
	if err := decodeCatalog(raw, &catalog); err != nil {
		return PriceSnapshot{}, err
	}
	if catalog.FormatVersion != "v1.0" || catalog.OfferCode != "AWSDataTransfer" || !versionPattern.MatchString(catalog.Version) || !strings.Contains(entry.URL, "/"+catalog.Version+"/") || len(catalog.Products) > 20000 {
		return PriceSnapshot{}, errors.New("AWS catalog identity is invalid")
	}
	publication, err := sourceTime(catalog.Published)
	if err != nil {
		return PriceSnapshot{}, err
	}
	selected := ""
	for sku, product := range catalog.Products {
		a := product.Attributes
		if product.Family == "Data Transfer" && a["servicecode"] == "AWSDataTransfer" && a["transferType"] == "AWS Outbound" && a["fromRegionCode"] == region && a["toLocation"] == "External" && a["toLocationType"] == "Other" && a["operation"] == "" {
			if selected != "" || product.SKU != sku {
				return PriceSnapshot{}, errors.New("AWS outbound product is ambiguous")
			}
			selected = sku
		}
	}
	if selected == "" {
		return PriceSnapshot{}, errors.New("AWS public outbound product was not found")
	}
	tiers, effective, err := awsTiers(catalog, selected)
	if err != nil {
		return PriceSnapshot{}, err
	}
	free := float64(0)
	freeFound := false
	for sku, product := range catalog.Products {
		a := product.Attributes
		if product.Family != "Data Transfer" || a["servicecode"] != "AWSDataTransfer" || a["transferType"] != "AWS Outbound" || a["fromLocation"] != "Global" || a["toLocation"] != "External" || a["usagetype"] != "Global-DataTransfer-Out-Bytes" {
			continue
		}
		terms := catalog.Terms.OnDemand[sku]
		for _, term := range terms {
			for _, dimension := range term.Dimensions {
				applies := false
				for _, target := range dimension.AppliesTo {
					if target == selected {
						applies = true
					}
				}
				if !applies {
					continue
				}
				if freeFound || product.SKU != sku || term.SKU != sku || dimension.Unit != "GB" {
					return PriceSnapshot{}, errors.New("AWS free allowance is ambiguous")
				}
				freeEffective, err := sourceTime(term.Effective)
				if err != nil {
					return PriceSnapshot{}, err
				}
				if freeEffective.After(time.Now()) {
					return PriceSnapshot{}, errors.New("AWS free allowance is not effective yet")
				}
				start, e1 := catalogNumber(dimension.Begin)
				end, e2 := catalogNumber(dimension.End)
				price, e3 := catalogNumber(dimension.Price["USD"])
				if e1 != nil || e2 != nil || e3 != nil || start != 0 || end <= 0 || price != 0 {
					return PriceSnapshot{}, errors.New("AWS free allowance format is invalid")
				}
				free, freeFound = end, true
			}
		}
	}
	// Regions outside the published appliesTo list do not inherit a free allowance.
	snapshot, err := makeSnapshot("aws", region, endpoint, raw, publication, effective, tiers, free)
	if err == nil {
		snapshot.Notes = append(snapshot.Notes, "AWS free allowance and paid volume tiers are shared across eligible account services and regions. No allowance is assigned to this host by default.")
		if !freeFound {
			snapshot.Notes = append(snapshot.Notes, "No associated global free allowance was found for this region.")
		}
	}
	return snapshot, err
}

func awsTiers(catalog awsCatalog, sku string) ([]rateTier, time.Time, error) {
	terms := catalog.Terms.OnDemand[sku]
	if len(terms) != 1 {
		return nil, time.Time{}, errors.New("AWS on-demand terms are missing or ambiguous")
	}
	var term awsTerm
	for _, t := range terms {
		term = t
	}
	if term.SKU != sku || len(term.Dimensions) == 0 || len(term.Dimensions) > 32 {
		return nil, time.Time{}, errors.New("AWS price dimensions are invalid")
	}
	effective, err := sourceTime(term.Effective)
	if err != nil {
		return nil, time.Time{}, err
	}
	if effective.After(time.Now()) {
		return nil, time.Time{}, errors.New("AWS price term is not effective yet")
	}
	tiers := make([]rateTier, 0, len(term.Dimensions))
	for _, d := range term.Dimensions {
		start, e1 := catalogNumber(d.Begin)
		price, e2 := catalogNumber(d.Price["USD"])
		end, e3 := catalogNumber(d.End)
		infinite := d.End == "Inf"
		if e1 != nil || e2 != nil || (e3 != nil && !infinite) || d.Unit != "GB" || len(d.AppliesTo) != 0 {
			return nil, time.Time{}, errors.New("AWS price dimension is unsupported")
		}
		tiers = append(tiers, rateTier{start, end, price, infinite})
	}
	return tiers, effective, nil
}

type ociCatalog struct {
	LastUpdated string `json:"lastUpdated"`
	Items       []struct {
		PartNumber    string `json:"partNumber"`
		Metric        string `json:"metricName"`
		Category      string `json:"serviceCategory"`
		Localizations []struct {
			Currency string `json:"currencyCode"`
			Prices   []struct {
				Model string   `json:"model"`
				Value *float64 `json:"value"`
				Min   *float64 `json:"rangeMin"`
				Max   *float64 `json:"rangeMax"`
			} `json:"prices"`
		} `json:"currencyCodeLocalizations"`
	} `json:"items"`
}

func (c *Client) fetchOCI(ctx context.Context, region string) (PriceSnapshot, error) {
	sku, exists := ociSKUs[region]
	if !exists {
		return PriceSnapshot{}, errors.New("OCI region must be one of the three supported geographic groups")
	}
	endpoint := "https://" + ociHost + "/pls/apex/cetools/api/v1/products/?partNumber=" + url.QueryEscape(sku) + "&currencyCode=USD"
	raw, err := c.request(ctx, endpoint, nil, 64<<10)
	if err != nil {
		return PriceSnapshot{}, err
	}
	var catalog ociCatalog
	if err := decodeCatalog(raw, &catalog); err != nil {
		return PriceSnapshot{}, err
	}
	publication, err := sourceTime(catalog.LastUpdated)
	if err != nil {
		return PriceSnapshot{}, err
	}
	if len(catalog.Items) != 1 {
		return PriceSnapshot{}, errors.New("OCI product selection is ambiguous")
	}
	item := catalog.Items[0]
	if item.PartNumber != sku || item.Metric != "Gigabyte Outbound Data Transfer Per Month" || item.Category != "Networking - Virtual Cloud Networks" || len(item.Localizations) != 1 || item.Localizations[0].Currency != "USD" {
		return PriceSnapshot{}, errors.New("OCI product identity is invalid")
	}
	prices := item.Localizations[0].Prices
	if len(prices) < 2 || len(prices) > 32 {
		return PriceSnapshot{}, errors.New("OCI price tiers are invalid")
	}
	tiers := make([]rateTier, 0, len(prices))
	for _, p := range prices {
		if p.Model != "PAY_AS_YOU_GO" || p.Value == nil || p.Min == nil || p.Max == nil || !validNumber(*p.Value) || !validNumber(*p.Min) || !validNumber(*p.Max) {
			return PriceSnapshot{}, errors.New("OCI price tier is unsupported")
		}
		tiers = append(tiers, rateTier{*p.Min, *p.Max, *p.Value, *p.Max == 999999999999999})
	}
	sort.Slice(tiers, func(i, j int) bool { return tiers[i].start < tiers[j].start })
	if tiers[0].start != 0 || tiers[0].price != 0 || tiers[0].end <= 0 || tiers[0].infinite {
		return PriceSnapshot{}, errors.New("OCI free tier is missing or invalid")
	}
	free := tiers[0].end
	tiers = tiers[1:]
	for i := range tiers {
		tiers[i].start -= free
		if !tiers[i].infinite {
			tiers[i].end -= free
		}
	}
	snapshot, err := makeSnapshot("oci", region, endpoint, raw, publication, publication, tiers, free)
	if err == nil {
		snapshot.Notes = append(snapshot.Notes, "OCI's published free allowance is shared rather than independently granted to each observed host; assign this host's remaining allowance explicitly.", "The source final upper range is 999999999999999 GB; this catalog sentinel is represented as the profile's final unbounded tier.", "OCI lastUpdated is a catalog update timestamp; it is not a separately supplied tariff effective date.")
	}
	return snapshot, err
}

func makeSnapshot(provider, region, source string, raw []byte, published, effective time.Time, tiers []rateTier, free float64) (PriceSnapshot, error) {
	sort.Slice(tiers, func(i, j int) bool { return tiers[i].start < tiers[j].start })
	previous := float64(0)
	profile := billing.Profile{SchemaVersion: 1, Name: strings.ToUpper(provider) + " Internet egress", Provider: provider, SourceRegion: region, Currency: "USD", EffectiveDate: effective.Format("2006-01-02"), SourceURL: source, UnitBytes: 1 << 30}
	for i, tier := range tiers {
		if !validNumber(tier.start) || !validNumber(tier.price) || tier.price == 0 || tier.start != previous || (tier.infinite != (i == len(tiers)-1)) || (!tier.infinite && (!validNumber(tier.end) || tier.end <= tier.start)) {
			return PriceSnapshot{}, errors.New("price tiers must be contiguous with one final unbounded tier")
		}
		end := tier.end
		if tier.infinite {
			end = 0
		}
		profile.InternetEgress = append(profile.InternetEgress, billing.Tier{UpToGB: end, PricePerGB: tier.price})
		previous = tier.end
	}
	if err := profile.Validate(); err != nil {
		return PriceSnapshot{}, errors.New("normalized price profile is invalid")
	}
	digest := sha256.Sum256(raw)
	return PriceSnapshot{SchemaVersion: 1, Profile: profile, Provider: provider, Region: region, SourceURL: source, PublishedAt: published, EffectiveAt: effective, FetchedAt: time.Now().UTC(), Digest: hex.EncodeToString(digest[:]), PublishedFreeGB: free, Notes: []string{"The profile's unit_bytes is an explicit GB-to-bytes calculation assumption, not verification of the provider's meter.", "Guest interface TX may include private, inter-region or duplicate traffic and omit gaps. Public Internet egress estimate only; shared tier usage, taxes, contracts, NAT, instance and storage charges are excluded."}}, nil
}

func validNumber(v float64) bool { return v >= 0 && v <= 1e15 && !math.IsNaN(v) && !math.IsInf(v, 0) }
func catalogNumber(s string) (float64, error) {
	if s == "" || len(s) > 32 {
		return 0, errors.New("invalid catalog number")
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || !validNumber(v) {
		return 0, errors.New("invalid catalog number")
	}
	return v, nil
}
func sourceTime(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil || t.Before(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)) || t.After(time.Now().Add(24*time.Hour)) {
		return time.Time{}, errors.New("catalog timestamp is invalid")
	}
	return t, nil
}
func decodeCatalog(raw []byte, value any) error {
	if err := checkJSON(raw); err != nil {
		return err
	}
	if err := json.Unmarshal(raw, value); err != nil {
		return fmt.Errorf("catalog schema is invalid")
	}
	return nil
}
