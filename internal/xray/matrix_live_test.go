package xray

// Protocol matrix, against a real core, over a real socket.
//
// Every other test in this package reads the configuration we generate and
// checks that it says what we meant. That is worth having, but it cannot
// answer the only question that matters to an operator: does a connection of
// this kind actually carry data? A field name the core silently ignores, a
// setting that moved between releases, a transport the core builds but never
// completes a handshake on — all of those produce a configuration that looks
// perfect and a tunnel that stays empty.
//
// So this test runs the real core twice: once as a server with an inbound of
// the kind under test, once as the client we configure from a share link. It
// then fetches a page through the client's SOCKS port and insists on reading
// the bytes the server-side target wrote. Nothing is asserted about the JSON;
// the assertion is the payload.
//
// It is opt-in because it needs the core binary and a few free ports:
//
//	XWRT_LIVE_CORE=1 XRAY_BIN=/path/to/xray go test ./internal/xray/ -run Matrix -v
//
// A failing row here means that protocol does not work for a user, whatever
// the unit tests say.

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"xwrt/internal/model"
	"xwrt/internal/uri"
)

const (
	testUUID     = "cbe82522-0000-4000-8000-000000000001"
	testPassword = "bir-parola-yeter"
	testBody     = "xwrt-ok"
	testSNI      = "test.local"
)

// --- helpers -----------------------------------------------------------------

func liveCoreBin(t *testing.T) string {
	t.Helper()
	if os.Getenv("XWRT_LIVE_CORE") != "1" {
		t.Skip("set XWRT_LIVE_CORE=1 (and XRAY_BIN) to run the protocol matrix")
	}
	bin := os.Getenv("XRAY_BIN")
	if bin == "" {
		bin = "xray"
	}
	p, err := exec.LookPath(bin)
	if err != nil {
		t.Skipf("core binary not found: %v", err)
	}
	return p
}

// freePort asks the kernel for a port and gives it straight back. There is a
// window between the two, which is why every port is used once and the test
// does not retry: a flaky port is easier to see than to hide.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("no free port: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// selfSigned returns a certificate for name, the files the core wants, and the
// hex SHA-256 of the leaf — the same digest `xwrt fetch-cert` stores, so the
// pinning path is exercised here exactly as a user exercises it.
func selfSigned(t *testing.T, dir, name string) (certFile, keyFile, sha string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: name},
		DNSNames:              []string{name},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("cert: %v", err)
	}
	sum := sha256.Sum256(der)
	sha = hex.EncodeToString(sum[:])

	certFile = filepath.Join(dir, name+".crt")
	keyFile = filepath.Join(dir, name+".key")
	kder, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("key marshal: %v", err)
	}
	write := func(path, typ string, b []byte) {
		if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: b}), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	write(certFile, "CERTIFICATE", der)
	write(keyFile, "EC PRIVATE KEY", kder)
	return certFile, keyFile, sha
}

// testNetIP is the address the far end listens on, and the choice is the
// single most important line in this file.
//
// The obvious thing is to put the target on 127.0.0.1. It does not work, and
// it fails in the worst possible way: silently, by passing. The generated
// config sends every private range straight out through `direct`, loopback
// included, so a client pointed at 127.0.0.1 never touches the outbound under
// test — it reaches the target the short way and reports success for a tunnel
// that was never used. The control test caught exactly that.
//
// 203.0.113.0/24 is TEST-NET-3: routable as far as any routing table is
// concerned, reserved by the RFC so it can never be a real destination, and
// absent from the private list. Bound to loopback it is reachable by both
// cores while still being, to the router's rules, the open internet.
const testNetIP = "203.0.113.1"

func ensureTestNet(t *testing.T) {
	t.Helper()
	if l, err := net.Listen("tcp", testNetIP+":0"); err == nil {
		l.Close()
		return
	}
	out, err := exec.Command("ip", "addr", "add", testNetIP+"/32", "dev", "lo").CombinedOutput()
	if err != nil && !strings.Contains(string(out), "File exists") {
		t.Skipf("cannot put %s on loopback (needs root): %v %s", testNetIP, err, out)
	}
}

