package mode

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(buf.String())
		if msg == "" {
			return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
		}
		return fmt.Errorf("%s %s: %s", name, strings.Join(args, " "), msg)
	}
	return nil
}

func runQuiet(name string, args ...string) {
	_ = exec.Command(name, args...).Run()
}

// output runs a command and returns its stdout, for the cases where the
// interesting part is what it prints rather than whether it succeeded.
func output(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).Output()
	return string(out), err
}

func sleepMS(ms int) {
	time.Sleep(time.Duration(ms) * time.Millisecond)
}

// firewallRenderError returns why the firewall cannot build its ruleset, or ""
// when it can — or when there is no fw4 to ask.
//
// A device can reach a state where every firewall reload fails: one broken file
// in /etc/nftables.d is enough, and fw4 refuses the whole ruleset rather than
// load a partial one. Nothing that runs later can fix that, and the visible
// symptom is unrelated to the cause — zones silently stop being installed while
// the running firewall keeps enforcing whatever it last loaded successfully.
var firewallRenderError = func() string {
	if _, err := exec.LookPath("fw4"); err != nil {
		return ""
	}
	err := run("fw4", "check")
	if err == nil {
		return ""
	}
	return fw4Reason(err.Error())
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
	// run() prefixes its error with the command it ran, so the first line
	// arrives as "fw4 check: [!] Section …". Without stripping that, the first
	// warning does not look like a warning and is promoted to the cause — which
	// is the exact line the reader was confused by.
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
