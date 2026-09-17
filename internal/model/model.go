// Package model contains the data types shared across the daemon.
//
// The model deliberately stays transport-agnostic: a Profile describes an
// outbound server in terms that map cleanly onto Xray's outbound + streamSettings
// structures, without embedding any Xray JSON shapes here.
package model

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
)

// Proto is the outbound protocol of a profile.
type Proto string

const (
	ProtoVLESS       Proto = "vless"
	ProtoVMess       Proto = "vmess"
	ProtoTrojan      Proto = "trojan"
	ProtoShadowsocks Proto = "shadowsocks"
)

// Mode is how traffic is captured from the LAN.
//
// The choice comes down to one kernel limitation: NAT REDIRECT cannot carry
// UDP, because an application recovers the pre-NAT destination with
// SO_ORIGINAL_DST and that only exists for TCP. TPROXY has no such problem —
// it delivers the packet locally without rewriting it — but it needs the
// tproxy kernel module and a policy route. Hence the split.
type Mode string

const (
	// ModeRedirect captures TCP with NAT REDIRECT and leaves UDP alone.
	// The most compatible option: no extra kernel modules, no policy routing.
	// The cost is that QUIC and every other UDP protocol bypasses the proxy.
	ModeRedirect Mode = "redirect"

	// ModeMixed captures TCP with NAT REDIRECT and carries UDP through the TUN
	// device. It is the default because it needs no tproxy kernel module at
	// all: TCP stays on the simplest, most battle-tested kernel path, and UDP
	// still reaches the proxy, so QUIC neither leaks nor stalls. The price is
	// that UDP crosses a userspace IP stack and hev-socks5-tunnel has to be
	// installed.
	ModeMixed Mode = "mixed"

	// ModeTProxy captures both TCP and UDP with TPROXY. It keeps TCP out of
	// conntrack's NAT table, which is worth something on a busy router, at the
	// price of depending on TPROXY for everything rather than just for UDP.
	ModeTProxy Mode = "tproxy"

	// ModeTUN routes traffic into a TUN device served by hev-socks5-tunnel,
	// which forwards to the core's SOCKS inbound. Heaviest of the four, but it
	// handles every protocol uniformly and needs no firewall capture at all.
	ModeTUN Mode = "tun"
)

// Modes lists every capture mode, in the order the UI should present them.
func Modes() []Mode {
	return []Mode{ModeRedirect, ModeMixed, ModeTProxy, ModeTUN}
}

// Valid reports whether m is a known mode.
func (m Mode) Valid() bool {
	for _, k := range Modes() {
		if m == k {
			return true
		}
	}
	return false
}

// NeedsTProxy reports whether the mode depends on kernel TPROXY support.
func (m Mode) NeedsTProxy() bool {
	return m == ModeTProxy
}

// NeedsTunProcess reports whether hev-socks5-tunnel has to run. Mixed mode
// needs it for UDP only; TUN mode needs it for everything.
func (m Mode) NeedsTunProcess() bool {
	return m == ModeMixed || m == ModeTUN
}

// NeedsFirewallCapture reports whether the mode installs capture rules. TUN
// mode does not: policy routing carries the traffic instead.
func (m Mode) NeedsFirewallCapture() bool {
	return m != ModeTUN
}

// CapturesTCPViaRedirect reports whether TCP arrives through NAT REDIRECT,
// which decides both the firewall rule and whether the inbound serving it may
// carry the tproxy socket option.
func (m Mode) CapturesTCPViaRedirect() bool {
	return m == ModeRedirect || m == ModeMixed
}

// CapturesTCPViaTProxy reports whether TCP arrives through TPROXY.
func (m Mode) CapturesTCPViaTProxy() bool {
	return m == ModeTProxy
}

// CapturesUDP reports whether UDP is proxied at all. Only redirect mode leaves
// it going straight out.
func (m Mode) CapturesUDP() bool {
	return m == ModeMixed || m == ModeTProxy || m == ModeTUN
}

