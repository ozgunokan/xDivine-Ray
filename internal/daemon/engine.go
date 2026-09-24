// Package daemon wires the pieces together: configuration, the core process,
// the capture mode and the firewall.
package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"xwrt/internal/fw"
	"xwrt/internal/mode"
	"xwrt/internal/model"
	"xwrt/internal/netenv"
	"xwrt/internal/netmon"
	"xwrt/internal/proc"
	"xwrt/internal/selftest"
	"xwrt/internal/ucicfg"
	"xwrt/internal/xray"
)

// Engine owns the runtime state of the connection.
type Engine struct {
	store *ucicfg.Store
	uci   *ucicfg.UCI
	Log   *LogRing

	mu          sync.Mutex
	env         *netenv.Env
	firewall    fw.Backend
	core        *proc.Process
	tun         *mode.Tun
	dns         *mode.DNS
	connected   bool
	since       time.Time
	profile     *model.Profile
	group       *model.Group
	members     []model.Profile
	settings    model.Settings
	coreVersion string

	// coreStarting is set while the connect sequence is waiting for the core.
	// An exit during that window is reported by the connect path, with the step
	// and the hint that belong to it, so the supervisor's own handler stays
	// quiet rather than filing the same event a second time. It is atomic
	// because the handler runs on the supervisor's goroutine while the connect
	// holds the engine lock.
	coreStarting atomic.Bool

	// liveFP fingerprints the material the running core was built from, so an
	// edit made while connected can be told from one that only looks like a
	// change. Empty when nothing is connected.
	liveFP string

	// stopping is set when the daemon is shutting down, and stopCh is closed at
	// the same moment. Both exist for the startup retry loop: it can be asleep
	// for five minutes, and a service stop must not wait for it.
	stopping  bool
	stopCh    chan struct{}
	closeOnce sync.Once

	// updater holds what the daemon last learned about releases, and whether an
	// install is in flight. Its own lock: an update check talks to the network
	// and must not hold the lock the whole interface waits on.
	updater updater

	statsStop chan struct{}
	stats     model.Stats
	prevStats model.Stats
	prevAt    time.Time
	history   *History

	// What each member of a group has carried, by outbound tag, and which of
	// them moved between the last two readings. A balancer never announces its
	// choice — but a counter that grows belongs to a member it chose, and that
	// is the only evidence there is.
	memberBytes  map[string]tagTraffic
	memberMoving map[string]bool

	// When the connection table was last reported as nearly full.
	conntrackWarnedAt time.Time
}

// conntrackWarnEvery is how often that warning may repeat.
const conntrackWarnEvery = 10 * time.Minute

// Version is the build this daemon was compiled from. It is set from the same
// ldflags value as the CLI's, and reported in the status so that a daemon still
// running from a previous install is visible rather than merely puzzling.
var Version = "dev"

// statsInterval is how often the traffic counters are read. It is also the
// resolution of the live chart, so it is a compromise between a responsive
// graph and how often a router wants to fork a process to ask the core.
const statsInterval = 2 * time.Second

// New returns an engine backed by the given store.
func New(store *ucicfg.Store, uci *ucicfg.UCI, log *LogRing) *Engine {
	// 300 samples at two seconds is ten minutes of history, a few tens of
	// kilobytes.
	return &Engine{store: store, uci: uci, Log: log, history: NewHistory(300),
		stopCh: make(chan struct{})}
}

// Traffic returns the live throughput view.
func (e *Engine) Traffic() TrafficReport {
	e.mu.Lock()
	connected := e.connected
	e.mu.Unlock()

	up, down := e.history.Peak()
	return TrafficReport{
		Connected:       connected,
		Samples:         e.history.Samples(),
		PeakUp:          up,
		PeakDown:        down,
		IntervalSeconds: int(statsInterval / time.Second),
		TakenAt:         time.Now(),
	}
}

// Env returns the last detected network layout, refreshing it when stale.
func (e *Engine) Env() *netenv.Env {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.env == nil || time.Since(e.env.DetectedAt) > 30*time.Second {
		e.refreshEnvLocked()
	}
	return e.env
}

func (e *Engine) refreshEnvLocked() {
	d, err := e.store.Load()
	ov := netenv.Overrides{}
	if err == nil {
		ov.LANDevice = d.Settings.LANDevice
		ov.WANDevice = d.Settings.WANDevice
	}
	e.env = netenv.Detect(ov)
}

// Status reports the current runtime state.
func (e *Engine) Status() model.Status {
	e.mu.Lock()
	defer e.mu.Unlock()

	st := model.Status{
		Connected:   e.connected,
		Mode:        e.settings.Mode,
		CoreRunning: e.core != nil && e.core.Running(),
		// The last failure comes from the log rather than a field here, so the
		// message a client shows and the message in the log are the same one.
		LastError:   e.Log.LastError(),
		Stats:       e.stats,
		Version:     Version,
		CoreVersion: e.coreVersion,
	}
	if e.tun != nil {
		st.TunRunning = e.tun.Running()
	}
	switch {
	case e.group != nil:
		st.TargetKind = model.TargetGroup
		st.ProfileID = e.group.ID
		st.ProfileName = e.group.Label()
		st.GroupStrategy = string(e.group.Strategy)
		st.GroupMembers = len(e.members)
		st.GroupUsage, st.GroupLive = e.memberUsageLocked()
	case e.profile != nil:
		st.TargetKind = model.TargetProfile
		st.ProfileID = e.profile.ID
		st.ProfileName = e.profile.Label()
	}
	if e.connected {
		st.Since = e.since.Format(time.RFC3339)
		st.UptimeSeconds = int64(time.Since(e.since).Seconds())
	}
	if e.env != nil {
		st.LANDevice = strings.Join(e.env.LANDevices, ",")
		st.WANDevice = e.env.WANDevice
		st.WANGateway = e.env.WANGateway
		st.Firewall = string(e.env.Firewall)
	}
	return st
}

