package daemon

import (
	"testing"

	"xwrt/internal/model"
	"xwrt/internal/xray"
)

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

	usage, _ := e.memberUsageLocked()
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
	e.memberMoving = map[string]bool{tag(0): true}

	usage, live := e.memberUsageLocked()
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
	e.memberMoving = map[string]bool{tag(0): true, tag(2): true}

	_, live := e.memberUsageLocked()
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

	usage, live := e.memberUsageLocked()
	if len(live) != 0 {
		t.Errorf("live = %v on the first reading, want none", live)
	}
	if usage[0].Uplink != 10 {
		t.Error("the totals should still be reported; only the live flag waits")
	}
}

func TestASingleProfileHasNoMemberList(t *testing.T) {
	e := &Engine{profile: &model.Profile{ID: "p1", Name: "tek"}}
	usage, live := e.memberUsageLocked()
	if usage != nil || live != nil {
		t.Errorf("a single server reported group usage %v / %v", usage, live)
	}
}
