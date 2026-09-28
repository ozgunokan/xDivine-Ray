package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"xwrt/internal/daemon"
	"xwrt/internal/model"
	"xwrt/internal/ucicfg"
)

// Every reply that carries a status carries the same one.
//
// /api/connect and /api/disconnect answer with a status too, and they used to
// answer with the engine's raw one. auto_connect and the update notice are
// filled in from the stored configuration, which the engine does not read — so
// a page drawn from the connect reply showed auto_connect as off on a device
// where it was on. On a line whose only route out is the tunnel, "does this
// come back by itself after a power cut" is not a detail to get wrong in half
// the replies.

func statusServer(t *testing.T, autoConnect bool) *Server {
	t.Helper()
	t.Setenv("XWRT_CONFDIR", t.TempDir())
	store := ucicfg.NewStore(ucicfg.New())
	s := model.Defaults()
	s.AutoConnect = autoConnect
	if err := store.Save(&ucicfg.Data{Settings: s}); err != nil {
		t.Fatal(err)
	}
	log := daemon.NewLogRing(50)
	t.Cleanup(log.Close)
	return New(daemon.New(store, ucicfg.New(), log), store, log, 0)
}

func decodeStatus(t *testing.T, body []byte) model.Status {
	t.Helper()
	var st model.Status
	if err := json.Unmarshal(body, &st); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	return st
}

func TestTheStatusPageReportsAutoConnectFromTheConfiguration(t *testing.T) {
	s := statusServer(t, true)
	w := httptest.NewRecorder()
	s.getStatus(w, httptest.NewRequest(http.MethodGet, "/api/status", nil))

	if !decodeStatus(t, w.Body.Bytes()).AutoConnect {
		t.Error("auto_connect is on in the configuration and off in the status")
	}
}

// The reply a client draws its page from right after acting has to say the same
// thing as the one it polls a second later.
func TestDisconnectingAnswersWithTheSameStatus(t *testing.T) {
	s := statusServer(t, true)

	w := httptest.NewRecorder()
	s.disconnect(w, httptest.NewRequest(http.MethodPost, "/api/disconnect", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("HTTP %d: %s", w.Code, w.Body.String())
	}

	if !decodeStatus(t, w.Body.Bytes()).AutoConnect {
		t.Error("the disconnect reply says auto_connect is off, so a page drawn " +
			"from it tells the owner this device will not come back after a " +
			"power cut — when it will")
	}

	// And it agrees with the page that polls.
	poll := httptest.NewRecorder()
	s.getStatus(poll, httptest.NewRequest(http.MethodGet, "/api/status", nil))
	if decodeStatus(t, w.Body.Bytes()).AutoConnect !=
		decodeStatus(t, poll.Body.Bytes()).AutoConnect {
		t.Error("the two replies disagree about the same device")
	}
}

// A connect that fails answers with an error, not a status, so what is checked
// here is the one path that does return one.
func TestAFailedConnectStillReportsTheError(t *testing.T) {
	s := statusServer(t, true)
	w := httptest.NewRecorder()
	s.connect(w, httptest.NewRequest(http.MethodPost, "/api/connect", nil))
	if w.Code == http.StatusOK {
		t.Error("connecting with nothing configured reported success")
	}
}
