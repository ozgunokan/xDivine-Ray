package fw

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"xwrt/internal/model"
)

// nftBackend targets OpenWrt 22.03 and newer, where fw4 owns `table inet fw4`.
// Everything here lives in `table ip xwrt`, a table fw4 does not know about, so
// the two coexist and teardown never has to reason about someone else's rules.
type nftBackend struct{}

func (b *nftBackend) Name() string { return "nftables" }

func (b *nftBackend) Apply(p Plan) error {
	// A mode that captures nothing owns no table. Reverting rather than
	// installing an empty one keeps Installed() honest, which the firewall
	// reload hook depends on.
	//
	// TUN mode is the exception, and only for DNS. Routing carries every packet
	// into the tunnel there, so no capture rules are needed — but "hijack port
	// 53" is a firewall rule too, and skipping the whole table skipped that as
	// well. Clients then kept asking whatever resolver they were handed, which
	// on a router means dnsmasq asking the ISP's resolvers *through the tunnel*:
	// addresses that only answer from the ISP's own network. Every lookup times
	// out, and a tunnel that is working perfectly looks completely dead.
	if !p.Mode.NeedsFirewallCapture() && !p.RedirectDNS {
		return b.Revert()
	}
	if p.Mode.NeedsTProxy() {
		if err := requireIP(); err != nil {
			return err
		}
	}

	ruleset := b.ruleset(p)

	cmd := exec.Command("nft", "-f", "-")
	cmd.Stdin = strings.NewReader(ruleset)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("nft: %s\n--- ruleset ---\n%s", strings.TrimSpace(string(out)), ruleset)
	}

	// TPROXY needs a local route for the packets it marks. Only tproxy mode
	// uses it; mixed mode's UDP marking is routed by the TUN manager instead.
	if p.Mode.NeedsTProxy() {
		if err := addTProxyRouting(p.MarkTProxy); err != nil {
			_ = b.Revert()
			return err
		}
	}
	return nil
}

func (b *nftBackend) Revert() error {
	// The bare table line makes the delete succeed even when nothing was
	// installed, which keeps teardown idempotent.
	script := "table ip " + Namespace + "\ndelete table ip " + Namespace + "\n"
	cmd := exec.Command("nft", "-f", "-")
	cmd.Stdin = strings.NewReader(script)
	_ = cmd.Run()
	delTProxyRouting()
	return nil
}

func (b *nftBackend) Installed() bool {
	out, err := runCmdOutput("nft", "list", "tables")
	if err != nil {
		return false
	}
	return strings.Contains(out, "ip "+Namespace)
}

// ruleset renders the complete table. Replacing it atomically (create, delete,
// recreate) means a reconnect never leaves half-applied rules behind.
//
// Which chains appear follows from the mode:
//
//	redirect  a nat chain redirecting TCP
//	mixed     the same nat chain, plus a mangle chain marking UDP so policy
//	          routing sends it into the TUN device
//	tproxy    a mangle chain tproxying both protocols; no nat chain at all
//	tun       nothing, because routing carries everything
func (b *nftBackend) ruleset(p Plan) string {
	var s strings.Builder

	s.WriteString("table ip " + Namespace + "\n")
	s.WriteString("delete table ip " + Namespace + "\n")
	s.WriteString("table ip " + Namespace + " {\n")

	// Bypass destinations.
	s.WriteString("\tset bypass {\n\t\ttype ipv4_addr\n\t\tflags interval\n\t\tauto-merge\n")
	if bypass := p.AllBypass(); len(bypass) > 0 {
		s.WriteString("\t\telements = { " + strings.Join(bypass, ", ") + " }\n")
	}
	s.WriteString("\t}\n\n")

	// Clients excluded from proxying entirely.
	s.WriteString("\tset bypassmac {\n\t\ttype ether_addr\n")
	if len(p.BypassMACs) > 0 {
		s.WriteString("\t\telements = { " + strings.Join(p.BypassMACs, ", ") + " }\n")
	}
	s.WriteString("\t}\n\n")

	if p.Mode.CapturesTCPViaRedirect() {
		b.writeNATChain(&s, p)
	}
	if !p.Mode.NeedsFirewallCapture() && p.RedirectDNS {
		b.writeDNSOnlyChain(&s, p)
	}
	if p.Mode.MarksUDPForTun() {
		b.writeUDPMarkChain(&s, p)
	}
	if p.Mode.NeedsTProxy() {
		b.writeTProxyChain(&s, p)
	}
	if p.ProxyRouter && p.Mode.NeedsFirewallCapture() {
		b.writeOutputChains(&s, p)
	}
	if p.BlockQUIC && !p.Mode.CarriesUDP() {
		b.writeQUICChain(&s, p)
	}

	s.WriteString("}\n")
	return s.String()
}

