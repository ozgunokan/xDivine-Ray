package api

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"xwrt/internal/daemon"
	"xwrt/internal/model"
	"xwrt/internal/ucicfg"
)

// Replacing a pin that is already there.
//
// The situation this happens in is the whole reason it is guarded. A pin stops
// matching, every connection is refused, and the obvious move is to fetch the
// certificate again — which, if something on the path is what changed, pins
// that instead and brings the tunnel back up through it looking healthy. The
// first pin is taken from a server nobody has reason to doubt; the second is
// taken at the one moment there is a reason.

// pinServer starts a TLS listener with a self-signed certificate and returns a
// store holding one profile pointed at it.
func pinServer(t *testing.T, existingPin string) (*Server, string) {
	t.Helper()
	tls := httptest.NewTLSServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(tls.Close)

	host, portStr, err := net.SplitHostPort(strings.TrimPrefix(tls.URL, "https://"))
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(portStr)

	t.Setenv("XWRT_CONFDIR", t.TempDir())
	store := ucicfg.NewStore(ucicfg.New())
	if err := store.Save(&ucicfg.Data{
		Settings: model.Defaults(),
		Profiles: []model.Profile{{
			ID: "p1", Name: "pinned", Proto: model.ProtoVLESS,
			Address: host, Port: port, Network: "tcp", Security: "tls",
			UUID:       "b831381d-6324-4d53-ad4f-8cda48b30811",
			PinnedCert: existingPin,
		}},
	}); err != nil {
		t.Fatal(err)
	}

	log := daemon.NewLogRing(100)
	t.Cleanup(log.Close)
	return New(daemon.New(store, ucicfg.New(), log), store, log, 0), "p1"
}

// The handler is called directly rather than through a mux: what is being
// tested is the decision it makes, and routing it there as well would only test
// the standard library.
func fetchCertCall(t *testing.T, s *Server, id, query string) (int, map[string]any) {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/profiles/"+id+"/fetch-cert"+query, nil)
	r.SetPathValue("id", id)
	s.fetchCert(w, r)
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	return w.Code, body
}

func storedPin(t *testing.T, s *Server, id string) string {
	t.Helper()
	d, err := s.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	p := d.Profile(id)
	if p == nil {
		t.Fatalf("profile %s is gone", id)
	}
	return p.PinnedCert
}

// The first pin is taken without ceremony: there is nothing to overwrite.
func TestTheFirstPinIsTakenStraightAway(t *testing.T) {
	s, id := pinServer(t, "")

	code, body := fetchCertCall(t, s, id, "")
	if code != http.StatusOK {
		t.Fatalf("HTTP %d taking the first pin: %v", code, body)
	}
	pin, _ := body["pin"].(string)
	if pin == "" || storedPin(t, s, id) != pin {
		t.Errorf("the pin was not stored: returned %q, stored %q",
			pin, storedPin(t, s, id))
	}
}

func TestADifferentCertificateIsNotPinnedOverTheOldOneUnasked(t *testing.T) {
	const stale = "0000000000000000000000000000000000000000000000000000000000000000"
	s, id := pinServer(t, stale)

	code, body := fetchCertCall(t, s, id, "")

	if code != http.StatusConflict {
		t.Fatalf("HTTP %d, want 409: a different certificate was accepted without "+
			"being asked twice", code)
	}
	// The assertion that matters is not the status but the stored value: a 409
	// that had already written the new pin would be theatre.
	if got := storedPin(t, s, id); got != stale {
		t.Errorf("the pin was replaced anyway: %q", got)
	}
	// And the refusal carries what it found, because that certificate is the
	// whole reason it refused and somebody has to look at it.
	found, _ := body["found"].(map[string]any)
	chain, _ := found["chain"].([]any)
	if len(chain) == 0 {
		t.Fatalf("the refusal does not include the certificate it found: %v", body)
	}
	first, _ := chain[0].(map[string]any)
	for _, k := range []string{"issuer", "not_before", "not_after", "sha256"} {
		if first[k] == nil || first[k] == "" {
			t.Errorf("the certificate is missing %s, which is what decides the answer", k)
		}
	}
	if body["current_pin"] != stale {
		t.Errorf("the refusal does not say what is pinned now: %v", body["current_pin"])
	}
	if body["needs_replace"] != true {
		t.Error("the refusal does not say that asking again is what unblocks it")
	}
}

func TestAskingAgainReplacesIt(t *testing.T) {
	const stale = "0000000000000000000000000000000000000000000000000000000000000000"
	s, id := pinServer(t, stale)

	code, body := fetchCertCall(t, s, id, "?replace=1")
	if code != http.StatusOK {
		t.Fatalf("HTTP %d with replace=1: %v", code, body)
	}
	pin, _ := body["pin"].(string)
	if pin == stale || pin == "" {
		t.Fatalf("no new pin came back: %q", pin)
	}
	if got := storedPin(t, s, id); got != pin {
		t.Errorf("stored pin is %q, want the one just fetched %q", got, pin)
	}
	if body["replaced"] != true {
		t.Error("the answer does not say that something was replaced")
	}
}

// Fetching the same certificate again is not a replacement and must not need
// asking twice: it is what somebody does to check, and turning a no-op into a
// scary question teaches them to pass -replace without reading.
func TestRefetchingTheSameCertificateIsNotAConflict(t *testing.T) {
	s, id := pinServer(t, "")
	if code, body := fetchCertCall(t, s, id, ""); code != http.StatusOK {
		t.Fatalf("HTTP %d on the first pin: %v", code, body)
	}
	first := storedPin(t, s, id)

	code, body := fetchCertCall(t, s, id, "")
	if code != http.StatusOK {
		t.Fatalf("HTTP %d re-fetching the certificate already pinned: %v", code, body)
	}
	if got := storedPin(t, s, id); got != first {
		t.Errorf("the pin changed on a re-fetch: %q then %q", first, got)
	}
	if body["replaced"] == true {
		t.Error("a re-fetch of the same certificate was reported as a replacement")
	}
}