// CapturesUDPViaTProxy reports whether UDP reaches the core through a
// transparent inbound rather than through the tunnel.
func (m Mode) CapturesUDPViaTProxy() bool {
	return m == ModeTProxy
}

// MarksUDPForTun reports whether UDP has to be marked so policy routing sends
// it into the TUN device. Only mixed mode does this; full TUN mode routes
// everything there without needing a mark.
func (m Mode) MarksUDPForTun() bool {
	return m == ModeMixed
}

// Describe returns a one-line summary for logs and the UI.
func (m Mode) Describe() string {
	switch m {
	case ModeRedirect:
		return "TCP via NAT redirect, UDP not proxied"
	case ModeMixed:
		return "TCP via NAT redirect, UDP through the TUN device"
	case ModeTProxy:
		return "TCP and UDP via TPROXY"
	case ModeTUN:
		return "everything through a TUN device"
	default:
		return string(m)
	}
}

// DNSMode selects how client DNS queries are steered through the core.
type DNSMode string

const (
	// DNSDnsmasq points the system resolver at the core's DNS inbound. This is
	// the OpenWrt-native choice: local hostnames and DHCP leases keep working
	// because dnsmasq stays in the path.
	DNSDnsmasq DNSMode = "dnsmasq"
	// DNSRedirect hijacks port 53 in the firewall instead. Use it when dnsmasq
	// is not the resolver on the device.
	DNSRedirect DNSMode = "redirect"
	// DNSOff leaves DNS alone.
	DNSOff DNSMode = "off"
)

// Profile is a single outbound server.
type Profile struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Proto Proto  `json:"proto"`

	Address string `json:"address"`
	Port    int    `json:"port"`

	// Credentials. UUID is used by VLESS/VMess, Password by Trojan and
	// Shadowsocks, Method only by Shadowsocks.
	UUID     string `json:"uuid,omitempty"`
	Password string `json:"password,omitempty"`
	Method   string `json:"method,omitempty"`
	AlterID  int    `json:"alter_id,omitempty"`

	// VLESS specifics.
	Encryption string `json:"encryption,omitempty"`
	Flow       string `json:"flow,omitempty"`

	// Transport.
	Network     string `json:"network"`  // tcp, ws, grpc, h2, xhttp, kcp, quic
	Security    string `json:"security"` // none, tls, reality
	SNI         string `json:"sni,omitempty"`
	ALPN        string `json:"alpn,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`

	// REALITY.
	PublicKey string `json:"public_key,omitempty"`
	ShortID   string `json:"short_id,omitempty"`
	SpiderX   string `json:"spider_x,omitempty"`

	// AllowInsecure skips certificate verification. Newer cores have removed
	// it; PinnedCert is the replacement and takes precedence when both are set.
	AllowInsecure bool `json:"allow_insecure,omitempty"`
	// PinnedCert is the SHA-256 of the server's leaf certificate, as hex —
	// optionally in the colon-separated form openssl prints. It authenticates
	// that one certificate instead of trusting a CA, which is what makes a
	// self-signed server (or one behind a borrowed SNI) usable without
	// accepting any certificate at all. Populate it with `xwrt fetch-cert`.
	PinnedCert string `json:"pinned_cert,omitempty"`

	// Per-transport knobs.
	Path        string `json:"path,omitempty"`
	Host        string `json:"host,omitempty"`
	ServiceName string `json:"service_name,omitempty"`
	HeaderType  string `json:"header_type,omitempty"`
	Seed        string `json:"seed,omitempty"`
	QUICSec     string `json:"quic_security,omitempty"`
	QUICKey     string `json:"quic_key,omitempty"`

	Mux bool `json:"mux,omitempty"`

	// Subscription is the ID of the subscription this profile came from,
	// empty for manually added profiles.
	Subscription string `json:"subscription,omitempty"`

	Remark string `json:"remark,omitempty"`
}

// Label returns a human readable name, falling back to address:port.
func (p *Profile) Label() string {
	if p.Name != "" {
		return p.Name
	}
	return net.JoinHostPort(p.Address, strconv.Itoa(p.Port))
}

