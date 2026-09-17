// Package xray builds Xray-core configuration from a profile.
//
// The generated config is deliberately conservative: LAN and private
// destinations are already excluded by the firewall before traffic reaches the
// core, so the routing section only needs a defence-in-depth direct rule and a
// DNS rule. That keeps `domainStrategy` at AsIs and avoids the extra lookups a
// router with 128 MB of RAM would rather not do.
package xray

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"xwrt/internal/model"
)

// Tags used in the generated configuration.
const (
	TagProxy = "proxy"
	// TagBalancer routes through a group rather than a single server. Member
	// outbounds are tagged with TagProxyPrefix + index, which is also what the
	// balancer's selector and the observatory match on.
	TagBalancer    = "balancer"
	TagProxyPrefix = "proxy-"
	TagDirect      = "direct"
	TagBlock       = "block"
	TagDNSOut      = "dns-out"
	TagAPI         = "api"
	TagAPIIn       = "api-in"
	TagSocksIn     = "socks-in"
	TagHTTPIn      = "http-in"
	TagTransparent = "transparent-in"
	TagDNSIn       = "dns-in"
)

// Options drive config generation.
type Options struct {
	// Exactly one of Profile or Group is used. With a Group, Members holds the
	// resolved member profiles in order.
	Profile  *model.Profile
	Group    *model.Group
	Members  []model.Profile
	Settings *model.Settings

	// DirectCIDRs never go through the proxy.
	DirectCIDRs []string

	// LocalResolvers are the DNS servers this device uses when nothing is
	// tunnelled — the ones the upstream link handed it. A domain the operator
	// bypasses has to be resolved by those, so the "direct" connection is made
	// to an address chosen for this part of the world.
	//
	// They cannot be replaced by the shorthand "localhost". The system resolver
	// on OpenWrt is dnsmasq, and in the recommended DNS mode dnsmasq's own
	// upstream is this core: asking localhost would send the query straight
	// back where it came from, and the loop takes every other name down with
	// it — the whole device looks like it lost its connection.
	LocalResolvers []string
	// Rules are the operator's exception list, evaluated in order.
	Rules []model.Rule

	// Caps says which optional features the installed core accepts. The zero
	// value means "not probed"; AllFeatures is then assumed, so a detection
	// failure never blocks a connect that would have worked.
	Caps Capabilities

	// Resolve turns the server's hostname into an address, once, at build time.
	//
	// This matters more than it looks. The core dials the server for every new
	// connection it proxies, and with a hostname there it asks the system
	// resolver every single time — musl caches nothing, and a router's resolver
	// is the least reliable thing on the path. One slow or filtered answer
	// becomes one slow connection, which is what "most pages are fine but some
	// hang for a second" actually is. Resolving here means the name is looked
	// up once per connect instead of once per connection.
	//
	// It returns "" to leave the hostname in place, which is also what a nil
	// Resolve does. The TLS server name is unaffected either way: it keeps
	// coming from the profile, so certificates and pins still match.
	Resolve func(host string) string
}

// Config is the root Xray configuration object.
type Config struct {
	Log              *LogConfig        `json:"log,omitempty"`
	API              *APIConfig        `json:"api,omitempty"`
	Stats            *struct{}         `json:"stats,omitempty"`
	Policy           *PolicyConfig     `json:"policy,omitempty"`
	DNS              *DNSConfig        `json:"dns,omitempty"`
	Observatory      *Observatory      `json:"observatory,omitempty"`
	BurstObservatory *BurstObservatory `json:"burstObservatory,omitempty"`
	Inbounds         []Inbound         `json:"inbounds"`
	Outbounds        []Outbound        `json:"outbounds"`
	Routing          *RoutingConfig    `json:"routing,omitempty"`
}

// Observatory drives the leastPing strategy: it probes each member and the
// balancer uses the results.
type Observatory struct {
	SubjectSelector []string `json:"subjectSelector"`
	ProbeURL        string   `json:"probeURL"`
	ProbeInterval   string   `json:"probeInterval"`
}

// BurstObservatory drives leastLoad, sampling latency several times per round.
type BurstObservatory struct {
	SubjectSelector []string    `json:"subjectSelector"`
	PingConfig      *PingConfig `json:"pingConfig"`
}

type PingConfig struct {
	Destination string `json:"destination"`
	Interval    string `json:"interval"`
	Timeout     string `json:"timeout"`
	Sampling    int    `json:"sampling"`
}

// Balancer picks among the outbounds its selector matches.
type Balancer struct {
	Tag      string            `json:"tag"`
	Selector []string          `json:"selector"`
	Strategy *BalancerStrategy `json:"strategy,omitempty"`
	// FallbackTag is used when no member is healthy. It needs an observatory
	// to be meaningful, so it is only emitted with a health-aware strategy.
	FallbackTag string `json:"fallbackTag,omitempty"`
}

type BalancerStrategy struct {
	Type string `json:"type"`
}

// accessLog decides whether the core writes a line per connection.
//
// It is off unless the log level is info or debug, and that is a deliberate
// trade against the core's own default. The access log is one line per
// connection on a shared line that is hundreds of lines a minute, and every one
// of them goes through our log ring — which would push the entry explaining a
// failure out of the buffer within seconds of it happening, and out of the
// core's own output tail even faster. Someone who wants to see which sites are
// being visited sets the log level to info and gets it back.
func accessLog(level string) string {
	switch level {
	case "debug", "info":
		return ""
	default:
		return "none"
	}
}

