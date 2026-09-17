package certpin

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"math/big"
	"net"
	"testing"
	"time"
)

// The pin has one job: be the exact value the core compares against. So the
// test pins down the algorithm — hex SHA-256 of the leaf's DER — rather than
// just checking it returns something.
func TestLeafHashIsHexSHA256OfTheDER(t *testing.T) {
	der := []byte("not really a certificate, but the hash does not care")
	want := sha256.Sum256(der)
	got := LeafHash(der)
	if got != hex.EncodeToString(want[:]) {
		t.Fatalf("LeafHash = %q", got)
	}
	if len(got) != 64 {
		t.Fatalf("want 64 hex characters, got %d", len(got))
	}
	for _, c := range got {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			t.Fatalf("non-hex character %q in %q", c, got)
		}
	}
}

// Fetch has to work against exactly the kind of server that needs it: one whose
// certificate no CA vouches for, presented under a name that does not match.
func TestFetchAgainstASelfSignedServer(t *testing.T) {
	cert, der := selfSigned(t, "server.invalid")

	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			// A handshake happens lazily; a read forces it, then we are done.
			_ = c.(*tls.Conn).Handshake()
			c.Close()
		}
	}()

	host, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port := 0
	if _, err := fmtSscan(portStr, &port); err != nil {
		t.Fatalf("port: %v", err)
	}

	// A borrowed SNI, which is the case this whole mechanism exists for: the
	// certificate says server.invalid and the client asks for something else.
	res, err := Fetch(host, port, "borrowed.example.net", 5*time.Second)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if res.Pin != LeafHash(der) {
		t.Fatalf("pin %q does not match the served certificate", res.Pin)
	}
	if res.Trusted {
		t.Error("a self-signed chain is not publicly trusted")
	}
	if res.NameMatch {
		t.Error("the certificate was not issued for the borrowed SNI")
	}
	if len(res.Chain) != 1 {
		t.Fatalf("want one certificate in the chain, got %d", len(res.Chain))
	}
	if res.Chain[0].SHA256 != LeafHash(der) {
		t.Error("the per-certificate digest should match the leaf hash for a single-cert chain")
	}
	if res.SNI != "borrowed.example.net" || res.Endpoint == "" {
		t.Errorf("the result must record what was dialled: %+v", res)
	}
}

func TestFetchRejectsNonsense(t *testing.T) {
	if _, err := Fetch("", 443, "", time.Second); err == nil {
		t.Error("want an error for an empty address")
	}
	if _, err := Fetch("127.0.0.1", 0, "", time.Second); err == nil {
		t.Error("want an error for port 0")
	}
	if _, err := Fetch("127.0.0.1", 70000, "", time.Second); err == nil {
		t.Error("want an error for an out-of-range port")
	}
}

// Nothing is listening, so this must fail quickly and clearly rather than hang.
func TestFetchFailsOnAClosedPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().(*net.TCPAddr)
	ln.Close()

	start := time.Now()
	if _, err := Fetch("127.0.0.1", addr.Port, "", 2*time.Second); err == nil {
		t.Fatal("want an error when nothing answers")
	}
	if time.Since(start) > 5*time.Second {
		t.Error("the timeout was not honoured")
	}
}

func selfSigned(t *testing.T, dnsName string) (tls.Certificate, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: dnsName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		DNSNames:     []string{dnsName},
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, der
}

// fmtSscan keeps the import list short; only one integer is ever parsed here.
func fmtSscan(s string, out *int) (int, error) {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, errNotANumber
		}
		n = n*10 + int(c-'0')
	}
	*out = n
	return 1, nil
}

var errNotANumber = errNotANumberType{}

type errNotANumberType struct{}

func (errNotANumberType) Error() string { return "not a number" }
