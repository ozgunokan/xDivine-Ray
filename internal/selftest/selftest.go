// Package selftest measures where latency comes from, from the router itself.
//
// It exists because diagnosing "the VPN feels slow" otherwise takes a shell and
// a dozen curl invocations. The three measurements here are the ones that
// actually separate the possible causes, in the order a person should read
// them:
//
//	server   a plain TCP handshake to the proxy server. This is the network
//	         between the router and the server, with nothing else in the way.
//	direct   a full TLS handshake to a fixed public address, not proxied. The
//	         baseline the tunnel is compared against.
//	tunnel   the same handshake to the same address, through the proxy.
//
// tunnel minus direct is what the tunnel costs. A large *spread* in the tunnel
// figures with a clean server figure means packets are being lost past the
// server, which no amount of configuration on this device will fix — and
// knowing that is worth more than any single average.
package selftest

import (
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"net"
	"sort"
	"strconv"
	"time"
)

// DefaultTarget is an address rather than a name on purpose: a name would put
// DNS resolution inside the measurement and muddy exactly the comparison this
// is for.
const DefaultTarget = "1.1.1.1:443"

// Stat summarises one set of samples, in milliseconds.
type Stat struct {
	Label    string    `json:"label"`
	Min      float64   `json:"min_ms"`
	Median   float64   `json:"median_ms"`
	Max      float64   `json:"max_ms"`
	Failures int       `json:"failures"`
	Attempts int       `json:"attempts"`
	Samples  []float64 `json:"samples_ms"`
	Error    string    `json:"error,omitempty"`
}

// Report is the whole measurement.
type Report struct {
	Target  string `json:"target"`
	Server  *Stat  `json:"server,omitempty"`
	Direct  *Stat  `json:"direct"`
	Tunnel  *Stat  `json:"tunnel"`
	Rounds  int    `json:"rounds"`
	TakenAt string `json:"taken_at"`
	// Verdict is a one-line reading of the numbers, so the page does not have
	// to re-derive the same conclusion in JavaScript.
	Verdict string `json:"verdict"`
	// VerdictCode names the same conclusion in a fixed vocabulary. The web
	// interface renders its own sentence from it, which is how the verdict can
	// be shown in the user's language without the daemon knowing any.
	VerdictCode string `json:"verdict_code"`
	// SkipCode says why the unproxied measurements were not taken, when that
	// was a deliberate choice rather than a failure. Empty means they were.
	//
	// There is only one reason so far and it is worth naming: with the
	// router's own traffic proxied, nothing this daemon dials is unproxied.
	// The comparison the page exists to draw cannot be drawn, and the numbers
	// that come out of trying look like an answer. See SkipProxyRouter.
	SkipCode string `json:"skip_code,omitempty"`
}

// SkipProxyRouter marks a run where the unproxied legs were left out because
// the router's own traffic goes through the tunnel.
//
// The alternative — a firewall exception so the probe can slip past the rules
// — was considered and rejected. It would mean the measurement is taken over a
// path no real traffic uses, and it would mean punching a hole in the capture
// rules for the convenience of a diagnostic. A measurement that says nothing
// is better than one that says something untrue, and the page can explain
// itself in one sentence.
const SkipProxyRouter = "proxy-router"

// The verdict vocabulary. Anything reading VerdictCode should treat an
// unrecognised value as "show Verdict instead".
const (
	VerdictUnreachable = "unreachable" // the tunnel did not answer at all
	VerdictNoBaseline  = "no-baseline" // direct failed, nothing to compare to
	VerdictFailures    = "failures"    // some tunnelled connections failed
	VerdictLossBeyond  = "loss-beyond" // spread, but the server link is clean
	VerdictLoss        = "loss"        // spread, cause not narrowed down
	VerdictOverhead    = "overhead"    // consistent, but expensive
	VerdictHealthy     = "healthy"     // consistent and cheap
	// The tunnel-only readings, for a run with no unproxied baseline to
	// compare against. They say what can honestly be said: how steady it is.
	VerdictTunnelSteady = "tunnel-only-steady"
	VerdictTunnelJitter = "tunnel-only-jitter"
	// And the one that outranks both: connections that did not complete at
	// all. A failure is worth more than any percentile — a median of 186 ms
	// over nine attempts is a fine number and says nothing about the tenth,
	// which never arrived. This used to be folded into the jitter reading,
	// whose sentence mentions only the median and the maximum, so the page
	// said "usually 186 ms" about a run where one connection in ten failed.
	VerdictTunnelFailures = "tunnel-only-failures"
)

