// SPDX-License-Identifier: MIT

package assets

import (
	"context"
	"errors"
	"fmt"
	"syscall"
)

// GeoDiagnostic is the complete public failure detail for an update attempt.
// Every field is an enum or bounded integer; URLs, paths, response bodies and
// credentials must never be copied into this daemon-readable contract.
type GeoDiagnostic struct {
	Stage      string `json:"stage"`
	Reason     string `json:"reason"`
	Edition    string `json:"edition,omitempty"`
	HTTPStatus int    `json:"http_status,omitempty"`
}

func (d GeoDiagnostic) Validate() error {
	invalid := errors.New("invalid GeoIP failure diagnostic")
	if d.Edition != "" && d.Edition != "City" && d.Edition != "ASN" {
		return invalid
	}
	if d.Reason == "http_status" {
		if d.Stage != "download" || d.HTTPStatus < 100 || d.HTTPStatus > 599 || d.HTTPStatus == 200 {
			return invalid
		}
	} else if d.HTTPStatus != 0 {
		return invalid
	}
	allowed := false
	switch d.Stage {
	case "configuration":
		allowed = d.Reason == "unavailable"
	case "credentials":
		allowed = d.Reason == "unavailable" || d.Reason == "invalid"
	case "download":
		switch d.Reason {
		case "failed", "dns_lookup_failed", "network_unreachable", "connection_refused", "timeout", "cancelled", "tls_failed", "redirect_rejected", "http_status", "response_read_failed", "response_too_large":
			allowed = true
		}
	case "staging":
		switch d.Reason {
		case "failed", "temporary_directory_unavailable", "temporary_directory_unsafe", "storage_full", "permission_denied":
			allowed = true
		}
	case "archive":
		allowed = d.Reason == "invalid"
	case "validation":
		switch d.Reason {
		case "validation_rejected", "resource_budget", "validator_privilege_drop_failed", "validator_setup_failed", "validator_failed", "validator_response_invalid", "source_changed", "timeout", "cancelled":
			allowed = true
		}
	case "activation":
		allowed = d.Reason == "failed" || d.Reason == "recovery_pending" || d.Reason == "metadata_save_failed"
	case "cleanup":
		allowed = d.Reason == "failed"
	}
	if !allowed {
		return invalid
	}
	return nil
}

// Summary renders only a validated diagnostic. Unknown data is never echoed.
func (d GeoDiagnostic) Summary() string {
	if d.Validate() != nil {
		return "GeoIP update failure detail is unavailable."
	}
	switch d.Reason {
	case "dns_lookup_failed":
		return "The GeoIP download host could not be resolved by DNS."
	case "network_unreachable":
		return "The GeoIP download network or host was unreachable."
	case "connection_refused":
		return "The GeoIP download connection was refused."
	case "timeout":
		if d.Stage == "validation" {
			return "The MMDB validator exceeded its time limit."
		}
		return "The GeoIP download exceeded its time limit."
	case "cancelled":
		return "The GeoIP operation was cancelled."
	case "tls_failed":
		return "The GeoIP download TLS handshake or certificate verification failed."
	case "redirect_rejected":
		return "The GeoIP download redirect was rejected by the endpoint policy."
	case "http_status":
		return fmt.Sprintf("The GeoIP download server returned HTTP %d.", d.HTTPStatus)
	case "response_read_failed":
		return "The GeoIP download response could not be read completely."
	case "response_too_large":
		return "The GeoIP download response exceeded its size limit."
	case "temporary_directory_unavailable":
		return "The GeoIP temporary directory was unavailable."
	case "temporary_directory_unsafe":
		return "The GeoIP temporary directory failed its ownership or permission checks."
	case "storage_full":
		return "GeoIP staging failed because storage space or quota was exhausted."
	case "permission_denied":
		return "GeoIP staging was denied by filesystem permissions."
	case "validation_rejected":
		return "MMDB database validation rejected the downloaded data."
	case "resource_budget":
		return "MMDB validation exceeded its resource budget."
	case "validator_privilege_drop_failed":
		return "The MMDB validator could not drop privileges; check updater capabilities and user namespace policy."
	case "validator_setup_failed":
		return "The MMDB validator could not establish its sandbox or resource limits."
	case "validator_failed":
		return "The MMDB validator failed before returning a verified result."
	case "validator_response_invalid":
		return "The MMDB validator returned an invalid result."
	case "source_changed":
		return "The staged MMDB file changed during validation."
	case "recovery_pending":
		return "GeoIP activation is blocked by a pending configuration recovery."
	case "metadata_save_failed":
		return "GeoIP data was verified, but update metadata could not be saved."
	}
	switch d.Stage {
	case "configuration":
		return "The configuration could not be loaded for the GeoIP update."
	case "credentials":
		if d.Reason == "invalid" {
			return "The saved MaxMind credential format is invalid."
		}
		return "The saved MaxMind credentials are unavailable."
	case "download":
		return "The GeoIP download request failed."
	case "staging":
		return "The downloaded GeoIP data could not be staged safely."
	case "archive":
		return "The GeoIP archive failed its integrity or structure checks."
	case "activation":
		return "The verified GeoIP data could not be activated."
	case "cleanup":
		return "Superseded GeoIP data could not be cleaned up."
	}
	return "GeoIP update failure detail is unavailable."
}

type geoDiagnosticError struct {
	diagnostic GeoDiagnostic
	cancelled  error // Only context cancellation sentinels may be retained.
}

func (e *geoDiagnosticError) Error() string { return e.diagnostic.Summary() }
func (e *geoDiagnosticError) Unwrap() error { return e.cancelled }

func geoFailure(stage, reason, edition string) error {
	label := ""
	if edition == "GeoLite2-City" || edition == "GeoLite2-ASN" {
		label = edition[len("GeoLite2-"):]
	}
	return &geoDiagnosticError{diagnostic: GeoDiagnostic{Stage: stage, Reason: reason, Edition: label}}
}

func geoCancelled(stage, edition string, err error) error {
	reason, sentinel := "cancelled", context.Canceled
	if errors.Is(err, context.DeadlineExceeded) {
		reason, sentinel = "timeout", context.DeadlineExceeded
	}
	failure := geoFailure(stage, reason, edition).(*geoDiagnosticError)
	failure.cancelled = sentinel
	return failure
}

func geoStagingFailure(edition string, err error) error {
	// io.Copy can stop in geoContextReader while persisting an MMDB member.
	// Preserve the caller's cancellation across that archive/staging boundary.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return geoCancelled("download", edition, err)
	}
	reason := "failed"
	switch {
	case errors.Is(err, syscall.ENOSPC), errors.Is(err, syscall.EDQUOT):
		reason = "storage_full"
	case errors.Is(err, syscall.EACCES), errors.Is(err, syscall.EPERM):
		reason = "permission_denied"
	}
	return geoFailure("staging", reason, edition)
}

// GeoUpdateDiagnostic extracts only typed, validated fields established at a
// trusted boundary. Matching an arbitrary error's text is deliberately avoided.
func GeoUpdateDiagnostic(err error) (GeoDiagnostic, bool) {
	var failure *geoDiagnosticError
	if errors.As(err, &failure) && failure != nil && failure.diagnostic.Validate() == nil {
		return failure.diagnostic, true
	}
	if edition, reason, ok := GeoValidationDiagnostic(err); ok {
		return GeoDiagnostic{Stage: "validation", Reason: reason, Edition: edition}, true
	}
	return GeoDiagnostic{}, false
}