type LogConfig struct {
	LogLevel string `json:"loglevel"`
	Access   string `json:"access,omitempty"`
	Error    string `json:"error,omitempty"`
}

type APIConfig struct {
	Tag      string   `json:"tag"`
	Services []string `json:"services"`
}

type PolicyConfig struct {
	System *SystemPolicy           `json:"system,omitempty"`
	Levels map[string]*LevelPolicy `json:"levels,omitempty"`
}

type SystemPolicy struct {
	StatsOutboundUplink   bool `json:"statsOutboundUplink"`
	StatsOutboundDownlink bool `json:"statsOutboundDownlink"`
}

// LevelPolicy holds the per-connection timeouts. Only connIdle is set here;
// the rest keep the core's defaults.
type LevelPolicy struct {
	ConnIdle int `json:"connIdle,omitempty"`
}

// connIdleSeconds is how long a connection may carry no data before the core
// closes it. The core's own default is 300 seconds, which is wrong for a
// general-purpose VPN: an SSH session left alone for five minutes is killed,
// and because the transparent proxy terminates the client's TCP locally, the
// client is told the *remote side* closed the connection. That sends whoever
// is debugging it to the server, or to their ISP, and never here.
//
// Half an hour is long enough for a terminal someone walked away from and
// short enough that abandoned connections still get cleaned up. Idle
// connections cost a socket and a small buffer, which is affordable even on
// the 128 MB floor this targets.
const connIdleSeconds = 1800

type DNSConfig struct {
	Servers       []any  `json:"servers"`
	QueryStrategy string `json:"queryStrategy,omitempty"`
	DisableCache  bool   `json:"disableCache,omitempty"`
	Tag           string `json:"tag,omitempty"`
}

// DNSServer is the object form of a DNS server entry, used to scope a
// resolver to a set of domains.
type DNSServer struct {
	Address string   `json:"address"`
	Port    int      `json:"port,omitempty"`
	Domains []string `json:"domains,omitempty"`
}

type Inbound struct {
	Tag            string          `json:"tag"`
	Listen         string          `json:"listen,omitempty"`
	Port           int             `json:"port"`
	Protocol       string          `json:"protocol"`
	Settings       any             `json:"settings,omitempty"`
	StreamSettings *StreamSettings `json:"streamSettings,omitempty"`
	Sniffing       *Sniffing       `json:"sniffing,omitempty"`
}

type Sniffing struct {
	Enabled      bool     `json:"enabled"`
	DestOverride []string `json:"destOverride,omitempty"`
	RouteOnly    bool     `json:"routeOnly,omitempty"`
}

type Outbound struct {
	Tag            string          `json:"tag"`
	Protocol       string          `json:"protocol"`
	Settings       any             `json:"settings,omitempty"`
	StreamSettings *StreamSettings `json:"streamSettings,omitempty"`
	Mux            *MuxConfig      `json:"mux,omitempty"`
}

// MuxConfig carries two independent things that share one object in Xray's
// schema: multiplexing for TCP, and XUDP, which is how UDP is carried at all.
//
// Concurrency -1 turns TCP multiplexing off while leaving the object in place.
// That combination is the point: XTLS Vision is designed around one connection
// per stream and multiplexing TCP on top of it costs throughput, but UDP needs
// XUDP to be carried properly — so UDP gets it and TCP does not.
type MuxConfig struct {
	Enabled     bool `json:"enabled"`
	Concurrency int  `json:"concurrency"`
	// XUDPConcurrency enables XUDP for UDP flows.
	XUDPConcurrency int `json:"xudpConcurrency,omitempty"`
	// XUDPProxyUDP443 decides what happens to UDP port 443 — QUIC. Xray's own
	// default is "reject", which silently drops exactly the traffic a browser
	// uses for video and leaves it waiting out its own timeout on every one.
	// Nothing here wants that: UDP is either carried or it is not.
	XUDPProxyUDP443 string `json:"xudpProxyUDP443,omitempty"`
}

type StreamSettings struct {
	Network  string `json:"network,omitempty"`
	Security string `json:"security,omitempty"`

	TLSSettings     *TLSSettings     `json:"tlsSettings,omitempty"`
	RealitySettings *RealitySettings `json:"realitySettings,omitempty"`

	TCPSettings         *TCPSettings         `json:"tcpSettings,omitempty"`
	WSSettings          *WSSettings          `json:"wsSettings,omitempty"`
	GRPCSettings        *GRPCSettings        `json:"grpcSettings,omitempty"`
	HTTPSettings        *HTTPSettings        `json:"httpSettings,omitempty"`
	KCPSettings         *KCPSettings         `json:"kcpSettings,omitempty"`
	QUICSettings        *QUICSettings        `json:"quicSettings,omitempty"`
	XHTTPSettings       *XHTTPSettings       `json:"xhttpSettings,omitempty"`
	HTTPUpgradeSettings *HTTPUpgradeSettings `json:"httpupgradeSettings,omitempty"`

	Sockopt *Sockopt `json:"sockopt,omitempty"`
}

