package certpin

import (
	"strings"
	"testing"
	"time"
)

// Reading the certificate that came back.
//
// A pin that stops matching is the same message whatever caused it, and the
// causes want opposite responses: a server reissuing every twelve hours needs a
// different arrangement entirely, a routine renewal needs one click, and
// somebody else's certificate on the path needs neither. These are the readings
// that separate them.

func at(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func result(subject, issuer string, from, to time.Time, trusted bool) *Result {
	return &Result{
		Trusted: trusted,
		Chain: []CertInfo{{
			Subject: subject, Issuer: issuer,
			NotBefore: at(from), NotAfter: at(to),
		}},
	}
}

func TestAShortLivedCertificateIsNamedAsOne(t *testing.T) {
	now := time.Date(2026, 9, 28, 9, 0, 0, 0, time.UTC)
	// Caddy's internal authority issues twelve-hour leaves; a pin taken from
	// one is stale before lunch.
	r := result("CN=198.51.100.20", "CN=Local", now.Add(-2*time.Hour), now.Add(10*time.Hour), false)

	n := r.Describe(now)

	if n.LifetimeHours != 12 {
		t.Errorf("lifetime = %d hours, want 12", n.LifetimeHours)
	}
	if !strings.Contains(n.Note, "12 hours") {
		t.Errorf("the note does not say how short-lived it is: %q", n.Note)
	}
	// And it says what to do instead, because re-pinning is not a fix here.
	if !strings.Contains(n.Note, "name") {
		t.Errorf("the note does not point at the arrangement that works: %q", n.Note)
	}
}

func TestARoutineRenewalIsNamedAsOne(t *testing.T) {
	now := time.Date(2026, 9, 28, 9, 0, 0, 0, time.UTC)
	r := result("CN=vpn.example.com", "CN=R11,O=Let's Encrypt",
		now.Add(-9*time.Hour), now.Add(90*24*time.Hour), true)

	n := r.Describe(now)

	if n.SelfSigned {
		t.Error("a CA-issued certificate was called self-signed")
	}
	if !strings.Contains(n.Note, "renewal") {
		t.Errorf("the note does not name it as a renewal: %q", n.Note)
	}
	// The useful part: a certificate that verifies on its own needs no pin.
	if !strings.Contains(n.Note, "does not need pinning") {
		t.Errorf("the note does not say the pin is unnecessary here: %q", n.Note)
	}
}

func TestARegeneratedSelfSignedCertificateIsNamedAsOne(t *testing.T) {
	now := time.Date(2026, 9, 28, 9, 0, 0, 0, time.UTC)
	r := result("CN=xui", "CN=xui", now.Add(-6*time.Hour), now.Add(365*24*time.Hour), false)

	n := r.Describe(now)

	if !n.SelfSigned {
		t.Error("a certificate that issued itself was not called self-signed")
	}
	if !strings.Contains(n.Note, "restart") {
		t.Errorf("the note does not name the cause: %q", n.Note)
	}
}

// The case a pin exists to catch. It must not be described as any of the
// innocent ones, and it must say to look before trusting.
func TestAnUnvouchedForeignCertificateIsNotExplainedAway(t *testing.T) {
	now := time.Date(2026, 9, 28, 9, 0, 0, 0, time.UTC)
	r := result("CN=198.51.100.20", "CN=Some Middlebox CA",
		now.Add(-400*time.Hour), now.Add(300*24*time.Hour), false)

	n := r.Describe(now)

	if n.SelfSigned {
		t.Error("a certificate issued by somebody else was called self-signed")
	}
	if !strings.Contains(n.Note, "look at the issuer") {
		t.Errorf("the note does not say to look at the issuer: %q", n.Note)
	}
	for _, innocent := range []string{"renewal", "restart"} {
		if strings.Contains(n.Note, innocent) {
			t.Errorf("an unvouched certificate was explained away as a %s: %q",
				innocent, n.Note)
		}
	}
}

// A long-standing, publicly trusted certificate that simply is not the one
// pinned says nothing in particular, and saying nothing is better than
// inventing a reason.
func TestNothingIsClaimedWhenNothingIsKnown(t *testing.T) {
	now := time.Date(2026, 9, 28, 9, 0, 0, 0, time.UTC)
	r := result("CN=vpn.example.com", "CN=R11,O=Let's Encrypt",
		now.Add(-40*24*time.Hour), now.Add(50*24*time.Hour), true)

	if n := r.Describe(now); n.Note != "" {
		t.Errorf("invented an explanation: %q", n.Note)
	}
}

func TestAnEmptyChainDoesNotPanic(t *testing.T) {
	if n := (&Result{}).Describe(time.Now()); n.Note != "" || n.LifetimeHours != 0 {
		t.Errorf("read something out of nothing: %+v", n)
	}
}
