package daemon

import (
	"strings"

	"xwrt/internal/model"
	"xwrt/internal/netenv"
)

// When the upstream link comes back as somewhere else.
//
// A mobile or PPPoE line does not fail politely. It goes, and it comes back a
// few minutes later with a different address and usually a different gateway —
// and everything this daemon built is built on the old ones. In TUN mode the
// route that carries the tunnel's own packets to the server points at the old
// gateway; in the capture modes the rules are written against the old device.
// None of it is wrong in a way anything notices: the core keeps dialling, the
// kernel keeps answering "network is unreachable", the status page keeps
// saying connected, and the person holding the phone concludes that the VPN is
// rubbish.
//
// A night's log off a router on an LTE line reads exactly like that. The
// connection came up at 00:40 from 100.112.56.89. At 01:11 the dials start
// failing with "network is unreachable" — no route at all. From 01:33 the same
// dials are leaving from 10.1.135.14: the link had come back with a new
// address, and nothing in this daemon had noticed, because nothing in this
// daemon was looking.
//
// Now something looks. OpenWrt announces an interface coming up, the hotplug
// hook passes the announcement on, and this decides whether the ground moved
// enough to be worth rebuilding on.

// wanPrint is the part of the network this daemon builds a connection on.
// Two of these being equal is the whole question: if the device, the address
// and the gateway are what they were, nothing needs doing, however many
// announcements arrive.
type wanPrint struct {
	Device  string
	Address string
	Gateway string
}

func wanPrintOf(env *netenv.Env) wanPrint {
	if env == nil {
		return wanPrint{}
	}
	return wanPrint{
		Device:  env.WANDevice,
		Address: env.WANAddress,
		Gateway: env.WANGateway,
	}
}

func (p wanPrint) String() string {
	parts := []string{}
	if p.Device != "" {
		parts = append(parts, p.Device)
	}
	if p.Address != "" {
		parts = append(parts, p.Address)
	}
	if p.Gateway != "" {
		parts = append(parts, "via "+p.Gateway)
	}
	if len(parts) == 0 {
		return "nothing"
	}
	return strings.Join(parts, " ")
}

// upstreamAction is what an announcement leads to.
type upstreamAction int

const (
	// upstreamIgnore: nothing to do, and by far the commonest answer. Every
	// interface on the device announces itself at boot and after every
	// reconfiguration, so this path has to be cheap and silent.
	upstreamIgnore upstreamAction = iota
	// upstreamWait: something changed, but into a state nothing can be built
	// on. Rebuilding now would tear down a connection in favour of a worse
	// one; there will be another announcement when the link finishes coming
	// up, and that one will be actionable.
	upstreamWait
	// upstreamRebuild: the ground moved. Reconnect on what is there now.
	upstreamRebuild
)

// upstreamVerdict decides, given what the upstream was and what it is.
//
// Separated from everything that touches the network so that the decision —
// the only part with a judgement in it — can be exercised without a router,
// without an interface going down, and without waiting for a mobile operator
// to hand out a different address.
func upstreamVerdict(connected bool, before, after wanPrint) upstreamAction {
	// Nothing is running, so there is nothing to rebuild. The next connect
	// detects the world as it finds it.
	if !connected {
		return upstreamIgnore
	}
	if before == after {
		return upstreamIgnore
	}
	// A gateway is what the tunnel's own packets leave by. Without one there
	// is no route to build and no point pulling a working connection down to
	// prove it.
	if after.Gateway == "" {
		return upstreamWait
	}
	return upstreamRebuild
}

// UpstreamChanged is called when an interface comes up. It reconnects if the
// connection this daemon built is standing on ground that has moved.
//
// iface is the OpenWrt interface name from the announcement, carried only so
// the log can say which one caused this.
func (e *Engine) UpstreamChanged(iface string) error {
	e.mu.Lock()
	target := e.activeTargetLocked()
	before := wanPrintOf(e.env)
	if target != "" {
		e.refreshEnvLocked()
	}
	after := wanPrintOf(e.env)
	e.mu.Unlock()

	switch upstreamVerdict(target != "", before, after) {
	case upstreamIgnore:
		return nil
	case upstreamWait:
		e.Log.Step(model.StepDetect).Warnf(
			"%s came up and the upstream changed (%s → %s), but there is no "+
				"gateway to build on yet; waiting for the link to finish",
			iface, before, after)
		return nil
	}

	// A warning rather than an informational line. The connection is about to
	// be taken down and rebuilt underneath whoever is using it, and when
	// somebody asks later why the tunnel restarted at 01:33, this is the
	// sentence that answers them.
	e.Log.Step(model.StepDetect).Warnf(
		"%s came up on different ground (%s → %s); rebuilding the connection",
		iface, before, after)

	// Connect takes the lock itself, and tears the old connection down on its
	// way through. A flapping link announcing itself three times in a minute
	// therefore queues rather than overlaps, and every announcement after the
	// first finds the environment already re-detected by the connect it was
	// waiting on — so it compares equal and does nothing. That is where the
	// protection against a reconnect storm lives; there is no timer.
	//
	// Rebuilding rather than patching the pieces is deliberate:
	// the routes, the rules, the core's own sockets and the tunnel device were
	// all derived from the old address, and there is no version of repairing
	// them in place that is shorter to write or easier to be sure of than
	// doing again what was done in the first place.
	return e.Connect(target)
}
