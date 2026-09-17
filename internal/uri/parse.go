// Package uri parses share links (vless://, vmess://, trojan://, ss://) into
// profiles.
package uri

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"xwrt/internal/model"
)

// ErrUnsupported is returned for a scheme this package does not handle.
var ErrUnsupported = errors.New("unsupported share link scheme")

// Parse converts a single share link into a profile. The returned profile has
// no ID; the caller assigns one when storing it.
func Parse(raw string) (*model.Profile, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("empty link")
	}
	switch {
	case strings.HasPrefix(raw, "vless://"):
		return parseVLESS(raw)
	case strings.HasPrefix(raw, "vmess://"):
		return parseVMess(raw)
	case strings.HasPrefix(raw, "trojan://"):
		return parseTrojan(raw)
	case strings.HasPrefix(raw, "ss://"):
		return parseShadowsocks(raw)
	default:
		return nil, fmt.Errorf("%w: %.16s", ErrUnsupported, raw)
	}
}

// ParseMany parses a newline separated list, skipping blank lines and
// comments. Links that fail to parse are reported but do not abort the batch,
// since one bad entry in a subscription should not discard the rest.
func ParseMany(body string) ([]model.Profile, []error) {
	var out []model.Profile
	var errs []error
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		p, err := Parse(line)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		out = append(out, *p)
	}
	return out, errs
}

func parseVLESS(raw string) (*model.Profile, error) {
	u, fragName, err := parseURL(raw)
	if err != nil {
		return nil, fmt.Errorf("vless: %w", err)
	}
	host, port, err := hostPort(u)
	if err != nil {
		return nil, fmt.Errorf("vless: %w", err)
	}
	if u.User == nil || u.User.Username() == "" {
		return nil, errors.New("vless: missing uuid")
	}
	q := u.Query()
	p := &model.Profile{
		Proto:      model.ProtoVLESS,
		Address:    host,
		Port:       port,
		UUID:       u.User.Username(),
		Encryption: firstNonEmpty(q.Get("encryption"), "none"),
		Flow:       q.Get("flow"),
		Name:       fragName,
	}
	applyTransport(p, q)
	return p, nil
}

// vmessJSON is the widely used v2rayN share format.
type vmessJSON struct {
	V    any    `json:"v"`
	PS   string `json:"ps"`
	Add  string `json:"add"`
	Port any    `json:"port"`
	ID   string `json:"id"`
	Aid  any    `json:"aid"`
	Scy  string `json:"scy"`
	Net  string `json:"net"`
	Type string `json:"type"`
	Host string `json:"host"`
	Path string `json:"path"`
	TLS  string `json:"tls"`
	SNI  string `json:"sni"`
	ALPN string `json:"alpn"`
	FP   string `json:"fp"`
}

func parseVMess(raw string) (*model.Profile, error) {
	payload := strings.TrimPrefix(raw, "vmess://")
	// Some producers emit a URL-shaped vmess link instead of base64 JSON.
	if strings.Contains(payload, "@") {
		return parseVMessURL(raw)
	}
	decoded, err := decodeBase64(payload)
	if err != nil {
		return nil, fmt.Errorf("vmess: %w", err)
	}
	var v vmessJSON
	if err := json.Unmarshal([]byte(decoded), &v); err != nil {
		return nil, fmt.Errorf("vmess: %w", err)
	}
	port, err := anyToInt(v.Port)
	if err != nil {
		return nil, fmt.Errorf("vmess: port: %w", err)
	}
	aid, _ := anyToInt(v.Aid)
	security := "none"
	if strings.EqualFold(v.TLS, "tls") {
		security = "tls"
	} else if strings.EqualFold(v.TLS, "reality") {
		security = "reality"
	}
	p := &model.Profile{
		Proto:       model.ProtoVMess,
		Name:        v.PS,
		Address:     v.Add,
		Port:        port,
		UUID:        v.ID,
		AlterID:     aid,
		Encryption:  firstNonEmpty(v.Scy, "auto"),
		Network:     normalizeNetwork(firstNonEmpty(v.Net, "tcp")),
		Security:    security,
		SNI:         firstNonEmpty(v.SNI, v.Host),
		ALPN:        v.ALPN,
		Fingerprint: v.FP,
		Host:        v.Host,
		Path:        v.Path,
		HeaderType:  v.Type,
	}
	// For gRPC the "path" field carries the service name.
	if p.Network == "grpc" && p.ServiceName == "" {
		p.ServiceName = v.Path
	}
	return p, nil
}

