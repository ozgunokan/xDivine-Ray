package mode

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"xwrt/internal/fault"
	"xwrt/internal/model"
)

// defaultConfDir is where OpenWrt's dnsmasq init script puts its conf-dir. A
// snippet dropped in there is picked up on the next restart and deleting it
// undoes the change completely, which is what keeps this integration fully
// reversible: the operator's own dnsmasq configuration is never edited.
//
// It is only the default, though. A build can point dnsmasq somewhere else, and
// hard-coding this path made such a device look like it had no dnsmasq at all —
// so confDir() asks the configuration first.
const defaultConfDir = "/tmp/dnsmasq.d"

// dnsmasqConfName is the snippet this daemon owns.
const dnsmasqConfName = "xwrt.conf"

// uciStatePath remembers what the dhcp configuration looked like before it was
// edited. It lives outside /tmp on purpose: a reboot or a kill must not lose
// the one piece of information needed to put the device back as it was.
const uciStatePath = "/etc/xwrt-dns.state"

// DNS steers client name resolution through the core.
type DNS struct {
	Settings *model.Settings
	applied  bool
	// dir is resolved once, at Apply, and reused by Revert so a snippet is
	// always removed from wherever it was written.
	dir string
	// viaUCI records that the dhcp configuration was edited rather than a
	// snippet written, which Revert has to undo differently.
	viaUCI bool
}

// confDir returns the directory dnsmasq includes, and whether dnsmasq looks
// usable at all.
//
// The running process is asked first, and it is the only answer that cannot be
// wrong: whatever --conf-dir it was started with is the directory it reads,
// whoever wrote it and whatever the configuration says now. OpenWrt commonly
// uses a per-instance subdirectory (/tmp/dnsmasq.d/cfg01411c), which no amount
// of guessing at /tmp/dnsmasq.d will find — and a snippet written into a
// directory nobody reads fails silently, which is the worst outcome available.
func confDir() (string, bool) {
	if dir, ok := confDirFromProcess(); ok {
		return dir, true
	}

	dir := defaultConfDir
	// `uci get dhcp.@dnsmasq[0].confdir` names a non-default location when a
	// build uses one.
	if out, err := output("uci", "-q", "get", "dhcp.@dnsmasq[0].confdir"); err == nil {
		if v := strings.TrimSpace(out); v != "" {
			dir = v
		}
	}
	if _, err := os.Stat(dir); err != nil {
		return dir, false
	}
	return dir, true
}

// confDirFromProcess reads --conf-dir out of the running dnsmasq's command
// line.
func confDirFromProcess() (string, bool) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return "", false
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		raw, err := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline"))
		if err != nil {
			continue
		}
		args := strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00")
		dir, ok := confDirFromCmdline(args)
		if !ok {
			continue
		}
		// dnsmasq was started with it, so it should exist; create it rather
		// than give up if something cleaned /tmp underneath it.
		if err := os.MkdirAll(dir, 0o755); err != nil {
			continue
		}
		return dir, true
	}
	return "", false
}

// confDirFromCmdline picks --conf-dir out of one process's arguments, if that
// process is dnsmasq.
func confDirFromCmdline(args []string) (string, bool) {
	if len(args) == 0 || !strings.Contains(args[0], "dnsmasq") {
		return "", false
	}
	for _, arg := range args {
		v, ok := strings.CutPrefix(arg, "--conf-dir=")
		if !ok {
			continue
		}
		// The value can carry a filename filter: "/tmp/dnsmasq.d/cfg01411c,*"
		// means "this directory, every file in it".
		if i := strings.IndexByte(v, ','); i >= 0 {
			v = v[:i]
		}
		if v != "" {
			return v, true
		}
	}
	return "", false
}

// dnsmasqPresent reports whether dnsmasq is installed at all, which decides
// which of two quite different explanations the operator gets.
func dnsmasqPresent() bool {
	_, err := os.Stat("/etc/init.d/dnsmasq")
	return err == nil
}

