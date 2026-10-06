package netenv

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"sync"
	"time"
)

// Does this processor do AES in hardware?
//
// It decides which TLS fingerprint is the fast one on this device, and the
// difference is large. A ClientHello carries the client's cipher suites in
// preference order and the server takes the first one it shares. Go's crypto/tls
// builds that order from what the CPU can do: AES-GCM first with hardware AES,
// ChaCha20-Poly1305 first without it. uTLS discards that by design, because its
// job is to send the list a browser sends — and a browser's list puts AES-GCM
// first, having been written for machines where AES is free.
//
// So on a router with no AES instructions, every browser fingerprint talks the
// server into AES-GCM and the device then does AES in software for every byte of
// every connection, while ChaCha20 — designed for exactly that processor — sits
// unused at the bottom of the list. Choosing the undisguised fingerprint hands
// the ordering back to Go and roughly doubles the throughput. On a processor
// that *has* AES instructions it changes nothing, because Go would have put
// AES-GCM first too.
//
// Which means the whole recommendation turns on one fact about the CPU, and
// nobody can be expected to know it offhand. Hence this.
//
// It is not read out of /proc/cpuinfo. That file spells the answer differently
// on every architecture — "flags" with aes on x86, "Features" with aes on
// arm64, nothing at all on MIPS — and a missing word is indistinguishable from
// a word this code did not know to look for, which would read as "no hardware
// AES" on a device that has it.
//
// Instead Go is asked directly, by making it choose. A TLS 1.3 handshake is run
// between a client and a server that are both this process, over an in-memory
// pipe, with crypto/tls's own defaults on both sides. Whichever suite comes out
// is the one Go's preference order put first, which is the boolean this file
// wants — not a proxy for it, the thing itself. It is the same decision the
// core's own Go build makes on the same processor.

// CryptoAccel is what Detect reports: "yes", "no", or "" when the question
// could not be answered. Three states rather than a boolean, because "we could
// not tell" and "it has none" lead somewhere different and an interface that
// conflates them recommends a change nobody asked for.
const (
	AccelYes     = "yes"
	AccelNo      = "no"
	AccelUnknown = ""
)

var (
	aesOnce   sync.Once
	aesAnswer string
)

// AESAccel reports whether this CPU does AES in hardware, as Go sees it.
//
// The answer cannot change while the process runs, so it is found once. It costs
// one key generation and one handshake, both in memory.
func AESAccel() string {
	aesOnce.Do(func() { aesAnswer = probeAESAccel() })
	return aesAnswer
}

func probeAESAccel() string {
	cert, err := selfSignedCert()
	if err != nil {
		return AccelUnknown
	}

	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	deadline := time.Now().Add(aesProbeBudget)
	_ = client.SetDeadline(deadline)
	_ = server.SetDeadline(deadline)

	// Both ends are deliberately left on crypto/tls's defaults, with nothing
	// pinned: a CipherSuites list of our own would answer our own question back
	// to us. TLS 1.3 both ways, because that is what the tunnel uses and
	// because its three suites are exactly the ones in question.
	srv := tls.Server(server, &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS13,
		MaxVersion:   tls.VersionTLS13,
	})
	cli := tls.Client(client, &tls.Config{
		// Nothing is being trusted here: the certificate was generated three
		// lines ago, the connection is a pipe inside this process, and no byte
		// of it leaves the process. What is being read off the handshake is
		// which cipher suite Go chose.
		InsecureSkipVerify: true,
		ServerName:         "aes.probe.invalid",
		MinVersion:         tls.VersionTLS13,
		MaxVersion:         tls.VersionTLS13,
	})

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Handshake() }()
	if err := cli.Handshake(); err != nil {
		return AccelUnknown
	}
	if err := <-errCh; err != nil {
		return AccelUnknown
	}

	switch cli.ConnectionState().CipherSuite {
	case tls.TLS_AES_128_GCM_SHA256, tls.TLS_AES_256_GCM_SHA384:
		return AccelYes
	case tls.TLS_CHACHA20_POLY1305_SHA256:
		return AccelNo
	}
	// A suite neither branch names means a future Go changed the set, and
	// guessing which way it leans would be worse than saying nothing.
	return AccelUnknown
}

// aesProbeBudget bounds a handshake between two halves of one process. It
// cannot be slow; it can only fail to happen, and then this must not be what
// holds up the first status page of a boot.
const aesProbeBudget = 5 * time.Second

// selfSignedCert makes a throwaway certificate for the probe. P-256 because it
// is the cheapest key generation available that TLS 1.3 will accept, which on a
// router is worth caring about.
func selfSignedCert() (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "aes.probe.invalid"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     []string{"aes.probe.invalid"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, nil
}
