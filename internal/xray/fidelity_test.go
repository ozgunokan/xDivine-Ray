package xray

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"xwrt/internal/model"
	"xwrt/internal/uri"
)

// Fidelity tests follow a share link all the way to the generated core config
// and check that every parameter it carried arrived in the right place.
//
// This is the property that matters most in practice: a link pasted into the
// UI has to produce exactly the connection the server operator described. A
// parameter silently dropped between the parser and the config builder is the
// worst kind of bug here, because the connection still comes up — it just
// fails, or worse, fingerprints differently than intended.

// at walks the decoded config by a path of map keys and array indices.
func at(t *testing.T, v any, path ...any) any {
	t.Helper()
	cur := v
	for i, step := range path {
		switch k := step.(type) {
		case string:
			m, ok := cur.(map[string]any)
			if !ok {
				t.Fatalf("path %v: element %d: expected an object, got %T", path, i, cur)
			}
			cur, ok = m[k]
			if !ok {
				t.Fatalf("path %v: key %q is missing", path, k)
			}
		case int:
			a, ok := cur.([]any)
			if !ok {
				t.Fatalf("path %v: element %d: expected an array, got %T", path, i, cur)
			}
			if k >= len(a) {
				t.Fatalf("path %v: index %d out of range (len %d)", path, k, len(a))
			}
			cur = a[k]
		}
	}
	return cur
}

func eq(t *testing.T, got any, want any, what string) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %#v, want %#v", what, got, want)
	}
}

