package fw

import (
	"fmt"
	"os/exec"
	"strings"
)

// The device's own firewall, as opposed to the capture rules in this package.
//
// Those are two different things and only one of them is ours. `table ip xwrt`
// is installed and removed by this daemon; `table inet fw4` belongs to OpenWrt
// and carries the rules that make a router a router — the masquerade that
// rewrites a LAN packet's source address on its way out, and the forward rules
// that let it leave at all.
//
// It can be missing. fw4 renders its whole ruleset in one pass and refuses to
// load a partial one, so a single unparseable file under /etc/nftables.d means
// it loads nothing: it flushes what was there, fails, and leaves the kernel with
// no firewall at all. The device keeps working perfectly — for itself. Packets
// it sends are its own and need no translation. Everything behind it loses the
// internet, because a LAN packet now leaves with a 192.168.x.x source address
// that nothing will answer.
//
// What made this worth detecting is how the symptom arrives. On a router whose
// clients reach the internet through a tunnel, the capture rules do the work and
// the masquerade is never consulted — so the tunnel keeps working and the fault
// stays completely invisible until somebody switches the tunnel off. Then: the
// router pings, the router resolves names, `ip route get` returns a correct
// answer, the configuration says `masq '1'`, and no client can reach anything.
// Every obvious check passes. The question "is the firewall loaded at all?" is
// the one nobody thinks to ask, and it is the whole answer.

// SystemState is what the device's own firewall is doing.
type SystemState struct {
	// Known is whether this check could form an opinion at all. A device with
	// no recognised firewall manager gets a zero value, and nothing is said
	// about it — saying "your firewall is missing" to someone who never had one
	// is noise, and worse, it is noise in the place where a real fault would
	// otherwise be seen.
	Known bool
	// Loaded is whether that firewall's rules are in the kernel.
	Loaded bool
	// Reason is why they are not, when the firewall can be made to say. Empty
	// is normal: the usual cause of an unloaded firewall is that nobody has
	// started it, and that renders fine.
	Reason string
}

// Down reports the state worth telling somebody about: a firewall this device
// manages, which is not in the kernel.
func (s SystemState) Down() bool { return s.Known && !s.Loaded }

// managerTable pairs a firewall manager with the table it owns, which is the
// only evidence that counts. The configuration describes intent; the kernel
// holds what is being enforced, and those two disagreeing is this whole file's
// reason for existing.
var managers = []struct {
	bin   string
	probe func() bool
}{
	{"fw4", func() bool { return kernelHas("nft", []string{"list", "tables"}, "inet fw4") }},
	// fw3 has no single table, so its postrouting chain stands in for one. A
	// loaded fw3 always has zone chains; an unloaded one has none.
	{"fw3", func() bool { return kernelHas("iptables-save", []string{"-t", "nat"}, "zone_") }},
}

// SystemCheck inspects the device's own firewall.
func SystemCheck() SystemState {
	for _, m := range managers {
		if !haveBinary(m.bin) {
			continue
		}
		if m.probe() {
			return SystemState{Known: true, Loaded: true}
		}
		return SystemState{Known: true, Reason: RenderError()}
	}
	return SystemState{}
}

// Seams. Replaced in tests, which have neither a kernel nor a firewall manager.
var (
	haveBinary = func(name string) bool {
		_, err := exec.LookPath(name)
		return err == nil
	}
	kernelHas = func(name string, args []string, want string) bool {
		out, err := exec.Command(name, args...).Output()
		if err != nil {
			return false
		}
		return strings.Contains(string(out), want)
	}
	fw4CheckOutput = func() (string, bool) {
		cmd := exec.Command("fw4", "check")
		out, err := cmd.CombinedOutput()
		return string(out), err == nil
	}
)

// RenderError returns why the firewall cannot build its ruleset, or "" when it
// can — or when there is no fw4 to ask.
//
// A device can reach a state where every firewall reload fails: one broken file
// in /etc/nftables.d is enough, and fw4 refuses the whole ruleset rather than
// load a partial one. Nothing that runs later can fix that, and the visible
// symptom is unrelated to the cause.
func RenderError() string {
	if !haveBinary("fw4") {
		return ""
	}
	out, ok := fw4CheckOutput()
	if ok {
		return ""
	}
	return fw4Reason(out)
}

// fw4Reason reduces `fw4 check` output to the part that is actually stopping
// the firewall.
//
// fw4 prints two kinds of line and does not distinguish them by tone. Lines
// beginning "[!]" are warnings about sections it is ignoring — and every device
// with another proxy package installed has several, because fw4 does not
// support the options those packages write. They are harmless. Somewhere after
// them comes the line that matters: one file it could not parse, which makes it
// refuse the entire ruleset.
//
// Printed together, the warnings come first and the error scrolls off, so the
// first thing anyone sees is four complaints about a package that has nothing
// to do with it. Someone read that and asked what passwall had to do with their
// tunnel. Nothing: the actual fault was a stray `comment` in a file under
// /etc/nftables.d, eight lines further down.
//
// So the error lines are reported, the file that carries them is named first,
// and the warnings are reduced to a count with a note that they are not the
// cause.
func fw4Reason(out string) string {
	// Callers have historically arrived with the command wrapped around the
	// output. Without stripping that, the first warning does not look like a
	// warning and is promoted to the cause — which is the exact line the
	// reader was confused by.
	out = strings.TrimPrefix(strings.TrimSpace(out), "fw4 check: ")

	var errs, warns []string
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "[!]") {
			warns = append(warns, trimmed)
			continue
		}
		errs = append(errs, trimmed)
	}

	// Nothing but warnings, yet fw4 still failed: say all of it rather than
	// inventing a cause. This should not happen, and if it does the raw output
	// is the only honest thing to show.
	if len(errs) == 0 {
		return clip(strings.Join(warns, "\n"), 600)
	}

	var b strings.Builder
	if file := offendingFile(errs); file != "" {
		fmt.Fprintf(&b, "the file it cannot parse is %s — move it aside or fix "+
			"it, then run: /etc/init.d/firewall restart\n\n", file)
	}
	b.WriteString(clip(strings.Join(errs, "\n"), 600))
	if len(warns) > 0 {
		fmt.Fprintf(&b, "\n\n(fw4 also printed %d warning(s) about sections it "+
			"ignores, from other packages installed on this device. Those are "+
			"not the cause and nothing needs doing about them.)", len(warns))
	}
	return b.String()
}

// offendingFile pulls the path out of a line like
//
//	/etc/nftables.d/99-reset-ttl.nft:3:34-40: Error: syntax error
//
// so it can be said plainly instead of being left for someone to find in the
// middle of a compiler-shaped message.
func offendingFile(lines []string) string {
	for _, line := range lines {
		if !strings.Contains(line, "Error:") {
			continue
		}
		i := strings.Index(line, "/etc/")
		if i < 0 {
			continue
		}
		rest := line[i:]
		// The path ends at the first colon, which begins the line number.
		if j := strings.IndexByte(rest, ':'); j > 0 {
			return rest[:j]
		}
	}
	return ""
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
