package mode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"xwrt/internal/model"
)

// Restarting dnsmasq is not free.
//
// It serves DHCP as well as DNS on this platform, so for a moment the network
// has neither, and its cache is emptied — every name looked up again at once,
// each over a tunnel that has only just come back. Apply runs on every connect,
// and on a link that reconnects a few times an hour that turns into a repeated
// network-wide outage. What it breaks worst is the traffic most sensitive to a
// failed lookup: a phone whose push connection dropped resolves the push server,
// fails, and backs off for tens of minutes.

func dnsWithConfDir(t *testing.T) (*DNS, string) {
	t.Helper()
	dir := t.TempDir()
	s := model.Defaults()
	s.DNSMode = model.DNSDnsmasq
	s.DNSPort = 15353
	return NewDNS(&s), dir
}

func TestTheSnippetIsWrittenTheFirstTime(t *testing.T) {
	d, dir := dnsWithConfDir(t)
	restarts := 0
	swapRestart(t, func() error { restarts++; return nil })

	if err := d.applyConfDir(dir); err != nil {
		t.Fatal(err)
	}

	body, err := os.ReadFile(filepath.Join(dir, dnsmasqConfName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "server=127.0.0.1#15353") {
		t.Errorf("the snippet does not point at the core:\n%s", body)
	}
	if restarts != 1 {
		t.Errorf("dnsmasq restarted %d times writing a new file, want 1", restarts)
	}
}

// The case this exists for: connect again, nothing about DNS has changed, and
// the file already says exactly what we would write.
func TestApplyingTheSameSnippetAgainDoesNotRestartDnsmasq(t *testing.T) {
	d, dir := dnsWithConfDir(t)
	restarts := 0
	swapRestart(t, func() error { restarts++; return nil })

	if err := d.applyConfDir(dir); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(filepath.Join(dir, dnsmasqConfName))
	if err != nil {
		t.Fatal(err)
	}

	// A fresh object each time, the way the engine builds one per connect.
	// That is what makes the state matter: a skipped restart that also skips
	// marking it applied leaves a DNS integration nothing will ever revert.
	var last *DNS
	for i := 0; i < 5; i++ {
		next, _ := dnsWithConfDir(t)
		if err := next.applyConfDir(dir); err != nil {
			t.Fatal(err)
		}
		last = next
	}

	if restarts != 1 {
		t.Errorf("dnsmasq restarted %d times for six connects that asked for "+
			"the same thing; each restart takes DNS and DHCP down for a moment "+
			"and empties the cache", restarts)
	}
	// And the file was not even rewritten, so nothing watching it is woken up.
	after, err := os.Stat(filepath.Join(dir, dnsmasqConfName))
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Error("the file was rewritten with identical content")
	}

	// It is still applied — skipping the restart must not skip the state.
	// Without this the snippet stays on disk and Revert walks past it, so the
	// network keeps resolving through a core that is no longer running.
	if !last.applied {
		t.Error("an apply that skipped the restart left the integration marked " +
			"as not applied, so nothing will ever take the snippet back out")
	}
}

// A real change still takes effect. Without this the optimisation would be a
// way of never applying anything.
func TestAChangedSnippetDoesRestartDnsmasq(t *testing.T) {
	d, dir := dnsWithConfDir(t)
	restarts := 0
	swapRestart(t, func() error { restarts++; return nil })

	if err := d.applyConfDir(dir); err != nil {
		t.Fatal(err)
	}
	d.Settings.DNSPort = 15354
	if err := d.applyConfDir(dir); err != nil {
		t.Fatal(err)
	}

	if restarts != 2 {
		t.Errorf("dnsmasq restarted %d times, want 2: the port changed and "+
			"dnsmasq is still pointed at the old one", restarts)
	}
	body, _ := os.ReadFile(filepath.Join(dir, dnsmasqConfName))
	if !strings.Contains(string(body), "15354") {
		t.Errorf("the file still names the old port:\n%s", body)
	}
}

// Somebody edited the file by hand, or a half-written one was left behind.
func TestADifferentFileIsReplaced(t *testing.T) {
	d, dir := dnsWithConfDir(t)
	restarts := 0
	swapRestart(t, func() error { restarts++; return nil })

	path := filepath.Join(dir, dnsmasqConfName)
	if err := os.WriteFile(path, []byte("server=8.8.8.8\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := d.applyConfDir(dir); err != nil {
		t.Fatal(err)
	}

	if restarts != 1 {
		t.Errorf("restarted %d times, want 1", restarts)
	}
	body, _ := os.ReadFile(path)
	if strings.Contains(string(body), "8.8.8.8") {
		t.Errorf("the stale file was left in place:\n%s", body)
	}
}

// swapRestart replaces the service restart for the duration of one test.
func swapRestart(t *testing.T, fn func() error) {
	t.Helper()
	saved := restartHook
	restartHook = fn
	t.Cleanup(func() { restartHook = saved })
}