// buildFromLink parses a share link and renders the config it produces.
func buildFromLink(t *testing.T, link string) (map[string]any, *model.Profile) {
	t.Helper()
	p, err := uri.Parse(link)
	if err != nil {
		t.Fatalf("parse link: %v", err)
	}
	s := model.Defaults()
	cfg, err := Build(Options{Profile: p, Settings: &s, Caps: AllFeatures()})
	if err != nil {
		t.Fatalf("build config: %v", err)
	}
	raw, err := cfg.JSON()
	if err != nil {
		t.Fatalf("render config: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	return out, p
}

// TestVLESSRealityLinkFidelity is the case most links in circulation use.
func TestVLESSRealityLinkFidelity(t *testing.T) {
	link := "vless://b831381d-6324-4d53-ad4f-8cda48b30811@example.com:8443" +
		"?encryption=none&security=reality&sni=www.microsoft.com&fp=chrome" +
		"&pbk=xdfA1s2WlnAGJdzTe4JBs0lPVcUcfWCNrTZUJ2Gq0Ck" +
		"&sid=6ba85179e30d4fc2&spx=%2Fpath&type=tcp&flow=xtls-rprx-vision" +
		"#My%20Server"

	cfg, p := buildFromLink(t, link)

	if p.Name != "My Server" {
		t.Errorf("remark = %q, want %q", p.Name, "My Server")
	}

	out := at(t, cfg, "outbounds", 0)
	eq(t, at(t, out, "protocol"), "vless", "protocol")

	vnext := at(t, out, "settings", "vnext", 0)
	eq(t, at(t, vnext, "address"), "example.com", "address")
	eq(t, at(t, vnext, "port"), float64(8443), "port")

	user := at(t, vnext, "users", 0)
	eq(t, at(t, user, "id"), "b831381d-6324-4d53-ad4f-8cda48b30811", "uuid")
	eq(t, at(t, user, "encryption"), "none", "encryption")
	eq(t, at(t, user, "flow"), "xtls-rprx-vision", "flow")

	ss := at(t, out, "streamSettings")
	eq(t, at(t, ss, "network"), "tcp", "network")
	eq(t, at(t, ss, "security"), "reality", "security")

	r := at(t, ss, "realitySettings")
	eq(t, at(t, r, "serverName"), "www.microsoft.com", "reality serverName")
	eq(t, at(t, r, "fingerprint"), "chrome", "reality fingerprint")
	eq(t, at(t, r, "publicKey"), "xdfA1s2WlnAGJdzTe4JBs0lPVcUcfWCNrTZUJ2Gq0Ck", "reality publicKey")
	eq(t, at(t, r, "shortId"), "6ba85179e30d4fc2", "reality shortId")
	eq(t, at(t, r, "spiderX"), "/path", "reality spiderX")

	// REALITY must never also emit a TLS block: the two are alternatives and
	// the core would use the wrong one.
	if _, has := at(t, ss).(map[string]any)["tlsSettings"]; has {
		t.Error("tlsSettings emitted alongside realitySettings")
	}
}

// TestVLESSWebsocketTLSLinkFidelity covers the other common VLESS shape.
func TestVLESSWebsocketTLSLinkFidelity(t *testing.T) {
	link := "vless://b831381d-6324-4d53-ad4f-8cda48b30811@cdn.example.com:443" +
		"?encryption=none&security=tls&type=ws&path=%2Fwebsocket%3Fed%3D2048" +
		"&host=front.example.com&sni=front.example.com&fp=firefox" +
		"&alpn=h2%2Chttp%2F1.1#WS%20Node"

	cfg, _ := buildFromLink(t, link)
	out := at(t, cfg, "outbounds", 0)
	ss := at(t, out, "streamSettings")

	eq(t, at(t, ss, "network"), "ws", "network")
	eq(t, at(t, ss, "security"), "tls", "security")

	ws := at(t, ss, "wsSettings")
	// The path carries a query string of its own; it must survive intact.
	eq(t, at(t, ws, "path"), "/websocket?ed=2048", "ws path")
	eq(t, at(t, ws, "headers", "Host"), "front.example.com", "ws Host header")

	tls := at(t, ss, "tlsSettings")
	eq(t, at(t, tls, "serverName"), "front.example.com", "tls serverName")
	eq(t, at(t, tls, "fingerprint"), "firefox", "tls fingerprint")

	alpn, ok := at(t, tls, "alpn").([]any)
	if !ok || len(alpn) != 2 || alpn[0] != "h2" || alpn[1] != "http/1.1" {
		t.Errorf("alpn = %#v, want [h2 http/1.1]", at(t, tls, "alpn"))
	}
}

// TestVLESSGRPCLinkFidelity checks the transport whose parameter naming
// differs most between link and config.
func TestVLESSGRPCLinkFidelity(t *testing.T) {
	link := "vless://b831381d-6324-4d53-ad4f-8cda48b30811@grpc.example.com:443" +
		"?encryption=none&security=tls&type=grpc&serviceName=my%2Fservice" +
		"&sni=grpc.example.com#GRPC"

	cfg, _ := buildFromLink(t, link)
	ss := at(t, cfg, "outbounds", 0, "streamSettings")

	eq(t, at(t, ss, "network"), "grpc", "network")
	eq(t, at(t, ss, "grpcSettings", "serviceName"), "my/service", "grpc serviceName")
}

// TestVLESSXHTTPLinkFidelity covers the newest transport.
func TestVLESSXHTTPLinkFidelity(t *testing.T) {
	link := "vless://b831381d-6324-4d53-ad4f-8cda48b30811@x.example.com:443" +
		"?encryption=none&security=tls&type=xhttp&path=%2Fsplit" +
		"&host=x.example.com&sni=x.example.com#XHTTP"

	cfg, _ := buildFromLink(t, link)
	ss := at(t, cfg, "outbounds", 0, "streamSettings")

	eq(t, at(t, ss, "network"), "xhttp", "network")
	eq(t, at(t, ss, "xhttpSettings", "path"), "/split", "xhttp path")
	eq(t, at(t, ss, "xhttpSettings", "host"), "x.example.com", "xhttp host")
}

// TestVMessLinkFidelity walks the base64 JSON form.
func TestVMessLinkFidelity(t *testing.T) {
	raw := `{"v":"2","ps":"Tokyo","add":"jp.example.com","port":"2053",` +
		`"id":"b831381d-6324-4d53-ad4f-8cda48b30811","aid":"64","scy":"chacha20-poly1305",` +
		`"net":"ws","type":"none","host":"jp.example.com","path":"/vm",` +
		`"tls":"tls","sni":"jp.example.com","alpn":"h2","fp":"safari"}`
	link := "vmess://" + b64(raw)

	cfg, p := buildFromLink(t, link)
	if p.Name != "Tokyo" {
		t.Errorf("remark = %q", p.Name)
	}

	out := at(t, cfg, "outbounds", 0)
	eq(t, at(t, out, "protocol"), "vmess", "protocol")

	vnext := at(t, out, "settings", "vnext", 0)
	eq(t, at(t, vnext, "address"), "jp.example.com", "address")
	eq(t, at(t, vnext, "port"), float64(2053), "port")

	user := at(t, vnext, "users", 0)
	eq(t, at(t, user, "id"), "b831381d-6324-4d53-ad4f-8cda48b30811", "uuid")
	eq(t, at(t, user, "alterId"), float64(64), "alterId")
	eq(t, at(t, user, "security"), "chacha20-poly1305", "vmess security")

	ss := at(t, out, "streamSettings")
	eq(t, at(t, ss, "network"), "ws", "network")
	eq(t, at(t, ss, "wsSettings", "path"), "/vm", "ws path")
	eq(t, at(t, ss, "tlsSettings", "fingerprint"), "safari", "tls fingerprint")
}

// TestTrojanLinkFidelity checks that the password is placed where the trojan
// outbound expects it.
func TestTrojanLinkFidelity(t *testing.T) {
	link := "trojan://p%40ssw0rd%3Aspecial@tj.example.com:8443" +
		"?security=tls&type=ws&path=%2Ftj&host=tj.example.com&sni=tj.example.com#TJ"

	cfg, _ := buildFromLink(t, link)
	out := at(t, cfg, "outbounds", 0)

	eq(t, at(t, out, "protocol"), "trojan", "protocol")
	server := at(t, out, "settings", "servers", 0)
	eq(t, at(t, server, "address"), "tj.example.com", "address")
	eq(t, at(t, server, "port"), float64(8443), "port")
	// The password contained percent-encoded characters, including a colon.
	eq(t, at(t, server, "password"), "p@ssw0rd:special", "password")
}

// TestShadowsocksLinkFidelity checks the SIP002 form.
func TestShadowsocksLinkFidelity(t *testing.T) {
	link := "ss://" + b64url("2022-blake3-aes-256-gcm:Zm9vYmFyYmF6") +
		"@ss.example.com:8388#SS"

	cfg, _ := buildFromLink(t, link)
	server := at(t, cfg, "outbounds", 0, "settings", "servers", 0)

	eq(t, at(t, server, "method"), "2022-blake3-aes-256-gcm", "method")
	eq(t, at(t, server, "password"), "Zm9vYmFyYmF6", "password")
	eq(t, at(t, server, "port"), float64(8388), "port")
}

// TestLinkWithoutOptionalParamsOmitsThem guards the other direction: a minimal
// link must not gain fields the operator never specified, because an invented
// fingerprint or ALPN changes how the connection looks on the wire.
func TestLinkWithoutOptionalParamsOmitsThem(t *testing.T) {
	link := "vless://b831381d-6324-4d53-ad4f-8cda48b30811@plain.example.com:80" +
		"?encryption=none&type=tcp&security=none#Plain"

	cfg, _ := buildFromLink(t, link)
	ss, ok := at(t, cfg, "outbounds", 0, "streamSettings").(map[string]any)
	if !ok {
		t.Fatal("streamSettings missing")
	}

	for _, key := range []string{"tlsSettings", "realitySettings", "wsSettings",
		"grpcSettings", "httpSettings", "kcpSettings", "quicSettings",
		"xhttpSettings", "httpupgradeSettings"} {
		if _, has := ss[key]; has {
			t.Errorf("%s emitted for a plain TCP link", key)
		}
	}
	eq(t, ss["security"], "none", "security")

	// A plain TCP link with no header type must not gain a header block.
	if tcp, has := ss["tcpSettings"]; has {
		t.Errorf("tcpSettings emitted without a header type: %#v", tcp)
	}

	user := at(t, cfg, "outbounds", 0, "settings", "vnext", 0, "users", 0)
	if m, ok := user.(map[string]any); ok {
		if _, has := m["flow"]; has {
			t.Error("flow emitted for a link that did not set one")
		}
	}
}

// TestPortAndAddressNeverSwapped is a small guard against the classic mistake
// of reading the link's host and port into the wrong fields.
func TestPortAndAddressNeverSwapped(t *testing.T) {
	cfg, _ := buildFromLink(t,
		"vless://b831381d-6324-4d53-ad4f-8cda48b30811@10.20.30.40:12345"+
			"?encryption=none&type=tcp&security=none")
	vnext := at(t, cfg, "outbounds", 0, "settings", "vnext", 0)
	eq(t, at(t, vnext, "address"), "10.20.30.40", "address")
	eq(t, at(t, vnext, "port"), float64(12345), "port")
}

func b64(s string) string    { return base64.StdEncoding.EncodeToString([]byte(s)) }
func b64url(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
