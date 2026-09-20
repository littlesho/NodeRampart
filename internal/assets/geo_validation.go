// SPDX-License-Identifier: MIT

package assets

import (
	"context"
	"errors"
)

// geoValidationError is created only at the MMDB validation boundary. Neither
// Error nor GeoValidationDiagnostic formats the underlying error or input.
type geoValidationError struct {
	edition, reason string
	cause           error
}

func (e *geoValidationError) Error() string { return "GeoIP MMDB validation failed" }
func (e *geoValidationError) Unwrap() error { return e.cause }

func geoValidationFailure(ctx context.Context, edition string, err error) error {
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	label := ""
	switch edition {
	case "GeoLite2-City":
		label = "City"
	case "GeoLite2-ASN":
		label = "ASN"
	default:
		return err
	}
	reason := "validation_rejected"
	if errors.Is(err, errMMDBResourceBudget) {
		reason = "resource_budget"
	}
	return &geoValidationError{edition: label, reason: reason, cause: err}
}

// GeoValidationDiagnostic returns only fixed, presentation-safe fields for an
// error produced while validating a supported GeoIP MMDB. Unknown errors and
// cancellations have no diagnostic; callers must keep their generic fallback.
func GeoValidationDiagnostic(err error) (edition, reason string, ok bool) {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "", "", false
	}
	var failure *geoValidationError
	if !errors.As(err, &failure) || failure == nil {
		return "", "", false
	}
	if failure.edition != "City" && failure.edition != "ASN" {
		return "", "", false
	}
	if failure.reason != "resource_budget" && failure.reason != "validation_rejected" {
		return "", "", false
	}
	return failure.edition, failure.reason, true
}
