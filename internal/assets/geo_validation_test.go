// SPDX-License-Identifier: MIT

package assets

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestGeoValidationDiagnosticExposesOnlyAllowlistedFields(t *testing.T) {
	secret := "SYNTHETIC_SECRET_IN_URL_AND_BODY"
	for _, edition := range []string{"GeoLite2-City", "GeoLite2-ASN"} {
		for _, budget := range []bool{false, true} {
			cause := errors.New("https://invalid.example/?key=" + secret)
			wantReason := "validation_rejected"
			if budget {
				cause = fmt.Errorf("%s: %w", secret, errMMDBResourceBudget)
				wantReason = "resource_budget"
			}
			err := geoValidationFailure(context.Background(), edition, cause)
			if strings.Contains(err.Error(), secret) {
				t.Fatal("typed error exposed its cause")
			}
			label, reason, ok := GeoValidationDiagnostic(fmt.Errorf("unsafe outer %s: %w", secret, err))
			if !ok || label != strings.TrimPrefix(edition, "GeoLite2-") || reason != wantReason {
				t.Fatalf("unexpected safe fields: %q %q %v", label, reason, ok)
			}
			if budget && !errors.Is(err, errMMDBResourceBudget) {
				t.Fatal("resource cause lost")
			}
		}
	}
	for _, err := range []error{
		nil,
		errors.New(secret),
		fmt.Errorf("unsafe %s: %w", secret, context.Canceled),
		geoValidationFailure(context.Background(), "GeoLite2-City", context.DeadlineExceeded),
		geoValidationFailure(context.Background(), secret, errMMDBResourceBudget),
		&geoValidationError{edition: secret, reason: "resource_budget"},
		&geoValidationError{edition: "City", reason: secret},
	} {
		if edition, reason, ok := GeoValidationDiagnostic(err); ok || edition != "" || reason != "" {
			t.Fatal("unknown or cancelled error gained a public diagnostic")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := geoValidationFailure(ctx, "GeoLite2-City", errMMDBResourceBudget)
	if !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation mislabeled as validation failure")
	}
}

func TestExtractGeoAddsSafeValidationBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, reason string
		data         []byte
	}{
		{"invalid", "validation_rejected", []byte("synthetic invalid database")},
		{"budget", "resource_budget", validationBudgetFixture()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, edition := range []string{"GeoLite2-City", "GeoLite2-ASN"} {
				root, err := os.OpenRoot(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = root.Close() })
				raw := geoArchive(t, edition, func(_ *[]*tar.Header, data *[][]byte) { (*data)[0] = tc.data })
				_, _, err = extractGeo(context.Background(), root, raw, edition)
				label, reason, ok := GeoValidationDiagnostic(err)
				if !ok || label != strings.TrimPrefix(edition, "GeoLite2-") || reason != tc.reason {
					t.Fatalf("missing validation classification: %q %q %v", label, reason, ok)
				}
				if _, statErr := root.Stat(edition + ".mmdb"); !errors.Is(statErr, os.ErrNotExist) {
					t.Fatal("rejected database was staged")
				}
			}
		})
	}
}

func validationBudgetFixture() []byte {
	// Metadata map {k: array[32] of array[512] of empty strings}. Every
	// declaration is bounded, but the one record expands beyond 16,384 values.
	data := []byte{0xab, 0xcd, 0xef, 'M', 'a', 'x', 'M', 'i', 'n', 'd', '.', 'c', 'o', 'm', 0xe1, 0x41, 'k', 0x1d, 4, 3}
	for range 32 {
		data = append(data, 0x1e, 4, 0, 227)
		for range 512 {
			data = append(data, 0x40)
		}
	}
	return data
}
