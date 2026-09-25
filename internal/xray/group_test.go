package xray

import (
	"testing"

	"xwrt/internal/model"
)

func members(n int) []model.Profile {
	out := make([]model.Profile, 0, n)
	for i := 0; i < n; i++ {
		p := *realityProfile()
		p.ID = string(rune('a' + i))
		p.Name = "Server " + p.ID
		p.Address = p.ID + ".example.com"
		out = append(out, p)
	}
	return out
}

func groupWith(st model.Strategy) *model.Group {
	g := &model.Group{
		ID:       "g1",
		Name:     "My Group",
		Strategy: st,
		Members:  []string{"a", "b", "c"},
	}
	g.Normalize()
	return g
}

// TestGroupProducesOneOutboundPerMember is the basic shape check: the balancer
// selects on a tag prefix, so every member needs its own tagged outbound.
func TestGroupProducesOneOutboundPerMember(t *testing.T) {
	g := groupWith(model.StrategyLeastPing)
	s := model.Defaults()
	cfg, err := Build(Options{Group: g, Members: members(3), Settings: &s, Caps: AllFeatures()})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	want := []string{"proxy-0", "proxy-1", "proxy-2"}
	got := []string{}
	for _, out := range cfg.Outbounds {
		if out.Tag == TagDirect || out.Tag == TagBlock || out.Tag == TagDNSOut {
			continue
		}
		got = append(got, out.Tag)
	}
	if len(got) != len(want) {
		t.Fatalf("member outbounds = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("outbound %d tag = %q, want %q", i, got[i], want[i])
		}
	}

	// Each member must point at its own server, not all at the first one.
	for i, out := range cfg.Outbounds[:3] {
		vnext := out.Settings.(map[string]any)["vnext"].([]any)[0].(map[string]any)
		want := string(rune('a'+i)) + ".example.com"
		if vnext["address"] != want {
			t.Errorf("outbound %d address = %v, want %v", i, vnext["address"], want)
		}
	}
}

// TestGroupRoutesThroughTheBalancer guards the mistake that would silently
// pin every connection to one member: without a catch-all balancer rule,
// unmatched traffic falls through to the first outbound.
func TestGroupRoutesThroughTheBalancer(t *testing.T) {
	g := groupWith(model.StrategyLeastPing)
	s := model.Defaults()
	cfg, err := Build(Options{Group: g, Members: members(3), Settings: &s, Caps: AllFeatures()})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if len(cfg.Routing.Balancers) != 1 {
		t.Fatalf("got %d balancers, want 1", len(cfg.Routing.Balancers))
	}
	bal := cfg.Routing.Balancers[0]
	if bal.Strategy == nil || bal.Strategy.Type != "leastPing" {
		t.Errorf("balancer strategy = %+v", bal.Strategy)
	}
	if len(bal.Selector) != 1 || bal.Selector[0] != TagProxyPrefix {
		t.Errorf("balancer selector = %v", bal.Selector)
	}

	last := cfg.Routing.Rules[len(cfg.Routing.Rules)-1]
	if last.BalancerTag != bal.Tag {
		t.Fatalf("last routing rule does not use the balancer: %+v", last)
	}
	if last.Network != "tcp,udp" {
		t.Errorf("balancer rule network = %q, want tcp,udp", last.Network)
	}
	if last.OutboundTag != "" {
		t.Errorf("balancer rule must not also name an outbound: %q", last.OutboundTag)
	}
}

// TestObservatoryMatchesStrategy pins the dependency the core enforces:
// leastPing needs an observatory, leastLoad needs a burst observatory, and the
// others need neither.
func TestObservatoryMatchesStrategy(t *testing.T) {
	cases := []struct {
		st        model.Strategy
		observer  bool
		burst     bool
		hasFallbk bool
	}{
		{model.StrategyLeastPing, true, false, true},
		{model.StrategyLeastLoad, false, true, true},
		{model.StrategyRandom, false, false, false},
		{model.StrategyRoundRobin, false, false, false},
	}

	for _, tc := range cases {
		g := groupWith(tc.st)
		s := model.Defaults()
		cfg, err := Build(Options{Group: g, Members: members(2), Settings: &s, Caps: AllFeatures()})
		if err != nil {
			t.Fatalf("%s: Build: %v", tc.st, err)
		}
		if (cfg.Observatory != nil) != tc.observer {
			t.Errorf("%s: observatory present = %v, want %v",
				tc.st, cfg.Observatory != nil, tc.observer)
		}
		if (cfg.BurstObservatory != nil) != tc.burst {
			t.Errorf("%s: burstObservatory present = %v, want %v",
				tc.st, cfg.BurstObservatory != nil, tc.burst)
		}
		bal := cfg.Routing.Balancers[0]
		if (bal.FallbackTag != "") != tc.hasFallbk {
			t.Errorf("%s: fallbackTag = %q, want present = %v",
				tc.st, bal.FallbackTag, tc.hasFallbk)
		}
		if cfg.Observatory != nil {
			if cfg.Observatory.ProbeURL != model.DefaultProbeURL {
				t.Errorf("%s: probe URL = %q", tc.st, cfg.Observatory.ProbeURL)
			}
			if cfg.Observatory.ProbeInterval != model.DefaultProbeInterval {
				t.Errorf("%s: probe interval = %q", tc.st, cfg.Observatory.ProbeInterval)
			}
		}
	}
}