// Connect brings up the given target, replacing any active connection. The ID
// names either a profile or a group; both are looked up, since the two ID
// spaces are distinct by construction.
//
// Every failure of the sequence is filed exactly once, here. The steps below
// return errors carrying their own step, detail and hint and do no logging of
// their own, which is what stops one failure from appearing three times at
// three levels of wrapping.
func (e *Engine) Connect(targetID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if err := e.connectLocked(targetID); err != nil {
		e.record(err)
		e.teardownLocked()
		return err
	}
	// Only a connect that got all the way through clears the banner; a failed
	// one has just filed a fresh error.
	e.Log.ClearLastError()
	return nil
}

func (e *Engine) connectLocked(targetID string) error {
	data, err := e.store.Load()
	if err != nil {
		return fail(model.StepConfig, "config.read", err).
			withHint("hint.config_syntax")
	}
	if targetID == "" {
		targetID = data.Settings.Active
	}
	if targetID == "" {
		return fail(model.StepConfig, "config.nothing_selected").
			withHint("hint.pick_target")
	}

	profile := data.Profile(targetID)
	group := data.Group(targetID)
	if profile == nil && group == nil {
		return fail(model.StepConfig, "config.no_target", targetID)
	}

	var members []model.Profile
	kind := model.TargetProfile
	label := ""
	if group != nil {
		kind = model.TargetGroup
		label = group.Label()
		if err := group.Validate(); err != nil {
			return fail(model.StepConfig, "config.group_invalid", label, err)
		}
		members = data.GroupMembers(group)
		if len(members) == 0 {
			return fail(model.StepConfig, "config.group_empty", label).
				withDetail("every profile the group referenced is gone").
				withHint("hint.group_members")
		}
		if len(members) < len(group.Members) {
			e.Log.Warnf("group %s: %d of %d members no longer exist and were skipped",
				label, len(group.Members)-len(members), len(group.Members))
		}
	} else {
		label = profile.Label()
		if err := profile.Validate(); err != nil {
			return fail(model.StepConfig, "config.profile_invalid", label, err)
		}
	}

	e.teardownLocked()
	e.settings = data.Settings
	e.refreshEnvLocked()

	if err := e.startLocked(profile, group, members, data.Rules, &data.Settings); err != nil {
		return err
	}

	e.profile = profile
	e.group = group
	e.members = members
	// What this connection was built from, so a later edit can be compared
	// against it rather than assumed to matter. See fingerprint.go.
	e.liveFP = fingerprint(materialFor(data, targetID))
	e.connected = true
	e.since = time.Now()
	e.startStatsLocked()

	// Remember the selection so a reboot reconnects to the same target. A
	// failure here does not fail the connect: the connection is up, it just
	// will not come back by itself after a reboot.
	if _, err := e.store.Update(func(d *ucicfg.Data) error {
		d.Settings.Active = targetID
		d.Settings.ActiveKind = kind
		d.Settings.Enabled = true
		return nil
	}); err != nil {
		e.Log.Warnf("connected, but the active selection could not be saved, "+
			"so a reboot will not reconnect on its own: %v", err)
	}

	if group != nil {
		e.Log.Infof("connected to group %s (%d servers, %s) via %s mode",
			label, len(members), group.Strategy.Describe(), e.settings.Mode)
		if !group.Strategy.HealthAware() {
			e.Log.Warnf("strategy %s does not check health, so a server that goes "+
				"down keeps receiving connections; use leastPing for failover",
				group.Strategy)
		}
	} else {
		e.Log.Infof("connected to %s via %s mode", label, e.settings.Mode)
	}
	return nil
}

// resolveServer looks the server's hostname up once, at connect time, so the
// address can go straight into the core's configuration.
//
// The alternative is what the core does on its own: ask the system resolver
// every time it opens a connection to the server, which with XTLS over plain
// TCP is every single proxied connection. musl caches nothing, so each of those
// is a real query to whatever is in resolv.conf — and one slow, filtered or
// dropped answer turns into one connection that hangs for a second before it
// even starts. That is exactly the shape of "most of it is fine, but some pages
// stall", and it disappears when the profile carries an address instead of a
// name. This makes a name behave like an address.
//
// A failure here is not a connect failure: the hostname stays in the config and
// the core resolves it the way it always did.
func (e *Engine) resolveServer(host string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil || len(addrs) == 0 {
		e.Log.Warnf("could not resolve %s (%v); the core will look it up itself, "+
			"once per connection, which is slower and less predictable", host, err)
		return ""
	}

	// IPv4 wins when the name has both. A half-broken IPv6 path to the server
	// is a common way for every connection to pause before falling back, and
	// this is the one address where being conservative costs nothing: if the
	// name has no IPv4 at all, the v6 address is used.
	var v4, v6 string
	for _, a := range addrs {
		if a.IP.To4() != nil {
			if v4 == "" {
				v4 = a.IP.String()
			}
			continue
		}
		if v6 == "" {
			v6 = a.IP.String()
		}
	}
	pick := v4
	if pick == "" && v6 != "" {
		pick = v6
	}
	if pick == "" {
		return ""
	}

	if len(addrs) > 1 {
		e.Log.Infof("%s resolved to %s (%d addresses); using it for this session",
			host, pick, len(addrs))
	} else {
		e.Log.Infof("%s resolved to %s; using it for this session", host, pick)
	}
	return pick
}