// NewDNS returns a DNS integration for the given settings.
func NewDNS(s *model.Settings) *DNS { return &DNS{Settings: s} }

// Apply configures resolution according to the selected DNS mode. Only dnsmasq
// mode writes anything here; redirect mode is handled by the firewall and off
// is a no-op by definition.
//
// Redirect mode briefly pointed dnsmasq here as well, to close a real gap: the
// hijack never sees a query a client sends to the router itself, so dnsmasq
// goes on asking whatever upstream it was handed, which leaks every lookup past
// the tunnel and fails outright in TUN mode. The snippet that does it carries
// no-resolv, though, and that turns one broken thing into a broken network:
// with no other upstream left, a core that does not answer on its DNS port
// takes name resolution down for everyone, which is exactly what happened.
//
// The gap is real and still open. Closing it needs the core's resolver proven
// to answer first, and a snippet that keeps a working fallback — not a default
// that can only be recovered from by being in the same building as the router.
func (d *DNS) Apply() error {
	if d.Settings.DNSMode != model.DNSDnsmasq {
		return nil
	}
	if !dnsmasqPresent() {
		return fault.Tagf("dns.no_dnsmasq", nil,
			"this device does not run dnsmasq, which is what "+
				"dnsmasq DNS mode configures")
	}

	// Two ways to say the same thing to dnsmasq, in order of preference.
	//
	// A file in its conf-dir is the better one: nothing of the operator's is
	// edited and removing the file undoes it completely. But not every build
	// runs dnsmasq with a conf-dir, and on one that does not, a file written
	// anywhere is a file nobody reads. So the second way edits the dhcp config
	// through uci — one server entry and one option, both recorded so they can
	// be put back exactly as they were.
	if dir, ok := confDir(); ok {
		if err := d.applyConfDir(dir); err != nil {
			return err
		}
	} else if err := d.applyUCI(); err != nil {
		return err
	}

	// Whichever way was used, dnsmasq now has the core as its only resolver.
	// That is the point — it is what stops lookups leaking past the tunnel —
	// and it is also why this has to be checked rather than assumed: if the core
	// does not answer, nothing else will, and name resolution stops for everyone
	// on the network. Someone who set this up over ssh then has no way back in.
	if err := probeResolver(d.Settings.DNSPort); err != nil {
		_ = d.Revert()
		return fault.Tagf("dns.core_not_answering",
			[]any{d.Settings.DNSPort, err},
			"the core is not answering DNS on port %d, so pointing "+
				"dnsmasq at it would have stopped name resolution for the whole "+
				"network; the previous resolver configuration was put back: %w",
			d.Settings.DNSPort, err)
	}

	d.applied = true
	return nil
}

// applyConfDir drops a snippet into the directory dnsmasq includes.
func (d *DNS) applyConfDir(dir string) error {
	d.dir = dir
	conf := "# Managed by xwrt. Removed automatically on disconnect.\n" +
		"server=" + d.upstream() + "\n" +
		"no-resolv\n"
	path := filepath.Join(dir, dnsmasqConfName)
	if err := os.WriteFile(path, []byte(conf), 0o644); err != nil {
		return fmt.Errorf("write dnsmasq snippet: %w", err)
	}
	// Mark it applied before restarting so a failed restart still reverts.
	d.applied = true
	if err := restartDnsmasq(); err != nil {
		d.applied = false
		_ = os.Remove(path)
		return err
	}
	return nil
}