// target is the thing at the far end. It listens on plain HTTP and writes one
// short body; reading that body through a tunnel is the whole assertion.
func startTarget(t *testing.T) int {
	t.Helper()
	ensureTestNet(t)
	l, err := net.Listen("tcp", testNetIP+":0")
	if err != nil {
		t.Fatalf("target: %v", err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, testBody)
	})}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })
	return l.Addr().(*net.TCPAddr).Port
}

// startCore writes cfg and runs the core with it. Output is kept and only
// printed when the row fails, where it is usually the whole explanation.
func startCore(t *testing.T, bin, dir, name string, cfg any) *strings.Builder {
	t.Helper()
	blob, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("%s config: %v", name, err)
	}
	path := filepath.Join(dir, name+".json")
	if err := os.WriteFile(path, blob, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	out := &strings.Builder{}
	cmd := exec.Command(bin, "run", "-c", path)
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Start(); err != nil {
		t.Fatalf("start %s: %v", name, err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			cmd.Process.Kill()
			cmd.Wait()
		}
	})
	return out
}

func waitPort(port int, d time.Duration) error {
	deadline := time.Now().Add(d)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 300*time.Millisecond)
		if err == nil {
			c.Close()
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("nothing listening on %s after %s", addr, d)
}

// socksDial is a minimal SOCKS5 client. The project is stdlib-only and that
// rule holds in tests too; a CONNECT handshake is forty lines.
func socksDial(socksPort int, host string, port int) (net.Conn, error) {
	c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", socksPort), 3*time.Second)
	if err != nil {
		return nil, err
	}
	c.SetDeadline(time.Now().Add(20 * time.Second))
	if _, err := c.Write([]byte{5, 1, 0}); err != nil {
		c.Close()
		return nil, err
	}
	hello := make([]byte, 2)
	if _, err := io.ReadFull(c, hello); err != nil {
		c.Close()
		return nil, fmt.Errorf("socks greeting: %w", err)
	}
	if hello[0] != 5 || hello[1] != 0 {
		c.Close()
		return nil, fmt.Errorf("socks refused auth: %v", hello)
	}
	req := []byte{5, 1, 0, 3, byte(len(host))}
	req = append(req, host...)
	req = binary.BigEndian.AppendUint16(req, uint16(port))
	if _, err := c.Write(req); err != nil {
		c.Close()
		return nil, err
	}
	head := make([]byte, 4)
	if _, err := io.ReadFull(c, head); err != nil {
		c.Close()
		return nil, fmt.Errorf("socks reply: %w", err)
	}
	if head[1] != 0 {
		c.Close()
		return nil, fmt.Errorf("socks connect failed, code %d", head[1])
	}
	var skip int
	switch head[3] {
	case 1:
		skip = 4
	case 3:
		n := make([]byte, 1)
		if _, err := io.ReadFull(c, n); err != nil {
			c.Close()
			return nil, err
		}
		skip = int(n[0])
	case 4:
		skip = 16
	}
	if _, err := io.ReadFull(c, make([]byte, skip+2)); err != nil {
		c.Close()
		return nil, err
	}
	c.SetDeadline(time.Now().Add(20 * time.Second))
	return c, nil
}

// fetchThrough asks the target for its one page, through the tunnel.
func fetchThrough(socksPort, targetPort int) (string, error) {
	c, err := socksDial(socksPort, testNetIP, targetPort)
	if err != nil {
		return "", err
	}
	defer c.Close()
	fmt.Fprintf(c, "GET / HTTP/1.0\r\nHost: %s\r\nConnection: close\r\n\r\n", testNetIP)
	body, err := io.ReadAll(c)
	if err != nil {
		return "", err
	}
	s := string(body)
	if i := strings.Index(s, "\r\n\r\n"); i >= 0 {
		s = s[i+4:]
	}
	return strings.TrimSpace(s), nil
}

// clientSettings is a client with no firewall and no DNS of its own: TUN mode
// generates exactly the SOCKS inbound this test needs and nothing privileged.
func clientSettings(socks, stats int) *model.Settings {
	return &model.Settings{
		Mode:      model.ModeTUN,
		DNSMode:   model.DNSOff,
		SocksPort: socks,
		StatsPort: stats,
		DNS:       "1.1.1.1",
		LogLevel:  "warning",
	}
}

// --- the matrix --------------------------------------------------------------

