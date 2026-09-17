package fw

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"xwrt/internal/model"
)

// Live tests apply the rules to the running kernel instead of only parsing
// them. `nft -c` catches syntax; only a real load catches a rule the kernel
// refuses, an expression the running kernel's modules do not implement, or a
// teardown that leaves something behind.
//
// They mutate the host's firewall, so they are opt-in: set XWRT_LIVE_FW=1 and
// run as root, ideally in a container or a network namespace.

func liveGuard(t *testing.T) {
	t.Helper()
	if os.Getenv("XWRT_LIVE_FW") != "1" {
		t.Skip("set XWRT_LIVE_FW=1 to apply rules to the running kernel")
	}
	if os.Geteuid() != 0 {
		t.Skip("live firewall tests need root")
	}
}

func livePlan(m model.Mode) Plan {
	p := planFor(m)
	// Use devices that exist here; the rules are about the shape, not the name.
	p.LANDevices = []string{"lo"}
	p.WANDevice = "eth0"
	return p
}

// TestLiveNFTApplyAndRevert loads every mode's ruleset into the kernel and
// checks that reverting leaves nothing behind.
func TestLiveNFTApplyAndRevert(t *testing.T) {
	liveGuard(t)
	if _, err := exec.LookPath("nft"); err != nil {
		t.Skip("nft not installed")
	}
	b := &nftBackend{}
	t.Cleanup(func() { _ = b.Revert() })

	for _, m := range model.Modes() {
		t.Run(string(m), func(t *testing.T) {
			p := livePlan(m)
			if err := b.Apply(p); err != nil {
				t.Fatalf("apply: %v", err)
			}

			// A mode that captures nothing still owns a table when DNS has
			// to be hijacked — that is a firewall rule like any other. What
			// it must not contain is capture: a leftover redirect in TUN mode
			// sends the LAN to a port nothing is listening on.
			//
			// This assertion used to read "TUN mode installs no table at
			// all", which stopped being true when the DNS-only table was
			// added — and nobody noticed, because these tests are opt-in and
			// need root. A test that is never run is a comment.
			if !m.NeedsFirewallCapture() {
				if !p.RedirectDNS {
					if b.Installed() {
						t.Error("no capture and no DNS hijack, yet a table was installed")
					}
					return
				}
				if !b.Installed() {
					t.Fatal("the DNS hijack needs a table, and there is none")
				}
				dump, err := runCmdOutput("nft", "list", "table", "ip", Namespace)
				if err != nil {
					t.Fatalf("list table: %v", err)
				}
				if !strings.Contains(dump, "dport 53") {
					t.Errorf("the table exists but does not hijack DNS:\n%s", dump)
				}
				if strings.Contains(dump, "redirect to :12345") {
					t.Errorf("TUN mode installed capture rules:\n%s", dump)
				}
				return
			}
			if !b.Installed() {
				t.Fatal("rules applied but the table is not present")
			}

			dump, err := runCmdOutput("nft", "list", "table", "ip", Namespace)
			if err != nil {
				t.Fatalf("list table: %v", err)
			}
			t.Logf("%s ruleset as the kernel sees it:\n%s", m, dump)

			// The kernel's own rendering is the authority on what was loaded.
			switch m {
			case model.ModeRedirect:
				mustContain(t, dump, "redirect to :12345")
				mustNotContain(t, dump, "tproxy")
			case model.ModeMixed:
				mustContain(t, dump, "redirect to :12345")
				mustContain(t, dump, "meta mark set 0x000001e2")
				mustNotContain(t, dump, "tproxy")
			case model.ModeTProxy:
				mustContain(t, dump, "tproxy to :12345")
			}

			if err := b.Revert(); err != nil {
				t.Fatalf("revert: %v", err)
			}
			if b.Installed() {
				t.Error("table still present after revert")
			}
		})
	}
}

// TestLiveNFTRevertIsIdempotent covers the path taken when the daemon is
// stopped twice, or stopped having never connected.
func TestLiveNFTRevertIsIdempotent(t *testing.T) {
	liveGuard(t)
	if _, err := exec.LookPath("nft"); err != nil {
		t.Skip("nft not installed")
	}
	b := &nftBackend{}
	for i := 0; i < 3; i++ {
		if err := b.Revert(); err != nil {
			t.Fatalf("revert %d: %v", i, err)
		}
	}
}

// TestLiveNFTReapplyReplacesCleanly checks the reconnect path: applying twice
// must not accumulate duplicate rules.
func TestLiveNFTReapplyReplacesCleanly(t *testing.T) {
	liveGuard(t)
	if _, err := exec.LookPath("nft"); err != nil {
		t.Skip("nft not installed")
	}
	b := &nftBackend{}
	t.Cleanup(func() { _ = b.Revert() })

	p := livePlan(model.ModeMixed)
	for i := 0; i < 3; i++ {
		if err := b.Apply(p); err != nil {
			t.Fatalf("apply %d: %v", i, err)
		}
	}
	dump, err := runCmdOutput("nft", "list", "table", "ip", Namespace)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(dump, "redirect to :12345"); n != 1 {
		t.Errorf("redirect rule appears %d times after three applies, want 1", n)
	}
	if n := strings.Count(dump, "chain prerouting"); n != 1 {
		t.Errorf("prerouting chain appears %d times, want 1", n)
	}
}