// TestGroupWithNoUsableMembersIsRefused covers the case a subscription refresh
// creates: the group still exists, but every profile it named is gone.
func TestGroupWithNoUsableMembersIsRefused(t *testing.T) {
	g := groupWith(model.StrategyLeastPing)
	s := model.Defaults()
	_, err := Build(Options{Group: g, Members: nil, Settings: &s, Caps: AllFeatures()})
	if err == nil {
		t.Fatal("expected a group with no resolvable members to be refused")
	}
}

// TestGroupMemberErrorNamesTheMember keeps a bad member diagnosable: with a
// dozen servers in a group, "invalid profile" is not enough to act on.
func TestGroupMemberErrorNamesTheMember(t *testing.T) {
	ms := members(3)
	ms[1].UUID = "" // makes this member invalid
	ms[1].Name = "Broken Server"

	g := groupWith(model.StrategyRandom)
	s := model.Defaults()
	_, err := Build(Options{Group: g, Members: ms, Settings: &s, Caps: AllFeatures()})
	if err == nil {
		t.Fatal("expected an invalid member to be refused")
	}
	if !contains(err.Error(), "Broken Server") {
		t.Errorf("error should name the offending member: %v", err)
	}
	if !contains(err.Error(), "My Group") {
		t.Errorf("error should name the group: %v", err)
	}
}

// TestSingleProfileStillUsesTheProxyTag makes sure adding groups did not
// change the single-server shape, which the stats query depends on.
func TestSingleProfileStillUsesTheProxyTag(t *testing.T) {
	s := model.Defaults()
	cfg, err := Build(Options{Profile: realityProfile(), Settings: &s, Caps: AllFeatures()})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if cfg.Outbounds[0].Tag != TagProxy {
		t.Errorf("first outbound tag = %q, want %q", cfg.Outbounds[0].Tag, TagProxy)
	}
	if len(cfg.Routing.Balancers) != 0 {
		t.Error("a single profile must not produce a balancer")
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) &&
		(haystack == needle || indexOf(haystack, needle) >= 0)
}

func indexOf(h, n string) int {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return i
		}
	}
	return -1
}

// TestAChosenProbeURLReachesTheCore guards the whole length of the field.
//
// Every layer between the dialog and the core copies this string, and a layer
// that drops it fails silently: Normalize puts the default back, the config is
// valid, the tunnel works, and the setting someone deliberately changed simply
// has no effect. Nothing anywhere says so.
func TestAChosenProbeURLReachesTheCore(t *testing.T) {
	const chosen = "https://cp.cloudflare.com/generate_204"

	g := groupWith(model.StrategyLeastPing)
	g.ProbeURL = chosen
	s := model.Defaults()
	cfg, err := Build(Options{Group: g, Members: members(2), Settings: &s, Caps: AllFeatures()})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if cfg.Observatory == nil {
		t.Fatal("no observatory, so leastPing has nothing to rank with")
	}
	if cfg.Observatory.ProbeURL != chosen {
		t.Errorf("probe URL = %q, want the one that was chosen (%q)",
			cfg.Observatory.ProbeURL, chosen)
	}

	// leastLoad reads it from a different field of a different structure, so
	// one of the two can be wired up and the other left on the default.
	g2 := groupWith(model.StrategyLeastLoad)
	g2.ProbeURL = chosen
	cfg2, err := Build(Options{Group: g2, Members: members(2), Settings: &s, Caps: AllFeatures()})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if cfg2.BurstObservatory == nil || cfg2.BurstObservatory.PingConfig == nil {
		t.Fatal("no burst observatory, so leastLoad has nothing to rank with")
	}
	if got := cfg2.BurstObservatory.PingConfig.Destination; got != chosen {
		t.Errorf("burst probe destination = %q, want %q", got, chosen)
	}
}