func parseVMessURL(raw string) (*model.Profile, error) {
	u, fragName, err := parseURL(raw)
	if err != nil {
		return nil, fmt.Errorf("vmess: %w", err)
	}
	host, port, err := hostPort(u)
	if err != nil {
		return nil, fmt.Errorf("vmess: %w", err)
	}
	q := u.Query()
	p := &model.Profile{
		Proto:      model.ProtoVMess,
		Address:    host,
		Port:       port,
		UUID:       u.User.Username(),
		Encryption: firstNonEmpty(q.Get("encryption"), "auto"),
		Name:       fragName,
	}
	applyTransport(p, q)
	return p, nil
}

func parseTrojan(raw string) (*model.Profile, error) {
	u, fragName, err := parseURL(raw)
	if err != nil {
		return nil, fmt.Errorf("trojan: %w", err)
	}
	host, port, err := hostPort(u)
	if err != nil {
		return nil, fmt.Errorf("trojan: %w", err)
	}
	if u.User == nil || u.User.Username() == "" {
		return nil, errors.New("trojan: missing password")
	}
	pass := u.User.Username()
	if pw, ok := u.User.Password(); ok && pw != "" {
		// Rare form trojan://user:pass@host — treat the pair as the password.
		pass = pass + ":" + pw
	}
	q := u.Query()
	p := &model.Profile{
		Proto:    model.ProtoTrojan,
		Address:  host,
		Port:     port,
		Password: pass,
		Name:     fragName,
	}
	applyTransport(p, q)
	if p.Security == "none" {
		// Trojan is TLS by definition unless explicitly told otherwise.
		p.Security = "tls"
	}
	return p, nil
}

func parseShadowsocks(raw string) (*model.Profile, error) {
	body := strings.TrimPrefix(raw, "ss://")
	name := ""
	if i := strings.IndexByte(body, '#'); i >= 0 {
		name = decodeFragment(body[i+1:])
		body = body[:i]
	}
	if i := strings.IndexByte(body, '?'); i >= 0 {
		body = body[:i]
	}

	var method, password, host string
	var port int

	if at := strings.LastIndexByte(body, '@'); at >= 0 {
		// SIP002: ss://base64url(method:password)@host:port
		creds, err := decodeBase64(body[:at])
		if err != nil {
			// Some links carry plain method:password.
			creds = body[:at]
			if unescaped, uerr := url.QueryUnescape(creds); uerr == nil {
				creds = unescaped
			}
		}
		m, pw, ok := strings.Cut(creds, ":")
		if !ok {
			return nil, errors.New("ss: malformed credentials")
		}
		method, password = m, pw
		h, prt, err := splitHostPort(body[at+1:])
		if err != nil {
			return nil, fmt.Errorf("ss: %w", err)
		}
		host, port = h, prt
	} else {
		// Legacy: ss://base64(method:password@host:port)
		decoded, err := decodeBase64(body)
		if err != nil {
			return nil, fmt.Errorf("ss: %w", err)
		}
		at := strings.LastIndexByte(decoded, '@')
		if at < 0 {
			return nil, errors.New("ss: malformed link")
		}
		m, pw, ok := strings.Cut(decoded[:at], ":")
		if !ok {
			return nil, errors.New("ss: malformed credentials")
		}
		method, password = m, pw
		h, prt, err := splitHostPort(decoded[at+1:])
		if err != nil {
			return nil, fmt.Errorf("ss: %w", err)
		}
		host, port = h, prt
	}

	return &model.Profile{
		Proto:    model.ProtoShadowsocks,
		Name:     name,
		Address:  host,
		Port:     port,
		Method:   method,
		Password: password,
		Network:  "tcp",
		Security: "none",
	}, nil
}

// applyTransport fills the transport related fields from the query string,
// which is shared by the vless/trojan/vmess URL forms.
func applyTransport(p *model.Profile, q url.Values) {
	p.Network = normalizeNetwork(firstNonEmpty(q.Get("type"), "tcp"))
	p.Security = firstNonEmpty(q.Get("security"), "none")
	p.SNI = firstNonEmpty(q.Get("sni"), q.Get("peer"))
	p.ALPN = q.Get("alpn")
	p.Fingerprint = q.Get("fp")
	p.PublicKey = q.Get("pbk")
	p.ShortID = q.Get("sid")
	p.SpiderX = q.Get("spx")
	p.Path = q.Get("path")
	p.Host = firstNonEmpty(q.Get("host"), q.Get("obfsParam"))
	p.ServiceName = q.Get("serviceName")
	p.HeaderType = q.Get("headerType")
	p.Seed = q.Get("seed")
	p.QUICSec = q.Get("quicSecurity")
	p.QUICKey = q.Get("key")
	if v := q.Get("allowInsecure"); v == "1" || strings.EqualFold(v, "true") {
		p.AllowInsecure = true
	}
	if p.SNI == "" && p.Host != "" {
		p.SNI = p.Host
	}
	if p.Network == "grpc" && p.ServiceName == "" && p.Path != "" {
		p.ServiceName = strings.TrimPrefix(p.Path, "/")
	}
}

