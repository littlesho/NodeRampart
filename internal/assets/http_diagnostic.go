// SPDX-License-Identifier: MIT

package assets

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"syscall"
)

var errAssetRedirectRejected = errors.New("asset redirect is not permitted")

// Never retain a transport error: url.Error and certificate/network errors may
// contain signed redirect URLs, account data, certificate names or addresses.
type assetRequestError struct {
	reason string
	status int
}

func (e *assetRequestError) Error() string {
	if e.reason == "http_status" {
		return fmt.Sprintf("asset download returned HTTP %d", e.status)
	}
	return "asset request failed: " + e.reason
}

func (e *assetRequestError) Unwrap() error {
	switch e.reason {
	case "cancelled":
		return context.Canceled
	case "timeout":
		return context.DeadlineExceeded
	}
	return nil
}

func classifyAssetRequestError(err error) *assetRequestError {
	reason := "failed"
	var dns *net.DNSError
	var certificate *tls.CertificateVerificationError
	var authority x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var invalidCertificate x509.CertificateInvalidError
	var record tls.RecordHeaderError
	var alert tls.AlertError
	var network net.Error
	switch {
	case errors.Is(err, context.Canceled):
		reason = "cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		reason = "timeout"
	case errors.Is(err, errAssetRedirectRejected):
		reason = "redirect_rejected"
	case errors.As(err, &dns):
		reason = "dns_lookup_failed"
	case errors.As(err, &certificate), errors.As(err, &authority), errors.As(err, &hostname), errors.As(err, &invalidCertificate), errors.As(err, &record), errors.As(err, &alert):
		reason = "tls_failed"
	case errors.Is(err, syscall.ENETUNREACH), errors.Is(err, syscall.EHOSTUNREACH):
		reason = "network_unreachable"
	case errors.Is(err, syscall.ECONNREFUSED):
		reason = "connection_refused"
	case errors.As(err, &network) && network.Timeout():
		reason = "timeout"
	}
	return &assetRequestError{reason: reason}
}

func geoDownloadFailure(edition string, err error) error {
	var request *assetRequestError
	if errors.As(err, &request) && request != nil {
		failure := geoFailure("download", request.reason, edition).(*geoDiagnosticError)
		failure.diagnostic.HTTPStatus = request.status
		failure.cancelled = request.Unwrap()
		return failure
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return geoCancelled("download", edition, err)
	}
	return geoFailure("download", "failed", edition)
}