// Endpoint is the server host:port pair.
func (p *Profile) Endpoint() string {
	return net.JoinHostPort(p.Address, strconv.Itoa(p.Port))
}

// Validate reports whether the profile is complete enough to build a config.
func (p *Profile) Validate() error {
	if p.Address == "" {
		return errors.New("address is empty")
	}
	if p.Port <= 0 || p.Port > 65535 {
		return fmt.Errorf("invalid port %d", p.Port)
	}
	switch p.Proto {
	case ProtoVLESS:
		if p.UUID == "" {
			return errors.New("vless profile needs a uuid")
		}
	case ProtoVMess:
		if p.UUID == "" {
			return errors.New("vmess profile needs a uuid")
		}
	case ProtoTrojan:
		if p.Password == "" {
			return errors.New("trojan profile needs a password")
		}
	case ProtoShadowsocks:
		if p.Password == "" {
			return errors.New("shadowsocks profile needs a password")
		}
		if p.Method == "" {
			return errors.New("shadowsocks profile needs a method")
		}
	default:
		return fmt.Errorf("unsupported protocol %q", p.Proto)
	}
	switch p.Network {
	case "", "tcp", "ws", "grpc", "h2", "http", "xhttp", "kcp", "mkcp", "quic", "httpupgrade", "splithttp":
	default:
		return fmt.Errorf("unsupported network %q", p.Network)
	}
	switch p.Security {
	case "", "none", "tls", "reality":
	default:
		return fmt.Errorf("unsupported security %q", p.Security)
	}
	if p.Security == "reality" && p.PublicKey == "" {
		return errors.New("reality needs a public key")
	}
	return nil
}

// ALPNList splits the comma separated ALPN field.
func (p *Profile) ALPNList() []string {
	list := splitList(p.ALPN)
	// h3 only exists over QUIC. Share links routinely carry it alongside h2 on
	// a plain TCP profile — offering it there advertises a protocol this
	// connection cannot speak, and a server that picks it leaves both ends
	// waiting for the other to start.
	if p.Network != "" && p.Network != "quic" {
		out := list[:0]
		for _, a := range list {
			if a != "h3" {
				out = append(out, a)
			}
		}
		list = out
	}
	return list
}

// PinnedCertHex returns the pinned digest in the plain lowercase hex form the
// core expects, or "" when none is set or it is not a SHA-256 digest.
//
// Both spellings people actually have are accepted: the bare hex a fingerprint
// tool prints, and the AA:BB:CC form openssl prints. Anything else is ignored
// rather than passed through, because a malformed pin would make the core
// reject every certificate and the failure would look like a network problem.
func (p *Profile) PinnedCertHex() string {
	h := strings.ToLower(strings.TrimSpace(p.PinnedCert))
	h = strings.ReplaceAll(h, ":", "")
	h = strings.ReplaceAll(h, " ", "")
	if len(h) != 64 {
		return ""
	}
	for _, c := range h {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return ""
		}
	}
	return h
}

// PinnedCertValid reports whether a non-empty PinnedCert is usable, so a typo
// can be refused at the point it is entered rather than at connect time.
func (p *Profile) PinnedCertValid() bool {
	return strings.TrimSpace(p.PinnedCert) == "" || p.PinnedCertHex() != ""
}

// Strategy is how a group picks among its members.
type Strategy string

const (
	// StrategyLeastPing probes every member and uses the one answering
	// fastest. This is the only setting that gives real failover: a member
	// that stops answering is dropped from selection without a reconnect.
	StrategyLeastPing Strategy = "leastPing"
	// StrategyLeastLoad samples latency repeatedly and prefers the member
	// with the steadiest results. Also health-aware, at the cost of more
	// probing.
	StrategyLeastLoad Strategy = "leastLoad"
	// StrategyRandom spreads connections at random. No health awareness: a
	// dead member keeps getting its share of connections.
	StrategyRandom Strategy = "random"
	// StrategyRoundRobin cycles through members in order, with the same
	// caveat as random.
	StrategyRoundRobin Strategy = "roundRobin"
)

