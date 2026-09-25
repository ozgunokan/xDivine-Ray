package daemon

import (
	"testing"
	"time"

	"xwrt/internal/model"
	"xwrt/internal/xray"
)

// t0 is the moment every test in this file measures from.
var t0 = time.Unix(1700000000, 0)

// moved marks a member as having carried bytes ago seconds before t0.
func moved(e *Engine, i int, ago time.Duration) {
	if e.memberLastMove == nil {
		e.memberLastMove = map[string]time.Time{}
	}
	e.memberLastMove[tag(i)] = t0.Add(-ago)
}

// Which server in a group is actually being used.
//
// The Status page named the group and stopped. On a router pointed at five
// servers that answers none of the question anyone has, and there is no way to
// ask the balancer directly: it does not announce its choice, and under random
// or round-robin it does not have one. What it cannot hide is which member's
// counters are moving, so that is what is read.
//
// The pairing is by position, and nothing else connects a counter to a server:
// the config generator tags a group's outbounds proxy-0, proxy-1… in the order
// the members are listed. Get that order wrong and the interface confidently
// names the wrong server, which is worse than naming none.

func groupEngine(members ...string) *Engine {
	e := &Engine{group: &model.Group{ID: "g1", Name: "grup"}}
	for i, name := range members {
		e.members = append(e.members, model.Profile{
			ID: "p" + string(rune('a'+i)), Name: name,
		})
	}
	return e
}

func tag(i int) string { return xray.TagProxyPrefix + string(rune('0'+i)) }

func TestMemberUsageIsPairedByPosition(t *testing.T) {
	e := groupEngine("birinci", "ikinci", "üçüncü")
	e.memberBytes = map[string]tagTraffic{
		tag(0): {Up: 10, Down: 20},
		tag(2): {Up: 5, Down: 7},
	}

	usage, _ := e.memberUsageAt(t0)
	if len(usage) != 3 {
		t.Fatalf("%d members reported, want 3", len(usage))
	}
	if usage[0].Name != "birinci" || usage[0].Uplink != 10 || usage[0].Downlink != 20 {
		t.Errorf("first member = %+v; proxy-0's counters belong to the first "+
			"member in the list and nowhere else", usage[0])
	}
	if usage[1].Uplink != 0 || usage[1].Downlink != 0 {
		t.Errorf("second member = %+v, want zeroes: nothing was counted "+
			"against proxy-1", usage[1])
	}
	if usage[2].Name != "üçüncü" || usage[2].Uplink != 5 {
		t.Errorf("third member = %+v, want üçüncü with 5 up", usage[2])
	}
	if usage[0].ProfileID == "" {
		t.Error("no profile id, so the interface cannot link to the server")
	}
}

func TestOnlyMovingMembersAreCalledLive(t *testing.T) {
	e := groupEngine("birinci", "ikinci")
	e.memberBytes = map[string]tagTraffic{
		tag(0): {Up: 10, Down: 20},
		tag(1): {Up: 99, Down: 99},
	}
	// The second has carried far more in total and is not moving now. Totals
	// are history; the question is which one is in use.
	moved(e, 0, time.Second)

	usage, live := e.memberUsageAt(t0)
	if !usage[0].Live || usage[1].Live {
		t.Errorf("live flags = %v/%v, want true/false", usage[0].Live, usage[1].Live)
	}
	if len(live) != 1 || live[0] != "birinci" {
		t.Errorf("live = %v, want [birinci]", live)
	}
}

func TestSeveralMembersCanBeLiveAtOnce(t *testing.T) {
	// Under random or round-robin the balancer really is using more than one
	// at a time. Reporting a single "active server" there would be a tidier
	// answer than the truth.
	e := groupEngine("bir", "iki", "üç")
	moved(e, 0, time.Second)
	moved(e, 2, 3*time.Second)

	_, live := e.memberUsageAt(t0)
	if len(live) != 2 {
		t.Fatalf("live = %v, want two of them", live)
	}
	if live[0] != "bir" || live[1] != "üç" {
		t.Errorf("live = %v, want [bir üç] in member order", live)
	}
}

func TestNothingIsClaimedBeforeTheFirstTwoReadings(t *testing.T) {
	// Movement needs two readings to exist. Until then no member is live, and
	// saying one is would be inventing an answer from a single sample.
	e := groupEngine("bir", "iki")
	e.memberBytes = map[string]tagTraffic{tag(0): {Up: 10}}

	usage, live := e.memberUsageAt(t0)
	if len(live) != 0 {
		t.Errorf("live = %v on the first reading, want none", live)
	}
	if usage[0].Uplink != 10 {
		t.Error("the totals should still be reported; only the live flag waits")
	}
}

func TestASingleProfileHasNoMemberList(t *testing.T) {
	e := &Engine{profile: &model.Profile{ID: "p1", Name: "tek"}}
	usage, live := e.memberUsageAt(t0)
	if usage != nil || live != nil {
		t.Errorf("a single server reported group usage %v / %v", usage, live)
	}
}

// The flicker this window exists to stop.
//
// "In use" used to mean "its counters grew between the last two readings",
// which are two seconds apart. A member that happened to move nothing in one
// of those windows — a pause in a video, a gap between requests — stopped
// being named, and the server's name blinked in and out on the Status page
// while nothing at all had changed about the connection.
func TestAnIdleMomentDoesNotEraseTheAnswer(t *testing.T) {
	e := groupEngine("bir", "iki")
	moved(e, 0, 6*time.Second) // three polls ago: nothing moved since

	_, live := e.memberUsageAt(t0)
	if len(live) != 1 || live[0] != "bir" {
		t.Fatalf("live = %v after six idle seconds; the member is still the "+
			"one the traffic goes through", live)
	}
}

func TestAMemberTheBalancerLeftStopsBeingNamed(t *testing.T) {
	// The other side of the same rule: the answer has to be able to change.
	e := groupEngine("bir", "iki")
	moved(e, 0, memberLiveWindow+time.Second)
	moved(e, 1, time.Second)

	_, live := e.memberUsageAt(t0)
	if len(live) != 1 || live[0] != "iki" {
		t.Fatalf("live = %v; the member that stopped carrying anything a "+
			"window ago should have dropped out", live)
	}
}

func TestTheWindowIsLongEnoughToBeUseful(t *testing.T) {
	// A window of one or two polls is the bug. This guards the value itself,
	// because the number is the whole fix.
	if memberLiveWindow < 15*time.Second {
		t.Fatalf("the live window is %v, which is a handful of polls — short "+
			"enough for an ordinary pause to blank the name again",
			memberLiveWindow)
	}
}
