package daemon

import (
	"strings"
	"sync"
	"testing"
	"time"

	"xwrt/internal/fw"
)

// The cache in front of the firewall check.
//
// The answer goes on the status page, and the status page is polled every few
// seconds by every open browser tab. The check runs `nft` and, when the news is
// bad, `fw4 check` — which re-renders the device's entire ruleset. Asking once
// per poll would be a process per poll for a fact that changes about once a
// month, so it is cached, and the refresh happens behind whoever asked rather
// than in front of them.

type fakeCheck struct {
	mu    sync.Mutex
	runs  int
	state fw.SystemState
	block chan struct{}
}

func (f *fakeCheck) call() fw.SystemState {
	f.mu.Lock()
	f.runs++
	state, block := f.state, f.block
	f.mu.Unlock()
	if block != nil {
		<-block
	}
	return state
}

func (f *fakeCheck) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.runs
}

func (f *fakeCheck) set(s fw.SystemState) {
	f.mu.Lock()
	f.state = s
	f.mu.Unlock()
}

// testClock is a clock the test moves by hand. It is locked because the
// watcher reads it from the refresh goroutine while the test is advancing it.
type testClock struct {
	mu sync.Mutex
	at time.Time
}

func newClock() *testClock { return &testClock{at: time.Now()} }

func (c *testClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	c.at = c.at.Add(d)
	c.mu.Unlock()
}

// settle waits for the background refresh to have landed. The refresh is a
// goroutine by design, so there is nothing to synchronise on except the result.
func settle(t *testing.T, w *fwWatch, want func(fw.SystemState) bool) fw.SystemState {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		got := w.get()
		if want(got) {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("the watcher never reached the expected state; it is at %+v", got)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestTheFirstAnswerIsNoOpinion(t *testing.T) {
	f := &fakeCheck{state: fw.SystemState{Known: true}}
	w := &fwWatch{check: f.call, now: time.Now}

	// Nobody has asked the device anything yet, so the only honest answer is
	// nothing — and nothing renders as nothing, which is what should be on
	// screen for the one poll it takes to find out.
	if got := w.get(); got.Known {
		t.Fatalf("an opinion appeared before the check had run: %+v", got)
	}
}

func TestTheAnswerArrivesAndIsThenReused(t *testing.T) {
	f := &fakeCheck{state: fw.SystemState{Known: true, Reason: "a broken file"}}
	w := &fwWatch{check: f.call, now: time.Now}

	got := settle(t, w, func(s fw.SystemState) bool { return s.Known })
	if !got.Down() {
		t.Fatalf("the state did not come through: %+v", got)
	}

	// A hundred polls, which is a few minutes of one open tab.
	for i := 0; i < 100; i++ {
		w.get()
	}
	if n := f.count(); n != 1 {
		t.Fatalf("the device was asked %d times for an answer that was already "+
			"known; a status page polls forever and this is a process each time", n)
	}
}

func TestItIsAskedAgainOnceTheAnswerIsOldEnough(t *testing.T) {
	f := &fakeCheck{state: fw.SystemState{Known: true, Loaded: true}}
	clock := newClock()
	w := &fwWatch{check: f.call, now: clock.now}

	settle(t, w, func(s fw.SystemState) bool { return s.Known })

	// Still inside the interval.
	clock.advance(fwCheckInterval - time.Second)
	w.get()
	if n := f.count(); n != 1 {
		t.Fatalf("it was asked %d times inside one interval", n)
	}

	// And past it, so somebody who fixes their firewall sees the warning go
	// away without restarting anything.
	clock.advance(2 * time.Second)
	f.set(fw.SystemState{Known: true})
	settle(t, w, func(s fw.SystemState) bool { return s.Down() })
}

// A refresh in flight must not start another one. Two polls arriving a
// millisecond apart is the normal case with two tabs open, and a check that
// takes a second would otherwise fork a process for each.
func TestOnlyOneRefreshRunsAtATime(t *testing.T) {
	f := &fakeCheck{state: fw.SystemState{Known: true}, block: make(chan struct{})}
	w := &fwWatch{check: f.call, now: time.Now}

	w.get()
	// Wait for the first one to have actually entered the check, so that a
	// second one would be a real second one rather than a race with the first.
	for i := 0; i < 400 && f.count() == 0; i++ {
		time.Sleep(5 * time.Millisecond)
	}
	for i := 0; i < 50; i++ {
		w.get()
	}
	if n := f.count(); n != 1 {
		t.Fatalf("%d checks are running at once", n)
	}
	close(f.block)
	settle(t, w, func(s fw.SystemState) bool { return s.Known })
}

// --- and the line in the log -------------------------------------------

// The status page is seen by whoever is looking at it. The log is what somebody
// sends to whoever is helping them, which in practice is where this fault gets
// diagnosed — so it is said there too, once.
func TestTheFaultIsAnnouncedOnce(t *testing.T) {
	f := &fakeCheck{state: fw.SystemState{Known: true, Reason: "a broken file"}}
	clock := newClock()

	var mu sync.Mutex
	var said []string
	w := &fwWatch{check: f.call, now: clock.now}
	w.onDown = func(reason string) {
		mu.Lock()
		said = append(said, reason)
		mu.Unlock()
	}

	for i := 0; i < 5; i++ {
		settle(t, w, func(s fw.SystemState) bool { return s.Down() })
		clock.advance(fwCheckInterval + time.Second)
	}
	// Give the last refresh a moment to land, so a second line would be caught.
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(said) != 1 {
		t.Fatalf("the same fault was written to the log %d times; said every two "+
			"minutes it is what teaches people to stop reading logs", len(said))
	}
	if !strings.Contains(said[0], "a broken file") {
		t.Errorf("the reason did not reach the log: %q", said[0])
	}
}

// But a device that breaks, is fixed and breaks again has had two faults, and
// the second one is news.
func TestItIsAnnouncedAgainAfterARecovery(t *testing.T) {
	f := &fakeCheck{state: fw.SystemState{Known: true}}
	clock := newClock()

	var mu sync.Mutex
	count := 0
	w := &fwWatch{check: f.call, now: clock.now}
	w.onDown = func(string) {
		mu.Lock()
		count++
		mu.Unlock()
	}

	settle(t, w, func(s fw.SystemState) bool { return s.Down() })

	clock.advance(fwCheckInterval + time.Second)
	f.set(fw.SystemState{Known: true, Loaded: true})
	settle(t, w, func(s fw.SystemState) bool { return !s.Down() })

	clock.advance(fwCheckInterval + time.Second)
	f.set(fw.SystemState{Known: true})
	settle(t, w, func(s fw.SystemState) bool { return s.Down() })
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if count != 2 {
		t.Fatalf("a fault that came back after being fixed was announced %d "+
			"time(s); it is a new fault and the log should carry it", count)
	}
}

// Nothing is said about a device with no firewall manager, and nothing is said
// about one whose firewall is working.
func TestSilenceWhenThereIsNothingToSay(t *testing.T) {
	for _, state := range []fw.SystemState{
		{},
		{Known: true, Loaded: true},
	} {
		f := &fakeCheck{state: state}
		w := &fwWatch{check: f.call, now: time.Now}
		var mu sync.Mutex
		spoke := false
		w.onDown = func(string) {
			mu.Lock()
			spoke = true
			mu.Unlock()
		}

		w.get()
		for i := 0; i < 200 && f.count() == 0; i++ {
			time.Sleep(time.Millisecond)
		}
		time.Sleep(20 * time.Millisecond)
		mu.Lock()
		said := spoke
		mu.Unlock()
		if said {
			t.Errorf("%+v produced a warning", state)
		}
	}
}
