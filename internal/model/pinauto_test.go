package model

import (
	"strings"
	"testing"
)

// Where accepting a changed certificate unseen is even an option.
//
// The rule is not "the address looks like an IP". It is "there is no name to
// check a certificate against", and the two come apart in the case that matters:
// a profile dialling an IP while sending a real SNI can be verified perfectly
// well, and turning the check off there would throw away protection that was
// there for the taking.

func tlsProfile(address, sni string) Profile {
	return Profile{
		ID: "p1", Proto: ProtoVLESS, UUID: "b831381d-6324-4d53-ad4f-8cda48b30811",
		Address: address, Port: 443, Network: "ws", Security: "tls", SNI: sni,
	}
}

func TestABareAddressHasNothingToVerify(t *testing.T) {
	p := tlsProfile("87.121.104.212", "")
	if p.VerifiableName() != "" {
		t.Errorf("VerifiableName = %q, want empty", p.VerifiableName())
	}
	if !p.CanAutoPin() {
		t.Error("a bare IP with no SNI was refused the option")
	}
}

func TestAnAddressWithANameIsVerifiable(t *testing.T) {
	p := tlsProfile("vpn.example.com", "")
	if p.VerifiableName() != "vpn.example.com" {
		t.Errorf("VerifiableName = %q", p.VerifiableName())
	}
	if p.CanAutoPin() {
		t.Error("a profile with a real name was offered the option")
	}
}

// The case the loose rule gets wrong. Dialling an IP while sending a name is a
// normal arrangement, and that connection's certificate is checked against the
// name — so there is a real check to keep.
func TestAnIPWithASNIIsStillVerifiable(t *testing.T) {
	p := tlsProfile("87.121.104.212", "vpn.example.com")
	if p.VerifiableName() != "vpn.example.com" {
		t.Errorf("VerifiableName = %q; the SNI is what the far end answers for",
			p.VerifiableName())
	}
	if p.CanAutoPin() {
		t.Error("a profile sending a real SNI was offered the option, which " +
			"would turn off a check that works")
	}
}

func TestAnSNIThatIsAlsoAnIPVerifiesNothing(t *testing.T) {
	p := tlsProfile("87.121.104.212", "87.121.104.212")
	if p.VerifiableName() != "" {
		t.Errorf("VerifiableName = %q, want empty", p.VerifiableName())
	}
	if !p.CanAutoPin() {
		t.Error("an IP SNI was treated as a name")
	}
}

func TestIPv6CountsAsNoName(t *testing.T) {
	p := tlsProfile("2001:db8::1", "")
	if p.VerifiableName() != "" {
		t.Errorf("VerifiableName = %q for an IPv6 literal", p.VerifiableName())
	}
}

// Without tls there is no certificate at all, so there is nothing to accept.
func TestARealityProfileIsNotOfferedTheOption(t *testing.T) {
	p := tlsProfile("87.121.104.212", "")
	p.Security = "reality"
	p.PublicKey = "abc"
	if p.CanAutoPin() {
		t.Error("a reality profile was offered certificate acceptance")
	}
}

// --- and the rule is enforced, not merely advertised -------------------------

func TestTurningItOnWhereItIsNotAllowedIsRefused(t *testing.T) {
	p := tlsProfile("87.121.104.212", "vpn.example.com")
	p.PinAuto = true

	err := p.Validate()
	if err == nil {
		t.Fatal("accepted on a profile that can verify its certificate properly")
	}
	// The refusal has to say what to do instead, or it reads as an arbitrary
	// restriction and the next thing somebody tries is editing the JSON.
	if !strings.Contains(err.Error(), "vpn.example.com") {
		t.Errorf("the refusal does not name what is verified: %v", err)
	}
	if !strings.Contains(err.Error(), "Clear the pin") {
		t.Errorf("the refusal does not say what to do instead: %v", err)
	}
}

func TestTurningItOnWhereItIsAllowedIsAccepted(t *testing.T) {
	p := tlsProfile("87.121.104.212", "")
	p.PinAuto = true
	if err := p.Validate(); err != nil {
		t.Errorf("refused on a profile with nothing to verify: %v", err)
	}
}

func TestItCannotBeTurnedOnWithoutTLS(t *testing.T) {
	p := tlsProfile("87.121.104.212", "")
	p.Security = "none"
	p.PinAuto = true
	if err := p.Validate(); err == nil {
		t.Error("accepted on a profile with no certificate at all")
	}
}

// Off is the default, and a profile that never mentions it stays off. A flag
// that protects nothing is not worth having; a flag that turns itself on is
// worse than no flag.
func TestItIsOffUnlessAskedFor(t *testing.T) {
	p := tlsProfile("87.121.104.212", "")
	if p.PinAuto {
		t.Error("on by default")
	}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	if p.PinAuto {
		t.Error("Validate turned it on")
	}
}
