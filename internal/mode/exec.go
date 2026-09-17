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
	if err := run("fw4", "check"); err != nil {
		msg := strings.TrimSpace(err.Error())
		if len(msg) > 600 {
			msg = msg[:600] + "…"
		}
		return msg
	}
	return ""
}
