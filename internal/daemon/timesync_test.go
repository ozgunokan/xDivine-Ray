package daemon

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"xwrt/internal/model"
)

// The clock, which on this hardware is part of the connection.
//
// A router with no battery boots in 1970 and waits for NTP. Here the way out is
// the tunnel, the tunnel needs a clock TLS will accept — a certificate is valid
// between two dates — and REALITY puts the client's clock into the session id
// for the server to compare against its own, inside a window the server sets and
// the client cannot influence. So the device cannot reach the network to learn
// the time, because it does not know the time.
//
// What is tested here is mostly what it refuses to do. Writing the system clock
// from something a network said is the kind of thing that has to be wrong
// carefully: one host with a stale cache or bad intent must not be able to move
// this device's idea of the date, and a clock that is merely a second out must
// not be written at all.

type fakeClockNet struct {
	mu      sync.Mutex
	answers map[string]time.Time
	fails   map[string]bool
	asked   []string
}

func (f *fakeClockNet) date(_ context.Context, _ dialFunc, url string) (time.Time, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, url)
	if f.fails[url] {
		return time.Time{}, errors.New("unreachable")
	}
	t, ok := f.answers[url]
	if !ok {
		return time.Time{}, errNoDate
	}
	return t, nil
}

func (f *fakeClockNet) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.asked)
}

// clockHarness wires an engine to a fake network and a fake clock, and records
// what the clock was set to.
type clockHarness struct {
	engine *Engine
	net    *fakeClockNet
	setTo  []time.Time
	setErr error
}

func newClockHarness(t *testing.T, deviceNow time.Time) *clockHarness {
	t.Helper()

	h := &clockHarness{net: &fakeClockNet{answers: map[string]time.Time{},
		fails: map[string]bool{}}}

	e := &Engine{Log: newTestRing(200)}
	e.settings = model.Defaults()
	h.engine = e

	oldNow, oldSet, oldDater := clockNow, setSystemClock, httpDater
	t.Cleanup(func() { clockNow, setSystemClock, httpDater = oldNow, oldSet, oldDater })

	clockNow = func() time.Time { return deviceNow }
	setSystemClock = func(to time.Time) error {
		h.setTo = append(h.setTo, to)
		return h.setErr
	}
	httpDater = h.net.date
	return h
}

func (h *clockHarness) says(host string, at time.Time) { h.net.answers[host] = at }
func (h *clockHarness) unreachable(host string)        { h.net.fails[host] = true }

var truth = time.Date(2026, 10, 6, 18, 30, 0, 0, time.UTC)

// --- when it moves the clock --------------------------------------------

func TestAColdBootIsSetFromTheNetwork(t *testing.T) {
	// 1970, which is where a board with no battery wakes up.
	h := newClockHarness(t, time.Unix(0, 0))
	for _, host := range clockHosts {
		h.says(host, truth)
	}

	h.engine.SyncClock(context.Background())

	if len(h.setTo) != 1 {
		t.Fatalf("the clock was set %d times from 1970", len(h.setTo))
	}
	if !h.setTo[0].Equal(truth) {
		t.Fatalf("it was set to %s", h.setTo[0])
	}
}

func TestAClockThatIsBarelyWrongIsLeftAlone(t *testing.T) {
	// Half the floor. Setting it gains nothing and puts a line in the log on
	// every connect, which is how a log stops being read.
	h := newClockHarness(t, truth.Add(clockFloor/2))
	for _, host := range clockHosts {
		h.says(host, truth)
	}

	h.engine.SyncClock(context.Background())

	if len(h.setTo) != 0 {
		t.Fatalf("a clock %s out was written", clockFloor/2)
	}
}

// Wrong in the other direction is just as fatal: a clock running ahead rejects
// certificates as expired rather than as not yet valid.
func TestAClockRunningAheadIsAlsoSet(t *testing.T) {
	h := newClockHarness(t, truth.Add(2*time.Hour))
	for _, host := range clockHosts {
		h.says(host, truth)
	}

	h.engine.SyncClock(context.Background())

	if len(h.setTo) != 1 || !h.setTo[0].Equal(truth) {
		t.Fatalf("a clock two hours ahead produced %v", h.setTo)
	}
}

// --- and when it refuses ------------------------------------------------

// One source is never enough. The answer decides whether this device accepts
// any certificate at all, so a single host with a stale cache, a
// misconfiguration, or an interest in the matter must not be able to set the
// date by itself.
func TestOneSourceCannotMoveTheClock(t *testing.T) {
	h := newClockHarness(t, time.Unix(0, 0))
	h.says(clockHosts[0], truth)
	h.unreachable(clockHosts[1])
	h.unreachable(clockHosts[2])

	h.engine.SyncClock(context.Background())

	if len(h.setTo) != 0 {
		t.Fatalf("the clock was set to %v on the word of one host", h.setTo)
	}
}

func TestSourcesThatDisagreeMoveNothing(t *testing.T) {
	h := newClockHarness(t, time.Unix(0, 0))
	h.says(clockHosts[0], truth)
	h.says(clockHosts[1], truth.Add(72*time.Hour))
	h.says(clockHosts[2], truth.Add(-500*time.Hour))

	h.engine.SyncClock(context.Background())

	if len(h.setTo) != 0 {
		t.Fatalf("three hosts disagreed and the clock was still set to %v", h.setTo)
	}
}