// Strategies lists every strategy, health-aware ones first.
func Strategies() []Strategy {
	return []Strategy{StrategyLeastPing, StrategyLeastLoad, StrategyRandom, StrategyRoundRobin}
}

// Valid reports whether st is a known strategy.
func (st Strategy) Valid() bool {
	for _, k := range Strategies() {
		if st == k {
			return true
		}
	}
	return false
}

// HealthAware reports whether the strategy reacts to a member going down.
// Only these give failover; the others merely spread load.
func (st Strategy) HealthAware() bool {
	return st == StrategyLeastPing || st == StrategyLeastLoad
}

// Describe returns a one-line summary for the UI and logs.
func (st Strategy) Describe() string {
	switch st {
	case StrategyLeastPing:
		return "use the fastest responding server, drop ones that stop answering"
	case StrategyLeastLoad:
		return "use the server with the steadiest latency"
	case StrategyRandom:
		return "spread connections at random, no failover"
	case StrategyRoundRobin:
		return "cycle through members in order, no failover"
	default:
		return string(st)
	}
}

// Group is a set of profiles used together, with a strategy deciding which
// member carries a given connection.
type Group struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Strategy Strategy `json:"strategy"`
	// Members holds profile IDs, in the order the user arranged them.
	Members []string `json:"members"`

	// ProbeURL and ProbeInterval drive the health checks the health-aware
	// strategies depend on.
	ProbeURL      string `json:"probe_url,omitempty"`
	ProbeInterval string `json:"probe_interval,omitempty"`
}

// Label returns a human readable name.
func (g *Group) Label() string {
	if g.Name != "" {
		return g.Name
	}
	return g.ID
}

// Normalize fills in defaults.
func (g *Group) Normalize() {
	if !g.Strategy.Valid() {
		g.Strategy = StrategyLeastPing
	}
	if g.ProbeURL == "" {
		g.ProbeURL = DefaultProbeURL
	}
	if g.ProbeInterval == "" {
		g.ProbeInterval = DefaultProbeInterval
	}
}

// Validate reports whether the group can be connected to.
func (g *Group) Validate() error {
	if len(g.Members) == 0 {
		return errors.New("group has no members")
	}
	if !g.Strategy.Valid() {
		return fmt.Errorf("unknown strategy %q", g.Strategy)
	}
	return nil
}

// Defaults for group health checking. A 204 endpoint is used because it
// returns no body, which keeps the probe cheap on a metered link.
const (
	DefaultProbeURL = "https://www.gstatic.com/generate_204"
	// How often each member is probed. This is also how long a dead server
	// keeps being used: the balancer picks from the last measurement, so
	// nothing changes until the next probe marks the member unhealthy. Three
	// minutes — the value this started with — means up to three minutes with
	// no internet on a home line, which is not failover as anyone means it.
	//
	// The probe is one request whose response body is empty, per member, so a
	// minute costs nothing worth counting even on the 128 MB floor.
	DefaultProbeInterval = "60s"
)

// TargetKind says whether the active selection is a single server or a group.
type TargetKind string

const (
	TargetProfile TargetKind = "profile"
	TargetGroup   TargetKind = "group"
)

// Subscription is a remote list of profiles.
type Subscription struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	URL     string `json:"url"`
	Updated string `json:"updated,omitempty"`
	Count   int    `json:"count"`
}

