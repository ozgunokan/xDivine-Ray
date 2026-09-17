// Package certpin fetches a server's TLS certificate chain and turns it into
// the pin the core expects.
//
// This exists because newer Xray builds removed `allowInsecure`, and a great
// many real deployments need it: the server presents a self-signed certificate
// (often under a borrowed SNI), so ordinary verification can never succeed.
// Pinning is the replacement — and a strictly better one, because "accept any
// certificate" accepts an interceptor's too, while a pin accepts exactly one
// chain.
//
// The honest caveat, which the caller must pass on: whatever is on the wire at
// fetch time is what gets pinned. Fetching through something that is already
// intercepting the connection pins the interceptor. So the fetch has to happen
// from the device that will use it, over the path it will use, and the operator
// should look at what came back.
package certpin

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"net"
	"strconv"
	"time"
)

// CertInfo describes one certificate in the chain, so an operator can see what
// they are about to trust.
type CertInfo struct {
	Subject  string `json:"subject"`
	Issuer   string `json:"issuer"`
	NotAfter string `json:"not_after"`
	// SHA256 is the hex digest of this certificate on its own, which is what
	// `xray tls ping` and `xray tls hash` print, so the two can be compared.
	SHA256 string `json:"sha256"`
	IsCA   bool   `json:"is_ca,omitempty"`
}

// Result is what a fetch produced.
type Result struct {
	// Pin goes into the profile's pinned_cert field and from there into the
	// core's pinnedPeerCertSha256: the hex SHA-256 of the leaf certificate.
	Pin string `json:"pin"`
	// Endpoint and SNI record what was actually dialled, because a pin is only
	// meaningful together with them.
	Endpoint string     `json:"endpoint"`
	SNI      string     `json:"sni"`
	Chain    []CertInfo `json:"chain"`

	// Trusted reports whether a public CA vouches for the chain, ignoring which
	// name it was issued for.
	//
	// NameMatch reports the separate question of whether it was issued for the
	// SNI being used. The two are kept apart because conflating them produces a
	// badly wrong message: a server with a perfectly valid Let's Encrypt
	// certificate, reached under a borrowed SNI, would be reported as vouched
	// for by nobody — alarming and false. Untrusted and name-mismatched are
	// different situations and deserve different words.
	Trusted   bool `json:"trusted"`
	NameMatch bool `json:"name_match"`
}

// Fetch performs a TLS handshake and returns the pin for the chain the server
// presented.
//
// Verification is deliberately skipped: the whole point is to reach a server
// whose certificate would not verify. The pin that comes out is what makes the
// connection safe afterwards.
func Fetch(host string, port int, sni string, timeout time.Duration) (*Result, error) {
	if host == "" {
		return nil, fmt.Errorf("no server address")
	}
	if port <= 0 || port > 65535 {
		return nil, fmt.Errorf("invalid port %d", port)
	}
	if sni == "" {
		sni = host
	}
	addr := net.JoinHostPort(host, strconv.Itoa(port))

	dialer := &net.Dialer{Timeout: timeout}
	conn, err := tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{
		ServerName: sni,
		// #nosec G402 -- pinning is the goal; see the package comment.
		InsecureSkipVerify: true,
		MinVersion:         tls.VersionTLS12,
	})
	if err != nil {
		return nil, fmt.Errorf("TLS handshake with %s (sni %s): %w", addr, sni, err)
	}
	defer conn.Close()

	state := conn.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return nil, fmt.Errorf("%s presented no certificate", addr)
	}

	raw := make([][]byte, 0, len(state.PeerCertificates))
	chain := make([]CertInfo, 0, len(state.PeerCertificates))
	for _, c := range state.PeerCertificates {
		raw = append(raw, c.Raw)
		sum := sha256.Sum256(c.Raw)
		chain = append(chain, CertInfo{
			Subject:  c.Subject.String(),
			Issuer:   c.Issuer.String(),
			NotAfter: c.NotAfter.UTC().Format(time.RFC3339),
			SHA256:   hex.EncodeToString(sum[:]),
			IsCA:     c.IsCA,
		})
	}

	return &Result{
		Pin:       LeafHash(raw[0]),
		Endpoint:  addr,
		SNI:       sni,
		Chain:     chain,
		Trusted:   verifies(state.PeerCertificates, ""),
		NameMatch: verifies(state.PeerCertificates, sni),
	}, nil
}

// LeafHash computes the value the core compares against: the SHA-256 of the
// leaf certificate's DER, in lowercase hex.
//
// This is the core's own definition of pinnedPeerCertSha256 and it was arrived
// at by experiment, not by reading: the similarly named
// pinnedPeerCertificateChainSha256 takes base64 digests of the whole chain and,
// crucially, does not relax verification at all — a self-signed server with only
// that field set still fails with "certificate signed by unknown authority".
// The hex leaf digest is the one that replaces allowInsecure, verified against a
// real core and a real self-signed server, including the case where the SNI does
// not match the certificate.
func LeafHash(der []byte) string {
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}

// verifies reports whether the chain validates against the system roots. An
// empty name skips the hostname check, which is how chain trust and name match
// are answered separately.
func verifies(certs []*x509.Certificate, name string) bool {
	if len(certs) == 0 {
		return false
	}
	pool := x509.NewCertPool()
	for _, c := range certs[1:] {
		pool.AddCert(c)
	}
	_, err := certs[0].Verify(x509.VerifyOptions{
		DNSName:       name,
		Intermediates: pool,
	})
	return err == nil
}
