package mode

import (
	"strings"
	"testing"

	"xwrt/internal/model"
)

// fakeUCI serves values from a map keyed "pkg.section.option".
func fakeUCI(values map[string]string) func(string, string, string) string {
	return func(pkg, sec, opt string) string {
		return values[pkg+"."+sec+"."+opt]
	}
}

func tunFor(name string) *Tun {
	s := model.Defaults()
	s.TunName = name
	return &Tun{Settings: &s}
}

// The three ways a TUN-mode connection comes up and still passes no traffic.
// Each one is a firewall rule that is missing, and fw4 drops the packets
// without logging anything, so the warning is the only thing standing between
// the operator and an evening of guessing.
func TestZoneWarning(t *testing.T) {
	withoutKernelView(t)

	complete := map[string]string{
		"firewall.@zone[0].name":       "lan",
		"firewall.@zone[0].network":    "lan",
		"firewall.@zone[1].name":       "xwrt",
		"firewall.@zone[1].device":     "xwrt0",
		"firewall.@zone[1].masq":       "1",
		"firewall.@forwarding[0].src":  "lan",
		"firewall.@forwarding[0].dest": "xwrt",
	}

	if warn := tunFor("xwrt0").ZoneWarning(fakeUCI(complete)); warn != "" {
		t.Errorf("a complete setup should be silent, got: %s", warn)
	}

	// The device was renamed in settings but the zone still names the old one.
	if warn := tunFor("tun9").ZoneWarning(fakeUCI(complete)); !strings.Contains(warn, "not in any firewall zone") {
		t.Errorf("want a missing-zone warning, got: %s", warn)
	}

	noForward := map[string]string{}
	for k, v := range complete {
		noForward[k] = v
	}
	delete(noForward, "firewall.@forwarding[0].src")
	delete(noForward, "firewall.@forwarding[0].dest")
	warn := tunFor("xwrt0").ZoneWarning(fakeUCI(noForward))
	if !strings.Contains(warn, "forwarding") {
		t.Errorf("want a missing-forwarding warning, got: %s", warn)
	}
	// A warning nobody can act on is only half a warning.
	if !strings.Contains(warn, "uci add firewall forwarding") {
		t.Errorf("the warning must carry the fix, got: %s", warn)
	}

	noMasq := map[string]string{}
	for k, v := range complete {
		noMasq[k] = v
	}
	noMasq["firewall.@zone[1].masq"] = ""
	if warn := tunFor("xwrt0").ZoneWarning(fakeUCI(noMasq)); !strings.Contains(warn, "masquerade") {
		t.Errorf("want a masquerading warning, got: %s", warn)
	}
}

// A device whose firewall configuration cannot be read must produce no warning
// at all. Reporting a missing zone there would be a confident wrong answer, and
// in TUN mode that answer now fails the connect.
func TestZoneWarningStaysQuietWhenNothingCanBeRead(t *testing.T) {
	withoutKernelView(t)

	if warn := tunFor("xwrt0").ZoneWarning(fakeUCI(nil)); warn != "" {
		t.Errorf("unreadable firewall config should say nothing, got: %s", warn)
	}
}

// withoutKernelView makes ZoneWarning fall back to the configuration, which is
// what the tests above are about.
func withoutKernelView(t *testing.T) {
	t.Helper()
	prev := loadedRuleset
	loadedRuleset = func() (string, bool) { return "", false }
	t.Cleanup(func() { loadedRuleset = prev })
}

// The kernel is the authority. A configuration that describes a perfect zone
// means nothing if fw4 never loaded it: the forward chain's policy is drop, so
// traffic to a device it has no rule for disappears. This is the exact state a
// real device turned out to be in, with a healthy-looking /etc/config/firewall.
func TestKernelRulesetOverridesTheConfiguration(t *testing.T) {
	good := map[string]string{
		"firewall.@zone[0].name":       "lan",
		"firewall.@zone[1].name":       "xwrt",
		"firewall.@zone[1].device":     "xwrt0",
		"firewall.@zone[1].masq":       "1",
		"firewall.@forwarding[0].src":  "lan",
		"firewall.@forwarding[0].dest": "xwrt",
	}

	prev, prevCheck := loadedRuleset, firewallRenderError
	firewallRenderError = func() string { return "" }
	t.Cleanup(func() { loadedRuleset, firewallRenderError = prev, prevCheck })

	// fw4 knows only lan and wan: the config is ignored, the warning stands.
	loadedRuleset = func() (string, bool) {
		return `chain forward_lan { jump accept_to_wan }
			chain accept_to_lan { oifname "br-lan" counter accept }`, true
	}
	warn := tunFor("xwrt0").ZoneWarning(fakeUCI(good))
	if !strings.Contains(warn, "no rule for xwrt0") {
		t.Errorf("a device absent from the loaded ruleset must be reported: %q", warn)
	}
	if !strings.Contains(warn, "firewall reload") {
		t.Errorf("the warning must name the fix: %q", warn)
	}

	// Once fw4 has the device, nothing is wrong regardless of what the config
	// looks like to a parser.
	loadedRuleset = func() (string, bool) {
		return `chain accept_to_xwrt { oifname "xwrt0" counter accept }`, true
	}
	if warn := tunFor("xwrt0").ZoneWarning(fakeUCI(nil)); warn != "" {
		t.Errorf("a loaded zone should be silent, got: %q", warn)
	}

	// fw3 spells the same thing differently.
	loadedRuleset = func() (string, bool) {
		return "-A zone_xwrt_dest_ACCEPT -o xwrt0 -j ACCEPT", true
	}
	if warn := tunFor("xwrt0").ZoneWarning(fakeUCI(nil)); warn != "" {
		t.Errorf("iptables form should be recognised too, got: %q", warn)
	}
}

// A firewall that cannot render its ruleset at all is the cause of the missing
// zone, not a separate problem — and the advice for it is different, because no
// amount of reloading will install anything. Saying "reload the firewall" to
// someone in that state sends them in circles.
func TestBrokenFirewallIsNamedInsteadOfTheZone(t *testing.T) {
	prevRules, prevCheck := loadedRuleset, firewallRenderError
	t.Cleanup(func() { loadedRuleset, firewallRenderError = prevRules, prevCheck })

	loadedRuleset = func() (string, bool) { return `chain forward_lan { }`, true }
	firewallRenderError = func() string {
		return `fw4 check: /etc/nftables.d/99-reset-ttl.nft:3: Error: syntax error`
	}

	warn := tunFor("xwrt0").ZoneWarning(fakeUCI(nil))
	if !strings.Contains(warn, "cannot build its ruleset") {
		t.Errorf("the real cause must lead: %q", warn)
	}
	if !strings.Contains(warn, "99-reset-ttl.nft") {
		t.Errorf("the offending file must be quoted verbatim: %q", warn)
	}
	if strings.Contains(warn, "sh /etc/uci-defaults") {
		t.Errorf("advice that cannot work must not be offered: %q", warn)
	}
}
