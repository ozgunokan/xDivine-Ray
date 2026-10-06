package update

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// Fetching updates through the tunnel.
//
// The device this runs on is installed precisely because its line cannot reach
// parts of the internet. The release page is one of those parts often enough to
// matter, and until this existed the update check was the only request on the
// whole router still going out over the raw line — so "check for updates" could
// fail on a device where everything else worked, and the failure read as a
// broken updater rather than as a blocked line.
//
// The setting that sends the router's own traffic through the tunnel happened to
// fix it, but it is off by default and it answers a different question. So the
// updater routes itself: through the core's SOCKS inbound when the tunnel is up,
// straight out when it is not.

// socksServer is a SOCKS5 proxy that connects where it is asked to. It records
// what it was asked for, which is the part the tests care about: a name must
// arrive as a name, because the far end of the tunnel is the only party that can
// resolve it over the line that will carry the connection.
type socksServer struct {
	ln net.Listener

	mu       sync.Mutex
	asked    []string
	refuseBy byte // non-zero: refuse every request with this reply code
	notSocks bool // answer the greeting with something that is not SOCKS5
	needAuth bool
}

func newSocksServer(t *testing.T) *socksServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &socksServer{ln: ln}
	t.Cleanup(func() { ln.Close() })
	go s.serve()
	return s
}

func (s *socksServer) addr() string { return s.ln.Addr().String() }

func (s *socksServer) requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string{}, s.asked...)
}

func (s *socksServer) serve() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handle(conn)
	}
}