type matrixCase struct {
	name string
	// server builds the inbound under test, given the port and the cert files.
	server func(port int, certFile, keyFile string, extra map[string]any) map[string]any
	// link builds the share link a user would paste for that inbound.
	link func(port int) string
	// pin says whether the client needs our self-signed leaf pinned.
	pin bool
	// udp marks a transport whose server listens on UDP, where a TCP probe
	// would report "not up" forever.
	udp bool
	// wantBuildErr marks a transport the installed core has removed. The row
	// then asserts the refusal and its wording instead of a data path.
	wantBuildErr string
	// prep runs before either core starts, for cases that need key material.
	prep func(t *testing.T, bin, dir string, extra map[string]any)
}

func inbound(port int, proto string, settings, stream map[string]any) map[string]any {
	return map[string]any{
		"listen":         "127.0.0.1",
		"port":           port,
		"protocol":       proto,
		"settings":       settings,
		"streamSettings": stream,
	}
}

func tlsStream(network string, certFile, keyFile string, transport map[string]any) map[string]any {
	s := map[string]any{
		"network":  network,
		"security": "tls",
		"tlsSettings": map[string]any{
			"serverName": testSNI,
			"certificates": []map[string]any{
				{"certificateFile": certFile, "keyFile": keyFile},
			},
		},
	}
	for k, v := range transport {
		s[k] = v
	}
	return s
}

func vlessClients(flow string) map[string]any {
	c := map[string]any{"id": testUUID}
	if flow != "" {
		c["flow"] = flow
	}
	return map[string]any{"clients": []map[string]any{c}, "decryption": "none"}
}

