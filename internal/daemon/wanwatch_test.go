package daemon

import (
	"testing"

	"xwrt/internal/netenv"
)

// What an interface announcement should lead to.
//
// This is the decision and nothing else: no router, no interface going down,
// no waiting for a mobile operator to hand out a different address. The parts
// that touch the network are one function call away from here, and they are
// the parts that cannot be tested at a desk — which is exactly why the
// judgement was pulled out of them.

func print3(dev, addr, gw string) wanPrint {
	return wanPrint{Device: dev, Address: addr, Gateway: gw}
}

// The night this was written for. The link went at 01:11 and came back by
// 01:33 with a different address; nothing noticed, and the core spent the gap
// dialling from an address that no longer existed.
func TestAnAddressThatChangedUnderneathUsIsRebuiltOn(t *testing.T) {
	before := print3("wwan0", "100.112.56.89", "100.112.56.1")
	after := print3("wwan0", "10.1.135.14", "10.1.135.1")
	if got := upstreamVerdict(true, before, after); got != upstreamRebuild {
		t.Fatalf("want a rebuild, got %v", got)
	}
}

func TestAGatewayThatMovedIsEnoughOnItsOwn(t *testing.T) {
	// Same address, different gateway: the route the tunnel's own packets
	// leave by is wrong, which is the whole failure even though nothing about
	// this device's address changed.
	before := print3("wwan0", "10.1.135.14", "10.1.135.1")
	after := print3("wwan0", "10.1.135.14", "10.1.200.1")
	if got := upstreamVerdict(true, before, after); got != upstreamRebuild {
		t.Fatalf("want a rebuild, got %v", got)
	}
}

func TestADeviceThatMovedIsEnoughOnItsOwn(t *testing.T) {
	before := print3("wwan0", "10.1.135.14", "10.1.135.1")
	after := print3("eth1", "10.1.135.14", "10.1.135.1")
	if got := upstreamVerdict(true, before, after); got != upstreamRebuild {
		t.Fatalf("want a rebuild, got %v", got)
	}
}

// The commonest case by a wide margin, and the one that has to be silent:
// every interface on the device announces itself at boot and after every
// reconfiguration, and none of that is a reason to drop somebody's connection.
func TestAnAnnouncementThatChangedNothingIsIgnored(t *testing.T) {
	same := print3("wwan0", "10.1.135.14", "10.1.135.1")
	if got := upstreamVerdict(true, same, same); got != upstreamIgnore {
		t.Fatalf("want nothing to happen, got %v", got)
	}
}

// Nothing is connected, so there is nothing standing on the old ground. The
// next connect will detect the world as it finds it.
func TestAnIdleDaemonDoesNotReconnectItself(t *testing.T) {
	before := print3("wwan0", "100.112.56.89", "100.112.56.1")
	after := print3("wwan0", "10.1.135.14", "10.1.135.1")
	if got := upstreamVerdict(false, before, after); got != upstreamIgnore {
		t.Fatalf("an idle daemon must stay idle, got %v", got)
	}
}

// A link can announce itself before it has a route. Tearing a working
// connection down in favour of one that cannot be built is the one outcome
// worse than doing nothing — and another announcement is coming.
func TestALinkWithNoGatewayYetIsWaitedFor(t *testing.T) {
	before := print3("wwan0", "100.112.56.89", "100.112.56.1")
	after := print3("wwan0", "10.1.135.14", "")
	if got := upstreamVerdict(true, before, after); got != upstreamWait {
		t.Fatalf("want to wait, got %v", got)
	}
}

// And the same when the upstream has gone entirely: an ifup for some other
// interface arriving while the WAN is down must not start a rebuild onto
// nothing.
func TestAnUpstreamThatIsGoneIsWaitedFor(t *testing.T) {
	before := print3("wwan0", "10.1.135.14", "10.1.135.1")
	if got := upstreamVerdict(true, before, wanPrint{}); got != upstreamWait {
		t.Fatalf("want to wait, got %v", got)
	}
}

// --- what the log will say --------------------------------------------------

func TestTheFingerprintReadsAsASentence(t *testing.T) {
	got := print3("wwan0", "10.1.135.14", "10.1.135.1").String()
	if got != "wwan0 10.1.135.14 via 10.1.135.1" {
		t.Fatalf("got %q", got)
	}
	if empty := (wanPrint{}).String(); empty != "nothing" {
		t.Fatalf("an absent upstream should read as something, got %q", empty)
	}
}

func TestTheFingerprintIsReadOffTheDetectedEnvironment(t *testing.T) {
	env := &netenv.Env{
		WANDevice:  "wwan0",
		WANAddress: "10.1.135.14",
		WANGateway: "10.1.135.1",
	}
	if got := wanPrintOf(env); got != print3("wwan0", "10.1.135.14", "10.1.135.1") {
		t.Fatalf("got %#v", got)
	}
	// Before the first detection there is no environment at all, and asking
	// about it must not be a crash on a device that has never connected.
	if got := wanPrintOf(nil); got != (wanPrint{}) {
		t.Fatalf("want an empty print, got %#v", got)
	}
}