// TestLiveIPTApplyAndRevert does the same for the iptables backend, which
// until now had no verification at all.
func TestLiveIPTApplyAndRevert(t *testing.T) {
	liveGuard(t)
	if _, err := exec.LookPath("iptables"); err != nil {
		t.Skip("iptables not installed")
	}
	b := &iptBackend{}
	t.Cleanup(func() { _ = b.Revert() })

	for _, m := range model.Modes() {
		t.Run(string(m), func(t *testing.T) {
			p := livePlan(m)
			if err := b.Apply(p); err != nil {
				t.Fatalf("apply: %v", err)
			}

			// The same story as the nft backend: no capture, but the DNS
			// hijack is still a firewall rule and still needs a chain.
			if !m.NeedsFirewallCapture() {
				if !p.RedirectDNS {
					if b.Installed() {
						t.Error("no capture and no DNS hijack, yet chains were installed")
					}
					return
				}
				if !b.Installed() {
					t.Fatal("the DNS hijack needs a chain, and there is none")
				}
				out, err := runCmdOutput("iptables", "-t", "nat", "-S")
				if err != nil {
					t.Fatalf("iptables -S: %v", err)
				}
				if !strings.Contains(out, "--dport 53") {
					t.Errorf("the chain exists but does not hijack DNS:\n%s", out)
				}
				if strings.Contains(out, "--to-ports 12345") {
					t.Errorf("TUN mode installed capture rules:\n%s", out)
				}
				return
			}
			if !b.Installed() {
				t.Fatal("rules applied but no chain is present")
			}

			for _, table := range []string{"nat", "mangle"} {
				out, err := runCmdOutput("iptables", "-t", table, "-S")
				if err != nil {
					continue
				}
				if strings.Contains(out, chainPre) || strings.Contains(out, chainMark) {
					t.Logf("%s / %s table:\n%s", m, table, out)
				}
			}

			if err := b.Revert(); err != nil {
				t.Fatalf("revert: %v", err)
			}
			if b.Installed() {
				t.Error("chains still present after revert")
			}
			// Nothing of ours may survive in either table, jumps included.
			for _, table := range []string{"nat", "mangle"} {
				out, _ := runCmdOutput("iptables", "-t", table, "-S")
				if strings.Contains(out, "XWRT_") {
					t.Errorf("%s table still references our chains after revert:\n%s",
						table, out)
				}
			}
		})
	}
}

// TestLiveIPTReapplyDoesNotAccumulate is the iptables counterpart of the nft
// reconnect test. Jumps are inserted rather than replaced, so a leak here
// would grow the ruleset on every reconnect.
func TestLiveIPTReapplyDoesNotAccumulate(t *testing.T) {
	liveGuard(t)
	if _, err := exec.LookPath("iptables"); err != nil {
		t.Skip("iptables not installed")
	}
	b := &iptBackend{}
	t.Cleanup(func() { _ = b.Revert() })

	p := livePlan(model.ModeRedirect)
	for i := 0; i < 3; i++ {
		if err := b.Apply(p); err != nil {
			t.Fatalf("apply %d: %v", i, err)
		}
	}
	out, err := runCmdOutput("iptables", "-t", "nat", "-S", "PREROUTING")
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(out, "-j "+chainPre); n != 1 {
		t.Errorf("jump to %s appears %d times after three applies, want 1:\n%s",
			chainPre, n, out)
	}
}

func mustContain(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Errorf("kernel ruleset does not contain %q", needle)
	}
}

func mustNotContain(t *testing.T, haystack, needle string) {
	t.Helper()
	if strings.Contains(haystack, needle) {
		t.Errorf("kernel ruleset unexpectedly contains %q", needle)
	}
}