func (s *socksServer) handle(conn net.Conn) {
	defer conn.Close()

	var greeting [2]byte
	if _, err := io.ReadFull(conn, greeting[:]); err != nil {
		return
	}
	if _, err := io.ReadFull(conn, make([]byte, int(greeting[1]))); err != nil {
		return
	}

	s.mu.Lock()
	notSocks, needAuth, refuse := s.notSocks, s.needAuth, s.refuseBy
	s.mu.Unlock()

	switch {
	case notSocks:
		conn.Write([]byte{4, 0})
		return
	case needAuth:
		conn.Write([]byte{5, 2})
		return
	}
	if _, err := conn.Write([]byte{5, 0}); err != nil {
		return
	}

	head := make([]byte, 4)
	if _, err := io.ReadFull(conn, head); err != nil {
		return
	}
	var host string
	switch head[3] {
	case 1:
		b := make([]byte, 4)
		if _, err := io.ReadFull(conn, b); err != nil {
			return
		}
		host = net.IP(b).String()
	case 3:
		var n [1]byte
		if _, err := io.ReadFull(conn, n[:]); err != nil {
			return
		}
		b := make([]byte, int(n[0]))
		if _, err := io.ReadFull(conn, b); err != nil {
			return
		}
		host = string(b)
	case 4:
		b := make([]byte, 16)
		if _, err := io.ReadFull(conn, b); err != nil {
			return
		}
		host = net.IP(b).String()
	default:
		return
	}
	var port [2]byte
	if _, err := io.ReadFull(conn, port[:]); err != nil {
		return
	}
	target := net.JoinHostPort(host, itoa(int(port[0])<<8|int(port[1])))

	s.mu.Lock()
	s.asked = append(s.asked, target)
	s.mu.Unlock()

	if refuse != 0 {
		conn.Write([]byte{5, refuse, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}

	up, err := net.DialTimeout("tcp", target, 5*time.Second)
	if err != nil {
		conn.Write([]byte{5, 4, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	defer up.Close()
	// A bound address of a deliberately awkward kind: a name. Nothing here
	// reads it, and that is the point — it has to be stepped over to reach the
	// first byte of the conversation.
	reply := []byte{5, 0, 0, 3, 4}
	reply = append(reply, "here"...)
	reply = append(reply, 0, 0)
	if _, err := conn.Write(reply); err != nil {
		return
	}

	done := make(chan struct{})
	go func() { io.Copy(up, conn); close(done) }()
	io.Copy(conn, up)
	<-done
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

// --- the dialer itself --------------------------------------------------

func TestARequestGoesThroughTheProxy(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("v1.0.27"))
	}))
	defer origin.Close()

	proxy := newSocksServer(t)
	client := &http.Client{Transport: &http.Transport{DialContext: Via(proxy.addr())}}

	resp, err := client.Get(origin.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "v1.0.27" {
		t.Fatalf("the body came back as %q", body)
	}

	// And it really went through the proxy rather than straight out, which is
	// the only thing this whole change is for.
	want := strings.TrimPrefix(origin.URL, "http://")
	got := proxy.requests()
	if len(got) != 1 || got[0] != want {
		t.Fatalf("the proxy was asked for %v, not for %s", got, want)
	}
}

// The name has to reach the proxy as a name. Resolving it here would use this
// device's resolver — which on a router with the tunnel up points into the
// tunnel, and in any case answers for a different vantage point than the one
// that is going to carry the connection.
func TestAHostNameIsHandedOverUnresolved(t *testing.T) {
	proxy := newSocksServer(t)
	proxy.mu.Lock()
	proxy.refuseBy = 4 // nothing needs to actually connect for this
	proxy.mu.Unlock()

	_, err := Via(proxy.addr())(context.Background(), "tcp", "api.github.com:443")
	if err == nil {
		t.Fatal("the proxy refused and the dial reported success")
	}

	got := proxy.requests()
	if len(got) != 1 || got[0] != "api.github.com:443" {
		t.Fatalf("the proxy was asked for %v; the name was resolved on this side", got)
	}
}

func TestAnIPv4DestinationIsSentAsAnAddress(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer origin.Close()

	proxy := newSocksServer(t)
	conn, err := Via(proxy.addr())(context.Background(), "tcp",
		strings.TrimPrefix(origin.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()

	got := proxy.requests()
	if len(got) != 1 || !strings.HasPrefix(got[0], "127.0.0.1:") {
		t.Fatalf("the proxy was asked for %v", got)
	}
}

// --- and when it does not work ------------------------------------------

// Each of these is a different thing to go and fix, so each has to say
// something different. "update failed" three times over is what sends somebody
// to a log they cannot read.

func TestNoProxyThereIsSaidAsSuch(t *testing.T) {
	// A port nothing is listening on: the tunnel's core is not running, which
	// is the state right after it has crashed.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dead := ln.Addr().String()
	ln.Close()

	_, err = Via(dead)(context.Background(), "tcp", "api.github.com:443")
	if err == nil {
		t.Fatal("dialling a closed port reported success")
	}
	if !strings.Contains(err.Error(), "did not accept a connection") {
		t.Errorf("the error does not say the proxy was not there: %v", err)
	}
}

func TestSomethingThatIsNotAProxyIsSaidAsSuch(t *testing.T) {
	proxy := newSocksServer(t)
	proxy.mu.Lock()
	proxy.notSocks = true
	proxy.mu.Unlock()

	_, err := Via(proxy.addr())(context.Background(), "tcp", "api.github.com:443")
	if err == nil {
		t.Fatal("a non-SOCKS5 answer was accepted")
	}
	if !strings.Contains(err.Error(), "not SOCKS5") {
		t.Errorf("the error does not say what was wrong: %v", err)
	}
}

func TestARefusalCarriesItsReason(t *testing.T) {
	for code, want := range map[byte]string{
		3: "network is unreachable",
		4: "host is unreachable",
		5: "refused the connection",
	} {
		proxy := newSocksServer(t)
		proxy.mu.Lock()
		proxy.refuseBy = code
		proxy.mu.Unlock()

		_, err := Via(proxy.addr())(context.Background(), "tcp", "api.github.com:443")
		if err == nil {
			t.Fatalf("reply code %d was accepted as success", code)
		}
		if !strings.Contains(err.Error(), want) {
			t.Errorf("reply code %d reads as %q, which does not contain %q",
				code, err, want)
		}
	}
}

func TestAuthenticationIsNotAttempted(t *testing.T) {
	// The core's own inbound is configured with "noauth", so a proxy asking for
	// a password is not the core — and guessing at credentials would be worse
	// than saying so.
	proxy := newSocksServer(t)
	proxy.mu.Lock()
	proxy.needAuth = true
	proxy.mu.Unlock()

	_, err := Via(proxy.addr())(context.Background(), "tcp", "api.github.com:443")
	if err == nil || !strings.Contains(err.Error(), "authentication") {
		t.Fatalf("a proxy demanding authentication produced: %v", err)
	}
}

func TestOnlyTCPIsCarried(t *testing.T) {
	_, err := Via("127.0.0.1:1080")(context.Background(), "udp", "1.1.1.1:53")
	if err == nil {
		t.Fatal("a UDP dial was accepted by a dialer that speaks CONNECT")
	}
}

// A cancelled context has to come back promptly rather than after the
// handshake budget. The interface has an update dialog open on the other end of
// this.
func TestACancelledContextStopsTheDial(t *testing.T) {
	proxy := newSocksServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	_, err := Via(proxy.addr())(ctx, "tcp", "api.github.com:443")
	if err == nil {
		t.Fatal("a cancelled dial reported success")
	}
	if !errors.Is(err, context.Canceled) && time.Since(start) > time.Second {
		t.Fatalf("a cancelled dial took %v to fail", time.Since(start))
	}
}

// The handshake gets a deadline because a proxy on the loopback interface
// answers in microseconds or not at all. The download that follows it must not
// inherit that deadline: a bundle on a router's line takes minutes, and a
// ten-second deadline left in place would cut off every real download while
// leaving every test — all of which finish instantly — perfectly green.
func TestTheHandshakeDeadlineDoesNotOutliveTheHandshake(t *testing.T) {
	old := socksHandshakeBudget
	socksHandshakeBudget = 80 * time.Millisecond
	t.Cleanup(func() { socksHandshakeBudget = old })

	// An origin that answers slowly, the way a release asset does.
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(4 * socksHandshakeBudget)
		w.Write([]byte("the bundle"))
	}))
	defer origin.Close()

	proxy := newSocksServer(t)
	client := &http.Client{Transport: &http.Transport{DialContext: Via(proxy.addr())}}

	resp, err := client.Get(origin.URL)
	if err != nil {
		t.Fatalf("a transfer slower than the handshake budget failed: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("the body was cut off: %v", err)
	}
	if string(body) != "the bundle" {
		t.Fatalf("the body came back as %q", body)
	}
}

// --- the switch ---------------------------------------------------------

// Route is what the daemon flips. The default has to be the ordinary dial: this
// package is also used by a command-line check on a device with no daemon
// running at all.
func TestTheDefaultRouteIsTheOrdinaryOne(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	defer origin.Close()

	proxy := newSocksServer(t)
	t.Cleanup(func() { Route(nil) })

	resp, err := client.Get(origin.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if n := len(proxy.requests()); n != 0 {
		t.Fatalf("the proxy saw %d requests with no route set", n)
	}

	// And once a route is set, the same client uses it — the transport is
	// built once at startup, so a route that only took effect on a new client
	// would never take effect at all.
	Route(Via(proxy.addr()))
	resp, err = client.Get(origin.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if n := len(proxy.requests()); n != 1 {
		t.Fatalf("the proxy saw %d requests after the route was set", n)
	}
}
