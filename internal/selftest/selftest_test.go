package selftest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"
)

// The point of the report is the comparison, so the test runs a real TLS
// server and a real SOCKS5 proxy in front of it and checks that both legs are
// measured and that the verdict reads the numbers correctly.
func TestRunMeasuresBothLegs(t *testing.T) {
	target := tlsServer(t)
	proxy := socksProxy(t)

	rep := Run(Options{SocksAddr: proxy, ServerAddr: target, Target: target, Rounds: 3})

	if rep.Direct == nil || len(rep.Direct.Samples) != 3 {
		t.Fatalf("direct leg not measured: %+v", rep.Direct)
	}
	if rep.Tunnel == nil || len(rep.Tunnel.Samples) != 3 {
		t.Fatalf("tunnel leg not measured: %+v", rep.Tunnel)
	}
	if rep.Server == nil || len(rep.Server.Samples) != 3 {
		t.Fatalf("server leg not measured: %+v", rep.Server)
	}
	if rep.Tunnel.Failures != 0 || rep.Direct.Failures != 0 {
		t.Fatalf("unexpected failures: %+v %+v", rep.Direct, rep.Tunnel)
	}
	if rep.Verdict == "" {
		t.Error("a report without a verdict makes the reader do the arithmetic")
	}
	if rep.Tunnel.Min > rep.Tunnel.Max || rep.Tunnel.Median < rep.Tunnel.Min {
		t.Errorf("summary is inconsistent: %+v", rep.Tunnel)
	}
}

// A proxy that is not there is a common situation — the daemon is disconnected
// — and it must produce a readable answer rather than an empty report.
func TestRunWithNoProxyExplainsItself(t *testing.T) {
	target := tlsServer(t)

	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	dead := ln.Addr().String()
	ln.Close()

	rep := Run(Options{SocksAddr: dead, Target: target, Rounds: 2})
	if rep.Tunnel.Failures != 2 {
		t.Fatalf("want both tunnel attempts to fail, got %+v", rep.Tunnel)
	}
	if rep.Tunnel.Error == "" {
		t.Error("the failure must say why")
	}
	if !strings.Contains(rep.Verdict, "not answering") {
		t.Errorf("verdict does not explain the failure: %q", rep.Verdict)
	}
}

func TestVerdictNamesTheServerWhenTheSpreadIsPastIt(t *testing.T) {
	r := &Report{
		Rounds: 10,
		Server: &Stat{Samples: []float64{4, 4, 4}, Median: 4, Max: 4.2},
		Direct: &Stat{Samples: []float64{80}, Median: 80, Max: 82},
		Tunnel: &Stat{Samples: []float64{95}, Median: 95, Max: 1100},
	}
	code, v := verdict(r)
	if code != VerdictLossBeyond {
		t.Errorf("a steady server plus a wide tunnel spread should point past the server: %q (%s)", v, code)
	}
}

func TestVerdictCallsASteadyTunnelHealthy(t *testing.T) {
	r := &Report{
		Rounds: 10,
		Direct: &Stat{Samples: []float64{80}, Median: 80, Max: 84},
		Tunnel: &Stat{Samples: []float64{95}, Median: 95, Max: 99},
	}
	if code, v := verdict(r); code != VerdictHealthy {
		t.Errorf("want a healthy verdict, got %q (%s)", v, code)
	}
}

// --- helpers ------------------------------------------------------------

func tlsServer(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "probe.invalid"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     []string{"probe.invalid"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
		MinVersion:   tls.VersionTLS12,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				_ = c.(*tls.Conn).Handshake()
				time.Sleep(20 * time.Millisecond)
				c.Close()
			}()
		}
	}()
	return ln.Addr().String()
}

// socksProxy is just enough SOCKS5 to answer the client in this package, which
// is what makes the tunnel leg testable without a proxy core.
func socksProxy(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })

	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go serveSocks(c)
		}
	}()
	return ln.Addr().String()
}

