package xray

import (
	"testing"

	"xwrt/internal/model"
)

// helloXdivine, and the one thing it must not do.
//
// It is this project's name for a fingerprint the core has but does not
// advertise: "hellogolang", the handshake Go's own crypto/tls sends, with no
// imitation of a browser in it. What makes it worth having is that Go orders
// its cipher suites by what the CPU can do and a browser's list does not — so
// on a router with no AES instructions every browser fingerprint ends up
// negotiating AES-GCM and doing it in software, while this one lets the server
// pick ChaCha20 and roughly doubles the throughput.
//
// The name is ours. The core has never heard of it, and a core handed a
// fingerprint it does not recognise falls back to its default without saying
// so — silently, with a connection that still works and is merely half the
// speed it should be. So the whole feature is one translation, and these are
// the tests that it happens.

func fingerprintIn(t *testing.T, p *model.Profile) string {
	t.Helper()
	s := model.Defaults()
	cfg, err := Build(Options{Profile: p, Settings: &s, Caps: AllFeatures()})
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range cfg.Outbounds {
		if o.StreamSettings == nil {
			continue
		}
		if ts := o.StreamSettings.TLSSettings; ts != nil {
			return ts.Fingerprint
		}
		if rs := o.StreamSettings.RealitySettings; rs != nil {
			return rs.Fingerprint
		}
	}
	t.Fatal("the configuration has no TLS or REALITY settings to carry a fingerprint")
	return ""
}

func fpTLSProfile(fp string) *model.Profile {
	return &model.Profile{ID: "p1", Proto: model.ProtoVLESS,
		Address: "203.0.113.10", Port: 443,
		UUID:    "b831381d-6324-4d53-ad4f-8cda48b30811",
		Network: "ws", Security: "tls", Fingerprint: fp}
}

func fpRealityProfile(fp string) *model.Profile {
	return &model.Profile{ID: "p1", Proto: model.ProtoVLESS,
		Address: "203.0.113.10", Port: 443,
		UUID:    "b831381d-6324-4d53-ad4f-8cda48b30811",
		Network: "tcp", Security: "reality", Fingerprint: fp,
		PublicKey: "aGVsbG8tdGhpcy1pcy1ub3QtYS1yZWFsLWtleS0wMDAwMDA", ShortID: "01"}
}

func TestTheCoreIsGivenTheNameItKnows(t *testing.T) {
	if got := fingerprintIn(t, fpTLSProfile("helloXdivine")); got != "hellogolang" {
		t.Fatalf("the core was handed %q, which is not in any of its fingerprint "+
			"tables, so it would silently use its default instead", got)
	}
}

// REALITY carries a fingerprint too, and it is the security a hidden option is
// most likely to be tried under.
func TestItIsTranslatedUnderRealityAsWell(t *testing.T) {
	if got := fingerprintIn(t, fpRealityProfile("helloXdivine")); got != "hellogolang" {
		t.Fatalf("under REALITY the core was handed %q", got)
	}
}

// UCI is a text file and a share link is a query string. The same value arrives
// spelled however whoever typed it felt like spelling it.
func TestHoweverItIsSpelled(t *testing.T) {
	for _, spelling := range []string{
		"helloXdivine", "helloxdivine", "HELLOXDIVINE", "HelloXDivine",
		"  helloXdivine  ",
	} {
		if got := fingerprintIn(t, fpTLSProfile(spelling)); got != "hellogolang" {
			t.Errorf("%q reached the core as %q", spelling, got)
		}
	}
}

// Everything else is passed through untouched — including names this build has
// never heard of. A share link can carry a fingerprint from a newer core, and a
// list of known values that quietly replaces anything else turns a working
// server into one that fails for no visible reason.
func TestEveryOtherFingerprintIsLeftAlone(t *testing.T) {
	for _, fp := range []string{
		"chrome", "firefox", "safari", "ios", "android", "edge", "360", "qq",
		"random", "randomized",
		// The core's own name for it, typed directly by somebody who knows.
		"hellogolang",
		// A specific version from the core's second table, and one from a
		// version of the core newer than this build.
		"hellochrome_106_shuffle", "hellochrome_133",
	} {
		if got := fingerprintIn(t, fpTLSProfile(fp)); got != fp {
			t.Errorf("%q was rewritten to %q", fp, got)
		}
	}
}

// A profile with no fingerprint set keeps the behaviour it had: nothing for
// TLS, where the core's own default applies, and chrome for REALITY, which has
// never worked without one.
func TestAnUnsetFingerprintIsUnchanged(t *testing.T) {
	if got := fingerprintIn(t, fpTLSProfile("")); got != "" {
		t.Errorf("an empty fingerprint became %q", got)
	}
	if got := fingerprintIn(t, fpRealityProfile("")); got != "chrome" {
		t.Errorf("REALITY with no fingerprint became %q", got)
	}
}

// And the translation is only a translation: it must not be the only way to get
// there, since the core's own name has to keep working for anyone reading the
// core's source rather than this interface.
func TestBothNamesReachTheSamePlace(t *testing.T) {
	ours := fingerprintIn(t, fpTLSProfile("helloXdivine"))
	theirs := fingerprintIn(t, fpTLSProfile("hellogolang"))
	if ours != theirs {
		t.Fatalf("our name produces %q and the core's own produces %q", ours, theirs)
	}
}
