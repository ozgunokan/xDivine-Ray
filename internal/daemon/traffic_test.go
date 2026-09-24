package daemon

import (
	"strings"
	"testing"
)

func TestHistoryReturnsAnArrayWhenEmpty(t *testing.T) {
	// A nil slice renders as JSON null, which every consumer then has to
	// special-case. The endpoint promises an array.
	h := NewHistory(4)
	got := h.Samples()
	if got == nil {
		t.Fatal("Samples() returned nil for an empty ring")
	}
	if len(got) != 0 {
		t.Errorf("len = %d, want 0", len(got))
	}
}

func TestHistoryKeepsOldestFirstAcrossTheWrap(t *testing.T) {
	h := NewHistory(3)
	for i := 1; i <= 5; i++ {
		h.Add(Sample{At: int64(i), UplinkRate: int64(i * 10)})
	}
	got := h.Samples()
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	// The ring holds the last three, oldest first: a chart drawn from a
	// wrapped ring in the wrong order reads as a time jump.
	for i, want := range []int64{3, 4, 5} {
		if got[i].At != want {
			t.Errorf("sample %d at = %d, want %d", i, got[i].At, want)
		}
	}
}

func TestHistoryPeakIgnoresEmptySlots(t *testing.T) {
	h := NewHistory(10)
	h.Add(Sample{At: 1, UplinkRate: 500, DownlinkRate: 9000})
	h.Add(Sample{At: 2, UplinkRate: 1500, DownlinkRate: 200})

	up, down := h.Peak()
	if up != 1500 || down != 9000 {
		t.Errorf("peak up/down = %d/%d, want 1500/9000", up, down)
	}
}

func TestHistoryResetClears(t *testing.T) {
	// A reconnect restarts the core's counters from zero, so keeping the old
	// samples would draw a cliff that never happened.
	h := NewHistory(4)
	for i := 0; i < 6; i++ {
		h.Add(Sample{At: int64(i), UplinkRate: 100})
	}
	h.Reset()
	if got := h.Samples(); len(got) != 0 {
		t.Errorf("len after reset = %d, want 0", len(got))
	}
	if up, down := h.Peak(); up != 0 || down != 0 {
		t.Errorf("peak after reset = %d/%d, want 0/0", up, down)
	}
}

// A group's traffic is the sum of its members'. The counters are named after
// the outbound tag, and a group tags one outbound per member — so a pattern
// that assumes the single-profile tag matches nothing at all, and the Status
// page shows a working connection moving zero bytes.
func TestGroupMemberTrafficIsCounted(t *testing.T) {
	out := []byte(`{"stat":[
		{"name":"outbound>>>proxy-0>>>traffic>>>uplink","value":"100"},
		{"name":"outbound>>>proxy-0>>>traffic>>>downlink","value":"200"},
		{"name":"outbound>>>proxy-1>>>traffic>>>uplink","value":"30"},
		{"name":"outbound>>>proxy-1>>>traffic>>>downlink","value":"40"}
	]}`)

	up, down, perTag, err := sumTraffic(out)
	if err != nil {
		t.Fatalf("sumTraffic: %v", err)
	}
	if up != 130 || down != 240 {
		t.Errorf("up/down = %d/%d, want 130/240", up, down)
	}

	// And kept apart per member, which is what lets the Status page name the
	// server a group is actually going through instead of only the group.
	if got := perTag["proxy-0"]; got.Up != 100 || got.Down != 200 {
		t.Errorf("proxy-0 = %d/%d, want 100/200", got.Up, got.Down)
	}
	if got := perTag["proxy-1"]; got.Up != 30 || got.Down != 40 {
		t.Errorf("proxy-1 = %d/%d, want 30/40", got.Up, got.Down)
	}
	if len(perTag) != 2 {
		t.Errorf("%d tags, want 2: %v", len(perTag), perTag)
	}

	// And the pattern the daemon sends has to reach those names in the first
	// place, which is the half that was broken.
	for _, name := range []string{
		"outbound>>>proxy>>>traffic>>>uplink",
		"outbound>>>proxy-0>>>traffic>>>uplink",
		"outbound>>>proxy-7>>>traffic>>>downlink",
	} {
		if !strings.Contains(name, statsPattern) {
			t.Errorf("pattern %q does not select %q", statsPattern, name)
		}
	}

	// It must not sweep up the counters of everything else, though.
	for _, name := range []string{
		"outbound>>>direct>>>traffic>>>uplink",
		"outbound>>>block>>>traffic>>>downlink",
		"inbound>>>socks-in>>>traffic>>>uplink",
	} {
		if strings.Contains(name, statsPattern) {
			t.Errorf("pattern %q wrongly selects %q", statsPattern, name)
		}
	}
}
