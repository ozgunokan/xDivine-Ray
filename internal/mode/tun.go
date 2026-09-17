// Package mode implements the two ways traffic reaches the core: a TUN device
// served by hev-socks5-tunnel, and firewall-based capture.
//
// TUN mode is the more portable of the two. It needs no TPROXY support in the
// kernel and handles every protocol identically, at the cost of one extra
// process and a userspace TCP/IP stack.
package mode

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"xwrt/internal/model"
	"xwrt/internal/netenv"
	"xwrt/internal/proc"
)

// Routing preferences. They sit above the kernel's main table (32766) so the
// TUN routes win, while the escape hatches stay above them.
const (
	prefBypassNets = 90  // LAN and bypass destinations keep using main
	prefCoreMark   = 100 // the core's own sockets keep using main
	prefTunMarked  = 190 // marked traffic goes into the TUN table (mixed mode)
	prefTunDefault = 200 // everything goes into the TUN table (tun mode)
)

// Scope is how much traffic the TUN device carries.
type Scope int

const (
	// ScopeAll routes everything through the tunnel: full TUN mode.
	ScopeAll Scope = iota
	// ScopeMarkedUDP routes only packets carrying the UDP mark, which is what
	// mixed mode needs: TCP stays on the kernel path through NAT redirect, and
	// only UDP is diverted here.
	ScopeMarkedUDP
)

// Tun manages the TUN device and the hev-socks5-tunnel process.
type Tun struct {
	Settings *model.Settings
	Env      *netenv.Env
	Scope    Scope
	OnLog    func(line string)

	process    *proc.Process
	configPath string
	applied    bool
}

// NewTun returns a TUN manager for the given scope.
func NewTun(s *model.Settings, env *netenv.Env, scope Scope, onLog func(string)) *Tun {
	return &Tun{Settings: s, Env: env, Scope: scope, OnLog: onLog}
}

// Running reports whether the tunnel process is alive.
func (t *Tun) Running() bool {
	return t.process != nil && t.process.Running()
}

// RecentOutput returns the tunnel's last output lines, which is where the
// reason for a failed start usually is: hev reports a refused SOCKS port or an
// unusable device in its own words and then simply does nothing.
func (t *Tun) RecentOutput(n int) []string {
	if t.process == nil {
		return nil
	}
	return t.process.RecentOutput(n)
}

// Start writes the tunnel config, launches hev-socks5-tunnel and installs the
// policy routes.
//
// The routing design never touches the main table. Instead a dedicated table
// holds the default route through the TUN device, and higher-priority rules
// carve out the traffic that must not go there: packets the core itself sends
// (matched by its socket mark) and packets bound for the LAN. Leaving the main
// table intact means an unclean shutdown cannot strand the device without a
// default route.
//
// The core's escape rule matters even more in mixed mode than in full TUN
// mode: a profile using a UDP transport such as QUIC or mKCP sends UDP itself,
// and without that rule the tunnel would swallow its own upstream traffic.
func (t *Tun) Start(bypassCIDRs []string) error {
	s := t.Settings
	if err := os.MkdirAll(s.RunDir, 0o755); err != nil {
		return fmt.Errorf("run dir: %w", err)
	}
	if _, err := os.Stat("/dev/net/tun"); err != nil {
		return fmt.Errorf("/dev/net/tun is missing: install kmod-tun")
	}

	t.configPath = filepath.Join(s.RunDir, "hev.yaml")
	if err := os.WriteFile(t.configPath, []byte(t.renderConfig()), 0o600); err != nil {
		return fmt.Errorf("write tunnel config: %w", err)
	}

	t.process = &proc.Process{
		Name:  "hev-socks5-tunnel",
		Path:  s.HevBin,
		Args:  []string{t.configPath},
		Env:   os.Environ(),
		OnLog: t.log,
	}
	if err := t.process.Start(); err != nil {
		return fmt.Errorf("start hev-socks5-tunnel: %w", err)
	}

	if err := t.waitForDevice(); err != nil {
		t.process.Stop()
		return err
	}
	if err := t.configureDevice(); err != nil {
		t.process.Stop()
		return err
	}
	if err := t.installRoutes(bypassCIDRs); err != nil {
		t.removeRoutes()
		t.process.Stop()
		return err
	}
	t.applied = true
	return nil
}

