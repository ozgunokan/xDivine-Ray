package fw

import (
	"strings"
	"testing"

	"xwrt/internal/model"
)

// QUIC in the mode that cannot carry it.
//
// Redirect mode proxies TCP and leaves UDP to go straight out, so a browser
// talking QUIC to a video site is not in the tunnel at all. That is a leak on
// its own, and on a network where the direct path is filtered or shaped it is
// also why a video stalls rather than failing cleanly: the client keeps
// working at a UDP connection that half functions instead of dropping to the
// TCP one that would have been proxied. It was reported as "TikTok freezes in
// redirect mode, and TUN mode is fine", which is exactly the shape of it.
//
// Refusing the UDP makes the client fall back at once. The tests below are
// mostly about the ways refusing it could go wrong.

func quicPlan(mode model.Mode) Plan {
	return Plan{
		Mode:       mode,
		LANDevices: []string{"br-lan"},
		WANDevice:  "wwan0_1",
		TProxyPort: 12345,
		DNSPort:    15353,
		Mark:       0x1234,
		MarkTProxy: 0x1235,
		MarkTunUDP: 0x1236,
		BlockQUIC:  true,
	}
}

func rules(t *testing.T, p Plan) string {
	t.Helper()
	return (&nftBackend{}).ruleset(p)
}

func TestRedirectModeRefusesQUIC(t *testing.T) {
	got := rules(t, quicPlan(model.ModeRedirect))
	if !strings.Contains(got, "udp dport 443") {
		t.Fatalf("redirect mode does not touch UDP 443, so QUIC still goes "+
			"straight past the tunnel:\n%s", got)
	}
	if !strings.Contains(got, "reject") {
		t.Error("UDP 443 is matched but not refused")
	}
	// A drop is a timeout, and a timeout is the stall this is meant to remove.
	if strings.Contains(got, "udp dport 443 counter drop") {
		t.Error("QUIC is dropped rather than refused; the client then waits " +
			"for a timeout instead of falling back to TCP immediately")
	}
}

func TestTheOtherModesLeaveQUICAlone(t *testing.T) {
	// They carry UDP themselves. Blocking it there would take away working
	// QUIC and replace it with nothing.
	for _, m := range []model.Mode{model.ModeMixed, model.ModeTProxy, model.ModeTUN} {
		got := rules(t, quicPlan(m))
		if strings.Contains(got, "udp dport 443") {
			t.Errorf("%s mode blocks QUIC, but it carries UDP through the "+
				"tunnel already:\n%s", m, got)
		}
	}
}

func TestTheBlockCanBeTurnedOff(t *testing.T) {
	p := quicPlan(model.ModeRedirect)
	p.BlockQUIC = false
	if got := rules(t, p); strings.Contains(got, "udp dport 443") {
		t.Errorf("the setting is off and QUIC is blocked anyway:\n%s", got)
	}
}

func TestBypassedClientsKeepTheirQUIC(t *testing.T) {
	// Someone excluded from the proxy is excluded from this too. Refusing
	// their QUIC would be taking something away from a client the operator
	// deliberately left outside the tunnel.
	p := quicPlan(model.ModeRedirect)
	p.BypassMACs = []string{"aa:bb:cc:dd:ee:ff"}
	p.BypassCIDRs = []string{"203.0.113.0/24"}

	got := rules(t, p)
	chain := forwardChain(t, got)
	if !strings.Contains(chain, "ether saddr @bypassmac return") {
		t.Errorf("a bypassed MAC is not excused from the QUIC block:\n%s", chain)
	}
	if !strings.Contains(chain, "ip daddr @bypass return") {
		t.Errorf("a bypassed destination is not excused:\n%s", chain)
	}
	// And the order matters: an excuse after the refusal excuses nothing.
	if strings.Index(chain, "@bypass return") > strings.Index(chain, "udp dport 443") {
		t.Error("the bypass returns come after the refusal, so they never run")
	}
}

func TestOnlyForwardedTrafficIsRefused(t *testing.T) {
	// The router's own UDP 443 is left alone on purpose: a profile whose
	// transport is QUIC or mKCP reaches its own server that way, and blocking
	// it here would take the tunnel down instead of fixing anything.
	p := quicPlan(model.ModeRedirect)
	p.ProxyRouter = true

	chain := forwardChain(t, rules(t, p))
	if !strings.Contains(chain, "hook forward") {
		t.Errorf("the QUIC chain is not on the forward hook:\n%s", chain)
	}
	if strings.Contains(chain, "hook output") {
		t.Error("the refusal reaches the router's own traffic, which is how " +
			"a QUIC or mKCP profile would lose its own connection")
	}
}

func TestTheRefusalRunsBeforeTheDistributionsOwnForwardRules(t *testing.T) {
	// fw4 puts its forward chain at priority 0. A refusal at the same priority
	// may or may not run first, and "may or may not" is not a firewall rule.
	chain := forwardChain(t, rules(t, quicPlan(model.ModeRedirect)))
	if !strings.Contains(chain, "priority -25") {
		t.Errorf("the chain does not run ahead of the usual filter "+
			"priority:\n%s", chain)
	}
}

// forwardChain returns just the forward chain, so a test about it is not
// satisfied by a match somewhere else in the ruleset.
func forwardChain(t *testing.T, ruleset string) string {
	t.Helper()
	i := strings.Index(ruleset, "chain forward {")
	if i < 0 {
		t.Fatalf("there is no forward chain in:\n%s", ruleset)
	}
	rest := ruleset[i:]
	j := strings.Index(rest, "\n\t}")
	if j < 0 {
		return rest
	}
	return rest[:j]
}