// Settings is the global daemon configuration, mirroring /etc/config/xwrt.
type Settings struct {
	Enabled bool   `json:"enabled"`
	Mode    Mode   `json:"mode"`
	Active  string `json:"active"`
	// ActiveKind says whether Active names a profile or a group.
	ActiveKind TargetKind `json:"active_kind"`

	SocksPort int `json:"socks_port"`
	HTTPPort  int `json:"http_port"`
	// TProxPort serves the transparent inbound: TCP only in redirect and mixed
	// modes, TCP and UDP in tproxy mode.
	TProxPort int `json:"tproxy_port"`
	DNSPort   int `json:"dns_port"`
	APIPort   int `json:"api_port"`
	StatsPort int `json:"stats_port"`

	DNS      string  `json:"dns"`
	DNSMode  DNSMode `json:"dns_mode"`
	LogLevel string  `json:"log_level"`

	AllowLAN    bool `json:"allow_lan"`
	AutoConnect bool `json:"auto_connect"`
	IPv6        bool `json:"ipv6"`
	// ProxyRouter also captures traffic originating on the router itself,
	// not just forwarded LAN traffic.
	ProxyRouter bool `json:"proxy_router"`

	XrayBin string `json:"xray_bin"`
	HevBin  string `json:"hev_bin"`
	RunDir  string `json:"run_dir"`

	// TUN mode parameters.
	TunName string `json:"tun_name"`
	TunAddr string `json:"tun_addr"`
	TunMask string `json:"tun_mask"`
	TunMTU  int    `json:"tun_mtu"`

	FwMark     string `json:"fwmark"`
	RouteTable int    `json:"route_table"`

	// LANDevice / WANDevice override auto-detection when non-empty. Leaving
	// them empty is the supported default: the daemon detects them at runtime,
	// which is what makes this portable across OpenWrt devices.
	LANDevice string `json:"lan_device"`
	WANDevice string `json:"wan_device"`

	// UpdateCheck lets the daemon ask, once a day, whether a newer release
	// exists. Nothing is ever installed without someone asking for it; this
	// only decides whether the question is asked at all, for people who would
	// rather their router talked to nobody.
	UpdateCheck bool `json:"update_check"`
	// UpdateRepo is where it looks: an owner/name repository whose releases
	// carry the bundles and a sha256sums file. Configurable because a fork
	// should be able to update from its own releases rather than from ours.
	UpdateRepo string `json:"update_repo"`

	// BypassIP holds extra CIDRs that must never go through the proxy.
	BypassIP []string `json:"bypass_ip"`
	// BypassMAC holds LAN client MACs excluded from transparent proxying.
	BypassMAC []string `json:"bypass_mac"`
}

// DefaultUpdateRepo is the project's own releases. A build that ships from
// somewhere else sets update_repo and never touches this.
const DefaultUpdateRepo = "ozgunokan/xDivine-Ray"

// Defaults returns the built-in settings used when /etc/config/xwrt is absent
// or incomplete.
func Defaults() Settings {
	return Settings{
		Enabled:   false,
		Mode:      ModeMixed,
		SocksPort: 10808,
		HTTPPort:  10809,
		TProxPort: 12345,
		DNSPort:   15353,
		APIPort:   8787,
		StatsPort: 10085,
		DNS:       "1.1.1.1",
		DNSMode:   DNSDnsmasq,
		LogLevel:  "warning",
		AllowLAN:  true,
		// On, so a device that comes up with no configuration file still
		// reconnects by itself; the shipped configuration says the same. It is
		// only consulted when there is no file at all — Normalize does not
		// touch booleans, because a false it filled in would be
		// indistinguishable from a false the operator chose, and a switch that
		// turns itself back on is worse than no switch.
		AutoConnect: true,
		UpdateCheck: true,
		UpdateRepo:  DefaultUpdateRepo,
		IPv6:        false,
		XrayBin:     "xray",
		HevBin:      "hev-socks5-tunnel",
		RunDir:      "/var/run/xwrt",
		TunName:     "xwrt0",
		TunAddr:     "198.18.0.1",
		TunMask:     "255.255.0.0",
		TunMTU:      8500,
		FwMark:      "0x1e0",
		RouteTable:  180,
	}
}