func (e *Engine) startLocked(p *model.Profile, g *model.Group, members []model.Profile, rules []model.Rule, s *model.Settings) error {
	if err := os.MkdirAll(s.RunDir, 0o755); err != nil {
		return fail(model.StepConfig, "config.rundir", s.RunDir, err)
	}

	if !s.Mode.Valid() {
		return fail(model.StepConfig, "config.mode_unknown", s.Mode).
			withHint("hint.mode_values")
	}
	// Fail before starting anything rather than half-way through: a mode whose
	// mechanism the kernel does not have will never work, and saying so here
	// keeps the device on its previous, working configuration.
	if s.Mode.NeedsTProxy() && !e.env.HasTProxy {
		return fail(model.StepConfig, "config.tproxy_missing", s.Mode).
			withHint("hint.tproxy_install")
	}
	e.Log.Infof("capture mode %s: %s", s.Mode, s.Mode.Describe())

	// --- config ---------------------------------------------------------

	// Ask the installed core which optional features it accepts before
	// generating anything. Cores differ in what they have removed, and a
	// config naming a removed feature is rejected outright.
	caps := xray.Probe(s.XrayBin)
	e.coreVersion = caps.Version

	cfg, err := xray.Build(xray.Options{
		Profile:        p,
		Group:          g,
		Members:        members,
		Rules:          rules,
		Settings:       s,
		Caps:           caps,
		DirectCIDRs:    append(append([]string{}, e.env.LANCIDRs...), s.BypassIP...),
		LocalResolvers: e.env.WANResolvers,
		Resolve:        e.resolveServer,
	})
	if err != nil {
		return fail(model.StepConfig, "config.build", err)
	}
	raw, err := cfg.JSON()
	if err != nil {
		return fail(model.StepConfig, "config.render", err)
	}
	configPath := filepath.Join(s.RunDir, "xray.json")
	if err := os.WriteFile(configPath, raw, 0o600); err != nil {
		return fail(model.StepConfig, "config.write", err)
	}
	if err := validateCoreConfig(s.XrayBin, configPath); err != nil {
		return err
	}
	e.Log.Step(model.StepConfig).Infof("core configuration written to %s and accepted",
		configPath)
	// Check the ports before starting anything. A core that cannot bind exits
	// immediately and gets restarted, which reads as a crash loop; and if
	// whatever holds the port is itself listening, a naive "wait for the port"
	// check would call that success and report a working connection that
	// carries no traffic at all.
	if err := checkPorts(cfg); err != nil {
		return err
	}

	// --- core -----------------------------------------------------------

	e.core = &proc.Process{
		Name:  "xray",
		Path:  s.XrayBin,
		Args:  []string{"run", "-c", configPath},
		Env:   os.Environ(),
		OnLog: func(line string) { e.Log.AddProcessLine(model.SourceCore, line) },
		OnExit: func(err error, intentional bool) {
			if intentional || e.coreStarting.Load() {
				return
			}
			// A core that dies says why in its own last lines, not in its exit
			// status, so those lines travel with the failure.
			if err == nil {
				err = errors.New("core exited unexpectedly")
			} else {
				err = fmt.Errorf("core exited: %w", err)
			}
			e.record(fail(model.StepCore, "core.exited", err).
				from(model.SourceCore).
				withLines(e.core.RecentOutput(15)).
				withHint("hint.core_restarting"))
		},
	}
	e.coreStarting.Store(true)
	if err := e.core.Start(); err != nil {
		e.coreStarting.Store(false)
		return fail(model.StepCore, "core.start", s.XrayBin, err).
			withHint("hint.install_xray")
	}
	err = e.waitForCore(s.SocksPort, 10*time.Second)
	e.coreStarting.Store(false)
	if err != nil {
		return err
	}

	e.Log.Step(model.StepCore).Infof("core %s listening (socks %d, transparent %d)",
		coreVersionLabel(caps), s.SocksPort, s.TProxPort)

	e.dns = mode.NewDNS(s)

	// --- tunnel ---------------------------------------------------------

	// The tunnel comes up before the firewall rules in mixed mode: the UDP mark
	// those rules set is meaningless until the policy route that acts on it
	// exists, and a mark with nowhere to go black-holes the packet.
	if s.Mode.NeedsTunProcess() {
		scope := mode.ScopeAll
		if s.Mode.MarksUDPForTun() {
			scope = mode.ScopeMarkedUDP
		}
		e.tun = mode.NewTun(s, e.env, scope, func(line string) {
			e.Log.AddProcessLine(model.SourceTunnel, line)
		})
		if err := e.tun.Start(s.BypassIP); err != nil {
			f := fail(model.StepTunnel, "tunnel.start", err).
				withLines(e.tun.RecentOutput(15))
			return tunnelHint(f, err, s)
		}
		step := e.Log.Step(model.StepTunnel)
		step.Infof("%s up, carrying %s", s.TunName, tunScopeName(scope))

		// What a missing firewall zone costs depends on the mode, so the same
		// finding is reported two different ways. In mixed mode TCP still goes
		// out through the NAT redirect and only UDP is lost, which is a warning.
		// In TUN mode everything goes through this device, so the connection
		// would come up and pass nothing at all — better to fail here, with the
		// commands that fix it, than to report success and leave someone
		// wondering why their internet stopped.
		if warn := e.tun.ZoneWarning(e.uci.GetOption); warn != "" {
			if scope == mode.ScopeAll {
				return fail(model.StepTunnel, "tunnel.firewall_drops").
					withDetail(warn).
					withHint("hint.tunnel_firewall")
			}
			step.Warnf("%s", warn)
		}
	}

	// --- does anything actually pass? -----------------------------------
	//
	// The core is running and listening, which is all the earlier steps can
	// tell. It says nothing about whether the far end accepts us: a wrong
	// password, an expired account, a server that renewed the certificate this
	// profile pins — each of those leaves a core that starts perfectly and a
	// tunnel that carries nothing.
	//
	// The check goes here, before the capture rules, because the failure is
	// far worse on the other side of them: rules that send the LAN into a core
	// that cannot reach its server do not degrade the connection, they end it,
	// and the device looks broken rather than unconnected. Reporting "connected"
	// in that state is the one answer that helps nobody.
	if err := selftest.Reachable(net.JoinHostPort("127.0.0.1",
		strconv.Itoa(s.SocksPort)), 8*time.Second); err != nil {
		f := fail(model.StepCore, "core.no_data", err).
			from(model.SourceCore).
			withLines(e.core.RecentOutput(15))
		return noDataHint(f, p, g)
	}
	e.Log.Step(model.StepCore).Infof("tunnel carries data")

	// --- firewall -------------------------------------------------------

	// TUN mode installs no capture rules, but it still needs the DNS hijack if
	// that is how DNS is configured — so the firewall step runs for it too.
	if s.Mode.NeedsFirewallCapture() || s.DNSMode == model.DNSRedirect {
		backend, err := fw.New(e.env)
		if err != nil {
			return fail(model.StepFirewall, "fw.no_backend", err).
				withHint("hint.fw_install_tools")
		}
		e.firewall = backend
		step := e.Log.Step(model.StepFirewall)
		if devs := strings.Join(e.env.LANDevices, ", "); devs != "" {
			step.Infof("installing %s capture rules for %s", e.env.Firewall, devs)
		} else {
			// Worth a warning rather than a bare statement: with no LAN device
			// detected the rules are installed but match nothing, so the
			// connection will look up while no client traffic is proxied.
			step.Warnf("installing %s capture rules, but no LAN device was "+
				"detected, so nothing will be captured; set lan_device if "+
				"detection guessed wrong", e.env.Firewall)
		}
		if err := backend.Apply(e.planLocked(s)); err != nil {
			return fail(model.StepFirewall, "fw.apply", e.env.Firewall, err).
				withHint("hint.fw_rolled_back")
		}
	}

	// --- dns ------------------------------------------------------------

	if s.DNSMode != model.DNSOff {
		e.Log.Step(model.StepDNS).Infof("resolution steered through the core (%s mode)",
			s.DNSMode)
	}
	if err := e.dns.Apply(); err != nil {
		return fail(model.StepDNS, "dns.apply", err).
			withHint("hint.dns_modes")
	}
	return nil
}

