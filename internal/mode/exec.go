package mode

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"xwrt/internal/fw"
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
// when it can. The reasoning lives in internal/fw, where the firewall is; this
// is a seam so a test can supply an answer without one.
var firewallRenderError = fw.RenderError