// applyUCI edits the dhcp configuration, for builds that run dnsmasq without a
// conf-dir. The previous value of noresolv is written to a state file first:
// restoring it has to survive this daemon being killed, and the one thing worse
// than not steering DNS is steering it and not being able to put it back.
func (d *DNS) applyUCI() error {
	prev, had := uciGet("dhcp.@dnsmasq[0].noresolv")
	state := "noresolv=\n"
	if had {
		state = "noresolv=" + prev + "\n"
	}
	if err := os.WriteFile(uciStatePath, []byte(state), 0o644); err != nil {
		return fmt.Errorf("record the current dnsmasq configuration: %w", err)
	}
	d.viaUCI = true

	if err := run("uci", "add_list", "dhcp.@dnsmasq[0].server="+d.upstream()); err != nil {
		_ = d.Revert()
		return fmt.Errorf("point dnsmasq at the core: %w", err)
	}
	if err := run("uci", "set", "dhcp.@dnsmasq[0].noresolv=1"); err != nil {
		_ = d.Revert()
		return fmt.Errorf("stop dnsmasq using its own upstream: %w", err)
	}
	if err := run("uci", "commit", "dhcp"); err != nil {
		_ = d.Revert()
		return fmt.Errorf("save the dnsmasq configuration: %w", err)
	}
	if err := restartDnsmasq(); err != nil {
		_ = d.Revert()
		return err
	}
	return nil
}

// parseNoresolvState reads back what noresolv was before the edit. An empty
// value and a missing line mean the same thing — it was not set — and that is
// the case that matters, because putting a "0" where there was nothing is not
// the same as leaving it alone.
func parseNoresolvState(raw string) (string, bool) {
	for _, line := range strings.Split(raw, "\n") {
		if v, ok := strings.CutPrefix(line, "noresolv="); ok {
			return v, v != ""
		}
	}
	return "", false
}

// upstream is the address dnsmasq is pointed at.
func (d *DNS) upstream() string {
	return "127.0.0.1#" + strconv.Itoa(d.Settings.DNSPort)
}

// uciGet reads one option, reporting whether it was set at all — which is the
// difference between putting a value back and removing it again.
func uciGet(path string) (string, bool) {
	out, err := output("uci", "-q", "get", path)
	if err != nil {
		return "", false
	}
	v := strings.TrimSpace(out)
	return v, v != ""
}

// probeBudget is how long the core's resolver is given to become usable.
//
// It is not a timeout on one query; it is how long a freshly started core is
// allowed to take before its DNS is declared broken. Switching capture modes
// tears the whole connection down and starts a new core, and the step that
// checks DNS runs seconds later — which sounds like plenty and is not, because
// "seconds later" is measured from the moment the previous mode was dismantled,
// not from the moment the new core finished opening its inbounds.
const probeBudget = 20 * time.Second

// probeResolver asks the core's DNS inbound for a name and waits for an answer.
//
// It keeps asking until the budget runs out, and this is the whole point.
//
// It used to try three times with no pause between them. Against a port with
// nothing listening yet, the kernel answers a UDP write with ICMP port
// unreachable and the read fails immediately — so all three attempts were spent
// in under half a millisecond, measured, and the core was declared dead before
// it had finished starting. That is the bug behind "switching from mixed to TUN
// fails with a DNS error, and connecting again works": nothing was wrong with
// DNS, the question was just asked too early, and pressing connect a second
// time changed the timing enough to ask it later.
//
// The query is hand-built because it is twelve bytes of header and a name: a
// DNS library for one packet on a router with 128 MB of RAM is not a trade
// worth making.
func probeResolver(port int) error {
	return probeResolverWithin(port, probeBudget, time.Now, time.Sleep, probeOnce)
}

// probeResolverWithin is the testable half: the clock and the one-shot query
// are arguments, because a test cannot start a core and should not wait twenty
// seconds to find out that waiting works.
func probeResolverWithin(
	port int,
	budget time.Duration,
	now func() time.Time,
	sleep func(time.Duration),
	once func(int) error,
) error {
	deadline := now().Add(budget)
	wait := 250 * time.Millisecond

	// A ceiling on tries as well as on time. The loop is meant to end because
	// the clock ran out, but a clock that does not move — a sleep that does not
	// sleep, a coarse timer on a router that reports the same second for a
	// while — turns "until the deadline" into forever, and this runs inside the
	// connect path with the engine's lock held. Whichever limit is reached
	// first ends it.
	const maxTries = 64

	var last error
	for try := 0; try < maxTries; try++ {
		if last = once(port); last == nil {
			return nil
		}
		// A retry that would finish after the budget is not a retry, it is an
		// overrun. Stop while the answer is still the one that was asked for.
		if !now().Add(wait).Before(deadline) {
			return last
		}
		sleep(wait)
		if wait < 2*time.Second {
			wait *= 2
		}
	}
	return last
}

