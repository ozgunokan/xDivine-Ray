package xray

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"xwrt/internal/model"
)

func realityProfile() *model.Profile {
	return &model.Profile{
		ID:          "p1",
		Name:        "Test",
		Proto:       model.ProtoVLESS,
		Address:     "example.com",
		Port:        443,
		UUID:        "b831381d-6324-4d53-ad4f-8cda48b30811",
		Flow:        "xtls-rprx-vision",
		Network:     "tcp",
		Security:    "reality",
		SNI:         "www.microsoft.com",
		Fingerprint: "chrome",
		PublicKey:   "xdfA1s2WlnAGJdzTe4JBs0lPVcUcfWCNrTZUJ2Gq0Ck",
		ShortID:     "6ba85179",
	}
}

func settings() *model.Settings {
	s := model.Defaults()
	return &s
}

// decode renders the config and parses it back, which is the closest thing to
// checking the shape Xray will actually see.
func decode(t *testing.T, c *Config) map[string]any {
	t.Helper()
	raw, err := c.JSON()
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("generated config is not valid JSON: %v", err)
	}
	return out
}

func TestBuildTProxyMode(t *testing.T) {
	s := settings()
	s.Mode = model.ModeTProxy
	cfg, err := Build(Options{
		Profile:     realityProfile(),
		Settings:    s,
		DirectCIDRs: []string{"192.168.9.0/24"},
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	m := decode(t, cfg)

	inbounds, _ := m["inbounds"].([]any)
	tags := map[string]map[string]any{}
	for _, in := range inbounds {
		obj := in.(map[string]any)
		tags[obj["tag"].(string)] = obj
	}
	tp, ok := tags[TagTransparent]
	if !ok {
		t.Fatal("transparent inbound is missing")
	}
	stream := tp["streamSettings"].(map[string]any)
	sockopt := stream["sockopt"].(map[string]any)
	if sockopt["tproxy"] != "tproxy" {
		t.Errorf("tproxy sockopt = %v, want \"tproxy\"", sockopt["tproxy"])
	}
	if net := tp["settings"].(map[string]any)["network"]; net != "tcp,udp" {
		t.Errorf("network = %v, want tcp,udp when TPROXY is available", net)
	}

	// The extra direct network must reach the routing rules.
	routing := m["routing"].(map[string]any)
	rules := routing["rules"].([]any)
	found := false
	for _, r := range rules {
		obj := r.(map[string]any)
		ips, ok := obj["ip"].([]any)
		if !ok {
			continue
		}
		for _, ip := range ips {
			if ip == "192.168.9.0/24" {
				found = true
			}
		}
	}
	if !found {
		t.Error("configured direct CIDR is not in the routing rules")
	}
}

// TestTransparentInboundShapePerMode pins the rule that the inbound must match
// how packets actually arrive: a NAT-redirected socket must not carry the
// tproxy option, and a socket fed by TPROXY must.
func TestTransparentInboundShapePerMode(t *testing.T) {
	cases := []struct {
		mode        model.Mode
		wantInbound bool
		wantNetwork string
		wantTProxy  bool
	}{
		{model.ModeRedirect, true, "tcp", false},
		{model.ModeMixed, true, "tcp", false},
		{model.ModeTProxy, true, "tcp,udp", true},
		{model.ModeTUN, false, "", false},
	}

	for _, tc := range cases {
		s := settings()
		s.Mode = tc.mode
		cfg, err := Build(Options{Profile: realityProfile(), Settings: s})
		if err != nil {
			t.Fatalf("%s: Build: %v", tc.mode, err)
		}

		var in *Inbound
		for i := range cfg.Inbounds {
			if cfg.Inbounds[i].Tag == TagTransparent {
				in = &cfg.Inbounds[i]
			}
		}
		if !tc.wantInbound {
			if in != nil {
				t.Errorf("%s: transparent inbound present but not needed", tc.mode)
			}
			continue
		}
		if in == nil {
			t.Errorf("%s: transparent inbound missing", tc.mode)
			continue
		}
		if got := in.Settings.(map[string]any)["network"]; got != tc.wantNetwork {
			t.Errorf("%s: network = %v, want %v", tc.mode, got, tc.wantNetwork)
		}
		hasTProxy := in.StreamSettings != nil && in.StreamSettings.Sockopt != nil &&
			in.StreamSettings.Sockopt.TProxy != ""
		if hasTProxy != tc.wantTProxy {
			t.Errorf("%s: tproxy sockopt = %v, want %v", tc.mode, hasTProxy, tc.wantTProxy)
		}
	}
}

// TestMixedModeHasNoUDPInbound records why mixed mode needs no second inbound:
// its UDP arrives through the tunnel as SOCKS, not as a transparent flow.
func TestMixedModeHasNoUDPInbound(t *testing.T) {
	s := settings()
	s.Mode = model.ModeMixed
	cfg, err := Build(Options{Profile: realityProfile(), Settings: s})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	transparent := 0
	socks := false
	for _, in := range cfg.Inbounds {
		if in.Tag == TagTransparent {
			transparent++
		}
		if in.Tag == TagSocksIn {
			socks = true
		}
	}
	if transparent != 1 {
		t.Errorf("got %d transparent inbounds, want exactly 1", transparent)
	}
	if !socks {
		t.Error("mixed mode needs the SOCKS inbound: the tunnel delivers UDP there")
	}
}

func TestRealityStreamSettings(t *testing.T) {
	cfg, err := Build(Options{Profile: realityProfile(), Settings: settings()})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	out := cfg.Outbounds[0]
	if out.Tag != TagProxy {
		t.Fatalf("first outbound tag = %q, want %q", out.Tag, TagProxy)
	}
	if out.StreamSettings.Security != "reality" {
		t.Errorf("security = %q", out.StreamSettings.Security)
	}
	r := out.StreamSettings.RealitySettings
	if r == nil || r.PublicKey == "" || r.ShortID != "6ba85179" {
		t.Fatalf("reality settings = %+v", r)
	}
	if r.ServerName != "www.microsoft.com" {
		t.Errorf("serverName = %q", r.ServerName)
	}
	// Every outbound must carry the socket mark, otherwise TUN mode loops the
	// core's own traffic back through the tunnel.
	if out.StreamSettings.Sockopt == nil || out.StreamSettings.Sockopt.Mark == 0 {
		t.Error("proxy outbound is missing the socket mark")
	}
}

func TestMarkMatchesSettings(t *testing.T) {
	s := settings()
	s.FwMark = "0x1e0"
	if got := s.MarkValue(); got != 480 {
		t.Fatalf("MarkValue() = %d, want 480", got)
	}
	// The three marks must stay distinct, or one mechanism captures another's
	// packets.
	if s.MarkTProxy() == s.MarkValue() || s.MarkTunUDP() == s.MarkValue() ||
		s.MarkTProxy() == s.MarkTunUDP() {
		t.Fatalf("marks collide: core=%d tproxy=%d tun=%d",
			s.MarkValue(), s.MarkTProxy(), s.MarkTunUDP())
	}
	cfg, err := Build(Options{Profile: realityProfile(), Settings: s})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, out := range cfg.Outbounds {
		if out.Tag == TagBlock {
			continue
		}
		if out.StreamSettings == nil || out.StreamSettings.Sockopt == nil {
			t.Fatalf("outbound %s has no sockopt", out.Tag)
		}
		if out.StreamSettings.Sockopt.Mark != 480 {
			t.Errorf("outbound %s mark = %d, want 480", out.Tag, out.StreamSettings.Sockopt.Mark)
		}
	}
}

func TestWebsocketTransport(t *testing.T) {
	p := realityProfile()
	p.Network = "ws"
	p.Security = "tls"
	p.Path = "/ws"
	p.Host = "cdn.example.com"

	cfg, err := Build(Options{Profile: p, Settings: settings()})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	ss := cfg.Outbounds[0].StreamSettings
	if ss.Network != "ws" || ss.WSSettings == nil {
		t.Fatalf("stream = %+v", ss)
	}
	if ss.WSSettings.Path != "/ws" || ss.WSSettings.Headers["Host"] != "cdn.example.com" {
		t.Errorf("ws settings = %+v", ss.WSSettings)
	}
	if ss.TLSSettings == nil || ss.TLSSettings.ServerName != "www.microsoft.com" {
		t.Errorf("tls settings = %+v", ss.TLSSettings)
	}
}

func TestDNSOffOmitsDNSPieces(t *testing.T) {
	s := settings()
	s.DNSMode = model.DNSOff
	cfg, err := Build(Options{Profile: realityProfile(), Settings: s})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if cfg.DNS != nil {
		t.Error("dns block present even though DNS handling is off")
	}
	for _, in := range cfg.Inbounds {
		if in.Tag == TagDNSIn {
			t.Error("dns inbound present even though DNS handling is off")
		}
	}
	for _, out := range cfg.Outbounds {
		if out.Tag == TagDNSOut {
			t.Error("dns outbound present even though DNS handling is off")
		}
	}
}

func TestBuildRejectsIncompleteProfile(t *testing.T) {
	p := realityProfile()
	p.PublicKey = ""
	if _, err := Build(Options{Profile: p, Settings: settings()}); err == nil {
		t.Fatal("expected reality profile without a public key to be rejected")
	} else if !strings.Contains(err.Error(), "public key") {
		t.Errorf("error = %v, want it to mention the public key", err)
	}
}

func TestShadowsocksOutbound(t *testing.T) {
	p := &model.Profile{
		Proto:    model.ProtoShadowsocks,
		Address:  "ss.example.com",
		Port:     8388,
		Method:   "aes-256-gcm",
		Password: "hunter2",
		Network:  "tcp",
	}
	cfg, err := Build(Options{Profile: p, Settings: settings()})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	s := cfg.Outbounds[0].Settings.(map[string]any)
	servers := s["servers"].([]any)
	first := servers[0].(map[string]any)
	if first["method"] != "aes-256-gcm" || first["uot"] != true {
		t.Errorf("shadowsocks server = %+v", first)
	}
}

func TestGeoIPOnlyUsedWhenAssetsExist(t *testing.T) {
	// Naming geoip:private without the data files makes the core refuse to
	// start, so the rule must appear only when they are actually installed.
	dir := t.TempDir()
	t.Setenv("XRAY_LOCATION_ASSET", dir)

	if AssetsAvailable() {
		t.Fatal("assets reported as available in an empty directory")
	}
	cfg, err := Build(Options{Profile: realityProfile(), Settings: settings()})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if hasGeoIPRule(cfg) {
		t.Error("geoip:private used even though the data files are absent")
	}

	if err := os.WriteFile(filepath.Join(dir, "geoip.dat"), []byte("stub"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !AssetsAvailable() {
		t.Fatal("assets not detected after geoip.dat was created")
	}
	cfg, err = Build(Options{Profile: realityProfile(), Settings: settings()})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !hasGeoIPRule(cfg) {
		t.Error("geoip:private not used even though the data files are present")
	}

	// The plain CIDR list must stay either way, so a broken or partial geo
	// database cannot expose LAN traffic to the proxy.
	if !hasIP(cfg, "192.168.0.0/16") {
		t.Error("private CIDR list dropped when geo data is available")
	}
}

func hasGeoIPRule(c *Config) bool { return hasIP(c, "geoip:private") }

func hasIP(c *Config, want string) bool {
	for _, r := range c.Routing.Rules {
		for _, ip := range r.IP {
			if ip == want {
				return true
			}
		}
	}
	return false
}

// Sniffing decides whether the far end resolves the name a second time, which
// is worth a round trip on every new connection — so the rule behind it is
// pinned down here rather than left to whoever edits the inbound next.
func TestSniffingResolvesTheNameOnlyOnce(t *testing.T) {
	p := model.Profile{
		Proto: model.ProtoVLESS, Address: "a.example.com", Port: 443,
		UUID: "b831381d-6324-4d53-ad4f-8cda48b30811", Network: "tcp",
	}

	// With a resolver of our own in the path, the address the client used came
	// from us, so it is passed through and the domain is kept for routing.
	for _, dns := range []model.DNSMode{model.DNSDnsmasq, model.DNSRedirect} {
		s := model.Defaults()
		s.DNSMode = dns
		cfg, err := Build(Options{Profile: &p, Settings: &s})
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		for _, in := range cfg.Inbounds {
			if in.Sniffing == nil {
				continue
			}
			if !in.Sniffing.RouteOnly {
				t.Errorf("dns_mode %s: inbound %s should keep the client's address",
					dns, in.Tag)
			}
			if len(in.Sniffing.DestOverride) == 0 {
				t.Errorf("dns_mode %s: inbound %s must still sniff for routing",
					dns, in.Tag)
			}
		}
	}

	// With DNS left alone, the client's resolver is unknown and may have been
	// given a poisoned answer, so the far end resolving the name is safer.
	s := model.Defaults()
	s.DNSMode = model.DNSOff
	cfg, err := Build(Options{Profile: &p, Settings: &s})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, in := range cfg.Inbounds {
		if in.Sniffing != nil && in.Sniffing.RouteOnly {
			t.Errorf("dns_mode off: inbound %s must let the far end resolve", in.Tag)
		}
	}
}

// The core dials the server once per proxied connection. With a hostname in
// the outbound that is one system DNS lookup per connection, on a resolver that
// caches nothing — so the address is resolved here instead, once, while
// everything that identifies the server stays as the profile wrote it.
func TestServerHostnameIsResolvedOnceIntoTheOutbound(t *testing.T) {
	p := model.Profile{
		Proto: model.ProtoVLESS, Address: "peer.example.com", Port: 443,
		UUID: "b831381d-6324-4d53-ad4f-8cda48b30811", Network: "tcp",
		Security: "tls", SNI: "borrowed.example.net",
	}
	s := model.Defaults()

	calls := 0
	cfg, err := Build(Options{Profile: &p, Settings: &s,
		Resolve: func(host string) string {
			calls++
			if host != "peer.example.com" {
				t.Errorf("resolved the wrong name: %q", host)
			}
			return "203.0.113.9"
		}})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if calls != 1 {
		t.Errorf("want one lookup at build time, got %d", calls)
	}

	out := proxyOutbound(t, cfg)
	if got := vnextAddress(t, out); got != "203.0.113.9" {
		t.Errorf("outbound should dial the address, got %q", got)
	}
	// The identity of the server is not the address: a pin or a certificate is
	// checked against this name, so substituting the address here would break
	// every TLS profile.
	if got := out.StreamSettings.TLSSettings.ServerName; got != "borrowed.example.net" {
		t.Errorf("TLS server name changed to %q", got)
	}
}

// A profile that already carries an address has nothing to look up, and a
// resolver asked to resolve one could only get it wrong.
func TestLiteralAddressIsNotResolved(t *testing.T) {
	p := model.Profile{
		Proto: model.ProtoVLESS, Address: "212.64.215.71", Port: 443,
		UUID: "b831381d-6324-4d53-ad4f-8cda48b30811", Network: "tcp",
	}
	s := model.Defaults()
	cfg, err := Build(Options{Profile: &p, Settings: &s,
		Resolve: func(string) string {
			t.Error("a literal address must not be sent to the resolver")
			return "198.51.100.1"
		}})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got := vnextAddress(t, proxyOutbound(t, cfg)); got != "212.64.215.71" {
		t.Errorf("address changed to %q", got)
	}
}

// A resolver that cannot answer must not break the connect: the hostname is
// left in place and the core does what it did before.
func TestFailedResolutionKeepsTheHostname(t *testing.T) {
	p := model.Profile{
		Proto: model.ProtoVLESS, Address: "peer.example.com", Port: 443,
		UUID: "b831381d-6324-4d53-ad4f-8cda48b30811", Network: "tcp",
	}
	s := model.Defaults()
	cfg, err := Build(Options{Profile: &p, Settings: &s,
		Resolve: func(string) string { return "" }})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got := vnextAddress(t, proxyOutbound(t, cfg)); got != "peer.example.com" {
		t.Errorf("want the hostname kept, got %q", got)
	}
}

func proxyOutbound(t *testing.T, c *Config) *Outbound {
	t.Helper()
	for i := range c.Outbounds {
		if c.Outbounds[i].Tag == TagProxy {
			return &c.Outbounds[i]
		}
	}
	t.Fatal("no proxy outbound")
	return nil
}

func vnextAddress(t *testing.T, out *Outbound) string {
	t.Helper()
	settings, ok := out.Settings.(map[string]any)
	if !ok {
		t.Fatalf("unexpected settings shape %T", out.Settings)
	}
	list, ok := settings["vnext"].([]any)
	if !ok || len(list) == 0 {
		t.Fatalf("no vnext in %v", settings)
	}
	entry, ok := list[0].(map[string]any)
	if !ok {
		t.Fatalf("unexpected vnext shape %T", list[0])
	}
	addr, _ := entry["address"].(string)
	return addr
}

// UDP has to be carried the same way whatever port it is going to, and the
// switch that does it must not quietly leave out port 443.
//
// Xray's own default for xudpProxyUDP443 is "reject", which drops UDP to 443 —
// QUIC — while every other UDP port works. That is the most confusing failure
// available: NTP goes through, a game's own port goes through, and video stalls
// for a second on every play while the browser waits out its QUIC timeout.
// Nothing in any log says "443".
func TestUDPIsCarriedOnEveryPort(t *testing.T) {
	p := model.Profile{
		Proto: model.ProtoVLESS, Address: "203.0.113.5", Port: 443,
		UUID: "b831381d-6324-4d53-ad4f-8cda48b30811", Network: "tcp",
		Security: "tls", Flow: "xtls-rprx-vision",
	}
	s := model.Defaults()

	cfg, err := Build(Options{Profile: &p, Settings: &s})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	out := proxyOutbound(t, cfg)
	if out.Mux == nil || !out.Mux.Enabled {
		t.Fatal("UDP needs XUDP, which lives in the mux object")
	}
	if out.Mux.XUDPProxyUDP443 != "allow" {
		t.Errorf("UDP 443 must be allowed explicitly, got %q", out.Mux.XUDPProxyUDP443)
	}
	if out.Mux.XUDPConcurrency <= 0 {
		t.Errorf("XUDP has to be enabled to carry UDP, got %d", out.Mux.XUDPConcurrency)
	}
	// Vision is built around one connection per stream; multiplexing TCP on top
	// of it costs throughput for nothing, so XUDP must not drag it in.
	if out.Mux.Concurrency != -1 {
		t.Errorf("TCP must not be multiplexed by default, got %d", out.Mux.Concurrency)
	}

	// A profile that does ask for TCP multiplexing gets it, and keeps XUDP.
	p.Mux = true
	cfg, err = Build(Options{Profile: &p, Settings: &s})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	out = proxyOutbound(t, cfg)
	if out.Mux.Concurrency <= 0 {
		t.Errorf("mux=true should multiplex TCP, got %d", out.Mux.Concurrency)
	}
	if out.Mux.XUDPProxyUDP443 != "allow" {
		t.Error("enabling TCP mux must not take UDP 443 away again")
	}
}

// The core's own name lookups have to work wherever the tunnel works.
//
// A bare resolver address means UDP, and a proxy server that carries TCP but
// drops UDP — common enough — then breaks every lookup on the network while the
// tunnel itself looks perfectly healthy. That failure is invisible from outside:
// the connection is up, TCP works, and nothing resolves.
func TestCoreResolvesOverTCPByDefault(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"1.1.1.1", "tcp://1.1.1.1"},
		{"8.8.8.8", "tcp://8.8.8.8"},
		// Anything the operator spelled out is theirs to decide.
		{"tcp://9.9.9.9", "tcp://9.9.9.9"},
		{"https://dns.google/dns-query", "https://dns.google/dns-query"},
		{"quic://dns.adguard.com", "quic://dns.adguard.com"},
		{"localhost", "localhost"},
		{"", ""},
	}
	for _, c := range cases {
		if got := upstreamDNS(c.in); got != c.want {
			t.Errorf("upstreamDNS(%q) = %q, want %q", c.in, got, c.want)
		}
	}

	// And it reaches the generated configuration.
	p := model.Profile{
		Proto: model.ProtoVLESS, Address: "203.0.113.5", Port: 443,
		UUID: "b831381d-6324-4d53-ad4f-8cda48b30811", Network: "tcp",
	}
	s := model.Defaults()
	s.DNS = "1.1.1.1"
	cfg, err := Build(Options{Profile: &p, Settings: &s})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if cfg.DNS == nil || len(cfg.DNS.Servers) == 0 {
		t.Fatal("no DNS servers in the configuration")
	}
	last := cfg.DNS.Servers[len(cfg.DNS.Servers)-1]
	if last != "tcp://1.1.1.1" {
		t.Errorf("the upstream resolver should be reached over TCP, got %v", last)
	}
}

// An idle connection must outlive a coffee break. The core's default is five
// minutes, and a transparent proxy reports its own timeout to the client as
// the far end hanging up — which is how an SSH session that dies at the
// router looks exactly like a server or a carrier problem.
func TestIdleConnectionsOutliveTheCoresDefault(t *testing.T) {
	cfg, err := Build(Options{Profile: realityProfile(), Settings: settings()})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if cfg.Policy == nil || cfg.Policy.Levels == nil {
		t.Fatal("no policy levels: idle connections are cut after the core's default 300s")
	}
	lvl := cfg.Policy.Levels["0"]
	if lvl == nil {
		t.Fatal("level 0 is the level every inbound uses; it has no policy")
	}
	if lvl.ConnIdle <= 300 {
		t.Errorf("connIdle is %ds, no better than the default 300s", lvl.ConnIdle)
	}
}
