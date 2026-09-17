package api

import (
	"testing"

	"xwrt/internal/model"
)

// Every reconnect drops every connection on the network for a second or two.
// That is the right price for a change the connection would notice — a port, a
// mode, an exempt network — and no price at all is right for one it would not.
// auto_connect is read once, at boot; applying it has nothing to apply.
func TestOnlyChangesTheConnectionNoticesCauseAReconnect(t *testing.T) {
	base := model.Settings{Mode: model.ModeMixed, SocksPort: 10808,
		DNSMode: model.DNSDnsmasq, AutoConnect: false}

	same := base
	same.AutoConnect = true
	if affectsTheConnection(base, same) {
		t.Error("changing auto_connect reconnected, dropping the LAN to apply " +
			"a setting that is not read until the next boot")
	}

	if affectsTheConnection(base, base) {
		t.Error("saving the same settings reconnected")
	}

	for name, changed := range map[string]func(*model.Settings){
		"mode":       func(s *model.Settings) { s.Mode = model.ModeTUN },
		"socks_port": func(s *model.Settings) { s.SocksPort = 1080 },
		"dns_mode":   func(s *model.Settings) { s.DNSMode = model.DNSOff },
		"bypass_ip":  func(s *model.Settings) { s.BypassIP = []string{"10.0.0.0/8"} },
		"bypass_mac": func(s *model.Settings) { s.BypassMAC = []string{"aa:bb:cc:dd:ee:ff"} },
	} {
		after := base
		changed(&after)
		if !affectsTheConnection(base, after) {
			t.Errorf("changing %s did not reconnect, so it would not take "+
				"effect until something else did", name)
		}
	}
}
