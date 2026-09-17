package ucicfg

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"xwrt/internal/model"
)

// PackageName is the UCI package backing the daemon: /etc/config/xwrt.
const PackageName = "xwrt"

const (
	secMain     = "main"
	typeMain    = "xwrt"
	typeProfile = "profile"
	typeGroup   = "group"
	typeRule    = "rule"
	typeSub     = "subscription"
)

// Data is the full configuration set.
type Data struct {
	Settings      model.Settings       `json:"settings"`
	Profiles      []model.Profile      `json:"profiles"`
	Groups        []model.Group        `json:"groups"`
	Rules         []model.Rule         `json:"rules"`
	Subscriptions []model.Subscription `json:"subscriptions"`

	// Warnings names configuration the loader could not use — a misspelled
	// option, an unknown section type. They are not errors: the rest of the
	// file is still loaded, and saying so beats silently ignoring a line
	// someone wrote on purpose.
	Warnings []string `json:"warnings,omitempty"`
}

// Group returns the group with the given ID, or nil.
func (d *Data) Group(id string) *model.Group {
	for i := range d.Groups {
		if d.Groups[i].ID == id {
			return &d.Groups[i]
		}
	}
	return nil
}

// GroupMembers resolves a group's member IDs to profiles, skipping any that
// no longer exist. A subscription refresh can delete profiles out from under
// a group, and a stale ID must not abort the whole connection.
func (d *Data) GroupMembers(g *model.Group) []model.Profile {
	out := make([]model.Profile, 0, len(g.Members))
	for _, id := range g.Members {
		if p := d.Profile(id); p != nil {
			out = append(out, *p)
		}
	}
	return out
}

// Profile returns the profile with the given ID, or nil.
func (d *Data) Profile(id string) *model.Profile {
	for i := range d.Profiles {
		if d.Profiles[i].ID == id {
			return &d.Profiles[i]
		}
	}
	return nil
}

// Store persists Data in UCI.
type Store struct {
	mu sync.Mutex
	u  *UCI
}

// NewStore returns a store backed by the given UCI handle.
func NewStore(u *UCI) *Store { return &Store{u: u} }

// Load reads the whole configuration.
func (s *Store) Load() (*Data, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()
}