// Options configure a run.
type Options struct {
	// SocksAddr is the core's SOCKS inbound, which is how the tunnel is
	// measured without touching the firewall rules.
	SocksAddr string
	// ServerAddr is the active profile's endpoint, measured on its own.
	ServerAddr string
	// Target is dialled both ways.
	Target string
	// ProxyRouter says the router's own traffic is captured. It changes what
	// can be measured at all: see SkipProxyRouter.
	ProxyRouter bool
	Rounds      int
	// Budget bounds the whole run. A leg that is timing out would otherwise
	// take rounds×timeout seconds, and the web interface's RPC call gives up
	// long before that — an incomplete answer beats no answer at all.
	Budget time.Duration
}

// DefaultBudget is set by the shortest timeout in the chain, not the longest:
// LuCI gives an RPC call 20 seconds, so a run that used all of that would be
// cut off and show nothing. Twelve seconds leaves room to answer.
const DefaultBudget = 12 * time.Second

// Run performs the measurements. It never returns an error: a leg that cannot
// be measured reports why in its own Error field, because "the tunnel is not
// reachable" is itself the answer someone is looking for.
func Run(o Options) *Report {
	if o.Target == "" {
		o.Target = DefaultTarget
	}
	if o.Rounds <= 0 || o.Rounds > 30 {
		o.Rounds = 10
	}
	if o.Budget <= 0 {
		o.Budget = DefaultBudget
	}

	rep := &Report{
		Target:  o.Target,
		Rounds:  o.Rounds,
		TakenAt: time.Now().Format(time.RFC3339),
	}

	// The budget is shared out as the run goes: a leg that finishes quickly
	// leaves its unused time to the ones after it, and the tunnel — the leg
	// anyone actually opened this page for — is measured last so it inherits
	// whatever is left.
	// With the router proxied there is one leg, and it gets the whole budget.
	legs := 1
	if !o.ProxyRouter {
		legs = 2
		if o.ServerAddr != "" {
			legs = 3
		}
	}
	start := time.Now()
	share := func() time.Time {
		left := o.Budget - time.Since(start)
		if left < time.Second {
			left = time.Second
		}
		d := left / time.Duration(legs)
		legs--
		return time.Now().Add(d)
	}

	if o.ProxyRouter {
		// Both unproxied legs are skipped, not just the obvious one. The
		// server leg looks the most innocent and is the most misleading: a
		// dial to the server's own address is captured like everything else,
		// carried through the tunnel, and completed by the server connecting
		// to itself. It comes back in about two milliseconds and reads as a
		// wonderfully fast link.
		rep.SkipCode = SkipProxyRouter
	}

	if o.ServerAddr != "" && !o.ProxyRouter {
		rep.Server = measure("server", o.Rounds, share(), func() error {
			c, err := net.DialTimeout("tcp", o.ServerAddr, 5*time.Second)
			if err != nil {
				return err
			}
			return c.Close()
		})
	}

	if !o.ProxyRouter {
		rep.Direct = measure("direct", o.Rounds, share(), func() error {
			c, err := net.DialTimeout("tcp", o.Target, 5*time.Second)
			if err != nil {
				return err
			}
			defer c.Close()
			return handshake(c, o.Target)
		})
	}

	rep.Tunnel = measure("tunnel", o.Rounds, share(), func() error {
		c, err := socksDial(o.SocksAddr, o.Target, 8*time.Second)
		if err != nil {
			return err
		}
		defer c.Close()
		return handshake(c, o.Target)
	})

	rep.VerdictCode, rep.Verdict = verdict(rep)
	return rep
}