// tunScopeName says what the tunnel is carrying, which is the one thing that
// differs between mixed and full TUN mode.
func tunScopeName(scope mode.Scope) string {
	if scope == mode.ScopeMarkedUDP {
		return "UDP only"
	}
	return "all traffic"
}

// tunnelHint turns the common TUN failures into something actionable. The
// distinction matters: a missing kernel module and a missing binary read almost
// the same in a log line but need different packages installed.
func tunnelHint(f *stepError, err error, s *model.Settings) *stepError {
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "/dev/net/tun"):
		return f.withHint("hint.tun_kmod")
	case strings.Contains(msg, "executable file not found"),
		strings.Contains(msg, "no such file or directory") &&
			strings.Contains(msg, s.HevBin):
		return f.withHint("hint.tun_hev")
	case strings.Contains(msg, "did not appear"):
		return f.withHint("hint.tun_no_device")
	case strings.Contains(msg, "ip command"), strings.Contains(msg, "ip route"):
		return f.withHint("hint.tun_ip_full")
	default:
		return f
	}
}

func (e *Engine) planLocked(s *model.Settings) fw.Plan {
	return fw.Plan{
		Mode:        s.Mode,
		LANDevices:  e.env.LANDevices,
		LANCIDRs:    e.env.LANCIDRs,
		WANDevice:   e.env.WANDevice,
		TunDevice:   s.TunName,
		TProxyPort:  s.TProxPort,
		DNSPort:     s.DNSPort,
		Mark:        s.MarkValue(),
		MarkTProxy:  s.MarkTProxy(),
		MarkTunUDP:  s.MarkTunUDP(),
		ProxyRouter: s.ProxyRouter,
		RedirectDNS: s.DNSMode == model.DNSRedirect,
		BlockQUIC:   s.BlockQUIC,
		BypassCIDRs: s.BypassIP,
		BypassMACs:  s.BypassMAC,
	}
}

// Disconnect tears everything down and clears the enabled flag.
func (e *Engine) Disconnect() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.teardownLocked()
	if _, err := e.store.Update(func(d *ucicfg.Data) error {
		d.Settings.Enabled = false
		return nil
	}); err != nil {
		return err
	}
	e.Log.Infof("disconnected")
	return nil
}

// teardownLocked reverses everything in the opposite order it was applied.
//
// Nothing here aborts on an error. A teardown that stops half-way is worse than
// one that pushes through: it leaves capture rules pointing at a core that is
// gone, which black-holes the LAN. So each failure is logged as a warning and
// the next step runs anyway.
func (e *Engine) teardownLocked() {
	e.stopStatsLocked()
	step := e.Log.Step(model.StepTeardown)

	if e.dns != nil {
		if err := e.dns.Revert(); err != nil {
			step.Warnf("could not restore the DNS configuration: %v", err)
		}
		e.dns = nil
	}
	if e.firewall != nil {
		if err := e.firewall.Revert(); err != nil {
			step.Warnf("could not remove the capture rules; "+
				"run `fw4 restart` (or `fw3 restart`) to clear them: %v", err)
		}
		e.firewall = nil
	}
	if e.tun != nil {
		e.tun.Stop()
		e.tun = nil
	}
	if e.core != nil {
		e.core.Stop()
		e.core = nil
	}
	e.connected = false
	e.profile = nil
	e.group = nil
	e.members = nil
	e.liveFP = ""
	e.stats = model.Stats{}
	e.prevStats = model.Stats{}
}

// ReapplyFirewall reinstalls the capture rules. The firewall hotplug hook calls
// this after fw4 or fw3 restarts, because a full firewall reload flushes rules
// this daemon owns.
func (e *Engine) ReapplyFirewall() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.connected || e.firewall == nil {
		return nil
	}
	if e.firewall.Installed() {
		return nil
	}
	e.refreshEnvLocked()
	step := e.Log.Step(model.StepFirewall)
	step.Infof("capture rules were flushed by a firewall reload; reapplying")
	if err := e.firewall.Apply(e.planLocked(&e.settings)); err != nil {
		// Worth an error rather than a warning: the connection looks up in
		// status but no LAN traffic is being captured any more.
		err = fail(model.StepFirewall, "fw.reapply", err).
			withHint("hint.fw_reconnect")
		e.record(err)
		return err
	}
	return nil
}

