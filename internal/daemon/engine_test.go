package daemon

import (
	"testing"
	"time"

	"xwrt/internal/model"
)

// Restore is what decides whether a router that has just booted brings the
// tunnel up on its own, and one setting decides it: auto_connect.
//
// It used to be two — the setting, or "it was connected when we stopped" — and
// the second made the first meaningless in the case that actually happens. A
// router reboots while connected, so it came back connected whatever the box
// said, and turning the box off changed nothing anybody could see.
//
// Both directions are checked, because only having both makes the setting a
// setting: off must stay off even with everything else saying "was connected",
// and on must connect even when it was left disconnected.
func TestOnlyTheSettingDecidesWhetherItConnectsAtStartup(t *testing.T) {
	cases := []struct {
		name        string
		autoConnect bool
		enabled     bool
		active      string
		want        bool
	}{
		{"off, though it was connected", false, true, "p1", false},
		{"off and idle", false, false, "p1", false},
		{"on, though it was left disconnected", true, false, "p1", true},
		{"on and it was connected", true, true, "p1", true},
		// Nothing to connect to is nothing to connect to.
		{"on but no target", true, true, "", false},
	}
	for _, c := range cases {
		s := model.Settings{
			AutoConnect: c.autoConnect,
			Enabled:     c.enabled,
			Active:      c.active,
		}
		if got := wouldRestore(s); got != c.want {
			t.Errorf("%s: restore=%v, want %v", c.name, got, c.want)
		}
	}
}

// The startup connect keeps trying.
//
// One attempt was enough on a device someone can walk over to. On a router
// whose only way out is the tunnel it is a lockout: the power comes back, the
// line takes two minutes longer than the router does, the single attempt is
// spent on a WAN that is not up yet, and the owner is left with no internet and
// no way to reach the box — until they drive home. That happened.
//
// So: it retries until it works. The loop is tested rather than the dialling,
// because the dialling needs a kernel, a WAN and a server, and none of those is
// what was wrong.
func TestTheStartupConnectKeepsTryingUntilItWorks(t *testing.T) {
	// Fails four times, then succeeds. Nothing about four is special; the
	// point is that it is more than one.
	attempts := 0
	n := restoreLoop(
		func() error {
			attempts++
			if attempts < 5 {
				return errNoWAN
			}
			return nil
		},
		func() bool { return false },
		func(time.Duration) bool { return true },
	)
	if n != 5 || attempts != 5 {
		t.Errorf("gave up after %d attempts (loop reported %d); a router whose "+
			"upstream comes back late would stay offline", attempts, n)
	}
}

// And stops when there is nothing left to retry: the daemon is shutting down,
// the operator connected by hand, or they unticked the box while it was asleep
// between attempts. Retrying past any of those is the daemon overriding a
// decision someone just made — or a service stop waiting on a sleep.
func TestTheStartupConnectStopsWhenItShould(t *testing.T) {
	// Already done before the first attempt: nothing is dialled at all.
	tries := 0
	if n := restoreLoop(
		func() error { tries++; return errNoWAN },
		func() bool { return true },
		func(time.Duration) bool { return true },
	); n != 0 || tries != 0 {
		t.Errorf("dialled %d times when there was nothing to do", tries)
	}

	// Shutting down during the backoff: one attempt, then out.
	tries = 0
	if n := restoreLoop(
		func() error { tries++; return errNoWAN },
		func() bool { return false },
		func(time.Duration) bool { return false },
	); n != 1 || tries != 1 {
		t.Errorf("a shutdown did not end the retry loop (%d attempts)", tries)
	}

	// And the operator taking over between attempts.
	tries = 0
	takenOver := false
	restoreLoop(
		func() error { tries++; takenOver = true; return errNoWAN },
		func() bool { return takenOver },
		func(time.Duration) bool { return true },
	)
	if tries != 1 {
		t.Errorf("kept dialling after the operator took over (%d attempts)", tries)
	}
}

// The backoff has a ceiling. Without one, a router that has been trying since
// the power cut would be down to one attempt an hour by the time the line comes
// back — which is the same lockout, just slower.
func TestTheRetryBackoffHasACeiling(t *testing.T) {
	prev := time.Duration(0)
	for n := 1; n <= 20; n++ {
		d := restoreDelay(n)
		if d < prev {
			t.Errorf("attempt %d waits %v, less than the %v before it", n, d, prev)
		}
		if d > 5*time.Minute {
			t.Errorf("attempt %d waits %v; nothing should wait longer than 5m", n, d)
		}
		prev = d
	}
	if restoreDelay(1) > 15*time.Second {
		t.Errorf("the first retry waits %v; a router at boot should try again "+
			"promptly", restoreDelay(1))
	}
	if restoreDelay(20) != 5*time.Minute {
		t.Errorf("the backoff does not settle at its ceiling: %v", restoreDelay(20))
	}
}