// writeNATChain redirects TCP into the transparent inbound. DNS is handled here
// too, for both protocols: a UDP redirect works fine for DNS because the DNS
// inbound has a fixed upstream configured and does not need to recover the
// original destination the way a proxied flow would.
func (b *nftBackend) writeNATChain(s *strings.Builder, p Plan) {
	tp := strconv.Itoa(p.TProxyPort)
	dnsPort := strconv.Itoa(p.DNSPort)

	s.WriteString("\tchain prerouting {\n")
	s.WriteString("\t\ttype nat hook prerouting priority -105; policy accept;\n")
	b.writeScopeGuards(s, p)
	if p.RedirectDNS {
		s.WriteString("\t\tudp dport 53 counter redirect to :" + dnsPort + "\n")
		s.WriteString("\t\ttcp dport 53 counter redirect to :" + dnsPort + "\n")
	}
	s.WriteString("\t\tmeta l4proto tcp counter redirect to :" + tp + "\n")
	s.WriteString("\t}\n\n")
}

// writeDNSOnlyChain is TUN mode's share of the firewall: nothing but the DNS
// hijack. Routing already carries the traffic, but a client that resolves names
// somewhere else still has to be sent to the core's resolver.
func (b *nftBackend) writeDNSOnlyChain(s *strings.Builder, p Plan) {
	dnsPort := strconv.Itoa(p.DNSPort)

	s.WriteString("\tchain prerouting {\n")
	s.WriteString("\t\ttype nat hook prerouting priority -105; policy accept;\n")
	b.writeScopeGuards(s, p)
	s.WriteString("\t\tudp dport 53 counter redirect to :" + dnsPort + "\n")
	s.WriteString("\t\ttcp dport 53 counter redirect to :" + dnsPort + "\n")
	s.WriteString("\t}\n\n")
}

// writeUDPMarkChain is mixed mode's UDP half. It only marks; the routing rule
// installed by the TUN manager is what actually diverts the packet. Marking in
// prerouting is deliberate: it runs before the routing decision for forwarded
// packets, which is exactly when the rule lookup happens.
func (b *nftBackend) writeUDPMarkChain(s *strings.Builder, p Plan) {
	mark := fmt.Sprintf("0x%x", p.MarkTunUDP)
	dnsPort := strconv.Itoa(p.DNSPort)

	s.WriteString("\tchain mangle_prerouting {\n")
	s.WriteString("\t\ttype filter hook prerouting priority -150; policy accept;\n")
	b.writeScopeGuards(s, p)
	if p.RedirectDNS {
		// DNS is redirected to a local port by the nat chain below. Marking it
		// would send it out of the TUN instead of to the local socket, so it
		// has to escape here.
		s.WriteString("\t\tudp dport 53 return\n")
		s.WriteString("\t\ttcp dport 53 return\n")
		_ = dnsPort
	}
	s.WriteString("\t\tmeta l4proto udp counter meta mark set " + mark + "\n")
	s.WriteString("\t}\n\n")
}

// writeQUICChain refuses QUIC from the LAN in the one mode that cannot carry
// it.
//
// Redirect mode proxies TCP and lets UDP go straight out, so a browser talking
// QUIC to a video site is not going through the tunnel at all. On a network
// where that direct path is filtered or shaped, the result is not a clean
// failure — it is a video that stalls, because the client keeps trying a UDP
// connection that half works instead of using the TCP one that would have been
// proxied. Refusing the UDP is what makes it give up and fall back, and a
// browser does that immediately.
//
// A refusal rather than a drop, and that is the whole point: a dropped packet
// is answered by a timeout, which is the stall this is meant to remove. An
// ICMP port-unreachable arrives at once.
//
// The forward hook only. The router's own UDP is left alone deliberately: a
// profile whose transport is QUIC or mKCP reaches its server over UDP 443 from
// this device, and blocking that would take the tunnel down rather than fix
// anything.
//
// Priority is ahead of the distribution's own forward chain so the refusal is
// not sitting behind an accept.
func (b *nftBackend) writeQUICChain(s *strings.Builder, p Plan) {
	s.WriteString("\tchain forward {\n")
	s.WriteString("\t\ttype filter hook forward priority -25; policy accept;\n")
	b.writeScopeGuards(s, p)
	s.WriteString("\t\tudp dport 443 counter reject\n")
	s.WriteString("\t}\n\n")
}

// writeTProxyChain implements tproxy mode: both protocols delivered locally
// without NAT.
func (b *nftBackend) writeTProxyChain(s *strings.Builder, p Plan) {
	tp := strconv.Itoa(p.TProxyPort)
	dnsPort := strconv.Itoa(p.DNSPort)
	tmark := fmt.Sprintf("0x%x", p.MarkTProxy)

	s.WriteString("\tchain prerouting {\n")
	s.WriteString("\t\ttype filter hook prerouting priority -150; policy accept;\n")
	b.writeScopeGuards(s, p)
	// Packets belonging to an already established transparent socket must be
	// re-marked, not re-tproxied.
	s.WriteString("\t\tmeta l4proto tcp socket transparent 1 meta mark set " + tmark + " accept\n")
	s.WriteString("\t\tmeta l4proto udp socket transparent 1 meta mark set " + tmark + " accept\n")
	if p.RedirectDNS {
		s.WriteString("\t\tudp dport 53 tproxy to :" + dnsPort + " meta mark set " + tmark + " accept\n")
		s.WriteString("\t\ttcp dport 53 tproxy to :" + dnsPort + " meta mark set " + tmark + " accept\n")
	}
	s.WriteString("\t\tmeta l4proto tcp counter tproxy to :" + tp + " meta mark set " + tmark + " accept\n")
	s.WriteString("\t\tmeta l4proto udp counter tproxy to :" + tp + " meta mark set " + tmark + " accept\n")
	s.WriteString("\t}\n\n")
}

