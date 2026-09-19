// SPDX-License-Identifier: MIT

package assets

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oschwald/maxminddb-golang"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(r *http.Request, status int, body []byte) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body)), Request: r}
}
func testClient(f transportFunc) *Client { return &Client{HTTP: &http.Client{Transport: f}} }

func TestRequestBoundariesAndRedaction(t *testing.T) {
	secret := "synthetic-secret-only"
	credentials := Credentials{"123", secret}
	for _, failure := range []string{"transport", "status", "redirect", "overflow"} {
		t.Run(failure, func(t *testing.T) {
			client := testClient(func(r *http.Request) (*http.Response, error) {
				switch failure {
				case "transport":
					return nil, errors.New("unsafe-url?secret=" + secret)
				case "status":
					return response(r, 401, []byte(secret)), nil
				case "redirect":
					v := response(r, 302, nil)
					v.Header.Set("Location", "https://invalid.example/?secret="+secret)
					return v, nil
				default:
					v := response(r, 200, []byte(strings.Repeat("x", 33)))
					v.ContentLength = -1
					return v, nil
				}
			})
			_, err := client.request(context.Background(), "https://"+geoHost+"/fixture", &credentials, 32)
			if err == nil || strings.Contains(err.Error(), secret) {
				t.Fatalf("unsafe or missing error: %v", err)
			}
		})
	}
	called := 0
	client := testClient(func(r *http.Request) (*http.Response, error) {
		called++
		if called == 1 {
			id, key, ok := r.BasicAuth()
			if !ok || id != "123" || key != secret {
				t.Fatal("initial authorization missing")
			}
			v := response(r, 302, nil)
			v.Header.Set("Location", "https://"+r2Host+"/fixture?signature=synthetic")
			return v, nil
		}
		if r.Header.Get("Authorization") != "" {
			t.Fatal("redirect leaked authorization")
		}
		if _, ok := r.Context().Deadline(); !ok {
			t.Fatal("request has no deadline")
		}
		return response(r, 200, []byte("ok")), nil
	})
	if _, err := client.request(context.Background(), "https://"+geoHost+"/fixture", &credentials, 32); err != nil || called != 2 {
		t.Fatalf("redirect: %v", err)
	}
	for _, endpoint := range []string{"http://" + geoHost + "/", "https://user@" + geoHost + "/", "https://" + geoHost + ":443/", "https://" + geoHost + ".example/"} {
		if _, err := client.request(context.Background(), endpoint, &credentials, 32); err == nil {
			t.Fatalf("accepted %s", endpoint)
		}
	}
}

func TestPriceCatalogNormalization(t *testing.T) {
	aws := awsFixture(t)
	oci := ociFixture(t)
	client := testClient(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "" {
			t.Fatal("catalog requested credentials")
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "region_index.json"):
			return response(r, 200, []byte(awsIndexFixture)), nil
		case r.URL.Host == awsHost:
			return response(r, 200, aws), nil
		case r.URL.Host == ociHost:
			return response(r, 200, oci), nil
		default:
			t.Fatal("unexpected endpoint")
			return nil, nil
		}
	})
	regions, err := client.Regions(context.Background())
	if err != nil || len(regions) != 2 || regions[1] != "us-east-1-wl1-bna1" {
		t.Fatalf("regions %#v %v", regions, err)
	}
	for _, provider := range []string{"aws", "oci"} {
		region := "us-east-1"
		if provider == "oci" {
			region = "north-america-europe-uk"
		}
		snapshot, err := client.FetchPrice(context.Background(), provider, region)
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.Profile.UnitBytes != 1<<30 || snapshot.Profile.FreeGB != 0 || len(snapshot.Digest) != 64 || snapshot.PublishedAt.IsZero() || snapshot.FetchedAt.IsZero() {
			t.Fatalf("missing metadata %#v", snapshot)
		}
		p := snapshot.Profile
		if provider == "aws" {
			if snapshot.PublishedFreeGB != 100 || p.InternetEgress[0].UpToGB != 10240 {
				t.Fatal("AWS free association/tier mismatch")
			}
			p.FreeGB = 100
			if got := p.Estimate(101 << 30); got.BillableGB != 1 || got.Cost != .09 {
				t.Fatalf("AWS free subtraction %#v", got)
			}
		} else {
			if snapshot.PublishedFreeGB != 10240 || len(p.InternetEgress) != 1 || p.InternetEgress[0].UpToGB != 0 {
				t.Fatal("OCI paid-tier normalization mismatch")
			}
			p.FreeGB = 10240
			if got := p.Estimate(10241 << 30); got.BillableGB != 1 || got.Cost != .0085 {
				t.Fatalf("OCI free subtraction %#v", got)
			}
		}
	}
}