func TestMatrixProtocolsCarryData(t *testing.T) {
	bin := liveCoreBin(t)
	dir := t.TempDir()
	certFile, keyFile, sha := selfSigned(t, dir, testSNI)
	targetPort := startTarget(t)

	cases := []matrixCase{
		{
			name: "vless-tcp-tls",
			server: func(p int, cf, kf string, _ map[string]any) map[string]any {
				return inbound(p, "vless", vlessClients(""), tlsStream("tcp", cf, kf, nil))
			},
			link: func(p int) string {
				return fmt.Sprintf("vless://%s@127.0.0.1:%d?type=tcp&encryption=none&security=tls&sni=%s#tcp-tls",
					testUUID, p, testSNI)
			},
			pin: true,
		},
		{
			name: "vless-ws-tls",
			server: func(p int, cf, kf string, _ map[string]any) map[string]any {
				return inbound(p, "vless", vlessClients(""), tlsStream("ws", cf, kf,
					map[string]any{"wsSettings": map[string]any{"path": "/ws"}}))
			},
			link: func(p int) string {
				return fmt.Sprintf("vless://%s@127.0.0.1:%d?type=ws&path=%%2Fws&encryption=none&security=tls&sni=%s#ws-tls",
					testUUID, p, testSNI)
			},
			pin: true,
		},
		{
			name: "vless-grpc-tls",
			server: func(p int, cf, kf string, _ map[string]any) map[string]any {
				return inbound(p, "vless", vlessClients(""), tlsStream("grpc", cf, kf,
					map[string]any{"grpcSettings": map[string]any{"serviceName": "gun"}}))
			},
			link: func(p int) string {
				return fmt.Sprintf("vless://%s@127.0.0.1:%d?type=grpc&serviceName=gun&encryption=none&security=tls&sni=%s#grpc-tls",
					testUUID, p, testSNI)
			},
			pin: true,
		},
		{
			name: "vless-xhttp-tls",
			server: func(p int, cf, kf string, _ map[string]any) map[string]any {
				return inbound(p, "vless", vlessClients(""), tlsStream("xhttp", cf, kf,
					map[string]any{"xhttpSettings": map[string]any{"path": "/xh"}}))
			},
			link: func(p int) string {
				return fmt.Sprintf("vless://%s@127.0.0.1:%d?type=xhttp&path=%%2Fxh&encryption=none&security=tls&sni=%s#xhttp-tls",
					testUUID, p, testSNI)
			},
			pin: true,
		},
		{
			name: "vless-httpupgrade-tls",
			server: func(p int, cf, kf string, _ map[string]any) map[string]any {
				return inbound(p, "vless", vlessClients(""), tlsStream("httpupgrade", cf, kf,
					map[string]any{"httpupgradeSettings": map[string]any{"path": "/hu"}}))
			},
			link: func(p int) string {
				return fmt.Sprintf("vless://%s@127.0.0.1:%d?type=httpupgrade&path=%%2Fhu&encryption=none&security=tls&sni=%s#hu-tls",
					testUUID, p, testSNI)
			},
			pin: true,
		},
		{
			// HTTP/2 as its own transport is gone from the core we ship: it
			// was folded into XHTTP. A share link that still says type=http
			// therefore cannot work, and the only useful thing we can do is
			// say so in one sentence the operator can act on.
			name: "vless-h2-tls-removed",
			link: func(p int) string {
				return fmt.Sprintf("vless://%s@127.0.0.1:%d?type=http&path=%%2Fh2&host=%s&encryption=none&security=tls&sni=%s#h2-tls",
					testUUID, p, testSNI, testSNI)
			},
			wantBuildErr: "XHTTP",
		},
		{
			// Same story one layer down: mKCP survives, its obfuscation
			// header and seed do not. A link carrying them is refused; a
			// plain mKCP link is not, and the row below proves it still runs.
			name: "vless-mkcp-seed-removed",
			link: func(p int) string {
				return fmt.Sprintf("vless://%s@127.0.0.1:%d?type=kcp&seed=tohum&headerType=none&encryption=none&security=none#mkcp-seed",
					testUUID, p)
			},
			wantBuildErr: "seed",
		},
		{
			// QUIC went the same way as HTTP/2. Old subscription lists still
			// carry type=quic links years after the core stopped speaking it.
			name: "vless-quic-removed",
			link: func(p int) string {
				return fmt.Sprintf("vless://%s@127.0.0.1:%d?type=quic&quicSecurity=none&encryption=none&security=tls&sni=%s#quic",
					testUUID, p, testSNI)
			},
			wantBuildErr: "QUIC",
		},
		{
			// Plain mKCP carries its payload over UDP, and it is the one row
			// here that proves the UDP path end to end.
			name: "vless-mkcp",
			server: func(p int, cf, kf string, _ map[string]any) map[string]any {
				return inbound(p, "vless", vlessClients(""), map[string]any{
					"network":     "kcp",
					"kcpSettings": map[string]any{"congestion": false},
				})
			},
			link: func(p int) string {
				return fmt.Sprintf("vless://%s@127.0.0.1:%d?type=kcp&encryption=none&security=none#mkcp",
					testUUID, p)
			},
			udp: true,
		},
		{
			name: "vless-reality-vision",
			prep: prepReality,
			server: func(p int, cf, kf string, extra map[string]any) map[string]any {
				return inbound(p, "vless", vlessClients("xtls-rprx-vision"), map[string]any{
					"network":  "tcp",
					"security": "reality",
					"realitySettings": map[string]any{
						"dest":        extra["dest"],
						"serverNames": []string{testSNI},
						"privateKey":  extra["private"],
						"shortIds":    []string{"0123456789abcdef"},
					},
				})
			},
			link: func(p int) string { return "" }, // built in prep, needs the public key
		},
		{
			name: "vmess-tcp",
			server: func(p int, cf, kf string, _ map[string]any) map[string]any {
				return inbound(p, "vmess",
					map[string]any{"clients": []map[string]any{{"id": testUUID, "alterId": 0}}},
					map[string]any{"network": "tcp"})
			},
			link: func(p int) string {
				blob, _ := json.Marshal(map[string]any{
					"v": "2", "ps": "vmess-tcp", "add": "127.0.0.1", "port": fmt.Sprint(p),
					"id": testUUID, "aid": "0", "net": "tcp", "type": "none", "tls": "",
				})
				return "vmess://" + base64.StdEncoding.EncodeToString(blob)
			},
		},
		{
			name: "vmess-ws-tls",
			server: func(p int, cf, kf string, _ map[string]any) map[string]any {
				return inbound(p, "vmess",
					map[string]any{"clients": []map[string]any{{"id": testUUID, "alterId": 0}}},
					tlsStream("ws", cf, kf, map[string]any{"wsSettings": map[string]any{"path": "/vw"}}))
			},
			link: func(p int) string {
				blob, _ := json.Marshal(map[string]any{
					"v": "2", "ps": "vmess-ws", "add": "127.0.0.1", "port": fmt.Sprint(p),
					"id": testUUID, "aid": "0", "net": "ws", "path": "/vw",
					"tls": "tls", "sni": testSNI,
				})
				return "vmess://" + base64.StdEncoding.EncodeToString(blob)
			},
			pin: true,
		},
		{
			name: "trojan-tcp-tls",
			server: func(p int, cf, kf string, _ map[string]any) map[string]any {
				return inbound(p, "trojan",
					map[string]any{"clients": []map[string]any{{"password": testPassword}}},
					tlsStream("tcp", cf, kf, nil))
			},
			link: func(p int) string {
				return fmt.Sprintf("trojan://%s@127.0.0.1:%d?security=tls&type=tcp&sni=%s#trojan",
					testPassword, p, testSNI)
			},
			pin: true,
		},
		{
			name: "shadowsocks-aes-gcm",
			server: func(p int, cf, kf string, _ map[string]any) map[string]any {
				return inbound(p, "shadowsocks",
					map[string]any{"method": "aes-128-gcm", "password": testPassword, "network": "tcp,udp"},
					map[string]any{"network": "tcp"})
			},
			link: func(p int) string {
				u := base64.RawURLEncoding.EncodeToString([]byte("aes-128-gcm:" + testPassword))
				return fmt.Sprintf("ss://%s@127.0.0.1:%d#ss", u, p)
			},
		},
	}

	// The real capabilities of the binary under test, not the optimistic
	// assumption. This is what the daemon uses at connect time, and it is the
	// difference between the two kinds of row below: one carries data, the
	// other is expected to be refused before a process is ever started.
	caps := Probe(bin)
	t.Logf("core %s: h2=%v quic=%v kcp-seed=%v pinned-cert=%v",
		caps.Version, caps.H2Transport, caps.QUICTransport, caps.KCPHeaderSeed, caps.PinnedCert)

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			extra := map[string]any{}
			if tc.prep != nil {
				tc.prep(t, bin, dir, extra)
			}

			// Rows the installed core cannot serve at all. What is under test
			// here is the refusal: it has to happen while building the config,
			// with a sentence naming the transport, rather than as a core that
			// dies on startup with a stack trace the operator has to decode.
			if tc.wantBuildErr != "" {
				p, err := uri.Parse(tc.link(443))
				if err != nil {
					t.Fatalf("share link did not parse: %v", err)
				}
				p.ID = "p1"
				_, err = Build(Options{Profile: p, Caps: caps,
					Settings: clientSettings(freePort(t), freePort(t))})
				if err == nil {
					t.Fatalf("core %s cannot serve this transport, but the config "+
						"was built anyway — the operator would see the core die instead",
						caps.Version)
				}
				if !strings.Contains(err.Error(), tc.wantBuildErr) {
					t.Fatalf("refused, but not in words that help:\n  got: %v\n  want a mention of %q",
						err, tc.wantBuildErr)
				}
				return
			}

			srvPort := freePort(t)
			srvCfg := map[string]any{
				"log":       map[string]any{"loglevel": "warning"},
				"inbounds":  []map[string]any{tc.server(srvPort, certFile, keyFile, extra)},
				"outbounds": []map[string]any{{"protocol": "freedom"}},
			}
			srvOut := startCore(t, bin, dir, tc.name+"-server", srvCfg)
			if tc.udp {
				time.Sleep(1500 * time.Millisecond)
			} else if err := waitPort(srvPort, 8*time.Second); err != nil {
				t.Fatalf("server never came up: %v\n%s", err, srvOut.String())
			}

			raw := tc.link(srvPort)
			if f, ok := extra["link"].(func(int) string); ok {
				raw = f(srvPort)
			}
			p, err := uri.Parse(raw)
			if err != nil {
				t.Fatalf("share link did not parse: %v\nlink: %s", err, raw)
			}
			if tc.pin {
				p.PinnedCert = sha
			}
			p.ID = "p1"

			socks, stats := freePort(t), freePort(t)
			cfg, err := Build(Options{Profile: p, Caps: caps,
				Settings: clientSettings(socks, stats)})
			if err != nil {
				t.Fatalf("client config: %v", err)
			}
			cliOut := startCore(t, bin, dir, tc.name+"-client", cfg)
			if err := waitPort(socks, 8*time.Second); err != nil {
				t.Fatalf("client never came up: %v\n%s", err, cliOut.String())
			}

			body, err := fetchThrough(socks, targetPort)
			if err != nil || body != testBody {
				t.Fatalf("no data through the tunnel: err=%v body=%q\n--- client ---\n%s\n--- server ---\n%s",
					err, body, cliOut.String(), srvOut.String())
			}
		})
	}
}

