package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"xwrt/internal/certpin"
	"xwrt/internal/model"
	"xwrt/internal/ucicfg"
)

// Looking at the certificate instead of guessing about it.
//
// The failure a stale pin causes says nothing about certificates: the core
// starts, the port listens, and nothing comes back — which is also what a wrong
// password and a dead server look like. One handshake settles it.

func tlsEndpoint(t *testing.T) (host string, port int, pin string) {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(srv.Close)
	h, p, err := net.SplitHostPort(strings.TrimPrefix(srv.URL, "https://"))
	if err != nil {
		t.Fatal(err)
	}
	n, _ := strconv.Atoi(p)
	sum := sha256.Sum256(srv.Certificate().Raw)
	return h, n, hex.EncodeToString(sum[:])
}

func pinnedProfile(host string, port int, pin string) *model.Profile {
	return &model.Profile{
		ID: "p1", Name: "pinned", Proto: model.ProtoVLESS,
		UUID:    "b831381d-6324-4d53-ad4f-8cda48b30811",
		Address: host, Port: port, Network: "tcp", Security: "tls",
		PinnedCert: pin,
	}
}

// Ruling the pin out matters as much as confirming it: a pinned profile that
// carries no data is assumed to be a pin problem by everybody, and saying it is
// not is what stops the search starting in the wrong place.
func TestAnUnchangedCertificateIsReportedAsUnchanged(t *testing.T) {
	host, port, pin := tlsEndpoint(t)

	look := inspectPin(pinnedProfile(host, port, pin), 5*time.Second)

	if look == nil {
		t.Fatal("nothing was looked at")
	}
	if look.Changed {
		t.Fatal("the certificate on the wire was called different from itself")
	}
	if !strings.Contains(look.describe(), "not what is wrong") {
		t.Errorf("the record does not rule the pin out: %q", look.describe())
	}
}

func TestAChangedCertificateIsDescribedInFull(t *testing.T) {
	host, port, _ := tlsEndpoint(t)
	const stale = "0000000000000000000000000000000000000000000000000000000000000000"

	look := inspectPin(pinnedProfile(host, port, stale), 5*time.Second)
	if look == nil || !look.Changed {
		t.Fatal("a different certificate was not noticed")
	}

	// The record has to carry what decides the answer, because the person
	// reading it is reading a failure, not running a command.
	d := look.describe()
	for _, want := range []string{stale, look.Found.Pin, "issued by", "valid "} {
		if !strings.Contains(d, want) {
			t.Errorf("the record is missing %q:\n%s", want, d)
		}
	}
}

func TestNothingIsLookedAtWithoutAPin(t *testing.T) {
	host, port, _ := tlsEndpoint(t)
	p := pinnedProfile(host, port, "")
	if inspectPin(p, 5*time.Second) != nil {
		t.Error("looked at a profile that pins nothing")
	}
}

func TestNothingIsLookedAtWithoutTLS(t *testing.T) {
	host, port, pin := tlsEndpoint(t)
	p := pinnedProfile(host, port, pin)
	p.Security = "none"
	if inspectPin(p, 5*time.Second) != nil {
		t.Error("looked for a certificate on a connection that has none")
	}
}

// A handshake that fails is its own answer and belongs to the failure already
// being reported. Replacing that failure with "could not read the certificate"
// would bury the real one.
func TestAServerThatCannotBeReachedIsNotAnAnswer(t *testing.T) {
	p := pinnedProfile("127.0.0.1", 1, "0000")
	if inspectPin(p, 200*time.Millisecond) != nil {
		t.Error("an unreachable server produced a finding")
	}
}

// --- accepting it -----------------------------------------------------------

func engineWithProfile(t *testing.T, p *model.Profile) (*Engine, *ucicfg.Store) {
	t.Helper()
	t.Setenv("XWRT_CONFDIR", t.TempDir())
	store := ucicfg.NewStore(ucicfg.New())
	if err := store.Save(&ucicfg.Data{
		Settings: model.Defaults(),
		Profiles: []model.Profile{*p},
	}); err != nil {
		t.Fatal(err)
	}
	log := NewLogRing(200)
	t.Cleanup(log.Close)
	return New(store, ucicfg.New(), log), store
}

func TestAcceptingANewPinStoresIt(t *testing.T) {
	host, port, real := tlsEndpoint(t)
	const stale = "0000000000000000000000000000000000000000000000000000000000000000"
	p := pinnedProfile(host, port, stale)
	p.PinAuto = true
	e, store := engineWithProfile(t, p)

	look := inspectPin(p, 5*time.Second)
	if look == nil || !look.Changed {
		t.Fatal("nothing to accept")
	}
	if err := e.acceptNewPin(p, look); err != nil {
		t.Fatal(err)
	}

	// The store, not the object: the engine reloads from it on every connect,
	// so a pin updated only in memory is a pin the next attempt will not use.
	d, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := d.Profile("p1").PinnedCert; got != real {
		t.Errorf("stored pin = %q, want the certificate on the wire %q", got, real)
	}
	if p.PinnedCert != real {
		t.Errorf("the profile in hand was not updated: %q", p.PinnedCert)
	}
}