// errUnresolving is the answer that means the resolver is on its socket and
// cannot resolve. It is a failure for the core's resolver, which exists to
// resolve, and a success for "is dnsmasq back up", so the two callers have to
// be able to tell it apart from the rest rather than read the sentence.
var errUnresolving = errors.New("the resolver is listening but could not " +
	"resolve anything, so the core has no working path out yet")

// probeOnce sends one query and reports what came back, in the words of what
// actually happened.
//
// The three failures below look identical in a log line and mean entirely
// different things, and lumping them together as "the core is not answering"
// sent people to look at their DNS settings when the core had simply not
// finished starting.
func probeOnce(port int) error {
	const name = "example.com"

	msg := []byte{
		0x0f, 0xa1, // a fixed ID; this is one query on a private socket
		0x01, 0x00, // standard query, recursion desired
		0x00, 0x01, // one question
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	}
	for _, label := range strings.Split(name, ".") {
		msg = append(msg, byte(len(label)))
		msg = append(msg, label...)
	}
	msg = append(msg, 0x00, 0x00, 0x01, 0x00, 0x01) // root, A, IN

	c, err := net.DialTimeout("udp",
		net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 2*time.Second)
	if err != nil {
		return err
	}
	defer c.Close()

	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := c.Write(msg); err != nil {
		return err
	}
	buf := make([]byte, 512)
	n, err := c.Read(buf)
	if err != nil {
		if isRefused(err) {
			return fmt.Errorf("nothing is listening on port %d yet", port)
		}
		return fmt.Errorf("no answer from port %d: %w", port, err)
	}
	if n < 12 || buf[0] != msg[0] || buf[1] != msg[1] {
		return fmt.Errorf("the reply did not match the query")
	}
	// NOERROR or NXDOMAIN both prove a working resolver; anything else says it
	// is there but cannot resolve, which is just as unusable — and during a
	// mode switch is usually the core briefly having no way out yet, which is
	// exactly what the retries above are for.
	switch rcode := buf[3] & 0x0f; rcode {
	case 0, 3:
		return nil
	case 2:
		return errUnresolving
	default:
		return fmt.Errorf("the resolver answered with rcode %d", rcode)
	}
}

// isRefused reports the one error that means "not started yet" rather than
// "broken": a UDP write to a port with no listener comes back as ICMP port
// unreachable, which the kernel reports on the next read as a refused
// connection.
func isRefused(err error) bool {
	return errors.Is(err, syscall.ECONNREFUSED)
}

// Revert restores the previous resolver configuration.
//
// It returns an error because this is the one teardown step whose failure is
// immediately visible to everyone on the network: a resolver left pointing at a
// DNS port that no longer has anything listening means the LAN stops resolving
// names entirely.
//
// Both ways of applying are undone here, and both are undone even when this
// object never applied anything — that is the startup path, cleaning up after a
// previous run that was killed rather than stopped.
func (d *DNS) Revert() error {
	var firstErr error

	path := filepath.Join(d.confDirForRevert(), dnsmasqConfName)
	snippet := false
	if _, err := os.Stat(path); err == nil {
		snippet = true
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			firstErr = fmt.Errorf("remove %s: %w", path, err)
		}
	}

	edited, err := d.revertUCI()
	if err != nil && firstErr == nil {
		firstErr = err
	}

	d.applied = false
	d.viaUCI = false

	// Nothing was in place, so there is nothing to restart and no reason to
	// interrupt name resolution for everyone while doing it.
	if !snippet && !edited {
		return firstErr
	}
	if err := restartDnsmasq(); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

