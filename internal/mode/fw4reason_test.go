package mode

import (
	"strings"
	"testing"
)

// What `fw4 check` prints on a device that has another proxy package installed
// and one broken file under /etc/nftables.d.
//
// This is copied from a real report, with the router's own words. The person
// who sent it asked what passwall had to do with their tunnel, which is the
// right question: nothing. Four warnings about a package fw4 merely ignores
// came first, and the one line that explains why the firewall is dead came
// last, after the part that fits on a phone screen.
const realFw4Output = `fw4 check: [!] Section passwall2 option 'reload' is not supported by fw4
[!] Section passwall2 specifies unreachable path '/var/etc/passwall2.include', ignoring section
[!] Section passwall2_server option 'reload' is not supported by fw4
[!] Section passwall2_server specifies unreachable path '/var/etc/passwall2_server.include', ignoring section
In file included from /dev/stdin:23:2-33:
/etc/nftables.d/99-reset-ttl-from-br-lan.nft:3:34-40: Error: syntax error, unexpected comment
    iifname "br-lan" ip ttl set  comment "Reset TTL for br-lan IPv4"
                                 ^^^^^^^`

func TestTheBrokenFileIsNamedFirst(t *testing.T) {
	got := fw4Reason(realFw4Output)

	if !strings.HasPrefix(got, "the file it cannot parse is "+
		"/etc/nftables.d/99-reset-ttl-from-br-lan.nft") {
		t.Fatalf("the first line is:\n%s\n\nIt should name the file that has "+
			"to be fixed, because that is the only thing the reader can act on",
			firstLine(got))
	}
	if !strings.Contains(got, "/etc/init.d/firewall restart") {
		t.Error("it names the file but not what to do once it is fixed")
	}
}

func TestPasswallIsNotPresentedAsTheCause(t *testing.T) {
	got := fw4Reason(realFw4Output)

	// The warnings themselves must not be quoted: quoted, they are what the
	// reader sees first and they name a package that is not involved.
	if strings.Contains(got, "passwall2") {
		t.Fatalf("the message still quotes passwall's warnings, so the reader "+
			"still sees another package's name where the cause should be:\n%s", got)
	}
	// But they are not hidden either — something printed them, and pretending
	// fw4 said nothing about them invites the next person to run fw4 by hand
	// and wonder what was being kept from them.
	if !strings.Contains(got, "4 warning") {
		t.Error("the warnings are dropped without trace; say how many there " +
			"were and that they are not the cause")
	}
	if !strings.Contains(got, "not the cause") {
		t.Error("the warnings are counted but not excused")
	}
}

func TestTheRealErrorSurvives(t *testing.T) {
	got := fw4Reason(realFw4Output)
	for _, want := range []string{
		"syntax error, unexpected comment",
		"99-reset-ttl-from-br-lan.nft:3:34-40",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the message no longer contains %q, which is the part "+
				"that says what is actually wrong", want)
		}
	}
}

func TestWarningsAloneAreNotTurnedIntoAnAccusation(t *testing.T) {
	// If fw4 somehow fails with nothing but warnings, there is no error line to
	// promote. Inventing one would be worse than showing what was printed.
	only := `[!] Section passwall2 option 'reload' is not supported by fw4
[!] Section passwall2 specifies unreachable path '/var/etc/passwall2.include', ignoring section`

	got := fw4Reason(only)
	if strings.Contains(got, "the file it cannot parse") {
		t.Fatalf("a file was named when fw4 never named one:\n%s", got)
	}
	if !strings.Contains(got, "passwall2") {
		t.Fatalf("with nothing else to show, the warnings should be shown:\n%s", got)
	}
}

func TestALongErrorIsClipped(t *testing.T) {
	long := "Error: " + strings.Repeat("x", 2000)
	got := fw4Reason(long)
	if len(got) > 900 {
		t.Fatalf("the message is %d characters; it goes into a log line and a "+
			"phone-sized box", len(got))
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
