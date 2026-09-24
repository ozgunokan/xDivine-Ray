package uri

import (
	"encoding/base64"
	"testing"

	"xwrt/internal/model"
)

func TestParseVLESSReality(t *testing.T) {
	link := "vless://b831381d-6324-4d53-ad4f-8cda48b30811@example.com:443" +
		"?encryption=none&security=reality&sni=www.microsoft.com&fp=chrome" +
		"&pbk=xdfA1s2WlnAGJdzTe4JBs0lPVcUcfWCNrTZUJ2Gq0Ck&sid=6ba85179e30d4fc2" +
		"&spx=%2F&type=tcp&flow=xtls-rprx-vision#Server%20One"

	p, err := Parse(link)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if p.Proto != model.ProtoVLESS {
		t.Errorf("proto = %q, want vless", p.Proto)
	}
	if p.Address != "example.com" || p.Port != 443 {
		t.Errorf("endpoint = %s:%d", p.Address, p.Port)
	}
	if p.UUID != "b831381d-6324-4d53-ad4f-8cda48b30811" {
		t.Errorf("uuid = %q", p.UUID)
	}
	if p.Security != "reality" || p.PublicKey == "" || p.ShortID != "6ba85179e30d4fc2" {
		t.Errorf("reality fields = %q %q %q", p.Security, p.PublicKey, p.ShortID)
	}
	if p.Flow != "xtls-rprx-vision" {
		t.Errorf("flow = %q", p.Flow)
	}
	if p.SNI != "www.microsoft.com" || p.Fingerprint != "chrome" {
		t.Errorf("tls fields = %q %q", p.SNI, p.Fingerprint)
	}
	if p.Name != "Server One" {
		t.Errorf("name = %q, want %q", p.Name, "Server One")
	}
	if err := p.Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}
}

func TestParseVLESSWebsocket(t *testing.T) {
	link := "vless://uuid-1234@1.2.3.4:8080?type=ws&security=tls&path=%2Fws&host=cdn.example.com#ws"
	p, err := Parse(link)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if p.Network != "ws" || p.Path != "/ws" || p.Host != "cdn.example.com" {
		t.Errorf("ws fields = %q %q %q", p.Network, p.Path, p.Host)
	}
	// SNI should fall back to the Host header when not given explicitly.
	if p.SNI != "cdn.example.com" {
		t.Errorf("sni = %q, want fallback to host", p.SNI)
	}
}

func TestParseVMessBase64(t *testing.T) {
	raw := `{"v":"2","ps":"Tokyo","add":"jp.example.com","port":"443","id":"aaaa-bbbb",` +
		`"aid":"0","scy":"auto","net":"ws","type":"none","host":"jp.example.com",` +
		`"path":"/path","tls":"tls","sni":"jp.example.com"}`
	link := "vmess://" + base64.StdEncoding.EncodeToString([]byte(raw))

	p, err := Parse(link)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if p.Proto != model.ProtoVMess || p.Name != "Tokyo" {
		t.Errorf("proto/name = %q %q", p.Proto, p.Name)
	}
	if p.Port != 443 || p.Network != "ws" || p.Security != "tls" || p.Path != "/path" {
		t.Errorf("fields = %d %q %q %q", p.Port, p.Network, p.Security, p.Path)
	}
	if err := p.Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}
}

func TestParseTrojanDefaultsToTLS(t *testing.T) {
	p, err := Parse("trojan://secretpass@tj.example.com:443#Trojan")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if p.Password != "secretpass" {
		t.Errorf("password = %q", p.Password)
	}
	if p.Security != "tls" {
		t.Errorf("security = %q, want tls by default", p.Security)
	}
}

