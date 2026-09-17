package model

import "testing"

// Share links commonly advertise h3 next to h2 regardless of transport. Over
// TCP that offers a protocol the connection cannot speak; a server that selects
// it leaves both ends waiting.
func TestALPNDropsH3OnNonQUICTransports(t *testing.T) {
	p := Profile{Network: "tcp", ALPN: "h2,http/1.1,h3"}
	got := p.ALPNList()
	for _, a := range got {
		if a == "h3" {
			t.Fatalf("h3 must not be offered over tcp: %v", got)
		}
	}
	if len(got) != 2 || got[0] != "h2" || got[1] != "http/1.1" {
		t.Errorf("the rest of the list must survive in order, got %v", got)
	}

	// Over QUIC it is the whole point.
	q := Profile{Network: "quic", ALPN: "h3"}
	if got := q.ALPNList(); len(got) != 1 || got[0] != "h3" {
		t.Errorf("quic must keep h3, got %v", got)
	}

	// An unset transport says nothing about what the link can speak, so the
	// operator's list is passed through untouched.
	u := Profile{ALPN: "h3"}
	if got := u.ALPNList(); len(got) != 1 || got[0] != "h3" {
		t.Errorf("an unknown transport must not be second-guessed, got %v", got)
	}
}

// A boolean the operator turned off must stay off. Normalize fills in empty
// values, and every false is "empty" for a bool — so a default applied there
// would turn the switch back on at every load and the interface would offer a
// setting that cannot be set.
func TestNormalizeLeavesBooleansAlone(t *testing.T) {
	s := Settings{Mode: ModeMixed, AutoConnect: false, AllowLAN: false}
	s.Normalize()
	if s.AutoConnect {
		t.Error("auto_connect turned itself back on")
	}
	if s.AllowLAN {
		t.Error("allow_lan turned itself back on")
	}
}

// And with no configuration at all, connecting on startup is the default: a
// device that reboots should come back the way it was, and the line this is
// usually installed on has no internet except through the tunnel.
func TestFreshSettingsConnectOnStartup(t *testing.T) {
	if !Defaults().AutoConnect {
		t.Error("a fresh configuration would not reconnect after a reboot")
	}
}
