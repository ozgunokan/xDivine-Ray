package fw

import (
	"strings"
	"testing"
)

// The device's own firewall, and the hour it cost somebody.
//
// A router came back from a line swap with no internet on any client. The
// router itself pinged, resolved names and reported a correct routing decision
// for a LAN source address; its configuration had `masq '1'` on a wan zone that
// included the new interface; `ip rule` was clean and no stale route was left
// anywhere. Every check anybody thought to run came back healthy, and with the
// tunnel switched on everything worked — which is what made it look like the
// tunnel's doing.
//
// The firewall was not loaded. `nft list table inet fw4` said the table did not
// exist. One file under /etc/nftables.d had `ip ttl set` with no value, fw4
// refused the whole ruleset rather than part of it, and from the moment
// something reloaded the firewall the kernel had no masquerade. Clients went out
// with a 192.168.1.x source address and nothing answered. The router needed no
// masquerade for itself, and neither did the tunnel, so both looked fine.
//
// These tests exist so that the one question nobody asked is now asked
// automatically.

// withSeams replaces the three things that talk to the device.
func withSeams(t *testing.T, bins []string, kernel map[string]string, check string, checkOK bool) {
	t.Helper()
	oldHave, oldKernel, oldCheck := haveBinary, kernelHas, fw4CheckOutput
	t.Cleanup(func() { haveBinary, kernelHas, fw4CheckOutput = oldHave, oldKernel, oldCheck })

	haveBinary = func(name string) bool {
		for _, b := range bins {
			if b == name {
				return true
			}
		}
		return false
	}
	kernelHas = func(name string, _ []string, want string) bool {
		return strings.Contains(kernel[name], want)
	}
	fw4CheckOutput = func() (string, bool) { return check, checkOK }
}

const brokenTTLFile = `[!] Section passwall2 option 'reload' is not supported by fw4
In file included from /dev/stdin:23:2-33:
/etc/nftables.d/99-reset-ttl-from-br-lan.nft:3:34-40: Error: syntax error, unexpected comment
    iifname "br-lan" ip ttl set  comment "Reset TTL for br-lan IPv4"
                                 ^^^^^^^`

func TestALoadedFirewallIsNotReported(t *testing.T) {
	withSeams(t, []string{"fw4"},
		map[string]string{"nft": "table inet fw4\ntable ip xwrt\n"}, "", true)

	got := SystemCheck()
	if !got.Known || !got.Loaded {
		t.Fatalf("a device whose fw4 table is in the kernel reports %+v", got)
	}
	if got.Down() {
		t.Error("Down() is true for a working firewall")
	}
}

// The case from the report: the table is absent and fw4 can say why.
func TestAnUnloadedFirewallIsReportedWithItsReason(t *testing.T) {
	withSeams(t, []string{"fw4"},
		map[string]string{"nft": "table ip xwrt\n"}, brokenTTLFile, false)

	got := SystemCheck()
	if !got.Down() {
		t.Fatalf("the fw4 table is missing from the kernel and this reports %+v", got)
	}
	if !strings.Contains(got.Reason, "99-reset-ttl-from-br-lan.nft") {
		t.Errorf("the reason does not name the file that has to be fixed:\n%s", got.Reason)
	}
}

// Our own table being present proves nothing about theirs. This is the exact
// state the router was in: `table ip xwrt` loaded and enforcing, `table inet
// fw4` gone — so a check that merely asked "is nftables working" would have
// said yes.
func TestOurOwnTableIsNotMistakenForTheirs(t *testing.T) {
	withSeams(t, []string{"fw4"},
		map[string]string{"nft": "table ip xwrt\n"}, "", false)

	if got := SystemCheck(); !got.Down() {
		t.Fatalf("the xwrt table was accepted as evidence of a firewall: %+v", got)
	}
}

// A firewall that is merely stopped has nothing to say for itself, and that is
// not a reason to say nothing about it: no masquerade is no masquerade whether
// a file is broken or a service is down.
func TestAStoppedFirewallIsStillReported(t *testing.T) {
	withSeams(t, []string{"fw4"}, map[string]string{"nft": ""}, "", true)

	got := SystemCheck()
	if !got.Down() {
		t.Fatalf("a stopped firewall reports %+v", got)
	}
	if got.Reason != "" {
		t.Errorf("a reason was invented where fw4 gave none: %q", got.Reason)
	}
}

// Saying "your firewall is missing" to a device that has no firewall manager is
// noise, and worse, it is noise in the place a real fault would be shown.
func TestADeviceWithNoFirewallManagerIsLeftAlone(t *testing.T) {
	withSeams(t, nil, map[string]string{"nft": ""}, "", true)

	got := SystemCheck()
	if got.Known {
		t.Fatalf("an opinion was formed about a device with no fw3 and no fw4: %+v", got)
	}
	if got.Down() {
		t.Error("Down() is true on a device this check knows nothing about")
	}
}

// The older generation. fw3 has no table of its own, so its zone chains stand
// in for one.
func TestFw3IsJudgedByItsZoneChains(t *testing.T) {
	withSeams(t, []string{"fw3"},
		map[string]string{"iptables-save": "-A zone_wan_postrouting -j MASQUERADE\n"},
		"", true)
	if got := SystemCheck(); !got.Known || !got.Loaded {
		t.Fatalf("a loaded fw3 reports %+v", got)
	}

	withSeams(t, []string{"fw3"},
		map[string]string{"iptables-save": "-P POSTROUTING ACCEPT\n"}, "", true)
	if got := SystemCheck(); !got.Down() {
		t.Fatalf("an fw3 with no zone chains at all reports %+v", got)
	}
}

// fw4 is asked before fw3. A 22.03 device can have both binaries installed, and
// only one of them is in charge.
func TestFw4WinsWhenBothArePresent(t *testing.T) {
	withSeams(t, []string{"fw3", "fw4"},
		map[string]string{
			"nft":           "table inet fw4\n",
			"iptables-save": "-P POSTROUTING ACCEPT\n",
		}, "", true)

	if got := SystemCheck(); !got.Loaded {
		t.Fatalf("fw4 is loaded and fw3 is not, and this reports %+v", got)
	}
}

// RenderError must not answer for a device it cannot ask.
func TestNoFw4MeansNoReason(t *testing.T) {
	withSeams(t, []string{"fw3"}, nil, brokenTTLFile, false)
	if got := RenderError(); got != "" {
		t.Fatalf("fw4 is not installed and it still reported: %q", got)
	}
}