// Reload reapplies the current configuration. Settings such as ports, mode or
// DNS handling only take effect on a fresh connection, so a reload of an active
// connection is a reconnect; an idle daemon has nothing to do.
func (e *Engine) Reload() error {
	e.mu.Lock()
	connected := e.connected
	id := ""
	switch {
	case e.group != nil:
		id = e.group.ID
	case e.profile != nil:
		id = e.profile.ID
	}
	e.mu.Unlock()

	if !connected {
		return nil
	}
	return e.Connect(id)
}

// ClearStaleState removes anything a previous run left behind and reports
// whether it found something.
//
// This runs at startup, before any connect. The daemon normally tears its own
// state down on exit, but an OOM kill, a `kill -9` or a power cut mid-connect
// skips that — and what is left behind is not harmless. Capture rules pointing
// at a core that is gone black-hole every LAN connection, and a policy route
// into an empty table does the same for whatever it matched. Nothing in the
// system would explain that to the owner; they would simply have no internet
// and a device that looks idle.
func (e *Engine) ClearStaleState() {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.connected {
		return
	}
	data, err := e.store.Load()
	if err != nil {
		return
	}
	s := data.Settings
	e.refreshEnvLocked()
	step := e.Log.Step(model.StepTeardown)

	if backend, err := fw.New(e.env); err == nil && backend.Installed() {
		if err := backend.Revert(); err != nil {
			step.Warnf("capture rules from an earlier run are still installed and "+
				"could not be removed; LAN traffic may be black-holed until "+
				"`fw4 restart` (or `fw3 restart`): %v", err)
		} else {
			step.Warnf("removed capture rules left behind by a previous run " +
				"that did not shut down cleanly")
		}
	}

	killStaleChildren(s.RunDir, step)

	tun := mode.NewTun(&s, e.env, mode.ScopeAll, nil)
	if tun.CleanStale() {
		step.Warnf("removed TUN routes and the %s device left behind by a "+
			"previous run", s.TunName)
	}

	// The dnsmasq snippet is removed by Revert even when this process never
	// wrote it, which is exactly the stale case.
	if err := mode.NewDNS(&s).Revert(); err != nil {
		step.Warnf("could not clean up the DNS configuration from an earlier run: %v", err)
	}
}

// Restore reconnects at startup, and one setting decides whether it does:
// auto_connect.
//
// It used to be two — auto_connect or "it was connected when we stopped" —
// and the second one made the first meaningless in the usual case. A router
// that reboots while connected came back connected whatever the box said, so
// turning the box off changed nothing anybody would notice, and the interface
// offered a switch that did not switch. A setting that is ignored in the
// common case is worse than no setting: it is a promise the software does not
// keep.
//
// So the box decides, both ways. Off means this daemon never dials out on its
// own — not after a reboot, not after a crash, not after `service xwrt
// restart` — and the operator connects when they want to. On means it always
// does, whatever state the connection was left in.
// wouldRestore is the decision on its own, so it can be checked without a
// daemon, a kernel and a WAN in the way. See TestOnlyTheSettingDecides…
func wouldRestore(s model.Settings) bool {
	return s.AutoConnect && s.Active != ""
}

// restoreDelay is how long to wait before attempt n+1 of the startup connect:
// ten seconds, doubling, capped at five minutes.
//
// The cap is the important half. Nothing about a router at boot is quick — the
// WAN negotiates, the clock is wrong until ntpd fixes it, the upstream provider
// is having the same power cut — and a schedule that backs off without a
// ceiling would be trying once an hour by the time the line comes back.
func restoreDelay(attempt int) time.Duration {
	d := 10 * time.Second
	for i := 1; i < attempt; i++ {
		d *= 2
		if d >= 5*time.Minute {
			return 5 * time.Minute
		}
	}
	return d
}

// restoreLoop retries the startup connect until it works, and returns how many
// attempts it took. It keeps trying rather than giving up, which is the whole
// point: see Restore.
//
// It is separate from Restore so the decision can be tested without a kernel, a
// WAN and a server on the other end. wait sleeps and reports whether the daemon
// is still running; done reports that something else has taken over — the
// operator pressed Connect, or the connection is already up — in which case
// there is nothing left to retry.
func restoreLoop(attempt func() error, done func() bool, wait func(time.Duration) bool) int {
	for n := 1; ; n++ {
		if done() {
			return n - 1
		}
		if err := attempt(); err == nil {
			return n
		}
		if done() {
			return n
		}
		if !wait(restoreDelay(n)) {
			return n
		}
	}
}

func (e *Engine) Restore() {
	data, err := e.store.Load()
	if err != nil {
		e.record(fail(model.StepConfig, "config.read_startup", err).
			withHint("hint.config_syntax"))
		return
	}
	if !wouldRestore(data.Settings) {
		return
	}

	// And then it keeps trying, for as long as the daemon is running.
	//
	// It used to be one attempt: wait ninety seconds for a gateway, dial once,
	// and on failure write a line in the log and stop. Every part of that is
	// survivable on a device someone can walk over to. On a router whose only
	// way out is this tunnel it is a lockout — the line comes back two minutes
	// after the power does, the one attempt has already been spent, and the
	// owner has no internet and no way to reach the box that could fix it until
	// they are standing in front of it. That is not a theoretical failure; it
	// cost someone an evening.
	//
	// Repeated failures collapse into a single journal entry with a count, so a
	// loop that runs all night does not push everything else out of the log.
	first := true
	n := restoreLoop(
		func() error {
			if !e.waitForWAN(90 * time.Second) {
				if first {
					e.record(fail(model.StepDetect, "detect.no_wan").
						withHint("hint.wan_keeps_trying"))
					first = false
				}
				return errNoWAN
			}
			first = false
			// Re-read rather than reuse: this loop can be running for hours,
			// and the operator may have picked a different server in the
			// meantime. Connect files its own failure; nobody is watching a
			// boot-time connect, so there is nothing to add here.
			target := data.Settings.Active
			if fresh, err := e.store.Load(); err == nil {
				target = fresh.Settings.Active
			}
			return e.Connect(target)
		},
		func() bool {
			e.mu.Lock()
			c, s := e.connected, e.stopping
			e.mu.Unlock()
			if c || s {
				return true
			}
			// Or the operator changed their mind while this was asleep:
			// unticked the box, or disconnected the active server. Retrying
			// after that is the daemon overriding a decision someone just
			// made.
			if fresh, err := e.store.Load(); err == nil && !wouldRestore(fresh.Settings) {
				return true
			}
			return false
		},
		e.sleep,
	)
	if n > 1 {
		e.Log.Infof("auto-connect at startup succeeded on attempt %d", n)
	}
}