func TestParseShadowsocksSIP002(t *testing.T) {
	creds := base64.RawURLEncoding.EncodeToString([]byte("aes-256-gcm:hunter2"))
	p, err := Parse("ss://" + creds + "@ss.example.com:8388#SS%20Node")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if p.Method != "aes-256-gcm" || p.Password != "hunter2" {
		t.Errorf("creds = %q %q", p.Method, p.Password)
	}
	if p.Address != "ss.example.com" || p.Port != 8388 {
		t.Errorf("endpoint = %s:%d", p.Address, p.Port)
	}
	if p.Name != "SS Node" {
		t.Errorf("name = %q", p.Name)
	}
}

func TestParseShadowsocksLegacy(t *testing.T) {
	blob := base64.StdEncoding.EncodeToString([]byte("aes-128-gcm:pw@1.1.1.1:443"))
	p, err := Parse("ss://" + blob + "#Legacy")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if p.Method != "aes-128-gcm" || p.Password != "pw" || p.Port != 443 {
		t.Errorf("parsed = %q %q %d", p.Method, p.Password, p.Port)
	}
}

func TestParseGRPCServiceNameFromPath(t *testing.T) {
	p, err := Parse("vless://u@h.example.com:443?type=grpc&security=tls&path=%2Fgrpcsvc")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if p.ServiceName != "grpcsvc" {
		t.Errorf("serviceName = %q", p.ServiceName)
	}
}

func TestParseRejectsUnknownScheme(t *testing.T) {
	if _, err := Parse("hysteria2://x@y:443"); err == nil {
		t.Fatal("expected an error for an unsupported scheme")
	}
}

func TestParseManySkipsBadLines(t *testing.T) {
	body := "vless://u1@a.example.com:443\n" +
		"# a comment\n" +
		"garbage-line\n" +
		"\n" +
		"trojan://pw@b.example.com:443\n"
	profiles, errs := ParseMany(body)
	if len(profiles) != 2 {
		t.Fatalf("got %d profiles, want 2", len(profiles))
	}
	if len(errs) != 1 {
		t.Fatalf("got %d errors, want 1", len(errs))
	}
}

// Providers put the node's load in its name, and a percent sign is where a URL
// parser stops reading. Losing those nodes looks like a short subscription,
// never like an error: the link is skipped, the rest import fine, and the
// count is simply wrong.
func TestNamesSurviveWhateverIsInThem(t *testing.T) {
	base := "vless://00000000-1111-2222-3333-444444444444@a.example:443" +
		"?type=tcp&encryption=none&security=tls&sni=a.example"

	cases := []struct{ frag, want string }{
		{"TR-1", "TR-1"},
		{"TR-1 · 20%", "TR-1 · 20%"}, // trailing percent: the common one
		{"%80 kalan", "%80 kalan"},   // leading, and not a valid escape
		{"🇹🇷 TR | 45% | 2026-10-01", "🇹🇷 TR | 45% | 2026-10-01"},
		{"TR-1%20node", "TR-1 node"}, // a real escape still decodes
		{"100%25", "100%"},           // and so does an escaped percent
	}
	for _, tc := range cases {
		p, err := Parse(base + "#" + tc.frag)
		if err != nil {
			t.Errorf("name %q: %v", tc.frag, err)
			continue
		}
		if p.Name != tc.want {
			t.Errorf("name %q parsed as %q, want %q", tc.frag, p.Name, tc.want)
		}
	}
}

// The same link in a list: one unparseable name must not cost the others, and
// with the fix there is nothing unparseable about it in the first place.
func TestSubscriptionListKeepsEveryNode(t *testing.T) {
	body := "vless://u1@a.example:443?type=tcp&encryption=none&security=tls#TR-1 · 20%\n" +
		"vless://u2@b.example:443?type=tcp&encryption=none&security=tls#DE-2\n" +
		"trojan://pw@c.example:8443?security=tls&type=tcp#NL-3 %99\n"

	out, errs := ParseMany(body)
	if len(out) != 3 {
		t.Fatalf("parsed %d of 3 nodes (errors: %v)", len(out), errs)
	}
}