// TestMatrixHarnessCanFail is the control. A test that cannot fail proves
// nothing, and this harness has a specific way of lying: if the client's
// routing ever sent traffic out directly instead of through the outbound we
// built, every row above would still pass — the target is on this machine and
// a direct connection reaches it too. So here the server is told a different
// UUID than the client uses. The handshake must fail and no body may arrive.
// If this test ever passes a body through, the matrix above is measuring the
// loopback interface rather than the tunnel.
func TestMatrixHarnessCanFail(t *testing.T) {
	bin := liveCoreBin(t)
	dir := t.TempDir()
	certFile, keyFile, sha := selfSigned(t, dir, testSNI)
	targetPort := startTarget(t)

	srvPort := freePort(t)
	srvCfg := map[string]any{
		"log": map[string]any{"loglevel": "warning"},
		"inbounds": []map[string]any{inbound(srvPort, "vless",
			map[string]any{
				"clients":    []map[string]any{{"id": "11111111-2222-3333-4444-555555555555"}},
				"decryption": "none",
			},
			tlsStream("tcp", certFile, keyFile, nil))},
		"outbounds": []map[string]any{{"protocol": "freedom"}},
	}
	srvOut := startCore(t, bin, dir, "control-server", srvCfg)
	if err := waitPort(srvPort, 8*time.Second); err != nil {
		t.Fatalf("server never came up: %v\n%s", err, srvOut.String())
	}

	p, err := uri.Parse(fmt.Sprintf(
		"vless://%s@127.0.0.1:%d?type=tcp&encryption=none&security=tls&sni=%s#control",
		testUUID, srvPort, testSNI))
	if err != nil {
		t.Fatalf("link: %v", err)
	}
	p.PinnedCert = sha
	p.ID = "p1"

	socks, stats := freePort(t), freePort(t)
	cfg, err := Build(Options{Profile: p, Settings: clientSettings(socks, stats)})
	if err != nil {
		t.Fatalf("client config: %v", err)
	}
	startCore(t, bin, dir, "control-client", cfg)
	if err := waitPort(socks, 8*time.Second); err != nil {
		t.Fatalf("client never came up: %v", err)
	}

	if body, err := fetchThrough(socks, targetPort); err == nil && body == testBody {
		t.Fatal("a wrong credential still delivered the page: the matrix is not " +
			"measuring the tunnel, it is reaching the target some other way")
	}
}