// measure runs one leg and summarises it, stopping early if the leg has used up
// its share of the run's time.
func measure(label string, rounds int, deadline time.Time, once func() error) *Stat {
	st := &Stat{Label: label, Samples: make([]float64, 0, rounds)}
	for i := 0; i < rounds; i++ {
		// Always take the first sample: a leg that is slow is exactly the one
		// worth having a number for.
		if i > 0 && time.Now().After(deadline) {
			break
		}
		start := time.Now()
		err := once()
		ms := float64(time.Since(start).Microseconds()) / 1000
		st.Attempts++
		if err != nil {
			st.Failures++
			if st.Error == "" {
				st.Error = err.Error()
			}
			continue
		}
		st.Samples = append(st.Samples, round2(ms))
	}
	if len(st.Samples) == 0 {
		return st
	}

	sorted := append([]float64(nil), st.Samples...)
	sort.Float64s(sorted)
	st.Min = sorted[0]
	st.Max = sorted[len(sorted)-1]
	// The median rather than the mean: one 3-second retransmission would drag
	// an average far away from what the connection usually feels like, and both
	// facts are wanted separately — the median for the usual case, the max for
	// the worst.
	st.Median = round2(sorted[len(sorted)/2])
	return st
}

// handshake completes TLS so the measurement covers a real connection setup,
// not just a SYN. Verification is skipped because the target is an address and
// the certificate is irrelevant to timing.
func handshake(c net.Conn, target string) error {
	host, _, err := net.SplitHostPort(target)
	if err != nil {
		host = target
	}
	_ = c.SetDeadline(time.Now().Add(8 * time.Second))
	tc := tls.Client(c, &tls.Config{
		ServerName: host,
		// #nosec G402 -- a timing probe; nothing is sent over this connection.
		InsecureSkipVerify: true,
		MinVersion:         tls.VersionTLS12,
	})
	return tc.Handshake()
}