// errNoWAN is internal to the retry loop: the failure is recorded by the
// caller, and the loop only needs to know that this attempt did not work.
var errNoWAN = errors.New("no upstream gateway yet")

// sleep waits, and reports whether the daemon is still running. A shutdown
// during a five-minute backoff must not hold the process open.
func (e *Engine) sleep(d time.Duration) bool {
	select {
	case <-e.stopCh:
		return false
	case <-time.After(d):
		return true
	}
}

func (e *Engine) waitForWAN(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		env := netenv.Detect(netenv.Overrides{})
		if env.WANGateway != "" {
			return true
		}
		// Through sleep, so a shutdown does not have to wait out the timeout.
		if !e.sleep(2 * time.Second) {
			return false
		}
	}
	return false
}

// Close stops everything. It does not change the stored enabled flag, so a
// service restart reconnects.
func (e *Engine) Close() {
	e.mu.Lock()
	e.stopping = true
	e.mu.Unlock()
	// Woken before the lock is taken for teardown: the retry loop checks
	// `stopping` under the same lock, and a five-minute sleep must not be what
	// a service stop waits for.
	e.closeOnce.Do(func() {
		if e.stopCh != nil {
			close(e.stopCh)
		}
	})

	e.mu.Lock()
	defer e.mu.Unlock()
	e.teardownLocked()
}

// --- stats -------------------------------------------------------------

func (e *Engine) startStatsLocked() {
	stop := make(chan struct{})
	e.statsStop = stop
	bin := e.settings.XrayBin
	port := e.settings.StatsPort

	e.history.Reset()

	go func() {
		ticker := time.NewTicker(statsInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				up, down, perTag, err := queryStats(bin, port)
				if err != nil {
					continue
				}
				e.mu.Lock()
				// Which members moved since the last reading. A counter that
				// grew belongs to a member the balancer actually chose; one
				// that stood still belongs to a member it did not, whatever
				// the strategy says it would do.
				if len(e.memberBytes) > 0 {
					moving := map[string]bool{}
					for tag, t := range perTag {
						if t.total() > e.memberBytes[tag].total() {
							moving[tag] = true
						}
					}
					e.memberMoving = moving
				}
				e.memberBytes = perTag
				now := time.Now()
				e.checkConntrackLocked(now)
				if !e.prevAt.IsZero() {
					if secs := now.Sub(e.prevAt).Seconds(); secs > 0 {
						e.stats.UplinkRate = int64(float64(up-e.prevStats.Uplink) / secs)
						e.stats.DownlinkRate = int64(float64(down-e.prevStats.Downlink) / secs)
					}
				}
				e.stats.Uplink, e.stats.Downlink = up, down
				e.prevStats = model.Stats{Uplink: up, Downlink: down}
				e.prevAt = now
				sample := Sample{
					At:           now.Unix(),
					Uplink:       up,
					Downlink:     down,
					UplinkRate:   e.stats.UplinkRate,
					DownlinkRate: e.stats.DownlinkRate,
				}
				e.mu.Unlock()

				e.history.Add(sample)
			}
		}
	}()
}

func (e *Engine) stopStatsLocked() {
	if e.statsStop != nil {
		close(e.statsStop)
		e.statsStop = nil
	}
	e.prevAt = time.Time{}
}

type statsResponse struct {
	Stat []struct {
		Name  string `json:"name"`
		Value any    `json:"value"`
	} `json:"stat"`
}

// queryStats reads the core's traffic counters through its own CLI. Talking to
// the gRPC API directly would pull the whole grpc stack into this binary; on a
// router, shelling out to a tool that is already installed is the better trade.
func queryStats(bin string, port int) (up, down int64, perTag map[string]tagTraffic, err error) {
	server := "127.0.0.1:" + strconv.Itoa(port)
	cmd := exec.Command(bin, "api", "statsquery", "--server="+server,
		"-pattern", statsPattern)
	out, err := cmd.Output()
	if err != nil {
		return 0, 0, nil, err
	}
	return sumTraffic(out)
}

// statsPattern selects the counters worth adding up.
//
// It stops at the tag, deliberately. A single profile is one outbound tagged
// "proxy", but a group is one outbound per member, tagged "proxy-0",
// "proxy-1"… — and a pattern of "outbound>>>proxy>>>traffic" matches the
// first and none of the rest, because what follows the tag is "-0>>>", not
// ">>>". That is why a group showed zero traffic while working perfectly:
// nothing was wrong with the connection, the counters were simply never
// asked for.
const statsPattern = "outbound>>>" + xray.TagProxy

// sumTraffic adds up whatever the query returned. A group has one counter per
// member and only their sum is meaningful: the balancer moves between members,
// so any single member's number is a fraction of what went through the tunnel.
// It also keeps the per-member figures, which are the only honest answer to
// "which server in this group am I actually on". A balancer does not announce
// its choice and can change it between one connection and the next; what it
// cannot hide is which member's counters are moving. The Status page used to
// name the group and stop there, which told an operator with five servers
// nothing about which of the five was carrying their traffic.
func sumTraffic(out []byte) (up, down int64, perTag map[string]tagTraffic, err error) {
	var res statsResponse
	if err := json.Unmarshal(out, &res); err != nil {
		return 0, 0, nil, err
	}
	perTag = map[string]tagTraffic{}
	for _, s := range res.Stat {
		v := toInt64(s.Value)
		tag := statTag(s.Name)
		switch {
		case strings.HasSuffix(s.Name, ">>>uplink"):
			up += v
			if tag != "" {
				t := perTag[tag]
				t.Up += v
				perTag[tag] = t
			}
		case strings.HasSuffix(s.Name, ">>>downlink"):
			down += v
			if tag != "" {
				t := perTag[tag]
				t.Down += v
				perTag[tag] = t
			}
		}
	}
	return up, down, perTag, nil
}

