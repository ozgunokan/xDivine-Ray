package daemon

import (
	"context"
	"net"
	"strconv"

	"xwrt/internal/update"
)

// Which line the updater's own requests leave on.
//
// The rule is the one anybody would state if asked: while the tunnel is up,
// everything this device fetches goes through the tunnel, the update included;
// while it is down, everything goes out the line. Nothing else is involved — in
// particular not the "send the router's own traffic through the tunnel"
// setting, which used to decide this by accident.
//
// That accident was worth removing. The setting exists for the router's general
// traffic and defaults to off, so on a normal installation the update check was
// the one request on the whole device still going out over the raw line. On the
// kind of line this daemon is usually installed to work around, that is the
// request least likely to succeed, and its failure reads as "updates are
// broken" rather than as what it is.
//
// The decision is made per dial rather than once, because it has to be: a check
// can be in flight when the tunnel comes up or goes down, and the right answer
// is the one that is true when the connection is actually opened.

// dialByTunnel is handed to the update package, and used by the clock probe, and
// called for every connection either of them makes.
func (e *Engine) dialByTunnel(ctx context.Context, network, addr string) (net.Conn, error) {
	if proxy := e.updateProxy(addr); proxy != "" {
		return update.Via(proxy)(ctx, network, addr)
	}
	var d net.Dialer
	return d.DialContext(ctx, network, addr)
}

// updateProxy returns the proxy to use for this destination, or "" to dial it
// directly.
func (e *Engine) updateProxy(addr string) string {
	e.mu.Lock()
	connected, port := e.connected, e.settings.SocksPort
	e.mu.Unlock()

	if !connected || port <= 0 {
		return ""
	}
	// A destination on this device or on the LAN is never worth sending round
	// through the tunnel, and one case of it is load-bearing: an end-to-end test
	// points the updater at a release server on the loopback interface, and
	// proxying that would route it out to the internet and back to find a
	// server that is three processes away.
	if host, _, err := net.SplitHostPort(addr); err == nil && isLocalHost(host) {
		return ""
	}
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
}

// isLocalHost reports whether an address is on this device or on a private
// network. A name is never local: the whole point of handing names to the proxy
// is that this side does not resolve them.
func isLocalHost(host string) bool {
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsUnspecified()
}