// Stop removes the routes and terminates the tunnel.
func (t *Tun) Stop() {
	if t.applied {
		t.removeRoutes()
		t.applied = false
	}
	if t.process != nil {
		t.process.Stop()
		t.process = nil
	}
	// The device disappears with the process, but a crashed process can leave
	// it behind.
	runQuiet("ip", "link", "del", t.Settings.TunName)
}

// renderConfig produces hev-socks5-tunnel's YAML. It is written by hand rather
// than with a YAML library: the schema is four fixed keys, and a dependency
// costs more than it saves on a device this size.
func (t *Tun) renderConfig() string {
	s := t.Settings
	var b strings.Builder
	b.WriteString("tunnel:\n")
	b.WriteString("  name: " + s.TunName + "\n")
	b.WriteString("  mtu: " + strconv.Itoa(s.TunMTU) + "\n")
	b.WriteString("  multi-queue: false\n")
	b.WriteString("  ipv4: " + s.TunAddr + "\n")
	b.WriteString("\nsocks5:\n")
	b.WriteString("  port: " + strconv.Itoa(s.SocksPort) + "\n")
	b.WriteString("  address: 127.0.0.1\n")
	b.WriteString("  udp: 'udp'\n")
	b.WriteString("  pipeline: false\n")
	b.WriteString("\nmisc:\n")
	// The tunnel allocates one task stack per connection. hev's own default is
	// used rather than a trimmed value: on a 128 MB device the memory is
	// affordable, and a stack that is too small shows up as corruption under
	// load rather than as a clean error.
	b.WriteString("  task-stack-size: 86016\n")
	b.WriteString("  log-file: stderr\n")
	b.WriteString("  log-level: " + hevLogLevel(s.LogLevel) + "\n")
	b.WriteString("  limit-nofile: 65535\n")
	return b.String()
}

// waitForDevice polls until hev-socks5-tunnel has created the interface.
func (t *Tun) waitForDevice() error {
	name := t.Settings.TunName
	for i := 0; i < 50; i++ {
		if _, err := net.InterfaceByName(name); err == nil {
			return nil
		}
		sleepMS(100)
	}
	return fmt.Errorf("tun device %s did not appear", name)
}

func (t *Tun) configureDevice() error {
	s := t.Settings
	prefix := maskToPrefix(s.TunMask)
	addr := s.TunAddr + "/" + strconv.Itoa(prefix)

	// hev-socks5-tunnel assigns the address itself on most builds, and on those
	// adding it again leaves the device carrying the same address twice with
	// different prefixes — /32 from hev and /16 from here. Nothing breaks, but
	// anyone reading `ip addr` has to work out which one matters. Add it only
	// when the device came up without one.
	if !hasAddress(s.TunName) {
		runQuiet("ip", "addr", "add", addr, "dev", s.TunName)
	}
	if err := run("ip", "link", "set", "dev", s.TunName, "up"); err != nil {
		return fmt.Errorf("bring up %s: %w", s.TunName, err)
	}
	runQuiet("ip", "link", "set", "dev", s.TunName, "mtu", strconv.Itoa(s.TunMTU))
	return nil
}