const awsIndexFixture = `{"formatVersion":"v1.0","publicationDate":"2026-01-02T00:00:00Z","regions":{"us-east-1":{"regionCode":"us-east-1","currentVersionUrl":"/offers/v1.0/aws/AWSDataTransfer/20260102000000/us-east-1/index.json"},"us-east-1-wl1-bna1":{"regionCode":"us-east-1-wl1-bna1","currentVersionUrl":"/offers/v1.0/aws/AWSDataTransfer/20260102000000/us-east-1-wl1-bna1/index.json"}}}`

func awsFixture(t *testing.T) []byte {
	t.Helper()
	c := awsCatalog{FormatVersion: "v1.0", OfferCode: "AWSDataTransfer", Version: "20260102000000", Published: "2026-01-02T00:00:00Z", Products: map[string]awsProduct{
		"paid": {SKU: "paid", Family: "Data Transfer", Attributes: map[string]string{"servicecode": "AWSDataTransfer", "transferType": "AWS Outbound", "fromRegionCode": "us-east-1", "toLocation": "External", "toLocationType": "Other", "operation": ""}},
		"free": {SKU: "free", Family: "Data Transfer", Attributes: map[string]string{"servicecode": "AWSDataTransfer", "transferType": "AWS Outbound", "fromLocation": "Global", "toLocation": "External", "usagetype": "Global-DataTransfer-Out-Bytes"}},
	}}
	c.Terms.OnDemand = map[string]map[string]awsTerm{
		"paid": {"offer": {SKU: "paid", Effective: "2026-01-01T00:00:00Z", Dimensions: map[string]awsDimension{"first": {Begin: "0", End: "10240", Unit: "GB", Price: map[string]string{"USD": "0.09"}}, "last": {Begin: "10240", End: "Inf", Unit: "GB", Price: map[string]string{"USD": "0.085"}}}}},
		"free": {"offer": {SKU: "free", Effective: "2026-01-01T00:00:00Z", Dimensions: map[string]awsDimension{"free": {Begin: "0", End: "100", Unit: "GB", Price: map[string]string{"USD": "0"}, AppliesTo: []string{"paid"}}}}},
	}
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func ociFixture(t *testing.T) []byte {
	t.Helper()
	return []byte(`{"lastUpdated":"2026-01-02T00:00:00Z","items":[{"partNumber":"B88327","metricName":"Gigabyte Outbound Data Transfer Per Month","serviceCategory":"Networking - Virtual Cloud Networks","currencyCodeLocalizations":[{"currencyCode":"USD","prices":[{"model":"PAY_AS_YOU_GO","value":0,"rangeMin":0,"rangeMax":10240},{"model":"PAY_AS_YOU_GO","value":0.0085,"rangeMin":10240,"rangeMax":999999999999999}]}]}]}`)
}

func TestRejectMalformedCatalogs(t *testing.T) {
	for _, test := range []struct {
		name, provider string
		mutate         func([]byte) []byte
	}{
		{"tier-gap", "aws", func(b []byte) []byte {
			return bytes.ReplaceAll(b, []byte(`"beginRange":"10240"`), []byte(`"beginRange":"10241"`))
		}},
		{"nan", "aws", func(b []byte) []byte { return bytes.ReplaceAll(b, []byte(`"0.09"`), []byte(`"NaN"`)) }},
		{"currency", "aws", func(b []byte) []byte { return bytes.ReplaceAll(b, []byte(`"USD"`), []byte(`"EUR"`)) }},
		{"bad-free", "aws", func(b []byte) []byte {
			return bytes.ReplaceAll(b, []byte(`"endRange":"100"`), []byte(`"endRange":"Inf"`))
		}},
		{"duplicate", "aws", func(b []byte) []byte {
			return bytes.Replace(b, []byte(`"formatVersion":"v1.0"`), []byte(`"formatVersion":"v1.0","FORMATVERSION":"v1.0"`), 1)
		}},
		{"wrong-sku", "oci", func(b []byte) []byte { return bytes.ReplaceAll(b, []byte(`B88327`), []byte(`B93455`)) }},
		{"missing-min", "oci", func(b []byte) []byte { return bytes.Replace(b, []byte(`"rangeMin":0,`), nil, 1) }},
		{"old-schema", "oci", func(b []byte) []byte {
			return bytes.Replace(b, []byte(`currencyCodeLocalizations`), []byte(`prices`), 1)
		}},
		{"wrong-sentinel", "oci", func(b []byte) []byte { return bytes.Replace(b, []byte(`999999999999999`), []byte(`999999`), 1) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw := awsFixture(t)
			region := "us-east-1"
			if test.provider == "oci" {
				raw = ociFixture(t)
				region = "north-america-europe-uk"
			}
			raw = test.mutate(raw)
			client := testClient(func(r *http.Request) (*http.Response, error) {
				if strings.HasSuffix(r.URL.Path, "region_index.json") {
					return response(r, 200, []byte(awsIndexFixture)), nil
				}
				return response(r, 200, raw), nil
			})
			if _, err := client.FetchPrice(context.Background(), test.provider, region); err == nil {
				t.Fatal("malformed catalog accepted")
			}
		})
	}
	for _, raw := range []string{`{"a":1,"a":2}`, `{"a":1} {}`, strings.Repeat("[", 18) + strings.Repeat("]", 18)} {
		if checkJSON([]byte(raw)) == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}

func TestCatalogEndpointAndSizeBounds(t *testing.T) {
	for _, target := range []struct {
		provider, region string
		maximum          int64
	}{
		{"aws", "us-east-1", 8 << 20},
		{"oci", "north-america-europe-uk", 64 << 10},
	} {
		t.Run(target.provider, func(t *testing.T) {
			client := testClient(func(r *http.Request) (*http.Response, error) {
				if strings.HasSuffix(r.URL.Path, "region_index.json") {
					return response(r, 200, []byte(awsIndexFixture)), nil
				}
				v := response(r, 200, nil)
				v.ContentLength = target.maximum + 1
				return v, nil
			})
			if _, err := client.FetchPrice(context.Background(), target.provider, target.region); err == nil {
				t.Fatal("oversized catalog accepted")
			}
		})
	}
	for _, fixture := range []string{
		strings.Replace(awsIndexFixture, "/offers/v1.0/aws/AWSDataTransfer/20260102000000/us-east-1/index.json", "https://evil.invalid/catalog", 1),
		strings.Replace(awsIndexFixture, "/20260102000000/", "/../../", 1),
	} {
		calls := 0
		client := testClient(func(r *http.Request) (*http.Response, error) { calls++; return response(r, 200, []byte(fixture)), nil })
		if _, err := client.FetchPrice(context.Background(), "aws", "us-east-1"); err == nil || calls != 1 {
			t.Fatal("unsafe version path followed")
		}
	}
}

func TestMMDBTreeBounds(t *testing.T) {
	for _, kind := range []string{"cycle", "expanded-dag"} {
		t.Run(kind, func(t *testing.T) {
			data := syntheticMMDB("GeoLite2-City", 1767225600)
			if kind == "cycle" {
				data[2] = 0
				if _, err := verifyMMDB(context.Background(), data, "GeoLite2-City"); err == nil {
					t.Fatal("tree cycle accepted")
				}
				return
			}
			// 26 nodes sharing both children grow to > 40 million visits.
			tree := make([]byte, 26*6)
			for node := 0; node < 26; node++ {
				tree[node*6+2] = byte(node + 1)
				tree[node*6+5] = byte(node + 1)
			}
			if err := boundMMDBTree(context.Background(), tree, maxminddb.Metadata{NodeCount: 26, RecordSize: 24}); err == nil {
				t.Fatal("exponential tree accepted")
			}
		})
	}
}

func TestGeoFailureDoesNotReturnPartialBundle(t *testing.T) {
	client := testClient(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "GeoLite2-City") {
			return response(r, 200, geoArchive(t, "GeoLite2-City", nil)), nil
		}
		return response(r, 401, []byte("synthetic rejection")), nil
	})
	bundle, err := client.DownloadGeo(context.Background(), Credentials{"123", "synthetic-key"})
	if err == nil || bundle != nil {
		t.Fatalf("partial bundle returned: %v", err)
	}
}

