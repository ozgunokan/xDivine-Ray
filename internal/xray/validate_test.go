package xray

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"xwrt/internal/model"
)

// These tests hand every generated configuration to the real Xray binary and
// ask it to parse them. Unit tests can only check the shape this package
// intends to produce; only the core can say whether it actually accepts it.
//
// The binary is found through XWRT_XRAY_BIN or PATH. When neither has it the
// tests skip, so a build host without Xray still runs the rest of the suite.

func xrayBin(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("XWRT_XRAY_BIN"); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p
		}
		t.Fatalf("XWRT_XRAY_BIN points at %s, which does not exist", p)
	}
	p, err := exec.LookPath("xray")
	if err != nil {
		t.Skip("xray not found; set XWRT_XRAY_BIN to validate against the real core")
	}
	return p
}

// validate writes the config out and runs `xray run -test` over it.
func validate(t *testing.T, bin string, cfg *Config) {
	t.Helper()
	raw, err := cfg.JSON()
	if err != nil {
		t.Fatalf("render config: %v", err)
	}
	path := filepath.Join(t.TempDir(), "xray.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	out, err := exec.Command(bin, "run", "-c", path, "-test").CombinedOutput()
	if err != nil {
		t.Fatalf("xray rejected the generated config: %v\n%s\n--- config ---\n%s",
			err, strings.TrimSpace(string(out)), raw)
	}
	if !strings.Contains(string(out), "Configuration OK") {
		t.Fatalf("xray did not confirm the config:\n%s\n--- config ---\n%s", out, raw)
	}
}

// profileMatrix covers every protocol and transport the builder claims to
// support, in the combinations that occur in real share links.
func profileMatrix() []struct {
	name string
	p    model.Profile
} {
	return []struct {
		name string
		p    model.Profile
	}{
		{"vless-reality-tcp-vision", model.Profile{
			Proto: model.ProtoVLESS, Address: "a.example.com", Port: 443,
			UUID: "b831381d-6324-4d53-ad4f-8cda48b30811", Flow: "xtls-rprx-vision",
			Network: "tcp", Security: "reality", SNI: "www.microsoft.com",
			Fingerprint: "chrome", PublicKey: "xdfA1s2WlnAGJdzTe4JBs0lPVcUcfWCNrTZUJ2Gq0Ck",
			ShortID: "6ba85179e30d4fc2", SpiderX: "/",
		}},
		{"vless-tls-ws", model.Profile{
			Proto: model.ProtoVLESS, Address: "b.example.com", Port: 443,
			UUID:    "b831381d-6324-4d53-ad4f-8cda48b30811",
			Network: "ws", Security: "tls", SNI: "b.example.com",
			Path: "/ws", Host: "b.example.com", ALPN: "h2,http/1.1",
			Fingerprint: "firefox",
		}},
		{"vless-tls-grpc", model.Profile{
			Proto: model.ProtoVLESS, Address: "c.example.com", Port: 443,
			UUID:    "b831381d-6324-4d53-ad4f-8cda48b30811",
			Network: "grpc", Security: "tls", SNI: "c.example.com",
			ServiceName: "grpcsvc",
		}},
		{"vless-tls-xhttp", model.Profile{
			Proto: model.ProtoVLESS, Address: "d.example.com", Port: 443,
			UUID:    "b831381d-6324-4d53-ad4f-8cda48b30811",
			Network: "xhttp", Security: "tls", SNI: "d.example.com",
			Path: "/x", Host: "d.example.com",
		}},
		{"vless-tls-httpupgrade", model.Profile{
			Proto: model.ProtoVLESS, Address: "e.example.com", Port: 443,
			UUID:    "b831381d-6324-4d53-ad4f-8cda48b30811",
			Network: "httpupgrade", Security: "tls", SNI: "e.example.com",
			Path: "/hu", Host: "e.example.com",
		}},
		{"vless-none-tcp-http-header", model.Profile{
			Proto: model.ProtoVLESS, Address: "f.example.com", Port: 80,
			UUID:    "b831381d-6324-4d53-ad4f-8cda48b30811",
			Network: "tcp", Security: "none", HeaderType: "http",
			Path: "/", Host: "f.example.com",
		}},
		{"vmess-tls-ws-mux", model.Profile{
			Proto: model.ProtoVMess, Address: "g.example.com", Port: 443,
			UUID: "b831381d-6324-4d53-ad4f-8cda48b30811", AlterID: 0,
			Encryption: "auto", Network: "ws", Security: "tls",
			SNI: "g.example.com", Path: "/v", Host: "g.example.com", Mux: true,
		}},
		{"vmess-none-kcp", model.Profile{
			Proto: model.ProtoVMess, Address: "h.example.com", Port: 2000,
			UUID:       "b831381d-6324-4d53-ad4f-8cda48b30811",
			Encryption: "auto", Network: "kcp", Security: "none",
			HeaderType: "dtls", Seed: "seedvalue",
		}},
		{"vmess-tls-quic", model.Profile{
			Proto: model.ProtoVMess, Address: "i.example.com", Port: 443,
			UUID:       "b831381d-6324-4d53-ad4f-8cda48b30811",
			Encryption: "auto", Network: "quic", Security: "tls",
			SNI: "i.example.com", QUICSec: "aes-128-gcm", QUICKey: "quickey",
			HeaderType: "none",
		}},
		{"trojan-tls-tcp", model.Profile{
			Proto: model.ProtoTrojan, Address: "j.example.com", Port: 443,
			Password: "hunter2", Network: "tcp", Security: "tls",
			SNI: "j.example.com",
		}},
		{"trojan-tls-ws", model.Profile{
			Proto: model.ProtoTrojan, Address: "k.example.com", Port: 443,
			Password: "hunter2", Network: "ws", Security: "tls",
			SNI: "k.example.com", Path: "/t", Host: "k.example.com",
		}},
		{"shadowsocks", model.Profile{
			Proto: model.ProtoShadowsocks, Address: "l.example.com", Port: 8388,
			Method: "aes-256-gcm", Password: "hunter2", Network: "tcp",
			Security: "none",
		}},
		{"vless-h2", model.Profile{
			Proto: model.ProtoVLESS, Address: "m.example.com", Port: 443,
			UUID:    "b831381d-6324-4d53-ad4f-8cda48b30811",
			Network: "h2", Security: "tls", SNI: "m.example.com",
			Path: "/h2", Host: "m.example.com",
		}},
		{"vless-insecure-tls", model.Profile{
			Proto: model.ProtoVLESS, Address: "n.example.com", Port: 8443,
			UUID:    "b831381d-6324-4d53-ad4f-8cda48b30811",
			Network: "tcp", Security: "tls", SNI: "n.example.com",
			AllowInsecure: true,
		}},
	}
}

// usesRemovedFeature reports whether a profile depends on something the
// installed core no longer has. Those profiles must be refused with a clear
// message, not turned into a config the core will reject.
func usesRemovedFeature(p model.Profile, caps Capabilities) bool {
	switch {
	case (p.Network == "h2" || p.Network == "http") && !caps.H2Transport:
		return true
	case p.Network == "quic" && !caps.QUICTransport:
		return true
	case (p.Network == "kcp" || p.Network == "mkcp") &&
		(hasHeader(p.HeaderType) || p.Seed != "") && !caps.KCPHeaderSeed:
		return true
	case p.AllowInsecure && p.PinnedCert == "" && !caps.AllowInsecure:
		return true
	}
	return false
}

// TestRealCoreAcceptsEveryProfile is the broadest check in the suite: every
// protocol and transport, in every capture mode, handed to the real core.
//
// A profile whose transport the core has dropped must fail at Build time with
// an explanatory error. Anything the builder does emit must parse.
func TestRealCoreAcceptsEveryProfile(t *testing.T) {
	bin := xrayBin(t)
	caps := Probe(bin)
	if !caps.Probed {
		t.Fatal("capability probing failed against a working binary")
	}
	t.Logf("core %s: kcpHeaderSeed=%v h2=%v quic=%v allowInsecure=%v",
		caps.Version, caps.KCPHeaderSeed, caps.H2Transport,
		caps.QUICTransport, caps.AllowInsecure)

	for _, tc := range profileMatrix() {
		for _, m := range model.Modes() {
			t.Run(tc.name+"/"+string(m), func(t *testing.T) {
				p := tc.p
				s := model.Defaults()
				s.Mode = m

				cfg, err := Build(Options{
					Profile:     &p,
					Settings:    &s,
					Caps:        caps,
					DirectCIDRs: []string{"192.168.1.0/24"},
				})

				if usesRemovedFeature(p, caps) {
					if err == nil {
						t.Fatal("expected a refusal: this core has removed the " +
							"feature the profile needs")
					}
					// The message has to name the core version and tell the
					// user what to do; a bare "invalid config" helps nobody.
					if !strings.Contains(err.Error(), caps.Version) {
						t.Errorf("error should name the core version: %v", err)
					}
					return
				}
				if err != nil {
					t.Fatalf("Build: %v", err)
				}
				validate(t, bin, cfg)
			})
		}
	}
}

// TestPinnedCertReplacesAllowInsecure covers the migration path an operator
// actually follows: pin the certificate and the same profile works again.
func TestPinnedCertReplacesAllowInsecure(t *testing.T) {
	bin := xrayBin(t)
	caps := Probe(bin)

	p := model.Profile{
		Proto: model.ProtoVLESS, Address: "n.example.com", Port: 8443,
		UUID:    "b831381d-6324-4d53-ad4f-8cda48b30811",
		Network: "tcp", Security: "tls", SNI: "n.example.com",
		AllowInsecure: true,
	}
	s := model.Defaults()

	if !caps.AllowInsecure {
		if _, err := Build(Options{Profile: &p, Settings: &s, Caps: caps}); err == nil {
			t.Fatal("expected allowInsecure to be refused by this core")
		}
	}

	// A hex SHA-256 digest, as `xwrt fetch-cert` produces.
	const digest = "fe7044b7e2af9455c338d9fcdf9033fadbc292721b33ea8340cd0de74941b105"
	p.AllowInsecure = true
	p.PinnedCert = digest
	cfg, err := Build(Options{Profile: &p, Settings: &s, Caps: caps})
	if err != nil {
		t.Fatalf("Build with a pinned certificate: %v", err)
	}
	tls := cfg.Outbounds[0].StreamSettings.TLSSettings
	if tls.PinnedPeerCertSha256 != digest {
		t.Fatalf("pinned digest not emitted: %+v", tls)
	}
	if tls.AllowInsecure {
		t.Error("allowInsecure must not be emitted once a certificate is pinned")
	}
	validate(t, bin, cfg)

	// The colon form openssl prints must reach the core as plain hex, since
	// that is the spelling someone pasting from `openssl x509 -fingerprint`
	// will have.
	p.PinnedCert = "FE:70:44:B7:E2:AF:94:55:C3:38:D9:FC:DF:90:33:FA:" +
		"DB:C2:92:72:1B:33:EA:83:40:CD:0D:E7:49:41:B1:05"
	cfg, err = Build(Options{Profile: &p, Settings: &s, Caps: caps})
	if err != nil {
		t.Fatalf("Build with a colon-separated pin: %v", err)
	}
	if got := cfg.Outbounds[0].StreamSettings.TLSSettings.PinnedPeerCertSha256; got != digest {
		t.Fatalf("colon form not normalised: %q", got)
	}
	validate(t, bin, cfg)

	// A malformed pin must be refused rather than passed through: the core
	// would then reject every certificate and the failure would look like a
	// network problem.
	p.PinnedCert = "not-a-digest"
	p.AllowInsecure = false
	if _, err := Build(Options{Profile: &p, Settings: &s, Caps: caps}); err == nil {
		t.Error("expected a malformed pin to be refused")
	}
}

// TestRealCoreAcceptsEveryDNSMode checks the DNS block and its inbound and
// outbound, which vary independently of the capture mode.
func TestRealCoreAcceptsEveryDNSMode(t *testing.T) {
	bin := xrayBin(t)
	p := profileMatrix()[0].p

	for _, dns := range []model.DNSMode{model.DNSDnsmasq, model.DNSRedirect, model.DNSOff} {
		for _, ipv6 := range []bool{false, true} {
			s := model.Defaults()
			s.DNSMode = dns
			s.IPv6 = ipv6
			cfg, err := Build(Options{Profile: &p, Settings: &s})
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			validate(t, bin, cfg)
		}
	}
}

// TestRealCoreAcceptsGeoIPRule guards the optional geo data path: the rule is
// only emitted when the files exist, and when it is emitted the core must
// still accept it.
func TestRealCoreAcceptsGeoIPRule(t *testing.T) {
	bin := xrayBin(t)

	// Xray resolves the asset directory at startup, so point it at one that
	// really holds the file the rule needs.
	assetDir := os.Getenv("XWRT_TEST_ASSET_DIR")
	if assetDir == "" {
		t.Skip("set XWRT_TEST_ASSET_DIR to a directory containing geoip.dat " +
			"to validate the geoip rule against the core")
	}
	t.Setenv("XRAY_LOCATION_ASSET", assetDir)
	if !AssetsAvailable() {
		t.Fatalf("no geoip.dat in %s", assetDir)
	}

	p := profileMatrix()[0].p
	s := model.Defaults()
	cfg, err := Build(Options{Profile: &p, Settings: &s})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !hasIP(cfg, "geoip:private") {
		t.Fatal("geoip rule missing even though the data file is present")
	}

	raw, _ := cfg.JSON()
	path := filepath.Join(t.TempDir(), "xray.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "run", "-c", path, "-test")
	cmd.Env = append(os.Environ(), "XRAY_LOCATION_ASSET="+assetDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("xray rejected the geoip config: %v\n%s", err, out)
	}
}

// TestRealCoreAcceptsGroups validates the balancer and observatory wiring the
// same way as single profiles: by asking the core. The dependency rules here
// are strict — leastPing without an observatory makes the core refuse to
// start — so this is the check that matters for groups.
func TestRealCoreAcceptsGroups(t *testing.T) {
	bin := xrayBin(t)
	caps := Probe(bin)

	for _, st := range model.Strategies() {
		for _, n := range []int{1, 2, 5} {
			for _, m := range model.Modes() {
				t.Run(string(st)+"/"+string(m), func(t *testing.T) {
					g := &model.Group{
						ID: "g1", Name: "G", Strategy: st,
						Members: make([]string, n),
					}
					g.Normalize()

					s := model.Defaults()
					s.Mode = m
					cfg, err := Build(Options{
						Group:    g,
						Members:  members(n),
						Settings: &s,
						Caps:     caps,
					})
					if err != nil {
						t.Fatalf("Build: %v", err)
					}
					validate(t, bin, cfg)
				})
			}
		}
	}
}

// TestRealCoreAcceptsRules checks the exception list against the core,
// including the scoped DNS entry a bypass rule adds.
func TestRealCoreAcceptsRules(t *testing.T) {
	bin := xrayBin(t)
	caps := Probe(bin)

	rules := []model.Rule{
		{ID: "r1", Name: "Bank", Enabled: true, Action: model.ActionDirect,
			Domains: []string{"bank.example.com", "domain:gov.tr", "full:www.x.com",
				"keyword:intranet", `regexp:\.local$`}},
		{ID: "r2", Name: "Printer", Enabled: true, Action: model.ActionDirect,
			IPs: []string{"203.0.113.0/24"}, Sources: []string{"192.168.1.50"}},
		{ID: "r3", Name: "Torrent", Enabled: true, Action: model.ActionBlock,
			Protocols: []string{"bittorrent"}},
		{ID: "r4", Name: "Force", Enabled: true, Action: model.ActionProxy,
			Domains: []string{"news.example.com"}, Port: "80,443", Network: "tcp"},
		{ID: "r5", Name: "Off", Enabled: false, Action: model.ActionBlock,
			Domains: []string{"disabled.example.com"}},
	}

	for _, m := range model.Modes() {
		for _, withGroup := range []bool{false, true} {
			s := model.Defaults()
			s.Mode = m

			opts := Options{Settings: &s, Caps: caps, Rules: rules}
			if withGroup {
				g := &model.Group{ID: "g1", Name: "G", Strategy: model.StrategyLeastPing,
					Members: []string{"a", "b"}}
				g.Normalize()
				opts.Group, opts.Members = g, members(2)
			} else {
				opts.Profile = realityProfile()
			}

			cfg, err := Build(opts)
			if err != nil {
				t.Fatalf("mode %s group=%v: Build: %v", m, withGroup, err)
			}
			validate(t, bin, cfg)
		}
	}
}
