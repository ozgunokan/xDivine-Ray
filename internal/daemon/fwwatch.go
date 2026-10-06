package daemon

import (
	"sync"
	"time"

	"xwrt/internal/fw"
)

// Watching the device's own firewall, cheaply.
//
// The answer is worth having on the status page, and the status page is polled
// every few seconds by every open browser tab. Asking the kernel each time would
// be a process per poll for a fact that changes once a month, and asking `fw4
// check` — which re-renders the entire ruleset — would be considerably worse.
//
// So it is cached, and refreshed in the background rather than on the way to an
// answer. Nothing waits for it: a caller gets whatever was last learned, and a
// stale value starts a refresh that the *next* caller benefits from. The first
// call of a daemon's life therefore returns the zero value, which says "no
// opinion" and renders as nothing at all. That is the right thing to show for
// one poll, and much better than holding the status lock for the length of a
// firewall render.

// fwCheckInterval is how often the question is asked again. A firewall that is
// down stays down until somebody fixes a file, and one that is up does not
// quietly fall over — this is not a condition that needs watching closely, it is
// one that needs noticing at all.
const fwCheckInterval = 2 * time.Minute

type fwWatch struct {
	mu    sync.Mutex
	state fw.SystemState
	at    time.Time
	busy  bool

	// onDown is called the first time the firewall is found missing, and again
	// only after it has been seen working in between. Said once it is a finding;
	// said every two minutes it is the reason people stop reading logs.
	onDown func(reason string)
	said   bool

	// Seams.
	check func() fw.SystemState
	now   func() time.Time
}

func newFWWatch() *fwWatch {
	return &fwWatch{check: fw.SystemCheck, now: time.Now}
}

// get returns the last known state and starts a refresh if it has aged out.
func (w *fwWatch) get() fw.SystemState {
	w.mu.Lock()
	defer w.mu.Unlock()

	// A zero `at` — nothing has been checked yet — is already older than any
	// interval, so it needs no case of its own.
	if !w.busy && w.now().Sub(w.at) >= fwCheckInterval {
		w.busy = true
		go w.refresh()
	}
	return w.state
}

func (w *fwWatch) refresh() {
	// Deliberately outside the lock: this runs a process, and the whole point of
	// the cache is that no caller ever waits for one.
	state := w.check()

	w.mu.Lock()
	w.state, w.at, w.busy = state, w.now(), false
	say := w.onDown
	switch {
	case state.Down() && !w.said:
		w.said = true
	case !state.Down():
		w.said = false
		say = nil
	default:
		say = nil
	}
	reason := state.Reason
	w.mu.Unlock()

	if say != nil {
		say(reason)
	}
}
