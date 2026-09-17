package mode

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

// probeResolver asks the core's DNS inbound for a name and waits for an answer.
//
// The query is hand-built because it is twelve bytes of header and a name: a
// DNS library for one packet on a router with 128 MB of RAM is not a trade
// worth making.
func probeResolver(port int) error {
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

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		c, err := net.DialTimeout("udp",
			net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 2*time.Second)
		if err != nil {
			lastErr = err
			continue
		}
		_ = c.SetDeadline(time.Now().Add(3 * time.Second))
		if _, err := c.Write(msg); err != nil {
			c.Close()
			lastErr = err
			continue
		}
		buf := make([]byte, 512)
		n, err := c.Read(buf)
		c.Close()
		if err != nil {
			lastErr = err
			continue
		}
		if n < 12 || buf[0] != msg[0] || buf[1] != msg[1] {
			lastErr = fmt.Errorf("the reply did not match the query")
			continue
		}
		// NOERROR or NXDOMAIN both prove a working resolver; anything else says
		// it is there but cannot resolve, which is just as unusable.
		switch rcode := buf[3] & 0x0f; rcode {
		case 0, 3:
			return nil
		default:
			lastErr = fmt.Errorf("the resolver answered with rcode %d", rcode)
		}
	}
	return lastErr
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

func restartDnsmasq() error {
	if _, err := os.Stat("/etc/init.d/dnsmasq"); err != nil {
		return nil
	}
	if err := run("/etc/init.d/dnsmasq", "restart"); err != nil {
		return fmt.Errorf("restart dnsmasq: %w", err)
	}
	return nil
}
