package mode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"xwrt/internal/model"
	"xwrt/internal/netenv"
)

// Whether this kernel can carry a tunnel.
//
// The question is not "is there a name at /dev/net/tun". On these devices the
// name and the driver come apart in both directions, and the state that costs
// an afternoon is the node being present with nothing behind it: it stats
// fine, so the daemon says the device is equipped, and then nothing works.

func TestAMissingDeviceIsSaidToBeMissing(t *testing.T) {
	err := checkTunDevice(filepath.Join(t.TempDir(), "net", "tun"))
	if err == nil {
		t.Fatal("a path that does not exist was accepted")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("the message does not say it is missing: %v", err)
	}
	// The old message said "install kmod-tun" to everyone, including owners of
	// firmware with no package manager and no such package.
	if strings.Contains(err.Error(), "install kmod-tun") {
		t.Errorf("sent everybody after an OpenWrt package: %v", err)
	}
	if !strings.Contains(err.Error(), "Redirect mode") {
		t.Errorf("the message does not name the mode that works anyway: %v", err)
	}
}

// A node that exists and cannot be opened is the case a stat cannot see, and
// the reason this function opens the device at all. On the B618 that state is
// a device node with no driver behind it, which stats fine and fails with
// ENODEV; here it is a directory, which stats fine and fails with EISDIR. What
// is being tested is the same thing either way, and unlike a mode 000 file it
// is refused for root too — the daemon runs as root, and a check that only
// holds for everybody else is not a check of the daemon.
func TestSomethingThatStatsButCannotBeOpened(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tun")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the stand-in does not even stat, so this proves nothing: %v", err)
	}

	err := checkTunDevice(path)
	if err == nil {
		t.Fatal("something that cannot be opened was reported as usable; a " +
			"stat-only check passes this and the device then looks equipped " +
			"while nothing can make a tunnel on it")
	}
	if strings.Contains(err.Error(), "does not exist") {
		t.Errorf("reported as missing rather than as unusable: %v", err)
	}
	if !strings.Contains(err.Error(), "present but cannot be opened") {
		t.Errorf("the message does not distinguish this from a missing device: %v", err)
	}
}

// And a device that opens is a device that works. An ordinary file stands in:
// what is being checked is that a successful open is not turned into a
// failure, which would leave TUN modes refusing to start on a working kernel.
func TestAnOpenableDeviceIsAccepted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tun")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := checkTunDevice(path); err != nil {
		t.Errorf("refused a device that opened cleanly: %v", err)
	}
}

// Every one of them has to be possible to act on from a telnet prompt: what is
// wrong, and what to do instead.
func TestEveryRefusalSaysWhatToDo(t *testing.T) {
	dir := t.TempDir()
	missing := checkTunDevice(filepath.Join(dir, "nothing-here"))
	if !strings.Contains(missing.Error(), "kmod-tun") {
		t.Errorf("OpenWrt is left without its package name: %v", missing)
	}
}

// And the check is actually made before anything is started.
//
// A function that decides correctly and is never called decides nothing. The
// order matters too: hev-socks5-tunnel is launched a few lines later, and a
// device that cannot carry a tunnel should be said so plainly rather than
// discovered as a helper that starts and then quietly does nothing.
func TestStartRefusesBeforeLaunchingAnything(t *testing.T) {
	saved := tunDevice
	tunDevice = filepath.Join(t.TempDir(), "no-tun-here")
	t.Cleanup(func() { tunDevice = saved })

	s := model.Defaults()
	s.RunDir = t.TempDir()
	// A binary that does not exist: if the check is skipped, the start gets
	// this far and fails for the wrong reason, which is exactly what the
	// assertion below tells apart.
	s.HevBin = filepath.Join(t.TempDir(), "no-hev-either")

	tun := NewTun(&s, &netenv.Env{}, ScopeAll, func(string) {})
	err := tun.Start(nil)

	if err == nil {
		t.Fatal("a device with no tunnel node started a tunnel")
	}
	if !strings.Contains(err.Error(), "does not exist") ||
		!strings.Contains(err.Error(), tunDevice) {
		t.Errorf("the failure does not name the missing device, so the check "+
			"was skipped and something else failed instead: %v", err)
	}
	if tun.Running() {
		t.Error("the tunnel helper was started anyway")
	}
}
