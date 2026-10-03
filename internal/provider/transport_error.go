package provider

import (
	"crypto/x509"
	"errors"
	"io"
	"net"
	"syscall"
)

// TransportError carries an allowlisted cause category, deliberately excluding
// raw URL/error text. A failure does not resolve submission uncertainty.
type TransportError struct{ Category string }

func (e *TransportError) Error() string {
	return "provider transport failed (" + e.Category + "); submission outcome unknown"
}
func transportCategory(err error) string {
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return "dns"
	}
	var cert x509.UnknownAuthorityError
	if errors.As(err, &cert) {
		return "tls"
	}
	var hostname x509.HostnameError
	if errors.As(err, &hostname) {
		return "tls"
	}
	var network net.Error
	if errors.As(err, &network) && network.Timeout() {
		return "timeout"
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return "connection_refused"
	}
	if errors.Is(err, syscall.ECONNRESET) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return "connection_closed"
	}
	return "network"
}
