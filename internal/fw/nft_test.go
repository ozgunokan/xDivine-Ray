package fw

import (
	"os/exec"
	"strings"
	"testing"

	"xwrt/internal/model"
)

func planFor(mode model.Mode) Plan {
	return Plan{
		Mode:        mode,
		LANDevices:  []string{"br-lan", "br-guest"},
		LANCIDRs:    []string{"192.168.1.0/24", "192.168.2.0/24"},
		WANDevice:   "eth0",
		TunDevice:   "xwrt0",
		TProxyPort:  12345,
		DNSPort:     15353,
		Mark:        480,
		MarkTProxy:  481,
		MarkTunUDP:  482,
		RedirectDNS: true,
		BypassCIDRs: []string{"203.0.113.0/24"},
		BypassMACs:  []string{"aa:bb:cc:dd:ee:ff"},
	}
}

// checkRuleset validates syntax with nft's own parser. It is skipped when nft
// is not installed, so the suite still runs on a bare build host.
func checkRuleset(t *testing.T, ruleset string) {
	t.Helper()
	if _, err := exec.LookPath("nft"); err != nil {
		t.Skip("nft not installed; skipping syntax validation")
	}
	cmd := exec.Command("nft", "-c", "-f", "-")
	cmd.Stdin = strings.NewReader(ruleset)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("nft rejected the generated ruleset: %s\n--- ruleset ---\n%s",
			strings.TrimSpace(string(out)), ruleset)
	}
}

// TestEveryModeProducesValidRuleset is the guard that matters most: a syntax
// error in a generated rule should fail here, not on someone's router.
func TestEveryModeProducesValidRuleset(t *testing.T) {
	b := &nftBackend{}
	for _, m := range model.Modes() {
		for _, router := range []bool{false, true} {
			for _, dns := range []bool{false, true} {
				p := planFor(m)
				p.ProxyRouter = router
				p.RedirectDNS = dns
				checkRuleset(t, b.ruleset(p))
			}
		}
	}
}

func TestRedirectModeIsTCPOnly(t *testing.T) {
	b := &nftBackend{}
	rs := b.ruleset(planFor(model.ModeRedirect))

	if !strings.Contains(rs, "meta l4proto tcp counter redirect to :12345") {
		t.Error("TCP is not redirected to the transparent port")
	}
	if strings.Contains(rs, "tproxy") {
		t.Error("redirect mode must not use tproxy")
	}
	if strings.Contains(rs, "meta mark set 0x1e2") {
		t.Error("redirect mode must not mark UDP for the tunnel")
	}
	if !strings.Contains(rs, "redirect to :15353") {
		t.Error("DNS redirect missing even though RedirectDNS is set")
	}
}

func TestMixedModeRedirectsTCPAndMarksUDP(t *testing.T) {
	b := &nftBackend{}
	rs := b.ruleset(planFor(model.ModeMixed))

	if !strings.Contains(rs, "meta l4proto tcp counter redirect to :12345") {
		t.Error("TCP should still take the NAT redirect path in mixed mode")
	}
	if !strings.Contains(rs, "meta l4proto udp counter meta mark set 0x1e2") {
		t.Error("UDP should be marked for the tunnel in mixed mode")
	}
	if strings.Contains(rs, "tproxy") {
		t.Error("mixed mode must not need tproxy at all")
	}
	// DNS is redirected to a local port by the nat chain, so it must escape the
	// marking chain or it would be routed out of the tunnel instead.
	mangleIdx := strings.Index(rs, "chain mangle_prerouting")
	if mangleIdx < 0 {
		t.Fatal("mixed mode has no marking chain")
	}
	mangle := rs[mangleIdx:]
	dnsIdx := strings.Index(mangle, "udp dport 53 return")
	markIdx := strings.Index(mangle, "meta l4proto udp counter meta mark set")
	if dnsIdx < 0 || markIdx < 0 || dnsIdx > markIdx {
		t.Error("DNS must return from the marking chain before UDP is marked")
	}
}

func TestTProxyModeCoversBothProtocols(t *testing.T) {
	b := &nftBackend{}
	rs := b.ruleset(planFor(model.ModeTProxy))

	if !strings.Contains(rs, "meta l4proto tcp counter tproxy to :12345") {
		t.Error("TCP is not tproxied")
	}
	if !strings.Contains(rs, "meta l4proto udp counter tproxy to :12345") {
		t.Error("UDP is not tproxied")
	}
	if strings.Contains(rs, "redirect to :12345") {
		t.Error("tproxy mode must not also NAT-redirect TCP")
	}
	// The tproxy mark must differ from the core's socket mark, or the core's
	// own upstream traffic would be captured again.
	if !strings.Contains(rs, "0x1e1") {
		t.Errorf("expected the distinct tproxy mark:\n%s", rs)
	}
}

func TestTunModeInstallsNoCaptureRules(t *testing.T) {
	b := &nftBackend{}
	p := planFor(model.ModeTUN)
	// DNS has its own test; this one is about the traffic itself, which TUN
	// mode carries by routing and must not touch in the firewall.
	p.RedirectDNS = false
	rs := b.ruleset(p)

	for _, forbidden := range []string{"redirect to", "tproxy to", "mark set"} {
		if strings.Contains(rs, forbidden) {
			t.Errorf("TUN mode must not install capture rules, found %q", forbidden)
		}
	}
}

