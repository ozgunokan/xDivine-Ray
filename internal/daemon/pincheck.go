package daemon

import (
	"fmt"
	"time"

	"xwrt/internal/certpin"
	"xwrt/internal/model"
	"xwrt/internal/ucicfg"
)

// When a pinned certificate stops matching.
//
// The failure it causes says nothing about certificates. The core starts, the
// port listens, the rules apply, and no byte ever comes back — which is also
// what a wrong password, an expired account and a dead server look like. Until
// now the daemon guessed from the fact that a pin was set and printed a
// sentence telling the operator to go and look.
//
// It can look itself. The certificate is one handshake away, on the same path
// the connection would have used, and reading it settles the question rather
// than raising it: if the certificate on the wire is the one pinned, the pin is
// not the problem and saying so saves an hour. If it is not, then everything
// worth knowing — who issued it, how long it lives, whether anybody vouches for
// it — is in the answer, and it goes into the failure record where somebody
// will actually meet it.
//
// Doing that costs one TLS handshake on a connection that has already failed.

// pinLook is what a look found.
type pinLook struct {
	// Changed is the fact everything else hangs on.
	Changed  bool
	Previous string
	Found    *certpin.Result
	Nature   certpin.Nature
}

// inspectPin reads the certificate the server is presenting now.
//
// It returns nil when there is nothing to compare — no pin, no TLS, or the
// handshake itself failed, which is its own answer and belongs to the failure
// that is already being reported rather than replacing it.
func inspectPin(p *model.Profile, timeout time.Duration) *pinLook {
	if p == nil || p.PinnedCert == "" || p.Security != "tls" {
		return nil
	}
	res, err := certpin.Fetch(p.Address, p.Port, p.SNI, timeout)
	if err != nil || res == nil {
		return nil
	}
	return &pinLook{
		Changed:  res.Pin != p.PinnedCert,
		Previous: p.PinnedCert,
		Found:    res,
		Nature:   res.Describe(time.Now()),
	}
}

// describe writes what was found in the form a failure record carries.
func (l *pinLook) describe() string {
	if l == nil {
		return ""
	}
	if !l.Changed {
		// Worth saying out loud. A pinned profile that fails to pass data is
		// assumed to be a pin problem by everyone including this daemon, and
		// ruling it out is what stops the search starting in the wrong place.
		return "the certificate this server is presenting is the one pinned, " +
			"so the pin is not what is wrong here."
	}
	out := "the certificate changed.\n" +
		"  pinned:    " + l.Previous + "\n" +
		"  presented: " + l.Found.Pin + "\n"
	for i, c := range l.Found.Chain {
		label := "  leaf: "
		if i > 0 {
			label = "  ca:   "
		}
		out += label + c.Subject + "\n" +
			"        issued by " + c.Issuer + "\n" +
			"        valid " + c.NotBefore + " to " + c.NotAfter + "\n"
	}
	if l.Nature.Note != "" {
		out += "\n" + l.Nature.Note
	}
	return out
}

// acceptNewPin replaces the stored pin with what the server is presenting.
//
// Only ever called for a profile that asked for this and is allowed to: an
// endpoint with no name behind it, where no certificate could have been
// verified in the first place. It is still recorded in full — both fingerprints
// and the issuer — because a device that silently changes what it trusts, and
// keeps no record of having done so, is a device nobody can ever audit.
func (e *Engine) acceptNewPin(p *model.Profile, l *pinLook) error {
	if l == nil || !l.Changed || p == nil {
		return nil
	}
	if _, err := e.store.Update(func(d *ucicfg.Data) error {
		target := d.Profile(p.ID)
		if target == nil {
			return fmt.Errorf("profile %q is gone", p.ID)
		}
		target.PinnedCert = l.Found.Pin
		return nil
	}); err != nil {
		return err
	}
	p.PinnedCert = l.Found.Pin

	issuer := ""
	if len(l.Found.Chain) > 0 {
		issuer = l.Found.Chain[0].Issuer
	}
	// A warning, not an info line. This is the daemon deciding to trust
	// something it has not checked, because it was told to; the log is the only
	// place that ever says so.
	e.Log.Step(model.StepCore).Warnf(
		"%s presented a different certificate and it was accepted without being "+
			"checked, because this profile is set to accept changes: %s → %s, "+
			"issued by %s. This profile does not authenticate its server",
		p.Label(), short(l.Previous), short(l.Found.Pin), issuer)
	return nil
}

func short(pin string) string {
	if len(pin) <= 16 {
		return pin
	}
	return pin[:16] + "…"
}

// pinOutcome is the decision a look leads to.
//
// Separated from the connect path deliberately. What is left in the engine is
// three lines of wiring; everything that has a judgement in it — whether to
// record, whether to accept, what to say — is here, where it can be tested
// without a running core. The eligibility rule in particular is the whole
// safety of this feature, and a rule that can only be exercised by connecting
// to a real server is a rule nobody exercises.
type pinOutcome struct {
	// Detail goes into the failure record, so somebody reading a failure reads
	// the certificate too.
	Detail string
	// Accept means the new pin has been stored and the connection should be
	// built again from it.
	Accept bool
}

func pinOutcomeFor(p *model.Profile, l *pinLook) pinOutcome {
	if l == nil {
		return pinOutcome{}
	}
	out := pinOutcome{Detail: l.describe()}
	// Three conditions and all three are load-bearing. Changed, or there is
	// nothing to accept. PinAuto, or nobody asked for this. CanAutoPin, or the
	// profile could have verified the certificate properly and accepting it
	// unseen would throw away a check that works.
	out.Accept = l.Changed && p != nil && p.PinAuto && p.CanAutoPin()
	return out
}