// writeOutputChains capture traffic the router itself originates. The core's
// own mark has to be checked first in every one of them: without it the core's
// upstream connections would be fed straight back into the core.
//
// TPROXY is a prerouting-only mechanism, so in tproxy mode the router's own UDP
// cannot be captured this way; only TCP is.
func (b *nftBackend) writeOutputChains(s *strings.Builder, p Plan) {
	mark := fmt.Sprintf("0x%x", p.Mark)

	// The router's own DNS is deliberately NOT redirected here.
	//
	// It looks like it should be: the prerouting hijack never sees a query the
	// router makes itself, and on OpenWrt the router is the resolver clients
	// actually use, so dnsmasq keeps asking whatever upstream it was handed.
	// Redirecting it in the output hook was tried and it broke name resolution
	// on a real device in every capture mode — the rule fired, the counters
	// climbed, and no answer ever came back. Whatever the reason, the mechanism
	// is not dependable, and DNS is not a thing to be clever about: when it
	// fails, everything looks broken and nothing says why.
	//
	// The supported way to put the router's own resolver behind the core is DNS
	// mode "dnsmasq", which points dnsmasq straight at the core's resolver with
	// no address translation in the path at all.
	if p.ProxyRouter && (p.Mode.CapturesTCPViaRedirect() || p.Mode.NeedsTProxy()) {
		s.WriteString("\tchain output {\n")
		s.WriteString("\t\ttype nat hook output priority -105; policy accept;\n")
		s.WriteString("\t\tmeta mark " + mark + " return\n")
		s.WriteString("\t\tip daddr @bypass return\n")
		s.WriteString("\t\tmeta l4proto tcp counter redirect to :" +
			strconv.Itoa(p.TProxyPort) + "\n")
		s.WriteString("\t}\n\n")
	}

	if p.ProxyRouter && p.Mode.MarksUDPForTun() {
		// A route hook, not a filter hook: changing the mark on a locally
		// generated packet only takes effect if the kernel is asked to
		// re-evaluate the route afterwards.
		s.WriteString("\tchain mangle_output {\n")
		s.WriteString("\t\ttype route hook output priority -150; policy accept;\n")
		s.WriteString("\t\tmeta mark " + mark + " return\n")
		s.WriteString("\t\tip daddr @bypass return\n")
		if p.RedirectDNS {
			s.WriteString("\t\tudp dport 53 return\n")
		}
		s.WriteString("\t\tmeta l4proto udp meta mark set " +
			fmt.Sprintf("0x%x", p.MarkTunUDP) + "\n")
		s.WriteString("\t}\n\n")
	}
}

// writeScopeGuards limits capture to client-facing interfaces and lets the
// bypass sets short-circuit everything else.
func (b *nftBackend) writeScopeGuards(s *strings.Builder, p Plan) {
	switch {
	case len(p.LANDevices) > 0:
		s.WriteString("\t\tiifname != { " + quoteList(p.LANDevices) + " } return\n")
	case p.WANDevice != "":
		// Nothing was detected as LAN, so capture everything except upstream.
		s.WriteString("\t\tiifname \"" + p.WANDevice + "\" return\n")
	}
	if len(p.BypassMACs) > 0 {
		s.WriteString("\t\tether saddr @bypassmac return\n")
	}
	s.WriteString("\t\tip daddr @bypass return\n")
}

// addTProxyRouting installs the policy route TPROXY depends on: marked packets
// are delivered locally rather than forwarded.
func addTProxyRouting(mark int) error {
	m := fmt.Sprintf("0x%x", mark)
	table := strconv.Itoa(tproxyRouteTable)
	delTProxyRouting()
	if err := runCmd("ip", "rule", "add", "fwmark", m, "lookup", table, "pref", tproxyRulePref); err != nil {
		return fmt.Errorf("tproxy policy rule: %w", err)
	}
	if err := runCmd("ip", "route", "add", "local", "0.0.0.0/0", "dev", "lo", "table", table); err != nil {
		runCmdQuiet("ip", "rule", "del", "fwmark", m, "lookup", table)
		return fmt.Errorf("tproxy local route: %w", err)
	}
	return nil
}

func delTProxyRouting() {
	table := strconv.Itoa(tproxyRouteTable)
	runCmdQuiet("ip", "route", "flush", "table", table)
	// The rule may have been added more than once across restarts.
	for i := 0; i < 4; i++ {
		runCmdQuiet("ip", "rule", "del", "pref", tproxyRulePref)
	}
}

const (
	// tproxyRouteTable holds the single "local default" route TPROXY needs.
	tproxyRouteTable = 181
	tproxyRulePref   = "181"
)

// compile-time guard that the mode helpers this file relies on exist.
var _ = model.ModeMixed