func TestOutputChainsReturnCoreTrafficFirst(t *testing.T) {
	// Without this the core's own upstream connection is fed back into the
	// core, which is an instant loop rather than a subtle bug.
	b := &nftBackend{}
	for _, m := range []model.Mode{model.ModeRedirect, model.ModeMixed, model.ModeTProxy} {
		p := planFor(m)
		p.ProxyRouter = true
		rs := b.ruleset(p)

		for _, chain := range []string{"hook output"} {
			idx := strings.Index(rs, chain)
			if idx < 0 {
				t.Errorf("%s: no output chain even though ProxyRouter is set", m)
				continue
			}
			body := rs[idx:]
			markIdx := strings.Index(body, "meta mark 0x1e0 return")
			actionIdx := strings.Index(body, "redirect to :")
			if a := strings.Index(body, "meta mark set"); a >= 0 && (actionIdx < 0 || a < actionIdx) {
				actionIdx = a
			}
			if markIdx < 0 {
				t.Errorf("%s: output chain does not return core-marked packets", m)
				continue
			}
			if actionIdx >= 0 && markIdx > actionIdx {
				t.Errorf("%s: core mark must be returned before any capture action", m)
			}
		}
	}
}

func TestMixedModeMarksRouterUDPViaRouteHook(t *testing.T) {
	// Changing a mark on a locally generated packet only takes effect if the
	// route is re-evaluated, which is what the route hook is for.
	b := &nftBackend{}
	p := planFor(model.ModeMixed)
	p.ProxyRouter = true
	rs := b.ruleset(p)

	if !strings.Contains(rs, "type route hook output") {
		t.Errorf("router UDP marking needs a route hook, got:\n%s", rs)
	}
}

func TestNoLANDevicesFallsBackToExcludingWAN(t *testing.T) {
	b := &nftBackend{}
	p := planFor(model.ModeRedirect)
	p.LANDevices = nil
	rs := b.ruleset(p)
	checkRuleset(t, rs)

	if !strings.Contains(rs, `iifname "eth0" return`) {
		t.Errorf("expected an upstream exclusion when no LAN device is known:\n%s", rs)
	}
}

func TestBypassIncludesPrivateAndConfigured(t *testing.T) {
	p := planFor(model.ModeMixed)
	got := strings.Join(p.AllBypass(), " ")
	for _, want := range []string{"10.0.0.0/8", "192.168.1.0/24", "203.0.113.0/24", "127.0.0.0/8"} {
		if !strings.Contains(got, want) {
			t.Errorf("bypass list is missing %s: %s", want, got)
		}
	}
}

func TestRulesetHasNoDuplicateBypassEntries(t *testing.T) {
	p := planFor(model.ModeMixed)
	// 192.168.1.0/24 is both a LAN network and inside the private ranges;
	// nft rejects a set with duplicate elements, so dedupe must hold.
	p.BypassCIDRs = append(p.BypassCIDRs, "192.168.1.0/24", "10.0.0.0/8")
	seen := map[string]int{}
	for _, c := range p.AllBypass() {
		seen[c]++
	}
	for cidr, n := range seen {
		if n > 1 {
			t.Errorf("%s appears %d times in the bypass list", cidr, n)
		}
	}
}

// The router's own DNS must stay out of the output hook. Redirecting it there
// is the obvious fix for dnsmasq asking its upstream past the tunnel, and it
// broke name resolution in every capture mode on a real device: the rule
// matched, the counter climbed, no answer came back. DNS mode "dnsmasq" is the
// path that works, and this test keeps the tempting one from coming back.
func TestRouterOwnDNSIsNotRedirected(t *testing.T) {
	b := &nftBackend{}

	for _, m := range model.Modes() {
		p := planFor(m)
		p.RedirectDNS = true
		p.ProxyRouter = false

		out := chainOf(b.ruleset(p), "chain output")
		if strings.Contains(out, "dport 53") {
			t.Errorf("%s: the router's own DNS must not be redirected:\n%s", m, out)
		}
		if out != "" {
			t.Errorf("%s: proxy_router is off, so there is no output chain:\n%s", m, out)
		}
	}

	// With proxy_router on, the output chain exists for TCP and still leaves
	// DNS alone.
	p := planFor(model.ModeRedirect)
	p.ProxyRouter = true
	p.RedirectDNS = true
	out := chainOf(b.ruleset(p), "chain output")
	if !strings.Contains(out, "redirect to :12345") {
		t.Errorf("proxy_router should redirect the router's TCP:\n%s", out)
	}
	if strings.Contains(out, "dport 53") {
		t.Errorf("DNS must stay out of it:\n%s", out)
	}
}

// chainOf returns the body of a named chain, or "" when it is absent.
func chainOf(ruleset, header string) string {
	i := strings.Index(ruleset, header+" {")
	if i < 0 {
		return ""
	}
	rest := ruleset[i:]
	end := strings.Index(rest, "\n\t}")
	if end < 0 {
		return rest
	}
	return rest[:end]
}
