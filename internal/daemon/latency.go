package daemon

import (
	"net"
	"strconv"
	"time"

	"xwrt/internal/model"
	"xwrt/internal/netmark"
)

// How far away each of a group's servers is.
//
// The core measures this too — it is what leastPing ranks on — and does not
// publish it: `xray api bi` returns the ranking and not the numbers behind it,
// and the observatory's own results have no command-line access at all. So the
// number on the screen is measured here rather than read from the core.
//
// What is measured is a plain TCP handshake from the router to the server's
// own address and port, with nothing else in the way: the same leg the speed
// test calls "server". It is not the same number the core ranks on — the
// core's probe is a full request through the tunnel, so it includes TLS and
// the server's own path to the probe address — but it is the part that differs
// between members, it is honest about what it is, and it works for every
// strategy rather than only the two with an observatory.
//
// It is also the number that answers the question people actually ask of this
// column: is this server reachable from here, and is it far away.

const (
	// latencyEvery is how often the round is repeated. Slow on purpose: this
	// is one connection per member and nothing about it changes by the
	// second, and a router with a metered uplink should not be opening
	// connections for a column nobody is looking at.
	latencyEvery = 60 * time.Second
	// latencyTimeout bounds one dial. A server that has not answered in this
	// long is reported as unreachable, which is the useful answer.
	latencyTimeout = 5 * time.Second
)

// memberLatency is one measurement.
type memberLatency struct {
	// MS is the handshake time in milliseconds. Meaningful only when OK.
	MS int
	// OK is false when the server did not answer at all.
	OK bool
	// At is when this was measured, so a stale round can be told from a
	// missing one.
	At time.Time
}

// measureMembers times a handshake to each server, in order.
//
// The dials are sequential rather than concurrent. A group has a handful of
// members, the whole round costs a few seconds at worst, and doing them at
// once on a 128 MB router buys nothing worth the file descriptors.
func measureMembers(members []model.Profile, dial func(string) (int, bool)) []memberLatency {
	out := make([]memberLatency, len(members))
	now := time.Now()
	for i := range members {
		addr := net.JoinHostPort(members[i].Address, strconv.Itoa(members[i].Port))
		ms, ok := dial(addr)
		out[i] = memberLatency{MS: ms, OK: ok, At: now}
	}
	return out
}

// dialLatency opens a TCP connection and reports how long it took.
//
// The socket carries the core's own firewall mark, which is what makes this
// measurement mean anything when the router's own traffic is proxied.
//
// Without it, a dial to a server's address is captured by the same rules as
// everything else: it goes into the core, out through the tunnel, and is
// completed by the server opening a connection to itself. It comes back in
// about two milliseconds and reads as a wonderfully fast link — the same
// number for every member, measuring nothing. The first rule of both output
// chains is `meta mark <mark> return`, put there so the core's own upstream
// connections are not fed back into the core; a measurement of the path to the
// server belongs on the same side of that rule.
func dialLatency(mark int) func(string) (int, bool) {
	d := net.Dialer{Timeout: latencyTimeout, Control: netmark.Control(mark)}
	return func(addr string) (int, bool) {
		start := time.Now()
		c, err := d.Dial("tcp", addr)
		if err != nil {
			return 0, false
		}
		_ = c.Close()
		ms := int(time.Since(start).Milliseconds())
		// A handshake that appears to take no time at all still took some:
		// report the smallest number the unit can carry rather than zero,
		// which the interface would have to treat as "not measured".
		if ms < 1 {
			ms = 1
		}
		return ms, true
	}
}