// Exemptions have to reach the kernel, in the right order, or they are a
// setting that does nothing.
//
// Both kinds are checked here because they work differently and fail
// differently. An exempt client is matched on its MAC, which only exists on
// the local segment; an exempt network is matched on the destination address.
// Both are "return" rules, and a return rule placed after the redirect is
// dead — the packet is already gone. That ordering is invisible in the config
// and obvious in the kernel's own listing, which is what this reads.
func TestLiveExemptionsReachTheKernel(t *testing.T) {
	liveGuard(t)
	if _, err := exec.LookPath("nft"); err != nil {
		t.Skip("nft not installed")
	}
	b := &nftBackend{}
	t.Cleanup(func() { _ = b.Revert() })

	p := livePlan(model.ModeMixed)
	p.BypassMACs = []string{"aa:bb:cc:dd:ee:ff", "11:22:33:44:55:66"}
	p.BypassCIDRs = []string{"203.0.113.0/24", "198.51.100.7/32"}

	if err := b.Apply(p); err != nil {
		t.Fatalf("apply: %v", err)
	}
	dump, err := runCmdOutput("nft", "list", "table", "ip", Namespace)
	if err != nil {
		t.Fatalf("list table: %v", err)
	}
	t.Logf("kernel's view:\n%s", dump)

	for _, mac := range p.BypassMACs {
		if !strings.Contains(dump, mac) {
			t.Errorf("exempt client %s never reached the kernel", mac)
		}
	}
	for _, cidr := range p.BypassCIDRs {
		// The kernel prints a /32 as a bare address.
		want := strings.TrimSuffix(cidr, "/32")
		if !strings.Contains(dump, want) {
			t.Errorf("exempt network %s never reached the kernel", cidr)
		}
	}

	// Order, per chain: every return must come before the redirect that would
	// otherwise have taken the packet.
	for _, chain := range []string{"chain prerouting", "chain mangle_prerouting"} {
		body := chainOf(dump, chain)
		if body == "" {
			continue
		}
		mac := strings.Index(body, "@bypassmac")
		ip := strings.Index(body, "@bypass")
		act := strings.Index(body, "redirect to")
		if act < 0 {
			act = strings.Index(body, "meta mark set")
		}
		if act < 0 {
			continue
		}
		if mac >= 0 && mac > act {
			t.Errorf("%s: the client exemption is below the rule that captures "+
				"the packet, so it never runs:\n%s", chain, body)
		}
		if ip >= 0 && ip > act {
			t.Errorf("%s: the network exemption is below the capture rule:\n%s",
				chain, body)
		}
	}
}

// TestLiveSurvivesAFirewallReload is the mechanism behind the durability test
// nobody has run on a router: `/etc/init.d/firewall restart`.
//
// A reload does not ask anyone's permission. fw4 rebuilds its own ruleset from
// scratch, and every table it does not know about — ours included — is gone
// when it finishes. What makes this dangerous rather than merely annoying is
// that nothing else changes: the daemon is running, the core is listening, the
// status page says connected, and not one packet is being captured any more.
//
// The recovery is a hotplug script that calls back in, and the only thing that
// can tell there is anything to do is Installed(). So that is what is tested
// here, against the kernel rather than against our own bookkeeping: gone means
// gone, back means back, and back twice does not mean twice as many rules.
func TestLiveSurvivesAFirewallReload(t *testing.T) {
	liveGuard(t)
	if _, err := exec.LookPath("nft"); err != nil {
		t.Skip("nft not installed")
	}
	b := &nftBackend{}
	defer b.Revert()

	plan := livePlan(model.ModeMixed)
	if err := b.Apply(plan); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !b.Installed() {
		t.Fatal("the rules were applied but the kernel does not list them")
	}
	before := ruleCount(t)

	// What a reload does to us, done the same way: the table is removed
	// without anyone telling the daemon.
	if out, err := exec.Command("nft", "delete", "table", "ip", Namespace).
		CombinedOutput(); err != nil {
		t.Fatalf("could not simulate the reload: %v %s", err, out)
	}
	if b.Installed() {
		t.Fatal("the table is gone from the kernel but Installed() still says " +
			"it is there — nothing would ever put the rules back")
	}

	if err := b.Apply(plan); err != nil {
		t.Fatalf("reapply after a reload: %v", err)
	}
	if !b.Installed() {
		t.Fatal("the rules did not come back")
	}
	if after := ruleCount(t); after != before {
		t.Fatalf("the ruleset changed across a reload and recovery: %d rules "+
			"before, %d after — a reload that doubles the rules is a reload "+
			"that eventually fills the table", before, after)
	}

	// And once more, this time without the table having been removed: the
	// hotplug script fires on events that are not reloads too, and a recovery
	// that appends rather than replaces would show up here.
	if err := b.Apply(plan); err != nil {
		t.Fatalf("apply while already installed: %v", err)
	}
	if after := ruleCount(t); after != before {
		t.Fatalf("applying over a live ruleset changed it: %d then %d",
			before, after)
	}

	if err := b.Revert(); err != nil {
		t.Fatalf("revert: %v", err)
	}
	if b.Installed() {
		t.Fatal("the table survived teardown")
	}
}

// ruleCount is the kernel's own count of what is in our table.
func ruleCount(t *testing.T) int {
	t.Helper()
	out, err := exec.Command("nft", "list", "table", "ip", Namespace).Output()
	if err != nil {
		t.Fatalf("list table: %v", err)
	}
	n := 0
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "table ") ||
			strings.HasPrefix(line, "chain ") || strings.HasPrefix(line, "set ") ||
			strings.HasPrefix(line, "type ") || strings.HasPrefix(line, "elements") ||
			strings.HasPrefix(line, "typeof") || strings.HasPrefix(line, "flags") ||
			line == "}" || line == "{" {
			continue
		}
		n++
	}
	return n
}
