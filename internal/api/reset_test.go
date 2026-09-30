package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"xwrt/internal/daemon"
	"xwrt/internal/model"
	"xwrt/internal/ucicfg"
)

// The factory reset, end to end through the handler the interface calls.

func populatedServer(t *testing.T) (*Server, *ucicfg.Store, *daemon.LogRing) {
	t.Helper()
	t.Setenv("XWRT_CONFDIR", t.TempDir())
	store := ucicfg.NewStore(ucicfg.New())

	s := model.Defaults()
	s.Mode = model.ModeTUN
	s.DNSMode = model.DNSRedirect
	s.ConnIdle = 600
	s.KeepAlive = 45
	s.LANDevice = "br-lan"
	data := &ucicfg.Data{
		Settings: s,
		Profiles: []model.Profile{{
			ID: "p1", Name: "okan", Proto: model.ProtoVLESS, Address: "203.0.113.10",
			Port: 443, UUID: "b831381d-6324-4d53-ad4f-8cda48b30811", Network: "ws",
			Security: "tls",
		}},
		Groups: []model.Group{{ID: "g1", Name: "grup", Strategy: model.StrategyLeastPing,
			Members: []string{"p1"}}},
		Rules: []model.Rule{{ID: "r1", Name: "direkt", Enabled: true, Action: model.ActionDirect,
			Domains: []string{"example.com"}}},
	}
	if err := store.Save(data); err != nil {
		t.Fatal(err)
	}
	log := daemon.NewLogRing(50)
	t.Cleanup(log.Close)
	return New(daemon.New(store, ucicfg.New(), log), store, log, 0), store, log
}

func TestAResetLeavesExactlyAFreshInstall(t *testing.T) {
	srv, store, _ := populatedServer(t)
	before, _ := store.Load()
	if len(before.Profiles) == 0 {
		t.Fatalf("the test did not start with anything to remove")
	}

	w := httptest.NewRecorder()
	srv.resetConfig(w, httptest.NewRequest(http.MethodPost, "/api/config/reset", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("reset answered %d: %s", w.Code, w.Body.String())
	}

	after, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if n := len(after.Profiles) + len(after.Groups) + len(after.Rules) + len(after.Subscriptions); n != 0 {
		t.Fatalf("%d item(s) survived the reset", n)
	}
	if !reflect.DeepEqual(after.Settings, ucicfg.Factory().Settings) {
		t.Fatalf("settings are not the factory ones:\n got  %+v\n want %+v",
			after.Settings, ucicfg.Factory().Settings)
	}
	// Specifically the ones that were changed, since those are the ones a
	// half-done reset would leave behind.
	if after.Settings.Mode != model.Defaults().Mode || after.Settings.LANDevice != "" ||
		after.Settings.KeepAlive != model.Defaults().KeepAlive {
		t.Fatalf("a changed setting survived: %+v", after.Settings)
	}
	// Disconnected, and marked so: a device that comes back from a power cut
	// must not try to reconnect to a server that no longer exists.
	if after.Settings.Enabled || after.Settings.Active != "" {
		t.Fatalf("still marked as connected after a reset: enabled=%v active=%q",
			after.Settings.Enabled, after.Settings.Active)
	}
}

// The answer carries what the device now has, read back from the store, so the
// page can show the empty state it produced rather than assume it.
func TestTheAnswerIsTheEmptyConfiguration(t *testing.T) {
	srv, _, _ := populatedServer(t)
	w := httptest.NewRecorder()
	srv.resetConfig(w, httptest.NewRequest(http.MethodPost, "/api/config/reset", nil))

	var got struct {
		Reset  bool        `json:"reset"`
		Config ucicfg.Data `json:"config"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("%v: %s", err, w.Body.String())
	}
	if !got.Reset || len(got.Config.Profiles) != 0 {
		t.Fatalf("the answer does not describe a reset device: %s", w.Body.String())
	}
}

// A failure banner about a server that no longer exists is a riddle, not help.
func TestAResetClearsTheOldFailure(t *testing.T) {
	srv, _, log := populatedServer(t)
	log.Fail(daemon.Fault{Source: model.SourceDaemon, Step: model.StepCore,
		Err: errors.New("core exited")})
	if log.LastError() == nil {
		t.Fatalf("the test could not file a failure")
	}

	w := httptest.NewRecorder()
	srv.resetConfig(w, httptest.NewRequest(http.MethodPost, "/api/config/reset", nil))
	if log.LastError() != nil {
		t.Fatalf("a failure from the old configuration survived the reset")
	}
}
