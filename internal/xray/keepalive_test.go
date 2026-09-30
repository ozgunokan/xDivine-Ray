package xray

import (
	"encoding/json"
	"strings"
	"testing"

	"xwrt/internal/model"
)

// The tunnel's own connections have to look alive to everything between here
// and the server, or something on the line forgets them and the phone riding
// inside one finds out half an hour later. These check that the pings and
// probes reach the configuration the core actually reads, on the transports
// they can be on, and nowhere they would do harm.

func keepAliveProfile(network string) *model.Profile {
	return &model.Profile{
		ID: "p1", Proto: model.ProtoVLESS, Address: "203.0.113.10", Port: 443,
		UUID:    "b831381d-6324-4d53-ad4f-8cda48b30811",
		Network: network, Security: "tls", SNI: "vpn.example.com",
	}
}

func buildWith(t *testing.T, p *model.Profile, keepalive int) *Config {
	t.Helper()
	s := model.Defaults()
	s.KeepAlive = keepalive
	cfg, err := Build(Options{Profile: p, Settings: &s, Caps: AllFeatures()})
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func proxyStream(t *testing.T, cfg *Config) *StreamSettings {
	t.Helper()
	for i := range cfg.Outbounds {
		if cfg.Outbounds[i].Tag == TagProxy {
			return cfg.Outbounds[i].StreamSettings
		}
	}
	t.Fatalf("no proxy outbound")
	return nil
}

// --- what everyone gets without touching anything --------------------------

// Every install upgrading from an earlier version has no keepalive option in
// its file at all. That has to come out as on, not as zero — zero here is the
// feature silently doing nothing, which is how the last three weeks went.
func TestAnOlderConfigurationGetsTheDefault(t *testing.T) {
	var s model.Settings
	s.Normalize()
	if s.KeepAlive != model.Defaults().KeepAlive || s.KeepAlive <= 0 {
		t.Fatalf("an unset keepalive normalised to %d", s.KeepAlive)
	}
}

func TestTheDefaultIsHalfAMinute(t *testing.T) {
	if got := model.Defaults().KeepAlive; got != 30 {
		t.Fatalf("want 30, got %d", got)
	}
}

// --- WebSocket gets the ping ------------------------------------------------

func TestAWebSocketProfileSendsPings(t *testing.T) {
	ss := proxyStream(t, buildWith(t, keepAliveProfile("ws"), 30))
	if ss.WSSettings == nil || ss.WSSettings.HeartbeatPeriod != 30 {
		t.Fatalf("no WebSocket heartbeat: %+v", ss.WSSettings)
	}
	// And under the name the core reads, since a misspelt key is ignored
	// without a word.
	b, _ := json.Marshal(ss)
	if !strings.Contains(string(b), `"heartbeatPeriod":30`) {
		t.Fatalf("heartbeatPeriod is not in the JSON the core reads: %s", b)
	}
}

// --- every TCP socket gets the probe ----------------------------------------

// A plain TCP transport has nowhere to put a ping: the stream is the
// application's own bytes end to end. The keepalive probe is the one thing it
// can have, and it gets it.
func TestAPlainTCPProfileGetsKeepaliveProbes(t *testing.T) {
	ss := proxyStream(t, buildWith(t, keepAliveProfile("tcp"), 30))
	if ss.Sockopt == nil ||
		ss.Sockopt.TCPKeepAliveIdle != 30 || ss.Sockopt.TCPKeepAliveInterval != 30 {
		t.Fatalf("no TCP keepalive on a plain TCP profile: %+v", ss.Sockopt)
	}
	b, _ := json.Marshal(ss.Sockopt)
	for _, k := range []string{`"tcpKeepAliveIdle":30`, `"tcpKeepAliveInterval":30`} {
		if !strings.Contains(string(b), k) {
			t.Fatalf("%s is not in the JSON the core reads: %s", k, b)
		}
	}
	if ss.WSSettings != nil {
		t.Fatalf("a TCP profile grew WebSocket settings")
	}
}

func TestWebSocketGetsTheProbeAsWellAsThePing(t *testing.T) {
	ss := proxyStream(t, buildWith(t, keepAliveProfile("ws"), 30))
	if ss.Sockopt == nil || ss.Sockopt.TCPKeepAliveIdle != 30 {
		t.Fatalf("the socket under a WebSocket has no keepalive: %+v", ss.Sockopt)
	}
}

// The routing mark shares the sockopt block. Setting keepalive must not lose
// it: without the mark the core's own connections are sent back into the
// tunnel, and the tunnel swallows itself.
func TestTheRoutingMarkSurvives(t *testing.T) {
	ss := proxyStream(t, buildWith(t, keepAliveProfile("tcp"), 30))
	if ss.Sockopt.Mark == 0 {
		t.Fatalf("setting keepalive lost the routing mark")
	}
}

// --- and nowhere it would do harm -------------------------------------------

// A gRPC server closes the connection of a client that pings more often than
// its enforcement policy allows, five minutes by default. Pinging every thirty
// seconds would have it hang up every thirty seconds. The TCP probe is below
// that layer and cannot trip it.
func TestGRPCGetsTheProbeButNotThePing(t *testing.T) {
	p := keepAliveProfile("grpc")
	p.ServiceName = "svc"
	ss := proxyStream(t, buildWith(t, p, 30))

	b, _ := json.Marshal(ss)
	if strings.Contains(string(b), "idle_timeout") || strings.Contains(string(b), "health_check") {
		t.Fatalf("a gRPC ping was switched on: %s", b)
	}
	if ss.Sockopt == nil || ss.Sockopt.TCPKeepAliveIdle != 30 {
		t.Fatalf("gRPC should still get the TCP probe: %+v", ss.Sockopt)
	}
}

// XHTTP pings by itself already, and may run over QUIC where a TCP socket
// option has nothing to apply to.
func TestXHTTPIsLeftToItsOwnPings(t *testing.T) {
	ss := proxyStream(t, buildWith(t, keepAliveProfile("xhttp"), 30))
	if ss.Sockopt != nil && ss.Sockopt.TCPKeepAliveIdle != 0 {
		t.Fatalf("XHTTP was given a TCP socket option: %+v", ss.Sockopt)
	}
}

// The outbounds that are not the tunnel — direct, block, DNS — are not kept
// alive. They are not what the phone's connection rides in.
func TestOnlyTheTunnelIsKeptAlive(t *testing.T) {
	cfg := buildWith(t, keepAliveProfile("tcp"), 30)
	for _, out := range cfg.Outbounds {
		if out.Tag == TagProxy || out.StreamSettings == nil || out.StreamSettings.Sockopt == nil {
			continue
		}
		if out.StreamSettings.Sockopt.TCPKeepAliveIdle != 0 {
			t.Fatalf("%s is not the tunnel and was given keepalive", out.Tag)
		}
	}
}

// Off means off: the core's own behaviour, nothing of ours written at all.
func TestTurnedOffWritesNothing(t *testing.T) {
	ss := proxyStream(t, buildWith(t, keepAliveProfile("ws"), -1))
	if ss.WSSettings.HeartbeatPeriod != 0 {
		t.Fatalf("a heartbeat was written with keepalive off")
	}
	if ss.Sockopt != nil && ss.Sockopt.TCPKeepAliveIdle != 0 {
		t.Fatalf("a TCP probe was written with keepalive off")
	}
}

// --- groups -----------------------------------------------------------------

// A group's members are each a tunnel of their own, and each one has to be
// kept alive — keeping only the first would protect whichever member the
// balancer happened not to pick.
func TestEveryMemberOfAGroupIsKeptAlive(t *testing.T) {
	a := *keepAliveProfile("ws")
	a.ID = "a"
	b := *keepAliveProfile("tcp")
	b.ID = "b"
	b.Address = "198.51.100.20"

	s := model.Defaults()
	cfg, err := Build(Options{
		Group:    &model.Group{ID: "g1", Name: "grup", Strategy: model.StrategyLeastPing, Members: []string{"a", "b"}},
		Members:  []model.Profile{a, b},
		Settings: &s,
		Caps:     AllFeatures(),
	})
	if err != nil {
		t.Fatal(err)
	}

	seen := 0
	for _, out := range cfg.Outbounds {
		if !keepAliveProtocols[out.Protocol] {
			continue
		}
		seen++
		ss := out.StreamSettings
		if ss.Sockopt == nil || ss.Sockopt.TCPKeepAliveIdle != s.KeepAlive {
			t.Fatalf("member %s has no keepalive: %+v", out.Tag, ss.Sockopt)
		}
		if ss.Network == "ws" && ss.WSSettings.HeartbeatPeriod != s.KeepAlive {
			t.Fatalf("WebSocket member %s has no heartbeat", out.Tag)
		}
	}
	if seen != 2 {
		t.Fatalf("want both members, saw %d", seen)
	}
}

// --- the bounds -------------------------------------------------------------

func TestTheSettingIsHeldToSaneBounds(t *testing.T) {
	cases := []struct{ in, want int }{
		{0, model.Defaults().KeepAlive}, // unset: the default
		{-5, -1},                        // any negative: off
		{3, model.KeepAliveMin},         // too eager
		{45, 45},                        // fine as it is
		{99999, model.KeepAliveMax},     // too lazy to protect anything
	}
	for _, c := range cases {
		s := model.Settings{KeepAlive: c.in}
		s.Normalize()
		if s.KeepAlive != c.want {
			t.Errorf("keepalive %d normalised to %d, want %d", c.in, s.KeepAlive, c.want)
		}
	}
}
