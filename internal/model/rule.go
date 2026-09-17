package model

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// Routing rules are the exception list: they say which traffic should not go
// through the proxy, which must, and which should not go anywhere at all.
//
// Matching happens inside the core rather than in the firewall, because the
// interesting matcher is the domain and the firewall never sees one. The core
// learns it by sniffing the TLS SNI or the HTTP Host out of the first packet,
// which is why sniffing is enabled on every inbound.

// RuleAction is what happens to traffic a rule matches.
type RuleAction string

const (
	// ActionDirect sends the traffic straight out of the WAN, bypassing the
	// proxy. This is the "keep this site off the VPN" case.
	ActionDirect RuleAction = "direct"
	// ActionProxy forces traffic through the proxy. Useful as a narrower
	// exception inside a broader direct rule.
	ActionProxy RuleAction = "proxy"
	// ActionBlock drops the traffic.
	ActionBlock RuleAction = "block"
)

// RuleActions lists every action, in the order the UI should present them.
func RuleActions() []RuleAction {
	return []RuleAction{ActionDirect, ActionProxy, ActionBlock}
}

// Valid reports whether a is a known action.
func (a RuleAction) Valid() bool {
	for _, k := range RuleActions() {
		if a == k {
			return true
		}
	}
	return false
}

// Describe returns a one-line summary for the UI.
func (a RuleAction) Describe() string {
	switch a {
	case ActionDirect:
		return "bypass the proxy and go out over the normal connection"
	case ActionProxy:
		return "force through the proxy"
	case ActionBlock:
		return "drop the traffic"
	default:
		return string(a)
	}
}

// Rule is one entry in the exception list. Every populated matcher must hold
// for the rule to fire, which is how the core evaluates them.
type Rule struct {
	ID      string     `json:"id"`
	Name    string     `json:"name"`
	Enabled bool       `json:"enabled"`
	Action  RuleAction `json:"action"`

	// Domains accepts the core's matcher syntax: a bare name matches the
	// domain and its subdomains, "full:" an exact name, "keyword:" a
	// substring, "regexp:" a pattern, and "geosite:" a named list from the
	// geo data files.
	Domains []string `json:"domains,omitempty"`
	// IPs accepts CIDRs and "geoip:" list names.
	IPs []string `json:"ips,omitempty"`
	// Sources restricts the rule to given LAN clients, by address or CIDR.
	Sources []string `json:"sources,omitempty"`

	// Port and SourcePort accept "443", "1000-2000" and comma separated
	// combinations of both.
	Port       string `json:"port,omitempty"`
	SourcePort string `json:"source_port,omitempty"`

	// Protocols matches what sniffing detected: http, tls, quic, bittorrent.
	Protocols []string `json:"protocols,omitempty"`
	// Network is "tcp", "udp" or "tcp,udp".
	Network string `json:"network,omitempty"`
}

// HasMatcher reports whether the rule constrains anything. A rule with no
// matcher would apply to all traffic, which is never what an exception list
// entry is meant to do.
func (r *Rule) HasMatcher() bool {
	return len(r.Domains) > 0 || len(r.IPs) > 0 || len(r.Sources) > 0 ||
		r.Port != "" || r.SourcePort != "" || len(r.Protocols) > 0 || r.Network != ""
}

// Label returns a human readable name.
func (r *Rule) Label() string {
	if r.Name != "" {
		return r.Name
	}
	if len(r.Domains) > 0 {
		return r.Domains[0]
	}
	if len(r.IPs) > 0 {
		return r.IPs[0]
	}
	return r.ID
}

// NeedsGeoData reports whether the rule references the geo data files, which
// are an optional package. Naming a list the core cannot load stops it from
// starting, so this has to be checked before generating a config.
func (r *Rule) NeedsGeoData() bool {
	for _, d := range r.Domains {
		if strings.HasPrefix(d, "geosite:") {
			return true
		}
	}
	for _, ip := range r.IPs {
		if strings.HasPrefix(ip, "geoip:") {
			return true
		}
	}
	return false
}