// Normalize fills in zero values with defaults so a partially written UCI file
// still produces a usable configuration.
func (s *Settings) Normalize() {
	d := Defaults()
	if !s.Mode.Valid() {
		s.Mode = d.Mode
	}
	if s.ActiveKind != TargetGroup {
		s.ActiveKind = TargetProfile
	}
	if s.SocksPort == 0 {
		s.SocksPort = d.SocksPort
	}
	if s.HTTPPort == 0 {
		s.HTTPPort = d.HTTPPort
	}
	if s.TProxPort == 0 {
		s.TProxPort = d.TProxPort
	}
	if s.DNSPort == 0 {
		s.DNSPort = d.DNSPort
	}
	if s.APIPort == 0 {
		s.APIPort = d.APIPort
	}
	if s.StatsPort == 0 {
		s.StatsPort = d.StatsPort
	}
	if s.DNS == "" {
		s.DNS = d.DNS
	}
	switch s.DNSMode {
	case DNSDnsmasq, DNSRedirect, DNSOff:
	default:
		s.DNSMode = d.DNSMode
	}
	if s.LogLevel == "" {
		s.LogLevel = d.LogLevel
	}
	if s.XrayBin == "" {
		s.XrayBin = d.XrayBin
	}
	if s.HevBin == "" {
		s.HevBin = d.HevBin
	}
	if s.RunDir == "" {
		s.RunDir = d.RunDir
	}
	if s.TunName == "" {
		s.TunName = d.TunName
	}
	if s.TunAddr == "" {
		s.TunAddr = d.TunAddr
	}
	if s.TunMask == "" {
		s.TunMask = d.TunMask
	}
	if s.TunMTU == 0 {
		s.TunMTU = d.TunMTU
	}
	if s.FwMark == "" {
		s.FwMark = d.FwMark
	}
	if s.RouteTable == 0 {
		s.RouteTable = d.RouteTable
	}
}

// MarkValue parses FwMark, which may be written as decimal or with an 0x
// prefix, into the integer the core's sockopt expects.
func (s *Settings) MarkValue() int {
	v, err := strconv.ParseInt(strings.TrimSpace(s.FwMark), 0, 32)
	if err != nil || v <= 0 {
		d, _ := strconv.ParseInt(Defaults().FwMark, 0, 32)
		return int(d)
	}
	return int(v)
}

// MarkHex renders the firewall mark in the 0x form nft and ip rule expect.
func (s *Settings) MarkHex() string {
	return fmt.Sprintf("0x%x", s.MarkValue())
}

// Three marks are derived from the one configured base so an operator has a
// single knob, and so the three can never be confused with each other:
//
//	base+0  set by the core on its own sockets, so its upstream traffic is
//	        never captured again
//	base+1  set on TPROXY'd packets, to steer them into the local table
//	base+2  set on UDP in mixed mode, to steer it into the TUN table
func (s *Settings) MarkTProxy() int { return s.MarkValue() + 1 }
func (s *Settings) MarkTunUDP() int { return s.MarkValue() + 2 }
func (s *Settings) MarkTProxyHex() string {
	return fmt.Sprintf("0x%x", s.MarkTProxy())
}
func (s *Settings) MarkTunUDPHex() string {
	return fmt.Sprintf("0x%x", s.MarkTunUDP())
}

// Stats is a traffic counter snapshot.
type Stats struct {
	Uplink       int64 `json:"uplink"`
	Downlink     int64 `json:"downlink"`
	UplinkRate   int64 `json:"uplink_rate"`
	DownlinkRate int64 `json:"downlink_rate"`
}

