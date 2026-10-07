package daemon

import (
	"context"
	"net"
	"net/http"
	"sort"
	"syscall"
	"time"

	"xwrt/internal/model"
)

// The clock, and why a tunnel has to care about it.
//
// Most routers have no battery-backed clock. They boot in 1970, or at whatever
// the firmware was built, and wait for NTP to tell them the time. On an ordinary
// router that is a detail nobody notices. On this one it is a deadlock: the
// device's way out is the tunnel, the tunnel will not come up until the clock is
// right, and the clock will not be right until something reaches the network.
//
// Two things make the clock load-bearing here:
//
// TLS. A certificate is valid between two dates, so a device that thinks it is
// 1970 rejects every certificate it is shown as not yet valid, and a device
// whose clock has run far ahead rejects them as expired. The failure arrives as
// a handshake error that says nothing about time.
//
// REALITY, more sharply. The client puts the current time into the session id
// it authenticates with, and the server compares it against its own. The server
// decides how much difference to allow (`maxTimeDiff`, in milliseconds, which is
// an inbound setting — the client has no say in it and nothing here can force
// it). Where the operator has set a window, a clock a minute out is a connection
// that simply never establishes, with no message naming the cause.
//
// So the clock is checked twice. Once before connecting, over whatever line the
// device has, which is what rescues a cold boot; and once after, through the
// tunnel, which is what works on a device whose line reaches nothing else.
//
// The time is read from HTTP Date headers rather than from NTP. Three reasons,
// all practical on this hardware: NTP is UDP 123 and a mobile line may drop it
// while passing TCP 80 perfectly; the tunnel carries the request either way,
// which NTP from a system service would not; and `Date` costs one request with
// no daemon to supervise. The precision is one second, which is far inside any
// window a REALITY operator would configure.
//
// Plain HTTP, deliberately, and this is the one place where that is the secure
// choice: validating a certificate needs a clock, and the clock is the thing
// being established. Over HTTPS a device booted in 1970 could not read the
// header that would tell it so. The request is encrypted on the wire by the
// tunnel whenever there is one, and when there is not, the only secret in it is
// that this device asked what time it is.
//
// Against a single lying source, agreement: three independent hosts are asked
// and the clock is only moved if two of them say the same thing. One host
// answering with a wrong Date — misconfigured, cached, hostile — moves nothing.

// clockHosts are asked what time it is. Three operators who are not each other,
// all of them serving plain HTTP that answers a HEAD, all of them reachable from
// the kind of line this runs on.
var clockHosts = []string{
	"http://www.cloudflare.com/",
	"http://www.msftconnecttest.com/connecttest.txt",
	"http://www.apple.com/",
}

const (
	// clockFloor is how wrong the clock has to be before it is touched. Below
	// this nothing is gained by setting it, and a daemon that writes the system
	// clock on every connect is a daemon that shows up in every log.
	clockFloor = 2 * time.Second

	// clockAgreement is how close two answers must be to count as the same
	// answer. Generous, because the hosts are asked one after another over a
	// slow link and their headers have one-second resolution.
	clockAgreement = 10 * time.Second

	// clockProbeBudget bounds one host. Three of them, so the whole thing
	// cannot hold anything up for long.
	clockProbeBudget = 5 * time.Second
)

// setSystemClock is a seam. Nothing in a test may set the machine's clock.
var setSystemClock = func(t time.Time) error {
	tv := syscall.NsecToTimeval(t.UnixNano())
	return syscall.Settimeofday(&tv)
}

// clockNow is the current time as this device believes it. A seam, since the
// whole point of this file is that the device may be wrong about it.
var clockNow = time.Now

// httpDater is the client used for one probe. A seam so a test can answer
// without a network.
var httpDater = func(ctx context.Context, dial dialFunc, url string) (time.Time, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return time.Time{}, err
	}
	req.Header.Set("User-Agent", "xwrt")
	// Cache-Control, because a cached response carries the Date of when it was
	// cached, which on a proxy somewhere can be hours old.
	req.Header.Set("Cache-Control", "no-cache")

	c := &http.Client{
		Timeout: clockProbeBudget,
		Transport: &http.Transport{
			DialContext:       dial,
			Proxy:             nil,
			DisableKeepAlives: true,
		},
		// A redirect to HTTPS would need the clock this is trying to find.
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := c.Do(req)
	if err != nil {
		return time.Time{}, err
	}
	defer resp.Body.Close()

	raw := resp.Header.Get("Date")
	if raw == "" {
		return time.Time{}, errNoDate
	}
	return http.ParseTime(raw)
}

type dialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// errNoDate is a response with no Date header, which is rare enough to be worth
// naming rather than reporting as a parse failure of an empty string.
var errNoDate = errNoDateType{}

type errNoDateType struct{}

func (errNoDateType) Error() string { return "the response carried no Date header" }

// clockFloorDate is a date before which this device's clock is certainly wrong,
// because this software did not exist then. It is how a cold boot is told from
// ordinary drift without asking the network anything.
//
// The distinction earns its keep on the connect path. Checking the time before
// every dial would add the probe's timeout to every connect on a device whose
// line reaches nothing — which is the device this is written for. Checking it
// only when the clock is impossible costs nothing on a device that has one, and
// rescues the one case that cannot rescue itself.
var clockFloorDate = time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)

func clockLooksUnset(now time.Time) bool { return now.Before(clockFloorDate) }

// SyncClockIfUnset checks the time only on a device whose clock cannot be right,
// which is the state a board with no battery boots into.
func (e *Engine) SyncClockIfUnset(ctx context.Context) {
	if clockLooksUnset(clockNow()) {
		e.SyncClock(ctx)
	}
}

// SyncClock sets the system clock from the network when it is far enough out to
// matter, and reports what it did.
//
// It never fails the thing that called it. A clock that could not be checked is
// the state the device was already in, and refusing to connect over it would
// turn a device that might work into one that certainly does not.
func (e *Engine) SyncClock(ctx context.Context) {
	if !e.clockSyncWanted() {
		return
	}

	before := clockNow()
	network, ok := e.networkTime(ctx)
	if !ok {
		return
	}

	skew := network.Sub(before)
	if skew < 0 {
		skew = -skew
	}
	step := e.Log.Step(model.StepConfig)
	if skew < clockFloor {
		return
	}

	if err := setSystemClock(network); err != nil {
		step.Warnf("this device's clock is %s out (it says %s, the network says "+
			"%s) and it could not be set: %v. TLS certificates are valid between "+
			"two dates and REALITY compares the client's clock against the "+
			"server's, so a clock this far out can stop connections that have "+
			"nothing else wrong with them",
			skew.Round(time.Second), before.Format(time.RFC3339),
			network.Format(time.RFC3339), err)
		return
	}
	step.Infof("the clock was %s out and has been set from the network: %s",
		skew.Round(time.Second), network.Format(time.RFC3339))
}

// networkTime asks the hosts and returns a time at least two of them agree on.
func (e *Engine) networkTime(ctx context.Context) (time.Time, bool) {
	var seen []time.Time
	for _, host := range clockHosts {
		probeCtx, cancel := context.WithTimeout(ctx, clockProbeBudget)
		t, err := httpDater(probeCtx, e.dialByTunnel, host)
		cancel()
		if err != nil {
			continue
		}
		seen = append(seen, t)
		// Two that agree is the whole requirement, so the third host is not
		// asked unless it is needed. On a slow line that is five seconds saved
		// on every connect.
		if at, ok := agreedTime(seen); ok {
			return at, true
		}
	}
	return agreedTime(seen)
}

// agreedTime returns the time two sources agree on, and whether there was one.
//
// One source is never enough to move a clock on. The answer decides whether TLS
// certificates validate at all, so a single host with a wrong Date — stale cache,
// misconfiguration, or something worse — must not be able to set this device's
// idea of the date by itself.
func agreedTime(seen []time.Time) (time.Time, bool) {
	sorted := append([]time.Time(nil), seen...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Before(sorted[j]) })

	// This loop is also what refuses a single source: with one answer there is
	// no neighbouring answer to compare it against, so it cannot agree with
	// anything and nothing is returned. A guard above saying the same thing
	// would be a line no test could tell the removal of, which is a worse way
	// to carry a rule this load-bearing than the loop that enforces it.
	for i := 0; i+1 < len(sorted); i++ {
		if sorted[i+1].Sub(sorted[i]) <= clockAgreement {
			// The later of the two, since every answer is at least as old as
			// the moment it was sent.
			return sorted[i+1], true
		}
	}
	return time.Time{}, false
}

// clockSyncWanted reads the setting without holding anything the connect path
// needs.
func (e *Engine) clockSyncWanted() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.settings.SyncTime
}
