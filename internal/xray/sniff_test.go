package xray

import (
	"testing"

	"xwrt/internal/model"
)

// Sniffing, and the connections it quietly prevented.
//
// Reading the destination name out of a connection's first packet is what makes
// a rule about a domain possible, since a firewall only sees addresses. The
// price is in the core's own documentation: with sniffing on, "the client must
// send data first before the proxy server actually establishes a connection".
//
// A protocol where the server speaks first therefore does not connect at all.
// The core waits for the client, the client waits for the server's handshake,
// and the application times out. SMTP is the example the documentation gives;
// the one that cost a week here was a game, whose map servers send their
// handshake before the client says anything — the game would load, play, and
// then drop to the login screen on the second teleport, every time, while the
// same server through a client that does not sniff was faultless.
//
// So it is on when something needs it and off when nothing does.

func profileFor(t *testing.T) *model.Profile {
	t.Helper()
	return &model.Profile{
		ID: "p1", Proto: model.ProtoVLESS, Address: "203.0.113.10", Port: 443,
		UUID: "b831381d-6324-4d53-ad4f-8cda48b30811", Network: "ws", Security: "tls",
	}
}

func sniffingOf(t *testing.T, s model.Settings, rules []model.Rule) map[string]*Sniffing {
	t.Helper()
	cfg, err := Build(Options{Profile: profileFor(t), Settings: &s, Rules: rules,
		Caps: AllFeatures()})
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]*Sniffing{}
	for _, in := range cfg.Inbounds {
		out[in.Tag] = in.Sniffing
	}
	return out
}

func domainRule() []model.Rule {
	return []model.Rule{{ID: "r1", Name: "kural", Enabled: true,
		Action: model.ActionDirect, Domains: []string{"fast.com"}}}
}

// --- auto, which is what everyone gets --------------------------------------

func TestWithNoDomainRuleNothingSniffs(t *testing.T) {
	for tag, sn := range sniffingOf(t, model.Defaults(), nil) {
		if sn != nil && sn.Enabled {
			t.Errorf("%s sniffs with no domain rule to sniff for", tag)
		}
	}
}

func TestADomainRuleTurnsItOn(t *testing.T) {
	got := sniffingOf(t, model.Defaults(), domainRule())
	on := 0
	for _, sn := range got {
		if sn != nil && sn.Enabled {
			on++
		}
	}
	if on == 0 {
		t.Fatalf("a domain rule was given and nothing sniffs; the rule cannot match")
	}
}

// A rule the operator switched off is not a reason to break server-first
// protocols for the whole network.
func TestADisabledRuleDoesNotCount(t *testing.T) {
	rules := domainRule()
	rules[0].Enabled = false
	for tag, sn := range sniffingOf(t, model.Defaults(), rules) {
		if sn != nil && sn.Enabled {
			t.Errorf("%s sniffs for a rule that is switched off", tag)
		}
	}
}

// A rule that matches on addresses needs no sniffing: the address is in the
// packet header, which is where routing reads it from anyway.
func TestAnAddressRuleDoesNotTurnItOn(t *testing.T) {
	rules := []model.Rule{{ID: "r1", Name: "kural", Enabled: true,
		Action: model.ActionDirect, IPs: []string{"198.51.100.0/24"}}}
	for tag, sn := range sniffingOf(t, model.Defaults(), rules) {
		if sn != nil && sn.Enabled {
			t.Errorf("%s sniffs for a rule that matches on addresses", tag)
		}
	}
}

// --- and the two the operator can ask for -----------------------------------

func TestOffStaysOffEvenWithADomainRule(t *testing.T) {
	s := model.Defaults()
	s.Sniff = model.SniffOff
	for tag, sn := range sniffingOf(t, s, domainRule()) {
		if sn != nil && sn.Enabled {
			t.Errorf("%s sniffs although sniffing is off", tag)
		}
	}
}

func TestOnStaysOnWithNoRulesAtAll(t *testing.T) {
	s := model.Defaults()
	s.Sniff = model.SniffOn
	on := 0
	for _, sn := range sniffingOf(t, s, nil) {
		if sn != nil && sn.Enabled {
			on++
		}
	}
	if on == 0 {
		t.Fatalf("sniffing is set to on and nothing sniffs")
	}
}

// --- what it looks like when it is on ---------------------------------------

// Turning it on must not change what it does: the sniffed name is for routing,
// the destination stays the address the client already resolved.
func TestWhenOnItIsStillRouteOnly(t *testing.T) {
	for tag, sn := range sniffingOf(t, model.Defaults(), domainRule()) {
		if sn == nil || !sn.Enabled {
			continue
		}
		if !sn.RouteOnly {
			t.Errorf("%s resolves the name a second time", tag)
		}
		if len(sn.DestOverride) == 0 {
			t.Errorf("%s is enabled with nothing to sniff for", tag)
		}
	}
}

// Off is written as a disabled block rather than left out. An absent sniffing
// object is the core's own default, and the core's default is on.
func TestOffIsWrittenDownRatherThanLeftOut(t *testing.T) {
	s := model.Defaults()
	s.Sniff = model.SniffOff
	got := sniffingOf(t, s, nil)
	seen := false
	for _, sn := range got {
		if sn != nil {
			seen = true
			if sn.Enabled {
				t.Fatalf("a sniffing block says enabled with sniffing off")
			}
		}
	}
	if !seen {
		t.Fatalf("no sniffing block at all: the core would use its own default, which is on")
	}
}

// --- the setting itself -----------------------------------------------------

func TestTheDefaultIsAuto(t *testing.T) {
	if got := model.Defaults().Sniff; got != model.SniffAuto {
		t.Fatalf("the shipped default is %q", got)
	}
}

// An installation upgrading from a version without this option has nothing in
// its file. That has to come out as auto, not as an empty string the config
// builder then reads as "on".
func TestAnOlderConfigurationBecomesAuto(t *testing.T) {
	var s model.Settings
	s.Normalize()
	if s.Sniff != model.SniffAuto {
		t.Fatalf("an unset sniff normalised to %q", s.Sniff)
	}
	// And something misspelt is not obeyed.
	s.Sniff = "evet"
	s.Normalize()
	if s.Sniff != model.SniffAuto {
		t.Fatalf("a misspelt sniff normalised to %q", s.Sniff)
	}
}