func normalizeNetwork(n string) string {
	switch strings.ToLower(strings.TrimSpace(n)) {
	case "", "tcp", "raw":
		return "tcp"
	case "ws", "websocket":
		return "ws"
	case "h2", "http":
		return "h2"
	case "grpc", "gun":
		return "grpc"
	case "kcp", "mkcp":
		return "kcp"
	case "quic":
		return "quic"
	case "xhttp", "splithttp":
		return "xhttp"
	case "httpupgrade":
		return "httpupgrade"
	default:
		return strings.ToLower(n)
	}
}

func hostPort(u *url.URL) (string, int, error) {
	host := u.Hostname()
	if host == "" {
		return "", 0, errors.New("missing host")
	}
	portStr := u.Port()
	if portStr == "" {
		return "", 0, errors.New("missing port")
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return "", 0, fmt.Errorf("invalid port %q", portStr)
	}
	return host, port, nil
}

func splitHostPort(s string) (string, int, error) {
	h, p, err := net.SplitHostPort(s)
	if err != nil {
		return "", 0, err
	}
	port, err := strconv.Atoi(p)
	if err != nil || port <= 0 || port > 65535 {
		return "", 0, fmt.Errorf("invalid port %q", p)
	}
	return h, port, nil
}

// DecodeBase64 exposes the lenient base64 decoder used for share links, for
// callers that need to unwrap a base64 encoded subscription body.
func DecodeBase64(s string) (string, error) { return decodeBase64(s) }

// decodeBase64 accepts padded and unpadded, standard and URL-safe alphabets,
// because share links in the wild use all four combinations.
func decodeBase64(s string) (string, error) {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\n", "")
	s = strings.ReplaceAll(s, "\r", "")
	encodings := []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	}
	var lastErr error
	for _, enc := range encodings {
		if b, err := enc.DecodeString(s); err == nil {
			return string(b), nil
		} else {
			lastErr = err
		}
	}
	return "", fmt.Errorf("base64: %w", lastErr)
}

// parseURL is url.Parse with the display name kept out of it.
//
// The fragment of a share link is a label written by a person or generated by
// a panel: "🇹🇷 TR | 45%", "Kalan %80", "node #2". Go's parser rejects the
// whole URL when that text contains a stray percent sign, because % starts an
// escape — so a subscription silently loses every node whose name mentions a
// percentage, and a load percentage in the name is what half the providers
// put there. The name is split off first and decoded leniently; what is left
// is a URL, and the parser can have it.
func parseURL(raw string) (*url.URL, string, error) {
	rest, frag := raw, ""
	if i := strings.IndexByte(raw, '#'); i >= 0 {
		rest, frag = raw[:i], raw[i+1:]
	}
	u, err := url.Parse(rest)
	if err != nil {
		return nil, "", err
	}
	return u, decodeFragment(frag), nil
}

// decodeFragment turns the label back into text, and knows when not to.
//
// "%20" in a name is an escaped space and should become one. "%80 kalan" is a
// person writing a percentage, and decoding it yields the byte 0x80 — not a
// character, just broken text where a name should be. The rule that separates
// them is whether the result is still valid UTF-8; if it is not, nothing was
// escaped and the label is taken literally. PathUnescape rather than
// QueryUnescape, so a "+" in a name stays a plus instead of turning into a
// space.
func decodeFragment(f string) string {
	if f == "" {
		return ""
	}
	if s, err := url.PathUnescape(f); err == nil && utf8.ValidString(s) {
		return s
	}
	return f
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func anyToInt(v any) (int, error) {
	switch t := v.(type) {
	case float64:
		return int(t), nil
	case int:
		return t, nil
	case string:
		if t == "" {
			return 0, nil
		}
		return strconv.Atoi(t)
	case nil:
		return 0, nil
	default:
		return 0, fmt.Errorf("unexpected numeric type %T", v)
	}
}