// socksDial opens a connection through a SOCKS5 proxy. Hand-written because the
// alternative is a dependency for forty lines of a fixed, tiny protocol.
func socksDial(proxyAddr, target string, timeout time.Duration) (net.Conn, error) {
	if proxyAddr == "" {
		return nil, fmt.Errorf("no SOCKS address")
	}
	host, portStr, err := net.SplitHostPort(target)
	if err != nil {
		return nil, fmt.Errorf("target %q: %w", target, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, fmt.Errorf("target port %q: %w", portStr, err)
	}

	c, err := net.DialTimeout("tcp", proxyAddr, timeout)
	if err != nil {
		return nil, fmt.Errorf("reach the proxy at %s: %w", proxyAddr, err)
	}
	_ = c.SetDeadline(time.Now().Add(timeout))

	// Greeting: version 5, one method, "no authentication".
	if _, err := c.Write([]byte{5, 1, 0}); err != nil {
		c.Close()
		return nil, err
	}
	resp := make([]byte, 2)
	if _, err := readFull(c, resp); err != nil {
		c.Close()
		return nil, err
	}
	if resp[0] != 5 || resp[1] != 0 {
		c.Close()
		return nil, fmt.Errorf("the proxy refused the handshake (%d/%d)", resp[0], resp[1])
	}

	req := []byte{5, 1, 0}
	if ip := net.ParseIP(host); ip != nil && ip.To4() != nil {
		req = append(req, 1)
		req = append(req, ip.To4()...)
	} else if ip != nil {
		req = append(req, 4)
		req = append(req, ip.To16()...)
	} else {
		if len(host) > 255 {
			c.Close()
			return nil, fmt.Errorf("hostname too long")
		}
		req = append(req, 3, byte(len(host)))
		req = append(req, host...)
	}
	req = binary.BigEndian.AppendUint16(req, uint16(port))

	if _, err := c.Write(req); err != nil {
		c.Close()
		return nil, err
	}

	head := make([]byte, 4)
	if _, err := readFull(c, head); err != nil {
		c.Close()
		return nil, err
	}
	if head[1] != 0 {
		c.Close()
		return nil, fmt.Errorf("the proxy could not reach %s (code %d)", target, head[1])
	}
	// Skip the bound address, whose length depends on its type.
	var skip int
	switch head[3] {
	case 1:
		skip = 4
	case 4:
		skip = 16
	case 3:
		l := make([]byte, 1)
		if _, err := readFull(c, l); err != nil {
			c.Close()
			return nil, err
		}
		skip = int(l[0])
	default:
		c.Close()
		return nil, fmt.Errorf("the proxy replied with address type %d", head[3])
	}
	if _, err := readFull(c, make([]byte, skip+2)); err != nil {
		c.Close()
		return nil, err
	}

	_ = c.SetDeadline(time.Time{})
	return c, nil
}

func readFull(c net.Conn, buf []byte) (int, error) {
	got := 0
	for got < len(buf) {
		n, err := c.Read(buf[got:])
		if err != nil {
			return got, err
		}
		got += n
	}
	return got, nil
}

// verdict states what the numbers mean, in the terms someone reading a status
// page needs: is the tunnel healthy, is it merely slower, or is something
// dropping packets beyond our reach.
func verdict(r *Report) (string, string) {
	if r.Tunnel == nil || len(r.Tunnel.Samples) == 0 {
		return VerdictUnreachable,
			"The tunnel could not be measured: the core is not answering on its SOCKS port."
	}
	// Skipped on purpose is not the same as failed, and saying "the direct
	// connection failed" for a setting the operator chose sends them looking
	// for a fault that is not there.
	if r.SkipCode != "" {
		spread := r.Tunnel.Max - r.Tunnel.Median
		if r.Tunnel.Failures > 0 {
			return VerdictTunnelFailures, fmt.Sprintf(
				"%d of %d connections through the tunnel failed outright; the "+
					"%.0f ms below is the timing of the ones that worked.",
				r.Tunnel.Failures, r.Tunnel.Attempts, r.Tunnel.Median)
		}
		if spread > 150 {
			return VerdictTunnelJitter, fmt.Sprintf(
				"Through the tunnel: usually %.0f ms, reaching %.0f ms. There is "+
					"no unproxied comparison, because the router's own traffic "+
					"goes through the tunnel too.",
				r.Tunnel.Median, r.Tunnel.Max)
		}
		return VerdictTunnelSteady, fmt.Sprintf(
			"Through the tunnel: %.0f ms, steady. There is no unproxied "+
				"comparison, because the router's own traffic goes through the "+
				"tunnel too.", r.Tunnel.Median)
	}
	if r.Direct == nil || len(r.Direct.Samples) == 0 {
		return VerdictNoBaseline,
			"Only the tunnel could be measured; the direct connection failed, " +
				"so there is nothing to compare against."
	}

	overhead := r.Tunnel.Median - r.Direct.Median
	spread := r.Tunnel.Max - r.Tunnel.Median

	switch {
	case r.Tunnel.Failures > 0:
		return VerdictFailures, fmt.Sprintf(
			"%d of %d connections through the tunnel failed outright.",
			r.Tunnel.Failures, r.Tunnel.Attempts)

	// A max far above the median is the signature of a lost packet being
	// retransmitted, and the server figure says on which side of the server it
	// was lost.
	case spread > 150:
		if r.Server != nil && len(r.Server.Samples) > 0 && r.Server.Max-r.Server.Median < 50 {
			return VerdictLossBeyond, fmt.Sprintf(
				"Usually %.0f ms, but some connections take up to %.0f ms. "+
					"The link to the server is steady, so the packets are being lost past it — "+
					"that is the server's own network, not this device.",
				r.Tunnel.Median, r.Tunnel.Max)
		}
		return VerdictLoss, fmt.Sprintf(
			"Usually %.0f ms, but some connections take up to %.0f ms, "+
				"which means packets are being lost and retransmitted.",
			r.Tunnel.Median, r.Tunnel.Max)

	case overhead > 100:
		return VerdictOverhead, fmt.Sprintf(
			"The tunnel adds %.0f ms to every connection, which is a lot: "+
				"the server is probably far away or busy.", overhead)

	default:
		return VerdictHealthy, fmt.Sprintf(
			"Healthy: the tunnel adds %.0f ms and is consistent.", overhead)
	}
}

func round2(v float64) float64 {
	return float64(int(v*100+0.5)) / 100
}

// Reachable reports whether real data can move through the tunnel.
//
// A SOCKS connect on its own proves nothing: the core answers it before the
// far end has been dialled, so a proxy whose server refuses it — wrong
// credentials, an expired account, a certificate that no longer matches the
// pin — accepts the connection and fails silently afterwards. Only bytes that
// come back are evidence, so this completes a TLS handshake with the target.
//
// The targets are addresses, not names, because a name would need the far end
// to resolve it and a resolver failure would be reported here as a dead
// tunnel. Two of them, so one blocked address is not mistaken for one either.
func Reachable(socksAddr string, timeout time.Duration) error {
	var err error
	for _, target := range []string{DefaultTarget, "8.8.8.8:443"} {
		var c net.Conn
		if c, err = socksDial(socksAddr, target, timeout); err != nil {
			continue
		}
		err = handshake(c, target)
		c.Close()
		if err == nil {
			return nil
		}
	}
	return err
}