func (s *Store) load() (*Data, error) {
	pkg, err := s.u.Load(PackageName)
	if err != nil {
		return nil, err
	}
	d := &Data{Settings: model.Defaults()}

	if m := pkg.Find(secMain); m != nil {
		st := &d.Settings
		st.Enabled = toBool(m.Get("enabled"), st.Enabled)
		if v := m.Get("mode"); v != "" {
			st.Mode = model.Mode(v)
		}
		st.Active = m.Get("active")
		if v := m.Get("active_kind"); v != "" {
			st.ActiveKind = model.TargetKind(v)
		}
		st.SocksPort = toInt(m.Get("socks_port"), st.SocksPort)
		st.HTTPPort = toInt(m.Get("http_port"), st.HTTPPort)
		st.TProxPort = toInt(m.Get("tproxy_port"), st.TProxPort)
		st.DNSPort = toInt(m.Get("dns_port"), st.DNSPort)
		st.APIPort = toInt(m.Get("api_port"), st.APIPort)
		st.StatsPort = toInt(m.Get("stats_port"), st.StatsPort)
		if v := m.Get("dns"); v != "" {
			st.DNS = v
		}
		if v := m.Get("dns_mode"); v != "" {
			st.DNSMode = model.DNSMode(v)
		}
		st.ProxyRouter = toBool(m.Get("proxy_router"), st.ProxyRouter)
		if v := m.Get("log_level"); v != "" {
			st.LogLevel = v
		}
		st.AllowLAN = toBool(m.Get("allow_lan"), st.AllowLAN)
		st.AutoConnect = toBool(m.Get("auto_connect"), st.AutoConnect)
		st.UpdateCheck = toBool(m.Get("update_check"), st.UpdateCheck)
		if v := m.Get("update_repo"); v != "" {
			st.UpdateRepo = v
		}
		// Migration: proxy_udp used to be a flag on top of redirect mode.
		// That combination is now its own mode, so honour the old setting once
		// and let the next save drop the option.
		if st.Mode == model.ModeRedirect && m.Get("proxy_udp") != "" &&
			toBool(m.Get("proxy_udp"), false) {
			st.Mode = model.ModeMixed
		}
		st.IPv6 = toBool(m.Get("ipv6"), st.IPv6)
		if v := m.Get("xray_bin"); v != "" {
			st.XrayBin = v
		}
		if v := m.Get("hev_bin"); v != "" {
			st.HevBin = v
		}
		if v := m.Get("run_dir"); v != "" {
			st.RunDir = v
		}
		if v := m.Get("tun_name"); v != "" {
			st.TunName = v
		}
		if v := m.Get("tun_addr"); v != "" {
			st.TunAddr = v
		}
		if v := m.Get("tun_mask"); v != "" {
			st.TunMask = v
		}
		st.TunMTU = toInt(m.Get("tun_mtu"), st.TunMTU)
		if v := m.Get("fwmark"); v != "" {
			st.FwMark = v
		}
		st.RouteTable = toInt(m.Get("route_table"), st.RouteTable)
		st.LANDevice = m.Get("lan_device")
		st.WANDevice = m.Get("wan_device")
		st.BypassIP = m.List("bypass_ip")
		st.BypassMAC = m.List("bypass_mac")
	}
	d.Settings.Normalize()
	d.Warnings = checkUnknownOptions(pkg)

	for _, sec := range pkg.OfType(typeProfile) {
		p := model.Profile{
			ID:            sec.Name,
			Name:          sec.Get("name"),
			Proto:         model.Proto(orDefault(sec.Get("proto"), string(model.ProtoVLESS))),
			Address:       sec.Get("address"),
			Port:          toInt(sec.Get("port"), 0),
			UUID:          sec.Get("uuid"),
			Password:      sec.Get("password"),
			Method:        sec.Get("method"),
			AlterID:       toInt(sec.Get("alter_id"), 0),
			Encryption:    sec.Get("encryption"),
			Flow:          sec.Get("flow"),
			Network:       orDefault(sec.Get("net"), "tcp"),
			Security:      orDefault(sec.Get("security"), "none"),
			SNI:           sec.Get("sni"),
			ALPN:          sec.Get("alpn"),
			Fingerprint:   sec.Get("fp"),
			PublicKey:     sec.Get("pbk"),
			ShortID:       sec.Get("sid"),
			SpiderX:       sec.Get("spx"),
			AllowInsecure: toBool(sec.Get("allow_insecure"), false),
			PinnedCert:    sec.Get("pinned_cert"),
			Path:          sec.Get("path"),
			Host:          sec.Get("host"),
			ServiceName:   sec.Get("service_name"),
			HeaderType:    sec.Get("header_type"),
			Seed:          sec.Get("seed"),
			QUICSec:       sec.Get("quic_security"),
			QUICKey:       sec.Get("quic_key"),
			Mux:           toBool(sec.Get("mux"), false),
			Subscription:  sec.Get("sub"),
			Remark:        sec.Get("remark"),
		}
		d.Profiles = append(d.Profiles, p)
	}

	for _, sec := range pkg.OfType(typeGroup) {
		g := model.Group{
			ID:            sec.Name,
			Name:          sec.Get("name"),
			Strategy:      model.Strategy(sec.Get("strategy")),
			Members:       sec.List("member"),
			ProbeURL:      sec.Get("probe_url"),
			ProbeInterval: sec.Get("probe_interval"),
		}
		g.Normalize()
		d.Groups = append(d.Groups, g)
	}

	// Rules keep their file order: the core evaluates them top to bottom and
	// the first match wins, so the order is part of the configuration.
	for _, sec := range pkg.OfType(typeRule) {
		r := model.Rule{
			ID:         sec.Name,
			Name:       sec.Get("name"),
			Enabled:    toBool(sec.Get("enabled"), true),
			Action:     model.RuleAction(orDefault(sec.Get("action"), string(model.ActionDirect))),
			Domains:    sec.List("domain"),
			IPs:        sec.List("ip"),
			Sources:    sec.List("source"),
			Port:       sec.Get("port"),
			SourcePort: sec.Get("source_port"),
			Protocols:  sec.List("protocol"),
			Network:    sec.Get("network"),
		}
		d.Rules = append(d.Rules, r)
	}

	for _, sec := range pkg.OfType(typeSub) {
		d.Subscriptions = append(d.Subscriptions, model.Subscription{
			ID:      sec.Name,
			Name:    sec.Get("name"),
			URL:     sec.Get("url"),
			Updated: sec.Get("updated"),
			Count:   toInt(sec.Get("count"), 0),
		})
	}
	return d, nil
}