// Status is the runtime state reported to clients.
type Status struct {
	Connected bool `json:"connected"`
	Mode      Mode `json:"mode"`
	// TargetKind says whether the daemon is on a single server or a group;
	// ProfileID/ProfileName name whichever it is.
	TargetKind    TargetKind `json:"target_kind,omitempty"`
	ProfileID     string     `json:"profile_id,omitempty"`
	ProfileName   string     `json:"profile_name,omitempty"`
	GroupStrategy string     `json:"group_strategy,omitempty"`
	GroupMembers  int        `json:"group_members,omitempty"`
	Since         string     `json:"since,omitempty"`
	UptimeSeconds int64      `json:"uptime_seconds"`
	CoreRunning   bool       `json:"core_running"`
	TunRunning    bool       `json:"tun_running,omitempty"`
	LANDevice     string     `json:"lan_device,omitempty"`
	WANDevice     string     `json:"wan_device,omitempty"`
	WANGateway    string     `json:"wan_gateway,omitempty"`
	Firewall      string     `json:"firewall,omitempty"`
	// LastError carries the most recent failure with the step it happened in,
	// so a client can say what went wrong without fetching the log. It is
	// cleared by a successful connect.
	LastError *LastError `json:"last_error,omitempty"`
	Stats     Stats      `json:"stats"`
	// Version is the build the *running* daemon came from, which is not always
	// the build on disk: upgrading replaces the binary but the old process
	// keeps serving until it is restarted. Reporting it here is what makes
	// that visible instead of mysterious.
	Version string `json:"version,omitempty"`
	// CoreVersion is the proxy core the daemon probed at connect time.
	CoreVersion string `json:"core_version,omitempty"`
	// AutoConnect is a setting rather than runtime state, and it is here
	// because it answers a question about right now: whether this device comes
	// back on its own after a power cut, or waits for someone to log in. On a
	// line with no way out except the tunnel, that is the difference between a
	// blip and an evening.
	AutoConnect bool `json:"auto_connect"`

	// UpdateAvailable and UpdateVersion are what the daemon last learned from
	// the release page. They are in the status because every page polls it
	// already, and an update nobody is told about is an update nobody installs.
	UpdateAvailable bool   `json:"update_available,omitempty"`
	UpdateVersion   string `json:"update_version,omitempty"`
}

func splitList(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// SameServer reports whether two profiles describe the same server.
//
// Identity deliberately excludes the name. Providers rename their nodes
// constantly — load percentages, flags, expiry dates in the label — and a
// refresh that treated a rename as a different server would throw away
// everything the operator attached to it.
//
// It also excludes anything cosmetic: what makes two entries the same server
// is where they connect and who they log in as.
func SameServer(a, b *Profile) bool {
	return a.Proto == b.Proto &&
		a.Address == b.Address &&
		a.Port == b.Port &&
		a.UUID == b.UUID &&
		a.Password == b.Password &&
		a.Path == b.Path &&
		a.SNI == b.SNI
}

// MergeSubscription reconciles what a subscription just returned with what is
// already stored for it.
//
// Replacing the lot is the obvious implementation and the wrong one: every
// profile would get a new id, and an id is what a group's membership and the
// active selection are made of. A subscription refreshed on a schedule would
// quietly empty every group it feeds. Anything the operator attached to a
// server by hand — a pinned certificate above all — would go with it.
//
// So a server that is still in the list keeps its id and its pinned
// certificate, and takes everything else from the new listing. A server that
// is gone from the listing is dropped, which is the point of a subscription.
// Profiles from elsewhere are untouched.
func MergeSubscription(stored, fetched []Profile, subID string, newID func() string) []Profile {
	out := make([]Profile, 0, len(stored)+len(fetched))
	var mine []Profile
	for _, p := range stored {
		if p.Subscription == subID {
			mine = append(mine, p)
			continue
		}
		out = append(out, p)
	}

	used := make([]bool, len(mine))
	for i := range fetched {
		p := fetched[i]
		p.Subscription = subID
		for j := range mine {
			if used[j] || !SameServer(&mine[j], &p) {
				continue
			}
			used[j] = true
			p.ID = mine[j].ID
			// The certificate was pinned against this server by hand, and the
			// listing never carries one. Keeping it is the difference between
			// a refresh and a reset.
			if p.PinnedCert == "" {
				p.PinnedCert = mine[j].PinnedCert
			}
			break
		}
		if p.ID == "" {
			p.ID = newID()
		}
		out = append(out, p)
	}
	return out
}

// PruneMembers drops member ids that no longer name a profile, which is what a
// subscription that stopped listing a server leaves behind.
func PruneMembers(groups []Group, profiles []Profile) []Group {
	alive := make(map[string]bool, len(profiles))
	for i := range profiles {
		alive[profiles[i].ID] = true
	}
	for i := range groups {
		kept := groups[i].Members[:0]
		for _, id := range groups[i].Members {
			if alive[id] {
				kept = append(kept, id)
			}
		}
		groups[i].Members = kept
	}
	return groups
}
