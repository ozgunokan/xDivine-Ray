package mode

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

// The resolver probe, and the mode switch it used to break.
//
// Switching capture modes tears the connection down and starts a new core. The
// DNS step then checks that the core's resolver works — and used to give it
// three tries with no pause, which against a port that nothing has bound yet
// are three instant refusals. Measured: the whole check gave up in 465
// microseconds. The connection failed with "the core is not answering DNS",
// the operator was told to switch DNS modes, and pressing connect again worked
// because the second attempt happened to ask later.

// clock is a fake that only moves when something sleeps on it.
type clock struct {
	t      time.Time
	slept  []time.Duration
	napped time.Duration
}

func (c *clock) now() time.Time { return c.t }
func (c *clock) sleep(d time.Duration) {
	c.slept = append(c.slept, d)
	c.napped += d
	c.t = c.t.Add(d)
}

func newClock() *clock { return &clock{t: time.Unix(1700000000, 0)} }

func TestAResolverThatComesUpLateIsWaitedFor(t *testing.T) {
	c := newClock()
	// Refused, refused, refused, then the core finishes starting.
	calls := 0
	once := func(int) error {
		calls++
		if calls < 4 {
			return fmt.Errorf("nothing is listening on port %d yet", 15353)
		}
		return nil
	}

	if err := probeResolverWithin(15353, probeBudget, c.now, c.sleep, once); err != nil {
		t.Fatalf("a core that answered on the fourth try was declared dead: %v", err)
	}
	if calls != 4 {
		t.Fatalf("asked %d times, want 4", calls)
	}
	if c.napped == 0 {
		t.Fatal("it retried without ever waiting, which is what made three " +
			"tries take half a millisecond and fail a working connection")
	}
}

func TestTheWaitBacksOffRatherThanSpinning(t *testing.T) {
	c := newClock()
	once := func(int) error { return errors.New("still nothing") }

	_ = probeResolverWithin(15353, probeBudget, c.now, c.sleep, once)

	if len(c.slept) < 2 {
		t.Fatalf("only %d waits in a %s budget", len(c.slept), probeBudget)
	}
	// Doubling, up to a ceiling: a router that is busy starting a core should
	// not also be answering a query every 250ms for twenty seconds.
	for i := 1; i < len(c.slept); i++ {
		if c.slept[i] < c.slept[i-1] {
			t.Fatalf("wait %d (%v) is shorter than wait %d (%v)",
				i, c.slept[i], i-1, c.slept[i-1])
		}
	}
	if c.slept[len(c.slept)-1] > 2*time.Second {
		t.Fatalf("the wait grew to %v, which is longer than anyone should sit "+
			"between two DNS queries", c.slept[len(c.slept)-1])
	}
}

func TestItGivesUpInsideTheBudget(t *testing.T) {
	c := newClock()
	start := c.now()
	once := func(int) error { return errors.New("never comes up") }

	err := probeResolverWithin(15353, probeBudget, c.now, c.sleep, once)
	if err == nil {
		t.Fatal("a resolver that never answered was reported as working")
	}
	waited := c.now().Sub(start)
	if waited > probeBudget {
		t.Fatalf("waited %v, which is past the %v budget — the connect step "+
			"would sit there longer than it promised", waited, probeBudget)
	}
	if waited < probeBudget/2 {
		t.Fatalf("gave up after %v of a %v budget, which is most of the "+
			"original bug still in place", waited, probeBudget)
	}
}

func TestTheLastFailureIsWhatIsReported(t *testing.T) {
	c := newClock()
	n := 0
	once := func(int) error {
		n++
		if n == 1 {
			return errors.New("nothing is listening yet")
		}
		return errors.New("could not resolve anything")
	}

	err := probeResolverWithin(15353, probeBudget, c.now, c.sleep, once)
	if err == nil || !strings.Contains(err.Error(), "could not resolve") {
		t.Fatalf("reported %v; the operator needs the state it ended in, not "+
			"the one it started in", err)
	}
}

// --- and what one query reports --------------------------------------------

func TestAnUnboundPortIsNamedAsSuch(t *testing.T) {
	// A free port with nothing on it. The kernel answers the write with ICMP
	// port unreachable and the read fails; that is "not started", not "broken".
	err := probeOnce(freeUDPPort(t))
	if err == nil {
		t.Fatal("something answered on a port that should be empty")
	}
	if !strings.Contains(err.Error(), "nothing is listening") {
		t.Fatalf("an unbound port was reported as %q, which reads like a "+
			"broken resolver rather than one that has not started", err)
	}
}

func TestAServfailSaysTheCoreHasNoWayOut(t *testing.T) {
	port := serveDNS(t, func(query []byte) []byte {
		reply := append([]byte(nil), query...)
		reply[2] = 0x81        // response, recursion desired
		reply[3] = 0x80 | 0x02 // recursion available, SERVFAIL
		return reply
	})
	err := probeOnce(port)
	if err == nil {
		t.Fatal("SERVFAIL was accepted as a working resolver")
	}
	if !strings.Contains(err.Error(), "no working path out") {
		t.Fatalf("SERVFAIL reported as %q; it means the resolver is there but "+
			"cannot reach anything, which is a different thing to say", err)
	}
}

func TestAnAnsweringResolverPasses(t *testing.T) {
	port := serveDNS(t, func(query []byte) []byte {
		reply := append([]byte(nil), query...)
		reply[2] = 0x81
		reply[3] = 0x80 // NOERROR
		return reply
	})
	if err := probeOnce(port); err != nil {
		t.Fatalf("a resolver that answered NOERROR was rejected: %v", err)
	}
}

// serveDNS runs a one-shot UDP responder and returns its port.
func serveDNS(t *testing.T, answer func([]byte) []byte) int {
	t.Helper()
	c, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("could not listen: %v", err)
	}
	t.Cleanup(func() { c.Close() })

	go func() {
		buf := make([]byte, 512)
		for {
			n, addr, err := c.ReadFrom(buf)
			if err != nil {
				return
			}
			_, _ = c.WriteTo(answer(buf[:n]), addr)
		}
	}()
	return c.LocalAddr().(*net.UDPAddr).Port
}

// freeUDPPort returns a port that was just released, so nothing is on it.
func freeUDPPort(t *testing.T) int {
	t.Helper()
	c, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("could not listen: %v", err)
	}
	port := c.LocalAddr().(*net.UDPAddr).Port
	c.Close()
	return port
}