// Save writes the whole configuration back.
func (s *Store) Save(d *Data) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.save(d)
}

func (s *Store) save(d *Data) error {
	pkg := &Package{Name: PackageName}

	main := &Section{Name: secMain, Type: typeMain, Options: map[string]string{}, Lists: map[string][]string{}}
	st := d.Settings
	main.Set("enabled", fromBool(st.Enabled))
	main.Set("mode", string(st.Mode))
	main.Set("active", st.Active)
	main.Set("active_kind", string(st.ActiveKind))
	main.Set("socks_port", strconv.Itoa(st.SocksPort))
	main.Set("http_port", strconv.Itoa(st.HTTPPort))
	main.Set("tproxy_port", strconv.Itoa(st.TProxPort))
	main.Set("dns_port", strconv.Itoa(st.DNSPort))
	main.Set("api_port", strconv.Itoa(st.APIPort))
	main.Set("stats_port", strconv.Itoa(st.StatsPort))
	main.Set("dns", st.DNS)
	main.Set("dns_mode", string(st.DNSMode))
	main.Set("proxy_router", fromBool(st.ProxyRouter))
	main.Set("log_level", st.LogLevel)
	main.Set("allow_lan", fromBool(st.AllowLAN))
	main.Set("auto_connect", fromBool(st.AutoConnect))
	main.Set("update_check", fromBool(st.UpdateCheck))
	main.Set("update_repo", st.UpdateRepo)
	main.Set("ipv6", fromBool(st.IPv6))
	main.Set("xray_bin", st.XrayBin)
	main.Set("hev_bin", st.HevBin)
	main.Set("run_dir", st.RunDir)
	main.Set("tun_name", st.TunName)
	main.Set("tun_addr", st.TunAddr)
	main.Set("tun_mask", st.TunMask)
	main.Set("tun_mtu", strconv.Itoa(st.TunMTU))
	main.Set("fwmark", st.FwMark)
	main.Set("route_table", strconv.Itoa(st.RouteTable))
	if st.LANDevice != "" {
		main.Set("lan_device", st.LANDevice)
	}
	if st.WANDevice != "" {
		main.Set("wan_device", st.WANDevice)
	}
	if len(st.BypassIP) > 0 {
		main.Lists["bypass_ip"] = st.BypassIP
	}
	if len(st.BypassMAC) > 0 {
		main.Lists["bypass_mac"] = st.BypassMAC
	}
	pkg.Sections = append(pkg.Sections, main)

	for i := range d.Profiles {
		p := &d.Profiles[i]
		if p.ID == "" {
			p.ID = NewID("p")
		}
		sec := &Section{Name: p.ID, Type: typeProfile, Options: map[string]string{}, Lists: map[string][]string{}}
		set := func(k, v string) {
			if v != "" {
				sec.Set(k, v)
			}
		}
		set("name", p.Name)
		set("proto", string(p.Proto))
		set("address", p.Address)
		set("port", strconv.Itoa(p.Port))
		set("uuid", p.UUID)
		set("password", p.Password)
		set("method", p.Method)
		if p.AlterID != 0 {
			set("alter_id", strconv.Itoa(p.AlterID))
		}
		set("encryption", p.Encryption)
		set("flow", p.Flow)
		set("net", p.Network)
		set("security", p.Security)
		set("sni", p.SNI)
		set("alpn", p.ALPN)
		set("fp", p.Fingerprint)
		set("pbk", p.PublicKey)
		set("sid", p.ShortID)
		set("spx", p.SpiderX)
		if p.AllowInsecure {
			set("allow_insecure", "1")
		}
		set("pinned_cert", p.PinnedCert)
		set("path", p.Path)
		set("host", p.Host)
		set("service_name", p.ServiceName)
		set("header_type", p.HeaderType)
		set("seed", p.Seed)
		set("quic_security", p.QUICSec)
		set("quic_key", p.QUICKey)
		if p.Mux {
			set("mux", "1")
		}
		set("sub", p.Subscription)
		set("remark", p.Remark)
		pkg.Sections = append(pkg.Sections, sec)
	}

	for i := range d.Groups {
		g := &d.Groups[i]
		if g.ID == "" {
			g.ID = NewID("g")
		}
		g.Normalize()
		sec := &Section{Name: g.ID, Type: typeGroup, Options: map[string]string{}, Lists: map[string][]string{}}
		sec.Set("name", g.Name)
		sec.Set("strategy", string(g.Strategy))
		sec.Set("probe_url", g.ProbeURL)
		sec.Set("probe_interval", g.ProbeInterval)
		if len(g.Members) > 0 {
			sec.Lists["member"] = g.Members
		}
		pkg.Sections = append(pkg.Sections, sec)
	}

	for i := range d.Rules {
		r := &d.Rules[i]
		if r.ID == "" {
			r.ID = NewID("r")
		}
		sec := &Section{Name: r.ID, Type: typeRule, Options: map[string]string{}, Lists: map[string][]string{}}
		sec.Set("name", r.Name)
		sec.Set("enabled", fromBool(r.Enabled))
		sec.Set("action", string(r.Action))
		if r.Port != "" {
			sec.Set("port", r.Port)
		}
		if r.SourcePort != "" {
			sec.Set("source_port", r.SourcePort)
		}
		if r.Network != "" {
			sec.Set("network", r.Network)
		}
		if len(r.Domains) > 0 {
			sec.Lists["domain"] = r.Domains
		}
		if len(r.IPs) > 0 {
			sec.Lists["ip"] = r.IPs
		}
		if len(r.Sources) > 0 {
			sec.Lists["source"] = r.Sources
		}
		if len(r.Protocols) > 0 {
			sec.Lists["protocol"] = r.Protocols
		}
		pkg.Sections = append(pkg.Sections, sec)
	}

	for i := range d.Subscriptions {
		sub := &d.Subscriptions[i]
		if sub.ID == "" {
			sub.ID = NewID("s")
		}
		sec := &Section{Name: sub.ID, Type: typeSub, Options: map[string]string{}, Lists: map[string][]string{}}
		sec.Set("name", sub.Name)
		sec.Set("url", sub.URL)
		if sub.Updated != "" {
			sec.Set("updated", sub.Updated)
		}
		sec.Set("count", strconv.Itoa(sub.Count))
		pkg.Sections = append(pkg.Sections, sec)
	}

	return s.u.Save(pkg)
}

// Update runs fn against the current configuration and saves the result. The
// whole read-modify-write is serialized, so concurrent API calls cannot lose
// each other's changes.
func (s *Store) Update(fn func(*Data) error) (*Data, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.load()
	if err != nil {
		return nil, err
	}
	if err := fn(d); err != nil {
		return nil, err
	}
	if err := s.save(d); err != nil {
		return nil, err
	}
	return d, nil
}

// NewID returns a short unique UCI section name with the given prefix. UCI
// section names must be alphanumeric, so a hex suffix is used rather than a
// UUID with dashes.
func NewID(prefix string) string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%s00000000", prefix)
	}
	return prefix + hex.EncodeToString(b[:])
}

func toBool(v string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on", "enabled":
		return true
	case "0", "false", "no", "off", "disabled":
		return false
	default:
		return def
	}
}

func fromBool(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func toInt(v string, def int) int {
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return def
	}
	return n
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