func serveSocks(c net.Conn) {
	defer c.Close()
	buf := make([]byte, 2)
	if _, err := readFull(c, buf); err != nil {
		return
	}
	if _, err := readFull(c, make([]byte, int(buf[1]))); err != nil {
		return
	}
	if _, err := c.Write([]byte{5, 0}); err != nil {
		return
	}

	head := make([]byte, 4)
	if _, err := readFull(c, head); err != nil {
		return
	}
	var host string
	switch head[3] {
	case 1:
		b := make([]byte, 4)
		readFull(c, b)
		host = net.IP(b).String()
	case 3:
		l := make([]byte, 1)
		readFull(c, l)
		b := make([]byte, int(l[0]))
		readFull(c, b)
		host = string(b)
	case 4:
		b := make([]byte, 16)
		readFull(c, b)
		host = net.IP(b).String()
	}
	p := make([]byte, 2)
	readFull(c, p)
	port := binary.BigEndian.Uint16(p)

	up, err := net.Dial("tcp", net.JoinHostPort(host, itoa(int(port))))
	if err != nil {
		c.Write([]byte{5, 1, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	defer up.Close()
	if _, err := c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		return
	}

	done := make(chan struct{}, 2)
	go func() { copyAll(up, c); done <- struct{}{} }()
	go func() { copyAll(c, up); done <- struct{}{} }()
	<-done
}

func copyAll(dst, src net.Conn) {
	buf := make([]byte, 32*1024)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// With the router's own traffic proxied, the unproxied legs must not be
// measured at all.
//
// This is not tidiness. Every dial this daemon makes is captured by the same
// rules as everything else on the router, so the "direct" leg goes through the
// tunnel and reports it as the unproxied baseline, and the tunnel's cost comes
// out as the difference between the tunnel and itself. The server leg is worse:
// a dial to the server's own address is carried to the server and completed
// there by the server connecting to itself, which comes back in about two
// milliseconds and reads as a superb link.
//
// The fix is to measure neither and say why.
func TestProxiedRouterMeasuresNothingItCannotMeasure(t *testing.T) {
	rep := Run(Options{
		SocksAddr:   "127.0.0.1:1", // nothing there; the tunnel leg will fail
		ServerAddr:  "127.0.0.1:1",
		Target:      "127.0.0.1:1",
		ProxyRouter: true,
		Rounds:      2,
		Budget:      2 * time.Second,
	})

	if rep.Direct != nil {
		t.Error("the direct leg was measured with the router proxied, so it " +
			"measured the tunnel and called it direct")
	}
	if rep.Server != nil {
		t.Error("the server leg was measured with the router proxied; that " +
			"number is the server dialling itself")
	}
	if rep.SkipCode != SkipProxyRouter {
		t.Errorf("nothing says why the legs are missing: skip_code = %q", rep.SkipCode)
	}
}

// And without the setting, all three are measured as before.
func TestAnUnproxiedRouterStillGetsItsBaseline(t *testing.T) {
	rep := Run(Options{
		SocksAddr:  "127.0.0.1:1",
		ServerAddr: "127.0.0.1:1",
		Target:     "127.0.0.1:1",
		Rounds:     2,
		Budget:     2 * time.Second,
	})
	if rep.Direct == nil || rep.Server == nil {
		t.Fatal("a leg is missing on an ordinary run")
	}
	if rep.SkipCode != "" {
		t.Errorf("an ordinary run claims something was skipped: %q", rep.SkipCode)
	}
}

// The verdict has to change too. Subtracting a baseline that does not exist
// gives the tunnel's cost as the tunnel's own median — a number that looks
// like an answer and means nothing.
func TestTheVerdictDoesNotInventABaseline(t *testing.T) {
	steady := &Report{
		SkipCode: SkipProxyRouter,
		Tunnel:   &Stat{Samples: []float64{200, 205, 210}, Median: 205, Max: 210},
	}
	code, text := verdict(steady)
	if code != VerdictTunnelSteady {
		t.Errorf("steady tunnel-only run read as %q", code)
	}
	if strings.Contains(text, "adds") {
		t.Errorf("the verdict quotes an overhead it cannot know: %q", text)
	}

	jittery := &Report{
		SkipCode: SkipProxyRouter,
		Tunnel:   &Stat{Samples: []float64{200, 205, 900}, Median: 205, Max: 900},
	}
	if code, _ := verdict(jittery); code != VerdictTunnelJitter {
		t.Errorf("a jittery tunnel-only run read as %q", code)
	}
}
