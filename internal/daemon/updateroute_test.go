package daemon

import (
	"testing"

	"xwrt/internal/model"
)

// Which line an update is fetched on.
//
// The rule has to be the one a person would state: tunnel up, fetch through the
// tunnel; tunnel down, fetch over the line. What it used to be instead was
// "whatever the router's-own-traffic setting happens to say", which is a
// separate choice, defaults to off, and left the update check as the one
// request on the device still going out over the line the device was installed
// to work around.

func routingEngine(t *testing.T, connected bool, socksPort int) *Engine {
	t.Helper()
	e := &Engine{}
	e.connected = connected
	e.settings = model.Defaults()
	e.settings.SocksPort = socksPort
	return e
}

func TestWithTheTunnelUpTheUpdateGoesThroughIt(t *testing.T) {
	e := routingEngine(t, true, 10808)
	if got := e.updateProxy("api.github.com:443"); got != "127.0.0.1:10808" {
		t.Fatalf("the update would be fetched via %q", got)
	}
}

func TestWithTheTunnelDownTheUpdateGoesOutTheLine(t *testing.T) {
	e := routingEngine(t, false, 10808)
	if got := e.updateProxy("api.github.com:443"); got != "" {
		t.Fatalf("a disconnected daemon would fetch via %q, which is not "+
			"listening", got)
	}
}

// The setting that sends the router's own traffic through the tunnel must not
// come into it any more. It is off in the defaults, and this is the assertion
// that the two questions have been separated.
func TestTheRouterTrafficSettingNoLongerDecidesIt(t *testing.T) {
	e := routingEngine(t, true, 10808)
	e.settings.ProxyRouter = false
	withIt := e.updateProxy("api.github.com:443")

	e.settings.ProxyRouter = true
	withoutIt := e.updateProxy("api.github.com:443")

	if withIt != withoutIt {
		t.Fatalf("the route still depends on the setting: %q against %q",
			withIt, withoutIt)
	}
	if withIt == "" {
		t.Fatal("neither case goes through the tunnel")
	}
}

// A core with no SOCKS inbound to speak of is not something to dial into.
// Nothing configures this, but a hand-edited file can.
func TestNoProxyPortMeansNoProxy(t *testing.T) {
	e := routingEngine(t, true, 0)
	if got := e.updateProxy("api.github.com:443"); got != "" {
		t.Fatalf("port 0 was dialled as %q", got)
	}
}

// Addresses on this device or the LAN are never sent round through the tunnel.
// One case of this is load-bearing: the end-to-end test points the updater at a
// release server on the loopback interface, and proxying that would send it out
// to the internet to look for a server three processes away.
func TestLocalDestinationsAreDialledDirectly(t *testing.T) {
	e := routingEngine(t, true, 10808)
	for _, addr := range []string{
		"127.0.0.1:8080",
		"[::1]:8080",
		"192.168.1.1:80",
		"10.0.0.5:443",
		"172.16.4.1:443",
		"169.254.1.1:80",
	} {
		if got := e.updateProxy(addr); got != "" {
			t.Errorf("%s would be fetched via %q", addr, got)
		}
	}
}

// But a name is never treated as local, however it looks. Resolving it here to
// find out would be the one lookup this whole arrangement exists to avoid.
func TestANameIsAlwaysProxied(t *testing.T) {
	e := routingEngine(t, true, 10808)
	for _, addr := range []string{
		"api.github.com:443",
		"localhost:8080",
		"objects.githubusercontent.com:443",
	} {
		if got := e.updateProxy(addr); got == "" {
			t.Errorf("%s would be fetched over the raw line", addr)
		}
	}
}

// A real public address still goes through, which is the ordinary case for the
// asset download: GitHub redirects to a CDN and the client dials that.
func TestAPublicAddressIsProxied(t *testing.T) {
	e := routingEngine(t, true, 10808)
	if got := e.updateProxy("140.82.121.4:443"); got == "" {
		t.Fatal("a public address would be fetched over the raw line")
	}
}