func (t *Tun) installRoutes(bypassCIDRs []string) error {
	s := t.Settings
	table := strconv.Itoa(s.RouteTable)

	// Start clean so a previous crash cannot leave duplicate rules.
	t.removeRoutes()

	if err := run("ip", "route", "add", "default", "dev", s.TunName, "table", table); err != nil {
		return fmt.Errorf("tun default route: %w", err)
	}

	// The core's own upstream connections must keep using the real WAN.
	if err := run("ip", "rule", "add", "fwmark", s.MarkHex(), "lookup", "main",
		"pref", strconv.Itoa(prefCoreMark)); err != nil {
		return fmt.Errorf("core bypass rule: %w", err)
	}

	// LAN and explicitly bypassed destinations keep using the main table, so
	// router management and local services are unaffected.
	nets := append([]string{}, t.Env.LANCIDRs...)
	nets = append(nets, bypassCIDRs...)
	for _, cidr := range dedupe(nets) {
		if !validCIDR(cidr) {
			continue
		}
		runQuiet("ip", "rule", "add", "to", cidr, "lookup", "main",
			"pref", strconv.Itoa(prefBypassNets))
	}

	// What gets diverted into the tunnel depends on the scope. In mixed mode
	// the firewall marks UDP and only that mark is matched here, so TCP keeps
	// using the main table and the kernel-fast NAT redirect path.
	switch t.Scope {
	case ScopeMarkedUDP:
		if err := run("ip", "rule", "add", "fwmark", s.MarkTunUDPHex(), "lookup", table,
			"pref", strconv.Itoa(prefTunMarked)); err != nil {
			return fmt.Errorf("tun policy rule for marked UDP: %w", err)
		}
	default:
		if err := run("ip", "rule", "add", "lookup", table,
			"pref", strconv.Itoa(prefTunDefault)); err != nil {
			return fmt.Errorf("tun policy rule: %w", err)
		}
	}
	return nil
}

// CleanStale removes policy rules, the routing table and the device left
// behind by a previous run that did not shut down cleanly — an OOM kill, a
// power cut mid-connect. It reports whether anything was actually there.
//
// This matters more than it sounds: a leftover rule sending traffic into a
// routing table whose device is gone black-holes it, and nothing in the system
// will tell the owner why their internet stopped working.
func (t *Tun) CleanStale() bool {
	found := t.hasStaleState()
	t.removeRoutes()
	runQuiet("ip", "link", "del", t.Settings.TunName)
	return found
}

func (t *Tun) hasStaleState() bool {
	if _, err := net.InterfaceByName(t.Settings.TunName); err == nil {
		return true
	}
	out, err := output("ip", "rule", "show")
	if err != nil {
		return false
	}
	for _, pref := range []int{prefCoreMark, prefTunMarked, prefTunDefault} {
		if strings.Contains(out, strconv.Itoa(pref)+":") {
			return true
		}
	}
	return false
}

func (t *Tun) removeRoutes() {
	table := strconv.Itoa(t.Settings.RouteTable)
	for _, pref := range []int{prefBypassNets, prefCoreMark, prefTunMarked, prefTunDefault} {
		// One rule may exist per bypass network, so delete until none remain.
		for i := 0; i < 32; i++ {
			if err := run("ip", "rule", "del", "pref", strconv.Itoa(pref)); err != nil {
				break
			}
		}
	}
	runQuiet("ip", "route", "flush", "table", table)
}

// loadedRuleset returns the firewall rules the kernel is actually enforcing.
// It is a variable so tests can supply one without a kernel.
var loadedRuleset = func() (string, bool) {
	if out, err := output("nft", "list", "ruleset"); err == nil && out != "" {
		return out, true
	}
	if out, err := output("iptables-save"); err == nil && out != "" {
		return out, true
	}
	return "", false
}

