package daemon

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"xwrt/internal/fault"
	"xwrt/internal/model"
)

func TestStepErrorCarriesContextThroughWrapping(t *testing.T) {
	inner := errors.New("address already in use")
	se := fail(model.StepCore, "core.start", "/usr/bin/xray", inner).
		from(model.SourceCore).
		withLines([]string{"first line", "second line"}).
		withHint("hint.install_xray")

	// Wrapping must not lose the context: the engine returns these through
	// several layers before anything files them.
	wrapped := fmt.Errorf("connect: %w", se)

	var got *stepError
	if !errors.As(wrapped, &got) {
		t.Fatal("step context did not survive wrapping")
	}
	if got.step != model.StepCore || got.source != model.SourceCore {
		t.Fatalf("wrong attribution: %+v", got)
	}
	if !errors.Is(wrapped, inner) {
		t.Fatal("the underlying error must still be matchable")
	}
	if got.detail != "first line\nsecond line" {
		t.Fatalf("detail lines lost: %q", got.detail)
	}
}

func TestRecordFilesStepContext(t *testing.T) {
	e := &Engine{Log: newTestRing(50)}
	e.record(fail(model.StepFirewall, "fw.apply", "nftables", errors.New("kernel said no")).
		withDetail("nft: syntax error").
		withHint("hint.fw_install_tools"))

	errs := e.Log.Errors(0)
	if len(errs) != 1 {
		t.Fatalf("want one journal entry, got %d", len(errs))
	}
	if errs[0].Step != model.StepFirewall {
		t.Fatalf("step not filed: %+v", errs[0])
	}
	if errs[0].Detail != "nft: syntax error" || errs[0].Hint != hints["hint.fw_install_tools"] {
		t.Fatalf("detail or hint not filed: %+v", errs[0])
	}
	// The codes travel with the entry, because the interface renders from
	// them rather than from the English above.
	if errs[0].Code != "fw.apply" || errs[0].HintCode != "hint.fw_install_tools" {
		t.Fatalf("translation keys not filed: %+v", errs[0])
	}
	if len(errs[0].Args) != 2 || errs[0].Args[0] != "nftables" ||
		errs[0].Args[1] != "kernel said no" {
		t.Fatalf("interpolated values not filed: %q", errs[0].Args)
	}
	if !strings.Contains(errs[0].Message, "kernel said no") {
		t.Fatalf("message lost the cause: %q", errs[0].Message)
	}
}

// An error from somewhere that knows nothing about steps still has to be
// recorded, just without the context. Silently dropping it would be the worst
// possible outcome for a logging change whose point is that nothing is silent.
func TestRecordFilesPlainErrors(t *testing.T) {
	e := &Engine{Log: newTestRing(50)}
	e.record(errors.New("something went wrong"))

	errs := e.Log.Errors(0)
	if len(errs) != 1 || errs[0].Step != model.StepNone {
		t.Fatalf("unexpected journal: %+v", errs)
	}
	if e.Log.LastError() == nil {
		t.Fatal("a plain error must still raise the banner")
	}
}

func TestRecordIgnoresNil(t *testing.T) {
	e := &Engine{Log: newTestRing(10)}
	e.record(nil)
	if len(e.Log.Errors(0)) != 0 {
		t.Fatal("nil must record nothing")
	}
}

func TestTunnelHintNamesTheRightPackage(t *testing.T) {
	s := &model.Settings{HevBin: "/usr/bin/hev-socks5-tunnel"}
	cases := []struct {
		err  string
		want string
	}{
		{"/dev/net/tun is missing: install kmod-tun", "kmod-tun"},
		{`exec: "hev-socks5-tunnel": executable file not found in $PATH`, "hev-socks5-tunnel"},
		{"device xwrt0 did not appear within 5s", "never created the device"},
	}
	for _, c := range cases {
		err := errors.New(c.err)
		got := tunnelHint(fail(model.StepTunnel, "tunnel.start", err), err, s)
		if !strings.Contains(got.hint, c.want) {
			t.Errorf("tunnelHint(%q) = %q, want it to mention %q", c.err, got.hint, c.want)
		}
		if got.hintCode == "" {
			t.Errorf("tunnelHint(%q) gave a hint with no code to translate it by", c.err)
		}
	}
	unknown := errors.New("some unrelated failure")
	if got := tunnelHint(fail(model.StepTunnel, "tunnel.start", unknown), unknown, s); got.hint != "" {
		t.Error("an unrecognised failure should get no hint rather than a wrong one")
	}
}

// The hint after "nothing passes through the tunnel" has one job: name the
// pinned certificate first. Everything else about a pin-mismatched connection
// looks healthy — the core starts, the port listens, the rules apply — so a
// hint that lists generic causes sends the reader everywhere but here.
func TestNoDataHintNamesThePinFirst(t *testing.T) {
	hintFor := func(p *model.Profile, g *model.Group) *stepError {
		return noDataHint(fail(model.StepCore, "core.no_data", errors.New("timeout")), p, g)
	}

	pinned := hintFor(&model.Profile{ID: "p1", PinnedCert: "aa"}, nil)
	if !strings.Contains(pinned.hint, "pins") || !strings.Contains(pinned.hint, "fetch-cert p1") {
		t.Errorf("a pinned profile's hint does not name the pin or the fix: %q", pinned.hint)
	}
	// The profile id travels as an argument, not baked into the sentence, or
	// the translated hint would name no profile at all.
	if pinned.hintCode != "hint.pinned_cert" || len(pinned.hintArgs) != 1 ||
		pinned.hintArgs[0] != "p1" {
		t.Errorf("the pin hint does not carry the profile id for translation: %+v", pinned)
	}

	if h := hintFor(&model.Profile{ID: "p2"}, nil); strings.Contains(h.hint, "pins") {
		t.Errorf("a profile with no pin should not be told about pins: %q", h.hint)
	}

	if h := hintFor(nil, &model.Group{ID: "g1"}); !strings.Contains(h.hint, "member") {
		t.Errorf("a group's hint should talk about its members: %q", h.hint)
	}
}

// A step that only forwards someone else's failure must forward its name too.
// Without this, the DNS step's message translates to "%s" wrapped around an
// English sentence — which is exactly the half-translated error box the whole
// scheme exists to avoid.
func TestATaggedCauseKeepsItsName(t *testing.T) {
	inner := fault.Tagf("dns.core_not_answering", []any{15353, errors.New("timeout")},
		"the core is not answering DNS on port %d: %w", 15353, errors.New("timeout"))

	se := fail(model.StepDNS, "dns.apply", inner)

	if se.code != "dns.core_not_answering" {
		t.Fatalf("the cause's name was dropped: %q", se.code)
	}
	if len(se.args) != 2 || se.args[0] != "15353" {
		t.Fatalf("the cause's values were dropped: %q", se.args)
	}
	// And the English is still the cause's own sentence, unchanged.
	if !strings.Contains(se.Error(), "not answering DNS on port 15353") {
		t.Fatalf("the English message changed: %q", se.Error())
	}
	// errors.Is has to keep working through both wrappings.
	if !errors.Is(se, inner) {
		t.Fatal("the cause is no longer matchable through the step error")
	}
}

// The reverse: a step with something of its own to say keeps its own name,
// even when the cause happens to carry one.
func TestAStepWithItsOwnSentenceKeepsIt(t *testing.T) {
	inner := fault.Tagf("fw.none_found", nil, "no supported firewall found")
	se := fail(model.StepFirewall, "fw.apply", "nftables", inner)
	if se.code != "fw.apply" {
		t.Fatalf("the step's own name was overwritten by its cause: %q", se.code)
	}
}