type Sockopt struct {
	Mark           int    `json:"mark,omitempty"`
	TProxy         string `json:"tproxy,omitempty"`
	TCPFastOpen    *bool  `json:"tcpFastOpen,omitempty"`
	DomainStrategy string `json:"domainStrategy,omitempty"`
}

type TLSSettings struct {
	ServerName    string   `json:"serverName,omitempty"`
	AllowInsecure bool     `json:"allowInsecure,omitempty"`
	Fingerprint   string   `json:"fingerprint,omitempty"`
	ALPN          []string `json:"alpn,omitempty"`
	// PinnedPeerCertSha256 is the hex SHA-256 of the server's leaf
	// certificate, and it is what actually replaced allowInsecure: setting it
	// makes the core accept that one certificate and skip both chain and
	// hostname verification.
	//
	// A single string, not a list — the core's own field type. Do not confuse
	// it with pinnedPeerCertificateChainSha256, which still exists, takes a
	// list of base64 chain digests, and does *not* relax verification: with a
	// self-signed certificate that field alone fails with "certificate signed
	// by unknown authority". Verified against a real core, both ways round.
	PinnedPeerCertSha256 string `json:"pinnedPeerCertSha256,omitempty"`
}

type RealitySettings struct {
	ServerName  string `json:"serverName,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	PublicKey   string `json:"publicKey"`
	ShortID     string `json:"shortId,omitempty"`
	SpiderX     string `json:"spiderX,omitempty"`
}

type TCPSettings struct {
	Header *Header `json:"header,omitempty"`
}

type Header struct {
	Type    string `json:"type"`
	Request any    `json:"request,omitempty"`
}

type WSSettings struct {
	Path    string            `json:"path,omitempty"`
	Host    string            `json:"host,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

type GRPCSettings struct {
	ServiceName string `json:"serviceName,omitempty"`
	MultiMode   bool   `json:"multiMode,omitempty"`
	Authority   string `json:"authority,omitempty"`
}

type HTTPSettings struct {
	Path string   `json:"path,omitempty"`
	Host []string `json:"host,omitempty"`
}

type KCPSettings struct {
	MTU              int     `json:"mtu,omitempty"`
	TTI              int     `json:"tti,omitempty"`
	UplinkCapacity   int     `json:"uplinkCapacity,omitempty"`
	DownlinkCapacity int     `json:"downlinkCapacity,omitempty"`
	Congestion       bool    `json:"congestion,omitempty"`
	ReadBufferSize   int     `json:"readBufferSize,omitempty"`
	WriteBufferSize  int     `json:"writeBufferSize,omitempty"`
	Header           *Header `json:"header,omitempty"`
	Seed             string  `json:"seed,omitempty"`
}

type QUICSettings struct {
	Security string  `json:"security,omitempty"`
	Key      string  `json:"key,omitempty"`
	Header   *Header `json:"header,omitempty"`
}

type XHTTPSettings struct {
	Path string `json:"path,omitempty"`
	Host string `json:"host,omitempty"`
	Mode string `json:"mode,omitempty"`
}

type HTTPUpgradeSettings struct {
	Path string `json:"path,omitempty"`
	Host string `json:"host,omitempty"`
}

type RoutingConfig struct {
	DomainStrategy string     `json:"domainStrategy,omitempty"`
	Balancers      []Balancer `json:"balancers,omitempty"`
	Rules          []Rule     `json:"rules"`
}

type Rule struct {
	Type        string   `json:"type"`
	InboundTag  []string `json:"inboundTag,omitempty"`
	OutboundTag string   `json:"outboundTag,omitempty"`
	BalancerTag string   `json:"balancerTag,omitempty"`
	IP          []string `json:"ip,omitempty"`
	Domain      []string `json:"domain,omitempty"`
	Source      []string `json:"source,omitempty"`
	Port        string   `json:"port,omitempty"`
	SourcePort  string   `json:"sourcePort,omitempty"`
	Network     string   `json:"network,omitempty"`
	Protocol    []string `json:"protocol,omitempty"`
}

// privateCIDRs are never sent through the proxy. Plain CIDRs are used rather
// than `geoip:private` so the config works on an install without the geo data
// files; when those files are present, geoip:private is added on top by
// Build, which also covers ranges this list does not.
var privateCIDRs = []string{
	"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8",
	"169.254.0.0/16", "172.16.0.0/12", "192.0.0.0/24", "192.168.0.0/16",
	"198.18.0.0/15", "224.0.0.0/4", "240.0.0.0/4", "255.255.255.255/32",
	"::1/128", "fc00::/7", "fe80::/10",
}

// Build produces the Xray configuration for a profile.
func Build(o Options) (*Config, error) {
	if o.Settings == nil {
		return nil, fmt.Errorf("no settings given")
	}
	if o.Profile == nil && o.Group == nil {
		return nil, fmt.Errorf("no profile or group given")
	}
	if o.Profile != nil {
		if err := o.Profile.Validate(); err != nil {
			return nil, err
		}
	}
	if o.Group != nil {
		if err := o.Group.Validate(); err != nil {
			return nil, err
		}
		if len(o.Members) == 0 {
			return nil, fmt.Errorf("group %s has no usable members: "+
				"every profile it referenced is gone", o.Group.Label())
		}
	}
	s := o.Settings
	mark := s.MarkValue()
	caps := o.Caps
	if !caps.Probed {
		caps = AllFeatures()
	}

	cfg := &Config{
		Log:   &LogConfig{LogLevel: s.LogLevel, Access: accessLog(s.LogLevel)},
		Stats: &struct{}{},
		API:   &APIConfig{Tag: TagAPI, Services: []string{"StatsService"}},
		Policy: &PolicyConfig{
			System: &SystemPolicy{
				StatsOutboundUplink:   true,
				StatsOutboundDownlink: true,
			},
			Levels: map[string]*LevelPolicy{
				"0": {ConnIdle: connIdleSeconds},
			},
		},
	}

	listen := "127.0.0.1"
	if s.AllowLAN {
		listen = "0.0.0.0"
	}

	// Local API inbound, used by `xray api statsquery` for traffic counters.
	cfg.Inbounds = append(cfg.Inbounds, Inbound{
		Tag:      TagAPIIn,
		Listen:   "127.0.0.1",
		Port:     s.StatsPort,
		Protocol: "dokodemo-door",
		Settings: map[string]any{"address": "127.0.0.1"},
	})

	// SOCKS and HTTP inbounds. TUN mode feeds the SOCKS inbound; they are also
	// useful on their own for clients that want an explicit proxy.
	cfg.Inbounds = append(cfg.Inbounds, Inbound{
		Tag:      TagSocksIn,
		Listen:   listen,
		Port:     s.SocksPort,
		Protocol: "socks",
		Settings: map[string]any{"auth": "noauth", "udp": true, "ip": "127.0.0.1"},
		Sniffing: sniffingFor(s),
	})
	if s.HTTPPort > 0 {
		cfg.Inbounds = append(cfg.Inbounds, Inbound{
			Tag:      TagHTTPIn,
			Listen:   listen,
			Port:     s.HTTPPort,
			Protocol: "http",
			Sniffing: sniffingFor(s),
		})
	}

	// The transparent inbound exists for the firewall capture modes. Its shape
	// follows directly from how packets reach it:
	//
	//   redirect  TCP arrives NAT-rewritten, recovered with SO_ORIGINAL_DST,
	//             so followRedirect alone; no tproxy socket option.
	//   mixed     same as redirect. UDP does not come here at all: it goes
	//             through the TUN device and reaches the core as SOCKS.
	//   tproxy    TCP and UDP arrive unrewritten and the socket must be
	//             transparent, so the tproxy sockopt is required.
	if s.Mode.NeedsFirewallCapture() {
		network := "tcp"
		if s.Mode.CapturesUDPViaTProxy() {
			network = "tcp,udp"
		}
		in := Inbound{
			Tag:      TagTransparent,
			Listen:   "0.0.0.0",
			Port:     s.TProxPort,
			Protocol: "dokodemo-door",
			Settings: map[string]any{
				"network":        network,
				"followRedirect": true,
			},
			Sniffing: sniffingFor(s),
		}
		if s.Mode.CapturesTCPViaTProxy() || s.Mode.CapturesUDPViaTProxy() {
			in.StreamSettings = &StreamSettings{Sockopt: &Sockopt{TProxy: "tproxy"}}
		}
		cfg.Inbounds = append(cfg.Inbounds, in)
	}

	if s.DNSMode != model.DNSOff {
		cfg.Inbounds = append(cfg.Inbounds, Inbound{
			Tag:      TagDNSIn,
			Listen:   dnsListen(s),
			Port:     s.DNSPort,
			Protocol: "dokodemo-door",
			Settings: map[string]any{
				"address": s.DNS,
				"port":    53,
				"network": "tcp,udp",
			},
		})
		// Domains the operator bypasses are resolved by the device's own
		// resolver, not through the proxy. Without this the name would be
		// looked up at the far end of the tunnel and the "direct" connection
		// would be made to an address chosen for the wrong part of the world —
		// exactly the failure a bypass rule is meant to avoid.
		servers := []any{}
		if local := bypassedDomains(o.Rules); len(local) > 0 {
			// Without a known resolver these domains are left to the upstream
			// server below. That resolves them through the tunnel, which gives
			// an address from the wrong part of the world — undesirable, but a
			// working connection beats a resolver loop.
			for _, r := range localResolvers(o.LocalResolvers) {
				servers = append(servers, DNSServer{Address: r, Domains: local})
			}
		}
		servers = append(servers, upstreamDNS(s.DNS))
		cfg.DNS = &DNSConfig{
			Servers:       servers,
			QueryStrategy: queryStrategy(s),
		}
	}

	var balancer *Balancer
	if o.Group != nil {
		outs, bal, err := buildGroupOutbounds(o.Group, o.Members, mark, caps, o.Resolve)
		if err != nil {
			return nil, err
		}
		cfg.Outbounds = outs
		balancer = bal

		// The health-aware strategies need somewhere to get health from.
		// Emitting the wrong observatory, or none, makes the core refuse to
		// start with an unresolved dependency.
		switch o.Group.Strategy {
		case model.StrategyLeastPing:
			cfg.Observatory = &Observatory{
				SubjectSelector: []string{TagProxyPrefix},
				ProbeURL:        o.Group.ProbeURL,
				ProbeInterval:   o.Group.ProbeInterval,
			}
		case model.StrategyLeastLoad:
			cfg.BurstObservatory = &BurstObservatory{
				SubjectSelector: []string{TagProxyPrefix},
				PingConfig: &PingConfig{
					Destination: o.Group.ProbeURL,
					Interval:    o.Group.ProbeInterval,
					Timeout:     "10s",
					Sampling:    3,
				},
			}
		}
	} else {
		proxyOut, err := buildProxyOutbound(o.Profile, TagProxy, mark, caps, o.Resolve)
		if err != nil {
			return nil, err
		}
		cfg.Outbounds = []Outbound{*proxyOut}
	}

	cfg.Outbounds = append(cfg.Outbounds,
		Outbound{
			Tag:      TagDirect,
			Protocol: "freedom",
			Settings: map[string]any{"domainStrategy": "UseIP"},
			StreamSettings: &StreamSettings{
				Sockopt: &Sockopt{Mark: mark},
			},
		},
		Outbound{Tag: TagBlock, Protocol: "blackhole"},
	)
	if s.DNSMode != model.DNSOff {
		cfg.Outbounds = append(cfg.Outbounds, Outbound{
			Tag:      TagDNSOut,
			Protocol: "dns",
			Settings: map[string]any{
				"address": s.DNS,
				"port":    53,
				"network": "tcp",
			},
			StreamSettings: &StreamSettings{Sockopt: &Sockopt{Mark: mark}},
		})
	}

	rules := []Rule{
		{Type: "field", InboundTag: []string{TagAPIIn}, OutboundTag: TagAPI},
	}
	if s.DNSMode != model.DNSOff {
		rules = append(rules, Rule{
			Type:        "field",
			InboundTag:  []string{TagDNSIn},
			OutboundTag: TagDNSOut,
		})
	}
	direct := append([]string{}, privateCIDRs...)
	if AssetsAvailable() {
		direct = append(direct, "geoip:private")
	}
	direct = append(direct, o.DirectCIDRs...)
	// The device's own resolvers are reached directly. An ISP resolver refuses
	// queries that arrive from a foreign address, so one sent through the
	// tunnel is answered with nothing at all.
	for _, r := range localResolvers(o.LocalResolvers) {
		direct = append(direct, r+"/32")
	}
	rules = append(rules, Rule{
		Type:        "field",
		IP:          dedupe(direct),
		OutboundTag: TagDirect,
	})

	// The operator's rules sit after the private-network rule, so an exception
	// can never accidentally push LAN traffic into the proxy, and before the
	// catch-all, so they actually get a chance to match.
	proxyTag, proxyBalancer := TagProxy, ""
	if o.Group != nil {
		proxyTag, proxyBalancer = "", TagBalancer
	}
	userRules, err := buildUserRules(o.Rules, proxyTag, proxyBalancer)
	if err != nil {
		return nil, err
	}
	rules = append(rules, userRules...)

	cfg.Routing = &RoutingConfig{DomainStrategy: "AsIs", Rules: rules}

	if balancer != nil {
		cfg.Routing.Balancers = []Balancer{*balancer}
		// Without this catch-all, traffic matching no rule would fall through
		// to the first outbound — one fixed member — and the balancer would
		// never be consulted.
		cfg.Routing.Rules = append(cfg.Routing.Rules, Rule{
			Type:        "field",
			Network:     "tcp,udp",
			BalancerTag: balancer.Tag,
		})
	}

	return cfg, nil
}

// buildUserRules converts the operator's exception list into routing rules.
// Disabled entries are skipped rather than emitted, so toggling one off does
// not change the meaning of the rules around it.
func buildUserRules(in []model.Rule, proxyTag, proxyBalancer string) ([]Rule, error) {
	geo := AssetsAvailable()
	out := make([]Rule, 0, len(in))

	for i := range in {
		r := in[i]
		if !r.Enabled {
			continue
		}
		if err := r.Validate(geo); err != nil {
			return nil, err
		}

		rule := Rule{
			Type:       "field",
			Domain:     domainMatchers(r.Domains),
			IP:         trimAll(r.IPs),
			Source:     trimAll(r.Sources),
			Port:       strings.TrimSpace(r.Port),
			SourcePort: strings.TrimSpace(r.SourcePort),
			Protocol:   trimAll(r.Protocols),
			Network:    strings.TrimSpace(r.Network),
		}
		switch r.Action {
		case model.ActionDirect:
			rule.OutboundTag = TagDirect
		case model.ActionBlock:
			rule.OutboundTag = TagBlock
		case model.ActionProxy:
			// With a group there is no single proxy outbound to name; the
			// balancer stands in for it.
			if proxyBalancer != "" {
				rule.BalancerTag = proxyBalancer
			} else {
				rule.OutboundTag = proxyTag
			}
		}
		out = append(out, rule)
	}
	return out, nil
}

// bypassedDomains collects the domain matchers of enabled direct rules, in the
// form the DNS section accepts. Pattern matchers are kept as they are, since
// the DNS section understands the same syntax; geosite lists are included too,
// so bypassing a whole category resolves locally as well.
func bypassedDomains(rules []model.Rule) []string {
	var out []string
	for i := range rules {
		r := rules[i]
		if !r.Enabled || r.Action != model.ActionDirect {
			continue
		}
		// A rule that also constrains the client or the port is not a blanket
		// "this site is off the VPN", so its domains are left on the proxy
		// resolver rather than being resolved locally for everyone.
		if len(r.Sources) > 0 || r.SourcePort != "" {
			continue
		}
		out = append(out, domainMatchers(r.Domains)...)
	}
	return dedupe(out)
}

func trimAll(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// buildGroupOutbounds turns a group into one outbound per member plus the
// balancer that selects between them.
func buildGroupOutbounds(g *model.Group, members []model.Profile, mark int, caps Capabilities,
	resolve func(string) string) ([]Outbound, *Balancer, error) {
	outs := make([]Outbound, 0, len(members))
	for i := range members {
		p := members[i]
		tag := fmt.Sprintf("%s%d", TagProxyPrefix, i)
		out, err := buildProxyOutbound(&p, tag, mark, caps, resolve)
		if err != nil {
			return nil, nil, fmt.Errorf("group %s, member %s: %w",
				g.Label(), p.Label(), err)
		}
		outs = append(outs, *out)
	}

	bal := &Balancer{
		Tag:      TagBalancer,
		Selector: []string{TagProxyPrefix},
		Strategy: &BalancerStrategy{Type: string(g.Strategy)},
	}
	// A fallback only means something when the balancer knows which members
	// are down, which is to say when a health-aware strategy is in use.
	if g.Strategy.HealthAware() && len(outs) > 0 {
		bal.FallbackTag = outs[0].Tag
	}
	return outs, bal, nil
}

func buildProxyOutbound(p *model.Profile, tag string, mark int, caps Capabilities,
	resolve func(string) string) (*Outbound, error) {

	if err := p.Validate(); err != nil {
		return nil, err
	}
	out := &Outbound{Tag: tag, Protocol: string(p.Proto)}

	// The address the core dials. Only this is replaced by the resolved
	// address: everything identity-related — the TLS server name, the
	// certificate pin, the Host header — still comes from the profile below,
	// so pinning the address changes what is dialled and nothing else.
	addr := serverAddress(p, resolve)

	switch p.Proto {
	case model.ProtoVLESS:
		user := map[string]any{
			"id":         p.UUID,
			"encryption": orDefault(p.Encryption, "none"),
		}
		if p.Flow != "" {
			user["flow"] = p.Flow
		}
		out.Settings = map[string]any{
			"vnext": []any{map[string]any{
				"address": addr,
				"port":    p.Port,
				"users":   []any{user},
			}},
		}
	case model.ProtoVMess:
		user := map[string]any{
			"id":       p.UUID,
			"alterId":  p.AlterID,
			"security": orDefault(p.Encryption, "auto"),
		}
		out.Settings = map[string]any{
			"vnext": []any{map[string]any{
				"address": addr,
				"port":    p.Port,
				"users":   []any{user},
			}},
		}
	case model.ProtoTrojan:
		out.Settings = map[string]any{
			"servers": []any{map[string]any{
				"address":  addr,
				"port":     p.Port,
				"password": p.Password,
			}},
		}
	case model.ProtoShadowsocks:
		out.Settings = map[string]any{
			"servers": []any{map[string]any{
				"address":  addr,
				"port":     p.Port,
				"method":   p.Method,
				"password": p.Password,
				"uot":      true,
			}},
		}
	default:
		return nil, fmt.Errorf("unsupported protocol %q", p.Proto)
	}

	ss, err := buildStream(p, mark, caps)
	if err != nil {
		return nil, err
	}
	out.StreamSettings = ss

	// XUDP is how UDP is carried, so it is always on; TCP multiplexing is a
	// separate question and stays off unless the profile asks.
	//
	// Without XUDP, UDP over VLESS works for some destinations and not others,
	// and fails in a way nobody can see: the stream is accepted, the request is
	// forwarded, the answer never arrives. xudpProxyUDP443 has to say "allow"
	// out loud because the core's own default is to reject exactly the port a
	// browser uses for video — which is why NTP would go through while every
	// video stalled, with nothing in any log mentioning 443.
	//
	// Measured on a real link: with this, QUIC to Google runs in both
	// directions through the tunnel; without it, the same flow is one-way.
	//
	// Concurrency -1 leaves TCP unmultiplexed. XTLS Vision is built around one
	// connection per stream, and multiplexing on top of it only costs
	// throughput — so this changes how UDP is carried and nothing else.
	out.Mux = &MuxConfig{
		Enabled:         true,
		Concurrency:     -1,
		XUDPConcurrency: 16,
		XUDPProxyUDP443: "allow",
	}
	if p.Mux {
		out.Mux.Concurrency = 8
	}
	return out, nil
}

func buildStream(p *model.Profile, mark int, caps Capabilities) (*StreamSettings, error) {
	network := p.Network
	if network == "" {
		network = "tcp"
	}
	ss := &StreamSettings{
		Network: network,
		Sockopt: &Sockopt{Mark: mark},
	}

	switch p.Security {
	case "tls":
		ss.Security = "tls"
		tls := &TLSSettings{
			ServerName:  orDefault(p.SNI, p.Host, p.Address),
			Fingerprint: p.Fingerprint,
			ALPN:        p.ALPNList(),
		}
		// Pinning is the modern replacement for skipping verification, and it
		// is strictly better: "accept anything" also accepts an interceptor's
		// certificate, while a pin accepts exactly one.
		if !p.PinnedCertValid() {
			return nil, fmt.Errorf(
				"pinned_cert on this profile is not a SHA-256 digest: %q. It must be "+
					"64 hex characters (the AA:BB: form openssl prints is fine). Run "+
					"`xwrt fetch-cert %s` to fill it in from the server itself",
				p.PinnedCert, p.ID)
		}
		if pin := p.PinnedCertHex(); pin != "" {
			if !caps.PinnedCert {
				return nil, fmt.Errorf(
					"this profile pins a certificate, which core %s does not support "+
						"(pinnedPeerCertSha256 is unknown to it). Upgrade the core, or "+
						"clear pinned_cert and set allow_insecure instead", caps.Version)
			}
			tls.PinnedPeerCertSha256 = pin
		} else if p.AllowInsecure {
			if !caps.AllowInsecure {
				return nil, fmt.Errorf(
					"this profile sets allowInsecure, which core %s has removed. "+
						"Pin the server's certificate instead: run `xwrt fetch-cert %s` "+
						"on this device, which records the certificate the server "+
						"presents and turns allowInsecure off", caps.Version, p.ID)
			}
			tls.AllowInsecure = true
		}
		ss.TLSSettings = tls
	case "reality":
		ss.Security = "reality"
		ss.RealitySettings = &RealitySettings{
			ServerName:  orDefault(p.SNI, p.Host),
			Fingerprint: orDefault(p.Fingerprint, "chrome"),
			PublicKey:   p.PublicKey,
			ShortID:     p.ShortID,
			SpiderX:     p.SpiderX,
		}
	default:
		ss.Security = "none"
	}

	switch network {
	case "ws":
		ws := &WSSettings{Path: orDefault(p.Path, "/")}
		if p.Host != "" {
			ws.Host = p.Host
			ws.Headers = map[string]string{"Host": p.Host}
		}
		ss.WSSettings = ws
	case "grpc":
		ss.GRPCSettings = &GRPCSettings{
			ServiceName: p.ServiceName,
			MultiMode:   strings.EqualFold(p.HeaderType, "multi"),
		}
	case "h2", "http":
		if !caps.H2Transport {
			return nil, fmt.Errorf(
				"this profile uses the HTTP/2 transport, which core %s has removed "+
					"in favour of XHTTP; ask the server operator for a current link",
				caps.Version)
		}
		ss.Network = "h2"
		h := &HTTPSettings{Path: orDefault(p.Path, "/")}
		if p.Host != "" {
			h.Host = strings.Split(p.Host, ",")
		}
		ss.HTTPSettings = h
	case "kcp", "mkcp":
		ss.Network = "kcp"
		kcp := &KCPSettings{
			MTU:              1350,
			TTI:              50,
			UplinkCapacity:   12,
			DownlinkCapacity: 100,
			Congestion:       false,
			ReadBufferSize:   2,
			WriteBufferSize:  2,
		}
		// Header and seed are emitted only when the profile actually sets
		// them. Newer cores reject both outright, and a profile that does not
		// use them has no reason to carry the fields.
		if hasHeader(p.HeaderType) || p.Seed != "" {
			if !caps.KCPHeaderSeed {
				return nil, fmt.Errorf(
					"this profile uses the mKCP header/seed options, which core %s "+
						"has removed; ask the server operator for a current link",
					caps.Version)
			}
			if hasHeader(p.HeaderType) {
				kcp.Header = &Header{Type: p.HeaderType}
			}
			kcp.Seed = p.Seed
		}
		ss.KCPSettings = kcp
	case "quic":
		if !caps.QUICTransport {
			return nil, fmt.Errorf(
				"this profile uses the QUIC transport, which core %s has removed "+
					"in favour of XHTTP; ask the server operator for a current link",
				caps.Version)
		}
		q := &QUICSettings{
			Security: orDefault(p.QUICSec, "none"),
			Key:      p.QUICKey,
		}
		if hasHeader(p.HeaderType) {
			q.Header = &Header{Type: p.HeaderType}
		}
		ss.QUICSettings = q
	case "xhttp", "splithttp":
		ss.Network = "xhttp"
		ss.XHTTPSettings = &XHTTPSettings{
			Path: orDefault(p.Path, "/"),
			Host: p.Host,
			Mode: "auto",
		}
	case "httpupgrade":
		ss.HTTPUpgradeSettings = &HTTPUpgradeSettings{
			Path: orDefault(p.Path, "/"),
			Host: p.Host,
		}
	default: // tcp
		ss.Network = "tcp"
		if strings.EqualFold(p.HeaderType, "http") {
			req := map[string]any{
				"version": "1.1",
				"method":  "GET",
				"path":    []string{orDefault(p.Path, "/")},
			}
			if p.Host != "" {
				req["headers"] = map[string]any{"Host": strings.Split(p.Host, ",")}
			}
			ss.TCPSettings = &TCPSettings{Header: &Header{Type: "http", Request: req}}
		}
	}
	return ss, nil
}

// hasHeader reports whether a header type is meaningfully set. "none" is the
// default and is better left out than spelled out.
func hasHeader(t string) bool {
	t = strings.TrimSpace(strings.ToLower(t))
	return t != "" && t != "none"
}

// JSON renders the configuration.
func (c *Config) JSON() ([]byte, error) {
	return json.MarshalIndent(c, "", "  ")
}

// serverAddress is what the core should dial for this profile.
//
// A literal address is used as it stands — there is nothing to look up, and a
// resolver that "resolved" it would only be able to get it wrong.
func serverAddress(p *model.Profile, resolve func(string) string) string {
	if resolve == nil || net.ParseIP(p.Address) != nil {
		return p.Address
	}
	if ip := resolve(p.Address); ip != "" {
		return ip
	}
	return p.Address
}

// sniffingFor returns the sniffing block for a client-facing inbound.
//
// Sniffing reads the domain out of the first packet, which is what makes
// domain rules possible at all: a firewall only ever sees addresses.
//
// The subtlety is what happens to the destination afterwards. By default the
// sniffed domain *replaces* it, so the far end resolves the name a second time
// — even though the client already resolved it to reach us. That second lookup
// costs a round trip on every new connection, and when the far end's resolver
// is slow it shows up as random hundreds of milliseconds on one connection in
// five. Measured on a real link: 100 ms for a domain target against 47 ms for
// the same path with an address, plus occasional spikes to 700 ms.
//
// routeOnly keeps the domain for routing and leaves the destination alone, so
// the name is resolved exactly once. That is only sound when the address came
// from a resolver we control: with dns_mode off the client uses whatever
// resolver it likes, and on a censored network that may be a poisoned answer —
// there, letting the far end resolve the name is the better trade, so the
// override stays.
func sniffingFor(s *model.Settings) *Sniffing {
	return &Sniffing{
		Enabled:      true,
		DestOverride: []string{"http", "tls", "quic"},
		RouteOnly:    s.DNSMode != model.DNSOff,
	}
}

// upstreamDNS decides how the core reaches the resolver it was given.
//
// A bare address means UDP, and UDP through a proxy is the least dependable
// thing in this whole stack: plenty of servers carry TCP perfectly and drop UDP
// on the floor, and one of them silently breaks every name lookup on the
// network. The failure is invisible from the outside — the tunnel is up, TCP
// works, and nothing resolves — so the default is the transport that works
// wherever the tunnel works at all.
//
// An address the operator spelled out (tcp://, https://, quic://, or one with a
// port) is left exactly as written.
func upstreamDNS(addr string) string {
	v := strings.TrimSpace(addr)
	if v == "" {
		return v
	}
	if strings.Contains(v, "://") || v == "localhost" {
		return v
	}
	return "tcp://" + v
}

func dnsListen(s *model.Settings) string {
	// In dnsmasq mode only the router itself talks to this inbound.
	if s.DNSMode == model.DNSDnsmasq {
		return "127.0.0.1"
	}
	return "0.0.0.0"
}

func queryStrategy(s *model.Settings) string {
	if s.IPv6 {
		return "UseIP"
	}
	return "UseIPv4"
}

func orDefault(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func dedupe(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// assetDirs lists where Xray looks for geoip.dat and geosite.dat, in search
// order. It is a function rather than a package variable so the environment is
// read at call time, which matches how the core resolves it.
func assetDirs() []string {
	return []string{
		os.Getenv("XRAY_LOCATION_ASSET"),
		"/usr/local/share/xray",
		"/usr/share/xray",
		"/usr/share/v2ray",
	}
}

// AssetsAvailable reports whether the geo data files are installed. They are an
// optional package, so their presence is detected rather than assumed: naming
// geoip:private in a config without them makes the core refuse to start.
func AssetsAvailable() bool {
	for _, dir := range assetDirs() {
		if dir == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, "geoip.dat")); err == nil {
			return true
		}
	}
	return false
}

// localResolvers keeps the usable upstream resolvers, at most two. Loopback is
// dropped: on this device it is dnsmasq, and dnsmasq's upstream is this core.
func localResolvers(in []string) []string {
	out := []string{}
	for _, r := range in {
		r = strings.TrimSpace(r)
		if r == "" || r == "localhost" {
			continue
		}
		ip := net.ParseIP(r)
		if ip == nil || ip.To4() == nil || ip.IsLoopback() {
			continue
		}
		out = append(out, r)
		if len(out) == 2 {
			break
		}
	}
	return out
}

// domainMatchers turns what the operator typed into what the core matches on.
//
// A bare name is the interesting case. The core reads an unprefixed entry as a
// substring, so "bank.com" also matches "bank.com.cn" and, worse for a rule
// whose whole purpose is to take traffic off the VPN, "bank.com.attacker.net".
// Nobody typing a domain into a bypass rule means that. A bare name is
// therefore emitted as "domain:", which matches the name and its subdomains
// and nothing else; the explicit prefixes, "keyword:" included, are passed
// through untouched for anyone who does want substring matching.
func domainMatchers(in []string) []string {
	trimmed := trimAll(in)
	if len(trimmed) == 0 {
		return nil
	}
	out := make([]string, 0, len(trimmed))
	for _, d := range trimmed {
		if strings.ContainsRune(d, ':') {
			out = append(out, d)
			continue
		}
		out = append(out, "domain:"+d)
	}
	return out
}
