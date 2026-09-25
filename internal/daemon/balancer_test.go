package daemon

import (
	"strings"
	"testing"
	"time"

	"xwrt/internal/model"
	"xwrt/internal/xray"
)

// Reading the balancer's answer out of the core's own output.
//
// The shape below is not invented: it is what Xray's `api bi` command prints,
// a section header followed by numbered rows of one tag each. It is someone
// else's format, which is the whole reason for the care here — a heading read
// as a server name would put the word "Selects" on the Status page next to a
// green dot, and nobody would know where it came from.

const biTypical = `  - Selects:
    1   proxy-2
    2   proxy-0
    3   proxy-1
`

func TestTheOrderIsReadAsPrinted(t *testing.T) {
	got := parseBalancerInfo(biTypical)
	want := []string{"proxy-2", "proxy-0", "proxy-1"}
	if len(got) != len(want) {
		t.Fatalf("read %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("read %v, want %v — the first entry is the member the "+
				"balancer hands the next connection to, so the order is the "+
				"answer, not just the contents", got, want)
		}
	}
}

func TestAMemberTheBalancerDroppedIsSimplyAbsent(t *testing.T) {
	// This is the case worth having: a group of three running on two. It
	// looks identical to a healthy group everywhere else on the page.
	got := parseBalancerInfo("  - Selects:\n    1   proxy-0\n    2   proxy-2\n")
	if len(got) != 2 || got[0] != "proxy-0" || got[1] != "proxy-2" {
		t.Fatalf("read %v, want the two that are left", got)
	}
}

func TestAPinnedMemberBeatsTheRanking(t *testing.T) {
	// `xray api bo` pins a member by hand. Nothing here sets one, but if
	// something did, the ranking underneath is not what is being used, and
	// reporting it would name a server that is carrying nothing.
	got := parseBalancerInfo(`  - Selecting Override:
    1   proxy-1
  - Selects:
    1   proxy-0
    2   proxy-1
`)
	if len(got) != 1 || got[0] != "proxy-1" {
		t.Fatalf("read %v, want the pinned member alone", got)
	}
}

func TestNothingIsReadFromWordsThatAreNotTags(t *testing.T) {
	for _, junk := range []string{
		"",
		"failed to get health information: rpc error: code = Unimplemented",
		"  - Selects:\n",
		"  - Selects:\n    1   direct\n    2   block\n",
		"set balancer tag\n",
		"Selects proxy-0",
	} {
		if got := parseBalancerInfo(junk); len(got) != 0 {
			t.Errorf("read %v out of %q", got, junk)
		}
	}
}

func TestTheTagsMatchWhatTheConfigGenerates(t *testing.T) {
	// The parser only accepts tags of the shape the generator writes. If the
	// generator's prefix ever changes, every answer becomes empty and the
	// page silently goes back to reading counters — so the two are tied
	// together here rather than by memory.
	if !isMemberTag(xray.TagProxyPrefix + "0") {
		t.Fatal("the generator's own tag is not recognised by the parser")
	}
	if isMemberTag(xray.TagProxyPrefix) {
		t.Error("a bare prefix with no index was accepted")
	}
	if isMemberTag(xray.TagProxyPrefix + "a") {
		t.Error("a tag with a non-numeric index was accepted")
	}
	if isMemberTag(xray.TagBalancer) {
		t.Error("the balancer's own tag was read as one of its members")
	}
}

// --- and what the Status page then says -------------------------------------

func TestTheCoresAnswerDecidesWhoIsInUse(t *testing.T) {
	// The bug this replaces: under leastPing the health check goes through
	// every member's own tunnel, so every member's counters move in turn and
	// two or three servers were named at once while one carried everything.
	e := groupEngine("bir", "iki", "üç")
	// Counters say all three moved a moment ago — which is exactly what the
	// health check does to them.
	moved(e, 0, time.Second)
	moved(e, 1, time.Second)
	moved(e, 2, time.Second)
	e.memberOrder = []string{tag(1), tag(0)}

	usage, live := e.memberUsageAt(t0)
	if len(live) != 1 || live[0] != "iki" {
		t.Fatalf("live = %v, want [iki]: the core named one member and the "+
			"counters were allowed to overrule it", live)
	}
	if usage[0].Rank != 2 || usage[1].Rank != 1 {
		t.Errorf("ranks = %d/%d, want 2/1", usage[0].Rank, usage[1].Rank)
	}
	if usage[2].Rank != 0 {
		t.Errorf("a member the balancer left out has rank %d, want 0 — that "+
			"is how the page can say it is not answering", usage[2].Rank)
	}
}

func TestWithoutAnAnswerTheCountersAreStillUsed(t *testing.T) {
	// A core without RoutingService, or one still starting. Falling back to
	// the old reading is worse than the core's answer and much better than an
	// empty column.
	e := groupEngine("bir", "iki")
	moved(e, 1, time.Second)

	_, live := e.memberUsageAt(t0)
	if len(live) != 1 || live[0] != "iki" {
		t.Fatalf("live = %v with no answer from the core, want [iki]", live)
	}
}

func TestLatencyIsPairedByPosition(t *testing.T) {
	// Same pairing rule as the counters, and the same failure if it is wrong:
	// the page confidently shows one server's distance next to another's name.
	e := groupEngine("bir", "iki", "üç")
	at := t0.Add(-time.Second)
	e.memberPing = []memberLatency{
		{MS: 42, OK: true, At: at},
		{MS: 0, OK: false, At: at},
		{}, // never measured
	}

	usage, _ := e.memberUsageAt(t0)
	if usage[0].LatencyMS != 42 || usage[0].Unreachable {
		t.Errorf("first member = %+v, want 42 ms and reachable", usage[0])
	}
	if usage[1].LatencyMS != 0 || !usage[1].Unreachable {
		t.Errorf("second member = %+v, want unreachable", usage[1])
	}
	if usage[2].Unreachable {
		t.Error("a member that was never measured was reported as not " +
			"answering, which is a fault it does not have")
	}
}

func TestMeasuringWalksEveryMemberInOrder(t *testing.T) {
	members := []model.Profile{
		{Name: "bir", Address: "192.0.2.1", Port: 443},
		{Name: "iki", Address: "example.test", Port: 8443},
	}
	var asked []string
	got := measureMembers(members, func(addr string) (int, bool) {
		asked = append(asked, addr)
		return len(asked) * 10, addr != "example.test:8443"
	})

	if strings.Join(asked, " ") != "192.0.2.1:443 example.test:8443" {
		t.Fatalf("dialled %v; the address and port come from the profile and "+
			"a host with a colon in it has to survive being joined", asked)
	}
	if got[0].MS != 10 || !got[0].OK {
		t.Errorf("first result = %+v", got[0])
	}
	if got[1].OK {
		t.Errorf("second result = %+v, want a failure", got[1])
	}
	if got[0].At.IsZero() {
		t.Error("no time on the measurement, so a stale round cannot be told " +
			"from one that never ran")
	}
}
