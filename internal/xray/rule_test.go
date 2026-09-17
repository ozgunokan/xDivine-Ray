package xray

import (
	"testing"

	"xwrt/internal/model"
)

func bypassRule(domains ...string) model.Rule {
	return model.Rule{
		ID: "r1", Name: "Bank", Enabled: true,
		Action: model.ActionDirect, Domains: domains,
	}
}

func buildWithRules(t *testing.T, rules []model.Rule) *Config {
	t.Helper()
	return buildWithRulesAndResolvers(t, rules, []string{"217.31.228.210"})
}

func buildWithRulesAndResolvers(t *testing.T, rules []model.Rule, resolvers []string) *Config {
	t.Helper()
	s := model.Defaults()
	cfg, err := Build(Options{
		Profile:        realityProfile(),
		Settings:       &s,
		Caps:           AllFeatures(),
		Rules:          rules,
		LocalResolvers: resolvers,
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cfg
}

// TestRulesComeAfterPrivateAndBeforeTheDefault pins the ordering the whole
// feature depends on: the core takes the first matching rule, so an exception
// placed after the catch-all would never fire, and one placed before the
// private-network rule could push LAN traffic into the proxy.
func TestRulesComeAfterPrivateAndBeforeTheDefault(t *testing.T) {
	cfg := buildWithRules(t, []model.Rule{bypassRule("bank.example.com")})

	privateIdx, userIdx := -1, -1
	for i, r := range cfg.Routing.Rules {
		if len(r.IP) > 0 && r.OutboundTag == TagDirect && privateIdx < 0 {
			privateIdx = i
		}
		if len(r.Domain) > 0 && r.Domain[0] == "domain:bank.example.com" {
			userIdx = i
		}
	}
	if privateIdx < 0 {
		t.Fatal("the private-network rule is missing")
	}
	if userIdx < 0 {
		t.Fatal("the user rule never made it into the config")
	}
	if userIdx < privateIdx {
		t.Error("user rules must come after the private-network rule")
	}
	if userIdx != len(cfg.Routing.Rules)-1 {
		t.Errorf("user rule at %d, expected it last for a single profile "+
			"(rules: %d)", userIdx, len(cfg.Routing.Rules))
	}
}

// A bypass only behaves like one if the name is resolved off the tunnel:
// resolving it through the tunnel hands back an address picked for the exit
// node's location. Which resolver does it is the whole question — see below.
func TestBypassedDomainsResolveOffTheTunnel(t *testing.T) {
	cfg := buildWithRules(t, []model.Rule{bypassRule("bank.example.com", "domain:gov.tr")})

	if cfg.DNS == nil || len(cfg.DNS.Servers) < 2 {
		t.Fatalf("dns section = %+v", cfg.DNS)
	}
	first, ok := cfg.DNS.Servers[0].(DNSServer)
	if !ok {
		t.Fatalf("first dns server = %#v, want a scoped entry", cfg.DNS.Servers[0])
	}
	// The device's own upstream resolver, never "localhost". The system
	// resolver here is dnsmasq, and in the recommended DNS mode dnsmasq's
	// upstream is this very core: "localhost" is a loop that takes down every
	// other name on the device with it.
	if first.Address != "217.31.228.210" {
		t.Errorf("first dns server address = %q, want the upstream resolver", first.Address)
	}
	if len(first.Domains) != 2 {
		t.Errorf("scoped domains = %v, want both rule domains", first.Domains)
	}
	// The upstream resolver must still be there for everything else, reached
	// over TCP so a server that drops UDP cannot take name resolution with it.
	if cfg.DNS.Servers[len(cfg.DNS.Servers)-1] != "tcp://1.1.1.1" {
		t.Errorf("upstream resolver missing: %#v", cfg.DNS.Servers)
	}
	// And the query has to leave the tunnel: an ISP resolver answers nothing
	// to a foreign address.
	if !hasIP(cfg, "217.31.228.210/32") {
		t.Error("the resolver is not routed directly; its answers never arrive")
	}
}

// Nothing is worse than the loop. With no resolver known, the scoped entry is
// simply left out and the domain resolves through the tunnel: a worse address,
// but a working device.
func TestBypassedDomainsWithoutAKnownResolver(t *testing.T) {
	cfg := buildWithRulesAndResolvers(t,
		[]model.Rule{bypassRule("bank.example.com")}, nil)

	for _, srv := range cfg.DNS.Servers {
		if s, ok := srv.(DNSServer); ok && s.Address == "localhost" {
			t.Fatal("localhost as a DNS server sends the query back to dnsmasq, " +
				"whose upstream is this core")
		}
	}
}

// TestClientScopedBypassDoesNotChangeGlobalDNS covers the case where a bypass
// applies to one client only: resolving that domain locally for everybody
// would leak the exception to clients the rule was never meant to touch.
func TestClientScopedBypassDoesNotChangeGlobalDNS(t *testing.T) {
	r := bypassRule("bank.example.com")
	r.Sources = []string{"192.168.1.50"}
	cfg := buildWithRules(t, []model.Rule{r})

	for _, srv := range cfg.DNS.Servers {
		if s, ok := srv.(DNSServer); ok && s.Address == "localhost" {
			t.Errorf("a client-scoped rule changed global DNS resolution: %+v", s)
		}
	}
}

func TestDisabledRulesAreNotEmitted(t *testing.T) {
	r := bypassRule("bank.example.com")
	r.Enabled = false
	cfg := buildWithRules(t, []model.Rule{r})

	for _, rule := range cfg.Routing.Rules {
		for _, d := range rule.Domain {
			if d == "bank.example.com" {
				t.Error("a disabled rule was emitted")
			}
		}
	}
	for _, srv := range cfg.DNS.Servers {
		if s, ok := srv.(DNSServer); ok && s.Address == "localhost" {
			t.Error("a disabled rule still changed DNS resolution")
		}
	}
}

func TestRuleActionsMapToTheRightOutbound(t *testing.T) {
	cases := []struct {
		action model.RuleAction
		want   string
	}{
		{model.ActionDirect, TagDirect},
		{model.ActionBlock, TagBlock},
		{model.ActionProxy, TagProxy},
	}
	for _, tc := range cases {
		r := bypassRule("x.example.com")
		r.Action = tc.action
		cfg := buildWithRules(t, []model.Rule{r})

		found := false
		for _, rule := range cfg.Routing.Rules {
			if len(rule.Domain) > 0 && rule.Domain[0] == "domain:x.example.com" {
				found = true
				if rule.OutboundTag != tc.want {
					t.Errorf("action %s -> outbound %q, want %q",
						tc.action, rule.OutboundTag, tc.want)
				}
			}
		}
		if !found {
			t.Errorf("action %s: rule missing", tc.action)
		}
	}
}

// TestProxyActionUsesTheBalancerInAGroup: with a group there is no single
// proxy outbound, so naming one would make the core reject the config.
func TestProxyActionUsesTheBalancerInAGroup(t *testing.T) {
	r := bypassRule("x.example.com")
	r.Action = model.ActionProxy

	g := groupWith(model.StrategyLeastPing)
	s := model.Defaults()
	cfg, err := Build(Options{
		Group: g, Members: members(2), Settings: &s,
		Caps: AllFeatures(), Rules: []model.Rule{r},
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, rule := range cfg.Routing.Rules {
		if len(rule.Domain) > 0 && rule.Domain[0] == "domain:x.example.com" {
			if rule.BalancerTag != TagBalancer {
				t.Errorf("proxy action in a group = %+v, want balancerTag", rule)
			}
			if rule.OutboundTag != "" {
				t.Error("a rule must not name both an outbound and a balancer")
			}
		}
	}
}

func TestAllMatchersReachTheConfig(t *testing.T) {
	r := model.Rule{
		ID: "r1", Name: "Everything", Enabled: true, Action: model.ActionDirect,
		Domains:    []string{"a.example.com"},
		IPs:        []string{"203.0.113.0/24"},
		Sources:    []string{"192.168.1.50"},
		Port:       "80,443,8000-9000",
		SourcePort: "1024-65535",
		Protocols:  []string{"tls", "http"},
		Network:    "tcp",
	}
	cfg := buildWithRules(t, []model.Rule{r})

	var got *Rule
	for i := range cfg.Routing.Rules {
		if len(cfg.Routing.Rules[i].Domain) > 0 &&
			cfg.Routing.Rules[i].Domain[0] == "domain:a.example.com" {
			got = &cfg.Routing.Rules[i]
		}
	}
	if got == nil {
		t.Fatal("rule missing")
	}
	if len(got.IP) != 1 || got.IP[0] != "203.0.113.0/24" {
		t.Errorf("ip = %v", got.IP)
	}
	if len(got.Source) != 1 || got.Source[0] != "192.168.1.50" {
		t.Errorf("source = %v", got.Source)
	}
	if got.Port != "80,443,8000-9000" {
		t.Errorf("port = %q", got.Port)
	}
	if got.SourcePort != "1024-65535" {
		t.Errorf("sourcePort = %q", got.SourcePort)
	}
	if len(got.Protocol) != 2 {
		t.Errorf("protocol = %v", got.Protocol)
	}
	if got.Network != "tcp" {
		t.Errorf("network = %q", got.Network)
	}
}

func TestInvalidRulesAreRefused(t *testing.T) {
	cases := []struct {
		name string
		rule model.Rule
		want string
	}{
		{"no matcher", model.Rule{ID: "r", Name: "Empty", Enabled: true,
			Action: model.ActionDirect}, "matches nothing"},
		{"bad action", model.Rule{ID: "r", Name: "Bad", Enabled: true,
			Action: "allow", Domains: []string{"a.com"}}, "unknown action"},
		{"url as domain", model.Rule{ID: "r", Name: "URL", Enabled: true,
			Action: model.ActionDirect, Domains: []string{"https://a.com/x"}}, "not a domain"},
		{"ip as domain", model.Rule{ID: "r", Name: "IP", Enabled: true,
			Action: model.ActionDirect, Domains: []string{"1.2.3.4"}}, "addresses field"},
		{"bad prefix", model.Rule{ID: "r", Name: "P", Enabled: true,
			Action: model.ActionDirect, Domains: []string{"host:a.com"}}, "unknown domain prefix"},
		{"bad cidr", model.Rule{ID: "r", Name: "C", Enabled: true,
			Action: model.ActionDirect, IPs: []string{"10.0.0.0/99"}}, "not a valid network"},
		{"bad port", model.Rule{ID: "r", Name: "Pt", Enabled: true,
			Action: model.ActionDirect, Port: "99999"}, "out of range"},
		{"backwards range", model.Rule{ID: "r", Name: "R", Enabled: true,
			Action: model.ActionDirect, Port: "900-100"}, "backwards"},
		{"bad protocol", model.Rule{ID: "r", Name: "Pr", Enabled: true,
			Action: model.ActionDirect, Protocols: []string{"smtp"}}, "unknown protocol"},
		{"bad network", model.Rule{ID: "r", Name: "N", Enabled: true,
			Action: model.ActionDirect, Network: "sctp"}, "network must be"},
	}

	s := model.Defaults()
	for _, tc := range cases {
		_, err := Build(Options{
			Profile: realityProfile(), Settings: &s,
			Caps: AllFeatures(), Rules: []model.Rule{tc.rule},
		})
		if err == nil {
			t.Errorf("%s: expected a refusal", tc.name)
			continue
		}
		if !contains(err.Error(), tc.want) {
			t.Errorf("%s: error %q should mention %q", tc.name, err, tc.want)
		}
	}
}

// TestGeoRuleRefusedWithoutData keeps a common footgun diagnosable: naming a
// geosite list without the data files installed stops the core from starting.
func TestGeoRuleRefusedWithoutData(t *testing.T) {
	t.Setenv("XRAY_LOCATION_ASSET", t.TempDir())
	if AssetsAvailable() {
		t.Skip("geo data present in the test environment")
	}
	r := model.Rule{ID: "r", Name: "Ads", Enabled: true,
		Action: model.ActionBlock, Domains: []string{"geosite:category-ads"}}

	s := model.Defaults()
	_, err := Build(Options{
		Profile: realityProfile(), Settings: &s,
		Caps: AllFeatures(), Rules: []model.Rule{r},
	})
	if err == nil {
		t.Fatal("expected a geosite rule without data files to be refused")
	}
	if !contains(err.Error(), "geo data") {
		t.Errorf("error should explain the missing data files: %v", err)
	}
}

// What the operator types is not what the core matches on. An unprefixed entry
// is a substring to the core: "bank.com" would also take "bank.com.attacker.net"
// off the VPN, which is the opposite of what a bypass rule is for.
func TestBareDomainMatchesTheNameAndItsSubdomains(t *testing.T) {
	cfg := buildWithRules(t, []model.Rule{
		bypassRule("bank.example.com", "keyword:ads", "full:exact.example.com",
			"regexp:.*\\.example\\.net$"),
	})

	var got []string
	for _, r := range cfg.Routing.Rules {
		if r.OutboundTag == TagDirect && len(r.Domain) > 0 {
			got = r.Domain
		}
	}
	want := []string{
		"domain:bank.example.com",
		"keyword:ads",
		"full:exact.example.com",
		"regexp:.*\\.example\\.net$",
	}
	if len(got) != len(want) {
		t.Fatalf("domains = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("domain %d = %q, want %q", i, got[i], want[i])
		}
	}
}