// prepReality generates the REALITY key pair with the core's own tool and
// stands up the TLS server the inbound borrows its handshake from. Pointing
// dest at a server we run keeps the test offline: REALITY needs a real TLS
// peer there, not a real internet.
func prepReality(t *testing.T, bin, dir string, extra map[string]any) {
	out, err := exec.Command(bin, "x25519").Output()
	if err != nil {
		t.Skipf("core cannot generate x25519 keys: %v", err)
	}
	var priv, pub string
	for _, line := range strings.Split(string(out), "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		// The label has moved between releases: "PublicKey" became
		// "Password (PublicKey)" when the same value started doubling as the
		// encryption password. Match on the words, not on the exact string.
		key := strings.ToLower(strings.TrimSpace(k))
		switch {
		case strings.Contains(key, "private"):
			priv = v
		case strings.Contains(key, "public"), key == "password":
			pub = v
		}
	}
	if priv == "" || pub == "" {
		t.Skipf("could not read a key pair out of: %s", out)
	}

	certFile, keyFile, _ := selfSigned(t, dir, testSNI+".dest")
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		t.Fatalf("dest cert: %v", err)
	}
	l, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
	if err != nil {
		t.Fatalf("dest listener: %v", err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "dest")
	})}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })

	extra["dest"] = l.Addr().String()
	extra["private"] = priv
	extra["link"] = func(p int) string {
		return fmt.Sprintf("vless://%s@127.0.0.1:%d?type=tcp&encryption=none&security=reality"+
			"&sni=%s&pbk=%s&sid=0123456789abcdef&fp=chrome&flow=xtls-rprx-vision#reality",
			testUUID, p, testSNI, pub)
	}
}
