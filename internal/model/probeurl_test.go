package model

import "testing"

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