// revertUCI puts the dhcp configuration back and reports whether it changed
// anything. The state file is what says an edit happened at all, so a daemon
// that was killed still cleans up after itself on the next start.
func (d *DNS) revertUCI() (bool, error) {
	raw, err := os.ReadFile(uciStatePath)
	if err != nil {
		return false, nil
	}

	// The entry is removed by value, so an operator's own server lines are left
	// alone however many there are.
	_ = run("uci", "del_list", "dhcp.@dnsmasq[0].server="+d.upstream())

	prev, had := parseNoresolvState(string(raw))
	if had {
		_ = run("uci", "set", "dhcp.@dnsmasq[0].noresolv="+prev)
	} else {
		// It was not set before, so it must not be set now.
		_ = run("uci", "-q", "delete", "dhcp.@dnsmasq[0].noresolv")
	}

	var firstErr error
	if err := run("uci", "commit", "dhcp"); err != nil {
		firstErr = fmt.Errorf("restore the dnsmasq configuration: %w", err)
	}
	if err := os.Remove(uciStatePath); err != nil && !os.IsNotExist(err) && firstErr == nil {
		firstErr = err
	}
	return true, firstErr
}

// confDirForRevert resolves the directory again when Revert runs on a DNS
// object that never applied anything — the startup cleanup path, which has to
// remove a snippet left by a previous run.
func (d *DNS) confDirForRevert() string {
	if d.dir != "" {
		return d.dir
	}
	dir, _ := confDir()
	return dir
}

// localResolverBudget is how long to wait for dnsmasq to answer again after it
// has been restarted. It is shorter than the budget for the core, because
// dnsmasq is starting from a configuration it has served before rather than
// waiting on a tunnel to come up.
const localResolverBudget = 15 * time.Second

const dnsmasqInit = "/etc/init.d/dnsmasq"

func restartDnsmasq() error {
	return restartDnsmasqWith(dnsmasqInit,
		func() error { return run(dnsmasqInit, "restart") },
		waitForLocalResolver)
}

// restartDnsmasqWith is the testable half. Restarting a service and waiting
// for a socket are both things a test cannot do, and the order of the two is
// the entire point of this function — a restart that returns before the
// service is up is what it exists to stop.
func restartDnsmasqWith(initPath string, restart, await func() error) error {
	if _, err := os.Stat(initPath); err != nil {
		return nil
	}
	if err := restart(); err != nil {
		return fmt.Errorf("restart dnsmasq: %w", err)
	}
	return await()
}

// waitForLocalResolver blocks until the device can resolve names again.
//
// `/etc/init.d/dnsmasq restart` hands the job to procd and returns; dnsmasq is
// not listening yet when it does. Everything the next few seconds needs the
// system resolver for then fails against a port with nothing on it.
//
// That is a switch between two servers, which goes teardown-then-start: the
// teardown restarts dnsmasq to put the old resolver configuration back, and
// the start immediately looks the new server's hostname up. The lookup fails,
// the address stays a name, the core cannot dial it either — and the connect
// ends at "the tunnel carries no data" with nothing in the log about DNS at
// all. Pressing connect again works, because by then dnsmasq is up. That is
// the whole failure: a question asked half a second too early.
//
// Any well-formed reply counts, including SERVFAIL. The question here is only
// whether dnsmasq is back on its socket; whether it can reach its upstreams is
// a different question with a different answer, and refusing to continue over
// it would turn a restart into an outage.
func waitForLocalResolver() error {
	return probeResolverWithin(53, localResolverBudget,
		time.Now, time.Sleep, probeListening)
}

// probeListening reports whether anything is answering on the port, without
// judging the answer.
func probeListening(port int) error {
	err := probeOnce(port)
	if err == nil {
		return nil
	}
	// probeOnce turns "listening, but could not resolve" into an error. Here
	// that is a pass: it took a query and wrote a reply, which is the entire
	// question being asked.
	if errors.Is(err, errUnresolving) {
		return nil
	}
	return err
}
