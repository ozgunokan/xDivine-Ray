// Package netmark puts the core's firewall mark on a socket.
//
// Everything this daemon dials from the router itself has the same problem:
// its own capture rules are in the way. A connection opened here to measure
// the network is redirected into the core, carried through the tunnel, and
// completed by the far end — so it measures the tunnel while claiming to
// measure the path around it. With the router's own traffic proxied, a dial to
// the proxy server's own address comes back in about two milliseconds, because
// the server is connecting to itself.
//
// The escape hatch already exists, because the core needs it too: the first
// rule of both output chains is
//
//	meta mark <mark> return
//
// and in TUN mode there is a routing rule to match, `ip rule add fwmark <mark>
// lookup main`. A socket carrying that mark is treated as the core's own and
// left alone in every capture mode. A measurement of the unproxied path
// belongs on that side of the rule, so it wears the same mark.
//
// This is not a way to bypass the tunnel for ordinary traffic. It is used for
// probes whose whole purpose is to measure the path the tunnel is compared
// against.
package netmark