// ZoneWarning reports a misconfiguration that would silently break forwarding.
//
// The kernel is asked first, and it is the only answer that counts. The UCI
// config can describe a perfectly good zone that fw4 has never loaded — a
// firewall that was reloaded from a config predating the install, a config
// restored from backup — and in that state everything in /etc/config looks
// right while the forward chain, whose policy is drop, has no rule that lets a
// packet out through the device. Checking the config alone would have declared
// that machine healthy.
func (t *Tun) ZoneWarning(uciGet func(pkg, sec, opt string) string) string {
	name := t.Settings.TunName

	if rules, ok := loadedRuleset(); ok {
		// fw4 renders a zone's device as `oifname "xwrt0"`; fw3 renders it as
		// `-o xwrt0`. Either way the device name appears, and if it appears
		// nowhere then no rule can accept traffic leaving through it.
		if strings.Contains(rules, `"`+name+`"`) || strings.Contains(rules, "-o "+name) {
			return ""
		}
		// Before blaming the zone, ask whether the firewall can load anything at
		// all. One broken file in /etc/nftables.d makes fw4 reject the entire
		// ruleset, and from then on every reload silently does nothing: the
		// kernel keeps enforcing the last ruleset that worked, new zones never
		// appear, and the error is nowhere near the symptom. Telling someone in
		// that state to reload the firewall is advice that cannot work.
		if reason := firewallRenderError(); reason != "" {
			return fmt.Sprintf("the firewall cannot build its ruleset, so no zone — "+
				"including the one %s needs — can ever be installed, and every "+
				"firewall reload on this device silently does nothing. Fix that "+
				"first; nothing here can work around it. fw4 says:\n%s", name, reason)
		}

		return fmt.Sprintf("the firewall has no rule for %s, so every forwarded "+
			"packet through it is dropped and clients have no internet at all. "+
			"The zone may be missing from the configuration, or configured but "+
			"never loaded. Fix both with: sh /etc/uci-defaults/99-xwrt; "+
			"uci commit firewall; /etc/init.d/firewall reload", name)
	}

	// No kernel view available, so fall back to what the configuration claims.
	// Three things have to be true, and each one fails the same silent way: the
	// device is in a zone, some zone is allowed to forward into it, and that
	// zone masquerades.
	if uciGet("firewall", "@zone[0]", "name") == "" {
		return ""
	}

	zone, masq := "", ""
	for i := 0; i < 32; i++ {
		sec := fmt.Sprintf("@zone[%d]", i)
		zoneName := uciGet("firewall", sec, "name")
		if zoneName == "" {
			break
		}
		if strings.Contains(uciGet("firewall", sec, "device"), name) ||
			strings.Contains(uciGet("firewall", sec, "network"), name) {
			zone = zoneName
			masq = uciGet("firewall", sec, "masq")
			break
		}
	}
	if zone == "" {
		return fmt.Sprintf("TUN device %q is not in any firewall zone, so every "+
			"forwarded packet is dropped and clients have no internet at all. "+
			"Add it to a zone with masquerading, or run "+
			"`sh /etc/uci-defaults/99-xwrt` to recreate the one this package ships.",
			name)
	}

	for i := 0; i < 64; i++ {
		sec := fmt.Sprintf("@forwarding[%d]", i)
		dest := uciGet("firewall", sec, "dest")
		if dest == "" {
			break
		}
		if dest == zone {
			if masq != "1" {
				return fmt.Sprintf("firewall zone %q carries %s but does not "+
					"masquerade, so replies cannot come back. Fix it with: "+
					"uci set firewall.%s.masq=1; uci commit firewall; "+
					"/etc/init.d/firewall reload", zone, name, zone)
			}
			return ""
		}
	}

	return fmt.Sprintf("no firewall rule allows forwarding into zone %q, so LAN "+
		"clients have no internet while this mode is active. Add one with: "+
		"uci add firewall forwarding; uci set firewall.@forwarding[-1].src=lan; "+
		"uci set firewall.@forwarding[-1].dest=%s; uci commit firewall; "+
		"/etc/init.d/firewall reload  (replace lan with your LAN zone's name "+
		"if it is called something else)", zone, zone)
}

func hevLogLevel(level string) string {
	switch strings.ToLower(level) {
	case "debug":
		return "debug"
	case "info":
		return "info"
	case "error", "none":
		return "error"
	default:
		return "warn"
	}
}

func maskToPrefix(mask string) int {
	ip := net.ParseIP(mask)
	if ip == nil {
		return 24
	}
	v4 := ip.To4()
	if v4 == nil {
		return 24
	}
	ones, _ := net.IPv4Mask(v4[0], v4[1], v4[2], v4[3]).Size()
	if ones == 0 {
		return 24
	}
	return ones
}

func validCIDR(s string) bool {
	_, _, err := net.ParseCIDR(s)
	return err == nil
}

func (t *Tun) log(line string) {
	if t.OnLog != nil {
		t.OnLog(line)
	}
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

// hasAddress reports whether the device already carries an IPv4 address.
func hasAddress(name string) bool {
	iface, err := net.InterfaceByName(name)
	if err != nil {
		return false
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return false
	}
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil {
			return true
		}
	}
	return false
}