// checkConntrackLocked says something when the kernel's connection table is
// nearly full.
//
// It exists because that condition is invisible from everywhere anyone would
// think to look. The tunnel is up, the core is running, the interface says
// connected, and what someone sees is a video that plays for twenty minutes
// and then stalls for a few seconds at a time, over and over. The kernel's own
// complaint goes to dmesg. Nothing in this project said a word about it.
//
// Only while connected, and only occasionally: the condition lasts as long as
// the device is busy, and a line every two seconds would bury the log it is
// meant to be found in.
func (e *Engine) checkConntrackLocked(now time.Time) {
	if !e.connected {
		return
	}
	if !e.conntrackWarnedAt.IsZero() &&
		now.Sub(e.conntrackWarnedAt) < conntrackWarnEvery {
		return
	}
	c := netmon.ReadCapacity()
	if !c.Tight() {
		// Recovered: let the next tight moment be reported promptly rather
		// than in ten minutes.
		e.conntrackWarnedAt = time.Time{}
		return
	}
	e.conntrackWarnedAt = now
	e.Log.Warnf("the kernel's connection table is %d%% full (%d of %d). When it "+
		"fills, new connections are dropped until old ones time out, which looks "+
		"like a video freezing for a few seconds and then carrying on. The "+
		"capture modes that use NAT — redirect and mixed — take a slot per client "+
		"connection; TUN mode takes almost none, which is why the same device can "+
		"be fine in TUN and stall in mixed. Raise it with: sysctl -w "+
		"net.netfilter.nf_conntrack_max=%d  (and put it in /etc/sysctl.conf so it "+
		"survives a reboot)",
		c.Percent, c.Count, c.Max, c.Max*2)
}

// memberUsageLocked pairs each member of the running group with its counters.
//
// The pairing is by position and only by position: the config generator tags a
// group's outbounds "proxy-0", "proxy-1"… in the order the members are listed,
// and e.members is that same list. Nothing else connects a counter to a server,
// which is why both ends are built from one order rather than matched by name.
func (e *Engine) memberUsageLocked() ([]model.MemberUsage, []string) {
	if e.group == nil || len(e.members) == 0 {
		return nil, nil
	}
	usage := make([]model.MemberUsage, 0, len(e.members))
	var live []string
	for i := range e.members {
		tag := fmt.Sprintf("%s%d", xray.TagProxyPrefix, i)
		t := e.memberBytes[tag]
		u := model.MemberUsage{
			ProfileID: e.members[i].ID,
			Name:      e.members[i].Label(),
			Uplink:    t.Up,
			Downlink:  t.Down,
			Live:      e.memberMoving[tag],
		}
		if u.Live {
			live = append(live, u.Name)
		}
		usage = append(usage, u)
	}
	return usage, live
}

// tagTraffic is what one outbound has carried.
type tagTraffic struct{ Up, Down int64 }

func (t tagTraffic) total() int64 { return t.Up + t.Down }

// statTag pulls the outbound tag out of a counter name such as
//
//	outbound>>>proxy-2>>>traffic>>>uplink
//
// A single server is one outbound called "proxy"; a group is one per member,
// "proxy-0" upwards, numbered in the order the members are listed. That
// numbering is what maps a counter back to a profile, and it is the only thing
// that does — so it is read here rather than guessed at the call site.
func statTag(name string) string {
	rest, ok := strings.CutPrefix(name, "outbound>>>")
	if !ok {
		return ""
	}
	i := strings.Index(rest, ">>>")
	if i <= 0 {
		return ""
	}
	return rest[:i]
}

func toInt64(v any) int64 {
	switch t := v.(type) {
	case float64:
		return int64(t)
	case string:
		n, _ := strconv.ParseInt(t, 10, 64)
		return n
	case nil:
		return 0
	default:
		return 0
	}
}

// --- helpers -----------------------------------------------------------

// validateCoreConfig asks the core to parse the config before it is started, so
// a typo surfaces as a clear error instead of a restart loop.
//
// The core's own complaint goes in the detail rather than the message: it is
// often several lines of nested causes, and a one-line summary with the whole
// thing underneath reads far better than a single very long error.
func validateCoreConfig(bin, path string) error {
	cmd := exec.Command(bin, "run", "-c", path, "-test")
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}

	// "could not run the core" and "the core refused the config" are different
	// failures with different fixes, and they arrive here through the same
	// return value. Telling someone to look at their config when the real
	// problem is a missing package sends them the wrong way entirely.
	var execErr *exec.Error
	if errors.As(err, &execErr) || errors.Is(err, fs.ErrNotExist) ||
		errors.Is(err, fs.ErrPermission) {
		return fail(model.StepCore, "core.cannot_run", bin, err).
			withHint("hint.install_xray_at")
	}

	msg := strings.TrimSpace(string(out))
	// Older cores do not understand -test; do not fail the connect over that.
	if strings.Contains(msg, "flag provided but not defined") ||
		strings.Contains(msg, "unknown flag") {
		return nil
	}
	if msg == "" {
		msg = err.Error()
	}
	return fail(model.StepConfig, "config.rejected").
		from(model.SourceCore).
		withDetail(msg).
		withHint("hint.rejected_config", path)
}

