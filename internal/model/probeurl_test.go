package model

import (
	"os"
	"strings"
	"testing"
)

// The address the health check is sent to.
//
// A group with a wrong one does not fail in any way anybody can see. The core
// starts, the tunnel comes up, traffic flows — and every probe errors, so every
// member scores the same, the ranking is made of nothing, and the balancer uses
// whichever member comes first. A group that has quietly stopped being a group
// looks exactly like one that is working.
//
// That is why this is refused at the point where someone types it rather than
// discovered later: there is no later. Nothing reports it.

func TestAnAddressTheCoreCanUseIsAccepted(t *testing.T) {
	for _, ok := range []string{
		"https://www.gstatic.com/generate_204",
		"https://cp.cloudflare.com/generate_204",
		"http://cp.cloudflare.com/generate_204",
		"https://connectivitycheck.gstatic.com/generate_204",
		"http://192.0.2.10:8080/ping",
	} {
		g := Group{ProbeURL: ok}
		if err := g.ValidateProbeURL(); err != nil {
			t.Errorf("%q was refused: %v", ok, err)
		}
	}
}

func TestAnEmptyAddressIsNotAnError(t *testing.T) {
	// Empty means "use the default", which Normalize fills in. Refusing it
	// here would make every group that never touched the field unsavable.
	g := Group{}
	if err := g.ValidateProbeURL(); err != nil {
		t.Fatalf("an unset address was refused: %v", err)
	}
	g.Normalize()
	if g.ProbeURL != DefaultProbeURL {
		t.Errorf("after Normalize the address is %q, want the default", g.ProbeURL)
	}
	if err := g.ValidateProbeURL(); err != nil {
		t.Errorf("the default this project ships is itself refused: %v", err)
	}
}

func TestAnAddressTheCoreCannotUseIsRefused(t *testing.T) {
	for _, bad := range []struct{ url, why string }{
		{"www.gstatic.com/generate_204", "no scheme: the core needs a URL, not a host"},
		{"cp.cloudflare.com", "a bare host is the likeliest thing to be typed here"},
		{"ftp://example.com/x", "a scheme the probe cannot speak"},
		{"https://", "a scheme and nothing to send it to"},
		{"tcp://1.1.1.1:53", "a resolver address, confused with the DNS field"},
	} {
		g := Group{ProbeURL: bad.url}
		if err := g.ValidateProbeURL(); err == nil {
			t.Errorf("%q was accepted — %s", bad.url, bad.why)
		}
	}
}

func TestTheWholeGroupCheckCoversTheAddress(t *testing.T) {
	// Validate is what the configuration checker calls. If the address is only
	// checked by the one caller that remembers to call the narrow function,
	// the JSON editor and the API go on accepting it.
	g := Group{
		Members:  []string{"p1"},
		Strategy: StrategyLeastPing,
		ProbeURL: "cp.cloudflare.com",
	}
	if err := g.Validate(); err == nil {
		t.Fatal("a group with an unusable health check address passed Validate")
	}
}

// TestTheDefaultIsWhatTheDialogOffersFirst keeps two files that cannot import
// each other in step.
//
// The daemon fills an empty address with DefaultProbeURL; the group dialog
// shows a list whose first entry is what a new group gets. If those two ever
// name different addresses, a group created through the API and then opened in
// the dialog shows "Other address…" with a box the operator never filled in,
// and changing the group's name rewrites its health check address.
func TestTheDefaultIsWhatTheDialogOffersFirst(t *testing.T) {
	const dialog = "../../luci-app-xwrt/htdocs/luci-static/resources/view/xwrt/profiles.js"

	b, err := os.ReadFile(dialog)
	if err != nil {
		t.Fatalf("cannot read the group dialog: %v", err)
	}
	src := string(b)

	// The first entry of the list the dialog builds.
	const marker = "var probeURLs = ["
	i := strings.Index(src, marker)
	if i < 0 {
		t.Fatalf("no probe address list in %s; if it was renamed, this check "+
			"has to be renamed with it rather than deleted", dialog)
	}
	rest := src[i+len(marker):]
	first := strings.Index(rest, "'")
	if first < 0 {
		t.Fatal("the probe address list has no entries")
	}
	rest = rest[first+1:]
	end := strings.Index(rest, "'")
	if end < 0 {
		t.Fatal("the first entry of the probe address list is not a string")
	}
	if got := rest[:end]; got != DefaultProbeURL {
		t.Errorf("the dialog offers %q first and the daemon defaults to %q;\n"+
			"a group made through the API then opens in the dialog showing "+
			"\"Other address…\"", got, DefaultProbeURL)
	}
}