func TestDownloadGeoAndSafeCleanup(t *testing.T) {
	client := testClient(func(r *http.Request) (*http.Response, error) {
		edition := "GeoLite2-City"
		if strings.Contains(r.URL.Path, "GeoLite2-ASN") {
			edition = "GeoLite2-ASN"
		}
		return response(r, 200, geoArchive(t, edition, nil)), nil
	})
	bundle, err := client.DownloadGeo(context.Background(), Credentials{"123", "synthetic-key"})
	if err != nil {
		t.Fatal(err)
	}
	directory := bundle.Directory
	info, err := os.Stat(directory)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatal("staging directory not private")
	}
	if len(bundle.Notices) != 4 || bundle.CityBuild.IsZero() || bundle.ASNBuild.IsZero() {
		t.Fatalf("missing metadata %#v", bundle)
	}
	for _, name := range []string{bundle.CityPath, bundle.ASNPath} {
		info, err := os.Stat(name)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatal("staged file not private")
		}
	}
	other := t.TempDir()
	marker := filepath.Join(other, "keep")
	if err := os.WriteFile(marker, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	bundle.Directory = other
	if err := bundle.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("staging was not cleaned")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("cleanup followed exported directory mutation")
	}
	if err := bundle.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRejectUnsafeGeoArchives(t *testing.T) {
	for _, kind := range []string{"traversal", "symlink", "duplicate", "bad-mmdb", "wrong-type", "future-build", "oversize", "trailer"} {
		t.Run(kind, func(t *testing.T) {
			mutate := func(headers *[]*tar.Header, data *[][]byte) {
				switch kind {
				case "traversal":
					(*headers)[0].Name = "../GeoLite2-City.mmdb"
				case "symlink":
					(*headers)[0].Typeflag = tar.TypeSymlink
					(*headers)[0].Linkname = "/outside"
					(*data)[0] = nil
				case "duplicate":
					*headers = append(*headers, (*headers)[0])
					*data = append(*data, (*data)[0])
				case "bad-mmdb":
					(*data)[0] = []byte("not a database")
				case "wrong-type":
					(*data)[0] = syntheticMMDB("GeoLite2-ASN", 1767225600)
				case "future-build":
					(*data)[0] = syntheticMMDB("GeoLite2-City", uint64(time.Now().Add(365*24*time.Hour).Unix()))
				case "oversize":
					(*headers)[0].Size = maxDatabase + 1
				}
			}
			raw := geoArchive(t, "GeoLite2-City", mutate)
			if kind == "trailer" {
				raw[len(raw)-5] ^= 1
			}
			root, err := os.OpenRoot(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			if _, _, err := extractGeo(context.Background(), root, raw, "GeoLite2-City"); err == nil {
				t.Fatal("unsafe archive accepted")
			}
		})
	}
}

func geoArchive(t *testing.T, edition string, mutate func(*[]*tar.Header, *[][]byte)) []byte {
	t.Helper()
	headers := []*tar.Header{{Name: edition + "_20260101/" + edition + ".mmdb", Mode: 0o644, Typeflag: tar.TypeReg}, {Name: edition + "_20260101/LICENSE.txt", Mode: 0o644, Typeflag: tar.TypeReg}, {Name: edition + "_20260101/COPYRIGHT.txt", Mode: 0o644, Typeflag: tar.TypeReg}}
	data := [][]byte{syntheticMMDB(edition, 1767225600), []byte("Synthetic MIT-licensed test data, created for NodeRampart."), []byte("Synthetic fixture copyright NodeRampart contributors.")}
	if mutate != nil {
		mutate(&headers, &data)
	}
	var output bytes.Buffer
	zip := gzip.NewWriter(&output)
	archive := tar.NewWriter(zip)
	for i, h := range headers {
		if h.Size == 0 {
			h.Size = int64(len(data[i]))
		}
		if err := archive.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Size > maxDatabase {
			break
		}
		if _, err := archive.Write(data[i]); err != nil {
			t.Fatal(err)
		}
	}
	_ = archive.Close()
	if err := zip.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

// This minimal empty IPv4 search tree is generated here, not copied from a
// licensed geolocation database. It is a valid binary MMDB with synthetic metadata.
func syntheticMMDB(edition string, epoch uint64) []byte {
	text := func(s string) []byte {
		if len(s) < 29 {
			return append([]byte{0x40 | byte(len(s))}, []byte(s)...)
		}
		return append([]byte{0x5d, byte(len(s) - 29)}, []byte(s)...)
	}
	integer := func(value uint64, kind byte) []byte {
		data := []byte{}
		for value > 0 {
			data = append([]byte{byte(value)}, data...)
			value >>= 8
		}
		if kind <= 7 {
			return append([]byte{kind<<5 | byte(len(data))}, data...)
		}
		return append([]byte{byte(len(data)), kind - 7}, data...)
	}
	metadata := []byte{0xe9}
	add := func(key string, value []byte) {
		metadata = append(metadata, text(key)...)
		metadata = append(metadata, value...)
	}
	add("binary_format_major_version", integer(2, 5))
	add("binary_format_minor_version", integer(0, 5))
	add("build_epoch", integer(epoch, 9))
	add("database_type", text(edition))
	description := append([]byte{0xe1}, text("en")...)
	description = append(description, text("Synthetic empty fixture")...)
	add("description", description)
	add("ip_version", integer(4, 5))
	add("node_count", integer(1, 6))
	add("record_size", integer(24, 5))
	add("languages", append([]byte{1, 4}, text("en")...))
	data := append([]byte{0, 0, 1, 0, 0, 1}, make([]byte, 16)...)
	data = append(data, []byte{0xab, 0xcd, 0xef, 'M', 'a', 'x', 'M', 'i', 'n', 'd', '.', 'c', 'o', 'm'}...)
	return append(data, metadata...)
}