// A device that changes what it trusts and keeps no record of having done so is
// a device nobody can audit afterwards. Both fingerprints and the issuer go in
// the log, at warning level, because this is the daemon trusting something it
// did not check.
func TestAcceptingANewPinSaysSoInTheLog(t *testing.T) {
	host, port, _ := tlsEndpoint(t)
	const stale = "0000000000000000000000000000000000000000000000000000000000000000"
	p := pinnedProfile(host, port, stale)
	p.PinAuto = true
	e, _ := engineWithProfile(t, p)

	look := inspectPin(p, 5*time.Second)
	if err := e.acceptNewPin(p, look); err != nil {
		t.Fatal(err)
	}

	var line string
	var level model.Level
	for _, entry := range e.Log.Entries(Query{Limit: 50}) {
		if strings.Contains(entry.Message, "accepted without being checked") {
			line, level = entry.Message, entry.Level
		}
	}
	if line == "" {
		t.Fatal("nothing in the log says the certificate was accepted unchecked")
	}
	if level != model.LevelWarn {
		t.Errorf("recorded at %v; this is the daemon trusting something it did "+
			"not check and an info line is not what that deserves", level)
	}
	if !strings.Contains(line, stale[:16]) || !strings.Contains(line, look.Found.Pin[:16]) {
		t.Errorf("the record does not carry both fingerprints: %s", line)
	}
	if !strings.Contains(line, "does not authenticate its server") {
		t.Errorf("the record does not say what this profile gives up: %s", line)
	}
}

func TestAnUnchangedCertificateIsNotAccepted(t *testing.T) {
	host, port, real := tlsEndpoint(t)
	p := pinnedProfile(host, port, real)
	p.PinAuto = true
	e, _ := engineWithProfile(t, p)

	look := inspectPin(p, 5*time.Second)
	if err := e.acceptNewPin(p, look); err != nil {
		t.Fatal(err)
	}
	for _, entry := range e.Log.Entries(Query{Limit: 50}) {
		if strings.Contains(entry.Message, "accepted without being checked") {
			t.Error("a certificate that never changed was recorded as accepted")
		}
	}
}

// --- the decision -----------------------------------------------------------
//
// Three conditions and all three are load-bearing. This is the whole safety of
// the feature, so it is tested where it can be tested rather than only where it
// runs.

func lookChanged(changed bool) *pinLook {
	return &pinLook{
		Changed: changed, Previous: "old",
		Found: &certpinResultStub, Nature: stubNature,
	}
}

func TestTheFindingIsAlwaysRecorded(t *testing.T) {
	p := pinnedProfile("87.121.104.212", 443, "old")
	for _, changed := range []bool{true, false} {
		out := pinOutcomeFor(p, lookChanged(changed))
		if out.Detail == "" {
			t.Errorf("changed=%v: the certificate was looked at and the finding "+
				"thrown away, so the failure record says no more than before", changed)
		}
	}
}

func TestNothingIsAcceptedUnlessItChanged(t *testing.T) {
	p := pinnedProfile("87.121.104.212", 443, "old")
	p.PinAuto = true
	if pinOutcomeFor(p, lookChanged(false)).Accept {
		t.Error("accepted a certificate that had not changed")
	}
}

func TestNothingIsAcceptedUnlessAskedFor(t *testing.T) {
	p := pinnedProfile("87.121.104.212", 443, "old")
	if pinOutcomeFor(p, lookChanged(true)).Accept {
		t.Error("accepted a changed certificate on a profile that never asked")
	}
}

// The rule the operator asked for: only where there is nothing to verify. A
// profile that sends a real SNI can have its certificate checked properly, and
// accepting one unseen there turns off a check that works.
func TestNothingIsAcceptedOnAProfileThatCouldVerifyProperly(t *testing.T) {
	p := pinnedProfile("87.121.104.212", 443, "old")
	p.SNI = "vpn.example.com"
	p.PinAuto = true

	if pinOutcomeFor(p, lookChanged(true)).Accept {
		t.Error("accepted a changed certificate on a profile that verifies a " +
			"real name, which throws away a check that was available")
	}
	// And the finding is still recorded, because the operator still has to
	// hear about it.
	if pinOutcomeFor(p, lookChanged(true)).Detail == "" {
		t.Error("and said nothing about it either")
	}
}

func TestItIsAcceptedWhereAllThreeHold(t *testing.T) {
	p := pinnedProfile("87.121.104.212", 443, "old")
	p.PinAuto = true
	if !pinOutcomeFor(p, lookChanged(true)).Accept {
		t.Error("refused on a profile that asked for it and has nothing to verify")
	}
}

func TestNoLookMeansNoOpinion(t *testing.T) {
	p := pinnedProfile("87.121.104.212", 443, "old")
	p.PinAuto = true
	out := pinOutcomeFor(p, nil)
	if out.Accept || out.Detail != "" {
		t.Errorf("invented an outcome with nothing to go on: %+v", out)
	}
}

var certpinResultStub = certpin.Result{
	Pin:      "1111111111111111111111111111111111111111111111111111111111111111",
	Endpoint: "87.121.104.212:443",
	Chain: []certpin.CertInfo{{
		Subject: "CN=87.121.104.212", Issuer: "CN=87.121.104.212",
		NotBefore: "2026-09-27T22:00:00Z", NotAfter: "2027-09-27T22:00:00Z",
	}},
}

var stubNature = certpin.Nature{SelfSigned: true}