// Validate checks a rule in isolation. geoAvailable says whether the geo data
// files are installed.
func (r *Rule) Validate(geoAvailable bool) error {
	if !r.Action.Valid() {
		names := make([]string, 0, len(RuleActions()))
		for _, a := range RuleActions() {
			names = append(names, string(a))
		}
		return fmt.Errorf("unknown action %q; use one of: %s",
			r.Action, strings.Join(names, ", "))
	}
	if !r.HasMatcher() {
		return fmt.Errorf("rule %q matches nothing; give it a domain, an address, "+
			"a client, a port or a protocol", r.Label())
	}
	if r.NeedsGeoData() && !geoAvailable {
		return fmt.Errorf("rule %q uses a geosite/geoip list, but the geo data "+
			"files are not installed; install xray-geodata or use plain domains "+
			"and addresses", r.Label())
	}
	for _, d := range r.Domains {
		if err := validateDomainMatcher(d); err != nil {
			return fmt.Errorf("rule %q: %w", r.Label(), err)
		}
	}
	for _, ip := range r.IPs {
		if err := validateIPMatcher(ip); err != nil {
			return fmt.Errorf("rule %q: %w", r.Label(), err)
		}
	}
	for _, src := range r.Sources {
		if err := validateIPMatcher(src); err != nil {
			return fmt.Errorf("rule %q: source: %w", r.Label(), err)
		}
	}
	if err := validatePortSpec(r.Port); err != nil {
		return fmt.Errorf("rule %q: port: %w", r.Label(), err)
	}
	if err := validatePortSpec(r.SourcePort); err != nil {
		return fmt.Errorf("rule %q: source port: %w", r.Label(), err)
	}
	for _, p := range r.Protocols {
		switch p {
		case "http", "tls", "quic", "bittorrent":
		default:
			return fmt.Errorf("rule %q: unknown protocol %q; use http, tls, quic or bittorrent",
				r.Label(), p)
		}
	}
	switch r.Network {
	case "", "tcp", "udp", "tcp,udp", "udp,tcp":
	default:
		return fmt.Errorf("rule %q: network must be tcp, udp or tcp,udp, got %q",
			r.Label(), r.Network)
	}
	return nil
}

func validateDomainMatcher(d string) error {
	d = strings.TrimSpace(d)
	if d == "" {
		return fmt.Errorf("empty domain entry")
	}
	// A pasted URL is the most likely wrong input, and it contains a colon, so
	// it has to be caught before the prefix check or the message would blame
	// the scheme for being an unknown matcher prefix.
	if strings.Contains(d, "://") || strings.ContainsAny(d, "/ ") {
		return fmt.Errorf("%q is not a domain; enter just the host name, "+
			"without a scheme or a path", d)
	}

	prefix, rest, hasPrefix := strings.Cut(d, ":")
	if hasPrefix {
		switch prefix {
		case "domain", "full", "keyword", "regexp", "geosite", "ext":
			if strings.TrimSpace(rest) == "" {
				return fmt.Errorf("%q has a prefix but no value", d)
			}
			return nil
		default:
			return fmt.Errorf("unknown domain prefix %q in %q; use domain:, full:, "+
				"keyword:, regexp: or geosite:", prefix, d)
		}
	}
	if net.ParseIP(d) != nil {
		return fmt.Errorf("%q is an address, not a domain; put it in the "+
			"addresses field instead", d)
	}
	return nil
}

func validateIPMatcher(v string) error {
	v = strings.TrimSpace(v)
	if v == "" {
		return fmt.Errorf("empty address entry")
	}
	if strings.HasPrefix(v, "geoip:") {
		if strings.TrimSpace(strings.TrimPrefix(v, "geoip:")) == "" {
			return fmt.Errorf("%q has no list name", v)
		}
		return nil
	}
	if strings.Contains(v, "/") {
		if _, _, err := net.ParseCIDR(v); err != nil {
			return fmt.Errorf("%q is not a valid network: %w", v, err)
		}
		return nil
	}
	if net.ParseIP(v) == nil {
		return fmt.Errorf("%q is not an address or network; a domain belongs "+
			"in the domains field", v)
	}
	return nil
}

// validatePortSpec accepts the core's syntax: single ports, ranges, and comma
// separated combinations.
func validatePortSpec(spec string) error {
	if strings.TrimSpace(spec) == "" {
		return nil
	}
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return fmt.Errorf("empty entry in %q", spec)
		}
		lo, hi, isRange := strings.Cut(part, "-")
		if err := validatePortNumber(lo); err != nil {
			return err
		}
		if isRange {
			if err := validatePortNumber(hi); err != nil {
				return err
			}
			l, _ := strconv.Atoi(strings.TrimSpace(lo))
			h, _ := strconv.Atoi(strings.TrimSpace(hi))
			if l > h {
				return fmt.Errorf("range %q runs backwards", part)
			}
		}
	}
	return nil
}

func validatePortNumber(v string) error {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return fmt.Errorf("%q is not a port number", v)
	}
	if n < 1 || n > 65535 {
		return fmt.Errorf("port %d is out of range", n)
	}
	return nil
}