// waitForCore waits until the core is serving, or says why it is not.
//
// Two things are watched, not one. Waiting for the port alone cannot tell a
// core that is still starting from one that has already died — and a core that
// dies is restarted on a backoff, so even "is the process running?" reads true
// again a moment later. The exit counter is what distinguishes them.
func (e *Engine) waitForCore(port int, timeout time.Duration) error {
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	deadline := time.Now().Add(timeout)
	var lastDialErr error

	for time.Now().Before(deadline) {
		if e.core.Exits() > 0 {
			return fail(model.StepCore, "core.exited_immediately").
				from(model.SourceCore).
				withLines(e.core.RecentOutput(15)).
				withHint("hint.core_last_lines")
		}
		conn, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			conn.Close()
			// One more liveness check: a listener can belong to something else
			// entirely, and a dead core with a stranger on its port is the one
			// failure that would otherwise be reported as success.
			if e.core.Exits() == 0 {
				return nil
			}
			continue
		}
		lastDialErr = err
		time.Sleep(200 * time.Millisecond)
	}

	f := fail(model.StepCore, "core.not_listening", port, timeout).
		from(model.SourceCore).
		withLines(e.core.RecentOutput(15)).
		withHint("hint.core_no_port")
	if lastDialErr != nil && len(e.core.RecentOutput(1)) == 0 {
		f = f.withDetail(lastDialErr.Error())
	}
	return f
}

// checkPorts refuses the connect when something else already holds a port the
// generated config needs. The config is the source of truth here rather than a
// second list of ports in this file: it knows which inbounds exist in this mode
// and which address each one listens on, so the check cannot drift from what
// the core will actually do.
func checkPorts(cfg *xray.Config) error {
	for _, in := range cfg.Inbounds {
		if in.Port <= 0 {
			continue
		}
		host := in.Listen
		if host == "" {
			host = "0.0.0.0"
		}
		addr := net.JoinHostPort(host, strconv.Itoa(in.Port))

		l, err := net.Listen("tcp", addr)
		if err != nil {
			return portInUse(in, addr, err)
		}
		l.Close()

		if inboundUsesUDP(in) {
			pc, err := net.ListenPacket("udp", addr)
			if err != nil {
				return portInUse(in, addr, err)
			}
			pc.Close()
		}
	}
	return nil
}

func portInUse(in xray.Inbound, addr string, err error) error {
	setting := portSetting(in.Tag)
	f := fail(model.StepConfig, "config.port_in_use", in.Port, inboundPurpose(in.Tag)).
		withDetail(err.Error())
	if setting != "" {
		f = f.withHint("hint.port_change", setting, addr)
	}
	return f
}

// inboundUsesUDP reports whether the inbound listens on UDP as well, which
// decides whether the UDP side of the port has to be free too. Only the DNS and
// tproxy inbounds ever do.
func inboundUsesUDP(in xray.Inbound) bool {
	settings, ok := in.Settings.(map[string]any)
	if !ok {
		return false
	}
	network, _ := settings["network"].(string)
	return strings.Contains(network, "udp")
}

func portSetting(tag string) string {
	switch tag {
	case xray.TagSocksIn:
		return "socks_port"
	case xray.TagHTTPIn:
		return "http_port"
	case xray.TagTransparent:
		return "tproxy_port"
	case xray.TagDNSIn:
		return "dns_port"
	case xray.TagAPIIn:
		return "stats_port"
	default:
		return ""
	}
}

func inboundPurpose(tag string) string {
	switch tag {
	case xray.TagSocksIn:
		return "SOCKS proxy"
	case xray.TagHTTPIn:
		return "HTTP proxy"
	case xray.TagTransparent:
		return "transparent capture inbound"
	case xray.TagDNSIn:
		return "DNS resolver"
	case xray.TagAPIIn:
		return "traffic counter API"
	default:
		return "inbound " + tag
	}
}

// coreVersionLabel names the core in a log line, without claiming a version
// when probing failed.
func coreVersionLabel(caps xray.Capabilities) string {
	if caps.Probed && caps.Version != "" {
		return caps.Version
	}
	return "(version unknown)"
}

// killStaleChildren terminates processes left over from a previous run.
//
// An OOM kill or a `kill -9` takes the daemon down without its teardown, and
// the core it started keeps running — holding the very ports the next connect
// needs. Without this, a restarted daemon can never connect again until someone
// notices and kills the core by hand.
//
// Only processes started with this daemon's own generated configuration are
// touched, matched by the config path on their command line. That is narrow
// enough that a core someone else runs on this device is left alone.
func killStaleChildren(runDir string, step *StepLog) {
	for _, name := range []string{"xray.json", "hev.yaml"} {
		marker := filepath.Join(runDir, name)
		for _, pid := range pidsWithArg(marker) {
			if pid == os.Getpid() {
				continue
			}
			step.Warnf("killing process %d left over from a previous run (%s)", pid, marker)
			proc, err := os.FindProcess(pid)
			if err != nil {
				continue
			}
			_ = proc.Signal(syscall.SIGTERM)
			time.Sleep(300 * time.Millisecond)
			_ = proc.Signal(syscall.SIGKILL)
		}
	}
}

// pidsWithArg finds processes whose command line contains the given string.
// /proc is read directly rather than shelling out to pgrep, which OpenWrt's
// busybox may not provide.
func pidsWithArg(needle string) []int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var out []int
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		raw, err := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline"))
		if err != nil {
			continue
		}
		// A command line is NUL-separated; joining with spaces is enough to
		// match a path argument.
		if strings.Contains(strings.ReplaceAll(string(raw), "\x00", " "), needle) {
			out = append(out, pid)
		}
	}
	return out
}

// noDataHint says where to look when the core runs but nothing comes back.
//
// The order is the order of likelihood, and the first line is the one that
// costs an evening when it is missing: a pinned certificate is checked on
// every connection and a server that renews its certificate breaks the pin
// without changing anything else about itself. Nothing else in the system
// looks different — the core starts, the port listens, the rules apply — so
// without this sentence the search starts everywhere except here.
func noDataHint(f *stepError, p *model.Profile, g *model.Group) *stepError {
	if p != nil && p.PinnedCert != "" {
		return f.withHint("hint.pinned_cert", p.ID)
	}
	if g != nil {
		return f.withHint("hint.group_no_data")
	}
	return f.withHint("hint.check_credentials")
}