// But a liar among two honest sources changes nothing, which is the point of
// asking more than one.
func TestTwoHonestSourcesOutweighOneLie(t *testing.T) {
	h := newClockHarness(t, time.Unix(0, 0))
	h.says(clockHosts[0], truth.Add(-9000*time.Hour))
	h.says(clockHosts[1], truth)
	h.says(clockHosts[2], truth.Add(time.Second))

	h.engine.SyncClock(context.Background())

	if len(h.setTo) != 1 {
		t.Fatalf("two agreeing hosts did not set the clock: %v", h.setTo)
	}
	if d := h.setTo[0].Sub(truth); d < 0 || d > clockAgreement {
		t.Fatalf("the clock was set to %s, which is not what the two agreed on",
			h.setTo[0])
	}
}

func TestAnUnreachableNetworkIsNotAFailure(t *testing.T) {
	h := newClockHarness(t, time.Unix(0, 0))
	for _, host := range clockHosts {
		h.unreachable(host)
	}

	// The thing being asserted is that this returns at all and writes nothing:
	// a clock that could not be checked is the state the device was already in,
	// and refusing to connect over it turns a device that might work into one
	// that certainly does not.
	h.engine.SyncClock(context.Background())

	if len(h.setTo) != 0 {
		t.Fatalf("the clock was set with no source at all: %v", h.setTo)
	}
}

func TestTheSettingIsObeyed(t *testing.T) {
	h := newClockHarness(t, time.Unix(0, 0))
	for _, host := range clockHosts {
		h.says(host, truth)
	}
	h.engine.settings.SyncTime = false

	h.engine.SyncClock(context.Background())

	if len(h.setTo) != 0 {
		t.Fatalf("the clock was written with the setting off: %v", h.setTo)
	}
	if n := h.net.count(); n != 0 {
		t.Fatalf("%d hosts were asked with the setting off", n)
	}
}

// --- what it costs ------------------------------------------------------

// Two agreeing answers is the whole requirement, so the third host is not asked
// for one. On a slow line that is a probe's timeout saved on every connect.
func TestTheThirdHostIsNotAskedWhenTwoAgree(t *testing.T) {
	h := newClockHarness(t, time.Unix(0, 0))
	for _, host := range clockHosts {
		h.says(host, truth)
	}

	h.engine.SyncClock(context.Background())

	if n := h.net.count(); n != 2 {
		t.Fatalf("%d hosts were asked when the first two already agreed", n)
	}
}

// --- the cold-boot shortcut ---------------------------------------------

// Checking the time before every dial would add the probe's timeout to every
// connect on a device whose line reaches nothing — which is the device this is
// written for. So before connecting it is checked only when the clock is
// impossible, which needs no network to decide.
func TestOnlyAnImpossibleClockIsCheckedBeforeConnecting(t *testing.T) {
	for _, tc := range []struct {
		name string
		now  time.Time
		want bool
	}{
		{"a board that woke up in 1970", time.Unix(0, 0), true},
		{"a clock reset to the epoch of some firmware", clockFloorDate.Add(-time.Hour), true},
		{"a clock that is merely wrong", truth.Add(-30 * time.Minute), false},
		{"a clock that is right", truth, false},
		{"a clock far in the future, which a probe cannot rule out", truth.AddDate(5, 0, 0), false},
	} {
		if got := clockLooksUnset(tc.now); got != tc.want {
			t.Errorf("%s: checked-before-connecting is %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestTheShortcutActuallyShortcuts(t *testing.T) {
	h := newClockHarness(t, truth.Add(-30*time.Minute))
	for _, host := range clockHosts {
		h.says(host, truth)
	}

	h.engine.SyncClockIfUnset(context.Background())
	if n := h.net.count(); n != 0 {
		t.Fatalf("a plausible clock still cost %d network probes before the dial", n)
	}

	h2 := newClockHarness(t, time.Unix(0, 0))
	for _, host := range clockHosts {
		h2.says(host, truth)
	}
	h2.engine.SyncClockIfUnset(context.Background())
	if len(h2.setTo) != 1 {
		t.Fatalf("a clock in 1970 was not fixed before the dial: %v", h2.setTo)
	}
}

// --- when the kernel says no --------------------------------------------

// Not every failure is this daemon's to fix, but every failure is its to
// explain: a clock that could not be set is why a connection that looks correct
// does not establish, and nothing else on the device would ever say so.
func TestAClockThatCannotBeSetIsSaidOutLoud(t *testing.T) {
	h := newClockHarness(t, time.Unix(0, 0))
	for _, host := range clockHosts {
		h.says(host, truth)
	}
	h.setErr = errors.New("operation not permitted")

	h.engine.SyncClock(context.Background())

	found := false
	for _, e := range h.engine.Log.Entries(Query{}) {
		if e.Level == model.LevelWarn &&
			containsAll(e.Message, "clock", "could not be set") {
			found = true
		}
	}
	if !found {
		t.Fatal("the clock could not be set and nothing in the log says so")
	}
}

func containsAll(s string, parts ...string) bool {
	for _, p := range parts {
		if !contains(s, p) {
			return false
		}
	}
	return true
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
