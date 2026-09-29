package mode

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"xwrt/internal/model"
)

// The timeout that made a message sent at 22:37 arrive at 22:47.
//
// In TUN mode every connection on the network goes through hev-socks5-tunnel,
// which gives each session an I/O timeout of its own. Nothing here set it, so
// hev's default applied: five minutes. A phone's push connection sends nothing
// between messages and heartbeats every fifteen to thirty minutes, so it was
// being closed in the gap, every time, on every device on the network.
//
// The setting the operator can already see — the idle timeout, four hours by
// default — now reaches this too. These checks are about that reaching: that
// the number arrives, that it arrives in the units hev reads, and that it is
// written under the name the installed tunnel actually knows.

func settings(connIdle int, hevBin string) *model.Settings {
	s := &model.Settings{
		Mode: model.ModeTUN, TunName: "xwrt0", TunMTU: 8500,
		TunAddr: "198.18.0.1", SocksPort: 10808,
		ConnIdle: connIdle, HevBin: hevBin, LogLevel: "info",
	}
	return s
}

// A binary is only read for the option names it carries, so a file with those
// names in it is as good as the real thing for this.
func fakeHev(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "hev-socks5-tunnel")
	body := strings.Repeat("\x00binary padding\x7f", 4000) +
		strings.Join(names, "\x00") +
		strings.Repeat("\x00more padding\x7f", 4000)
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// --- the number reaches the file --------------------------------------------

func TestTheIdleSettingReachesTheTunnel(t *testing.T) {
	bin := fakeHev(t, "tcp-read-write-timeout", "udp-read-write-timeout")
	tun := &Tun{Settings: settings(14400, bin)}
	got := tun.renderConfig()

	// Four hours, in the milliseconds hev reads.
	if !strings.Contains(got, "tcp-read-write-timeout: 14400000") {
		t.Fatalf("the TCP timeout is not in the config:\n%s", got)
	}
}

// The whole point. Five minutes is shorter than a push heartbeat; whatever is
// written has to be longer, or the bug is still there with a different number
// on it.
func TestTheWrittenTimeoutOutlastsAPushHeartbeat(t *testing.T) {
	bin := fakeHev(t, "tcp-read-write-timeout")
	tun := &Tun{Settings: settings(14400, bin)}

	ms := valueOf(t, tun.renderConfig(), "tcp-read-write-timeout")
	if ms <= 30*60*1000 {
		t.Fatalf("a %d ms timeout still closes a phone's push connection", ms)
	}
}

// UDP is capped rather than following the setting. A UDP session is a mapping
// held in userspace with nothing to close it, and a household's worth held for
// four hours is memory a 128 MB router does not have.
func TestUDPIsCappedWhileTCPFollowsTheSetting(t *testing.T) {
	bin := fakeHev(t, "tcp-read-write-timeout", "udp-read-write-timeout")
	tun := &Tun{Settings: settings(14400, bin)}
	out := tun.renderConfig()

	if got := valueOf(t, out, "tcp-read-write-timeout"); got != 14400000 {
		t.Fatalf("TCP should follow the setting exactly, got %d", got)
	}
	if got := valueOf(t, out, "udp-read-write-timeout"); got != hevUDPCeilingSec*1000 {
		t.Fatalf("UDP should be capped at %ds, got %d ms", hevUDPCeilingSec, got)
	}
}

// And below the ceiling there is nothing to cap: a short setting is a short
// setting for both.
func TestAShortSettingIsNotRaisedToTheCeiling(t *testing.T) {
	tcp, udp := hevIdleMillis(120)
	if tcp != 120000 || udp != 120000 {
		t.Fatalf("want both at 120000, got %d and %d", tcp, udp)
	}
}

func TestAnAbsurdSettingIsBounded(t *testing.T) {
	tcp, _ := hevIdleMillis(999999999)
	if tcp != hevMaxSec*1000 {
		t.Fatalf("want the day ceiling, got %d", tcp)
	}
}

// Zero means "no opinion", which is what the setting meant before this existed
// and has to keep meaning: write nothing and leave hev to its own defaults
// rather than writing a zero whose meaning nobody here knows.
func TestNoSettingWritesNothing(t *testing.T) {
	bin := fakeHev(t, "tcp-read-write-timeout")
	tun := &Tun{Settings: settings(0, bin)}
	if strings.Contains(tun.renderConfig(), "read-write-timeout") {
		t.Fatalf("a zero setting should leave the tunnel's own defaults alone")
	}
}

// --- under the name this tunnel knows ---------------------------------------
//
// Writing a key the installed binary does not understand is a gamble on what
// its parser does with one, and the stake is a tunnel that will not start on a
// device whose only way out is that tunnel. So the binary is read first.

func TestTheCurrentSpellingIsUsedWhenTheTunnelKnowsIt(t *testing.T) {
	bin := fakeHev(t, "connect-timeout", "tcp-read-write-timeout", "udp-read-write-timeout")
	if got := hevKeys(bin); got != hevSplitKeys {
		t.Fatalf("want the split spelling, got %v", got)
	}
}

func TestAnOlderTunnelGetsTheOlderSpelling(t *testing.T) {
	bin := fakeHev(t, "connect-timeout", "read-write-timeout")
	if got := hevKeys(bin); got != hevSingleKey {
		t.Fatalf("want the single spelling, got %v", got)
	}

	tun := &Tun{Settings: settings(14400, bin)}
	out := tun.renderConfig()
	if !strings.Contains(out, "  read-write-timeout: 14400000") {
		t.Fatalf("the older key is not written:\n%s", out)
	}
	// And the names it does not know must not appear at all.
	if strings.Contains(out, "tcp-read-write-timeout") ||
		strings.Contains(out, "udp-read-write-timeout") {
		t.Fatalf("a key this tunnel does not know was written:\n%s", out)
	}
}

// The older name is a substring of the newer one, so a binary carrying only
// the new names must not be read as an old one.
func TestTheNewNameIsNotMistakenForTheOldOne(t *testing.T) {
	bin := fakeHev(t, "tcp-read-write-timeout")
	if got := hevKeys(bin); got != hevSplitKeys {
		t.Fatalf("the split spelling was misread as the single one")
	}
}

// dribble hands out a few bytes at a time, the way a real file read can, so
// that the name being looked for lands across a boundary rather than inside
// one comfortable buffer.
type dribble struct {
	data []byte
	at   int
	each int
}

func (d *dribble) Read(p []byte) (int, error) {
	if d.at >= len(d.data) {
		return 0, io.EOF
	}
	n := d.each
	if n > len(p) {
		n = len(p)
	}
	if d.at+n > len(d.data) {
		n = len(d.data) - d.at
	}
	copy(p, d.data[d.at:d.at+n])
	d.at += n
	return n, nil
}

// A name that lands across two reads must still be found. Without the overlap
// the search only ever sees one read's worth at a time, and a name straddling
// the join is invisible — which on a real binary is a coin toss decided by
// where in the file the option names happen to sit.
func TestANameSplitAcrossReadsIsStillFound(t *testing.T) {
	// Reads of 100 bytes, with the name starting at 95, so it straddles the
	// join between two of them. Anything shorter than the overlap would be
	// reassembled by accident and prove nothing.
	//
	// The old name is the one placed there deliberately: missing it changes
	// the answer, where missing the new one would fall back to the same answer
	// and this would pass whether the search worked or not.
	body := strings.Repeat("x", 95) + "read-write-timeout" + strings.Repeat("x", 300)
	r := &dribble{data: []byte(body), each: 100}
	if got := hevKeysIn(r); got != hevSingleKey {
		t.Fatalf("a name across a read boundary was missed")
	}
}

// A tunnel that mentions neither, and a path that is not there at all, both
// fall back to the current spelling rather than to no timeout: silence here is
// the bug this file exists to fix.
func TestAnUnreadableTunnelStillGetsATimeout(t *testing.T) {
	if got := hevKeys(filepath.Join(t.TempDir(), "absent")); got != hevSplitKeys {
		t.Fatalf("want the current spelling for an unreadable binary, got %v", got)
	}
	bin := fakeHev(t, "nothing-relevant-here")
	tun := &Tun{Settings: settings(14400, bin)}
	if !strings.Contains(tun.renderConfig(), "tcp-read-write-timeout: 14400000") {
		t.Fatalf("a binary with no recognisable names should still be given a timeout")
	}
}

// --- both modes that run a tunnel, not just the one that showed the bug -----

// Mixed mode runs the same tunnel process. Its TCP goes through the firewall,
// so the five-minute timeout never showed there — but its UDP does go through
// hev, on a one-minute default, and every QUIC connection on the network is
// UDP. A fix applied only to the mode where the symptom was noticed would
// leave that in place and nobody would find it for another month.
func TestEveryModeThatRunsATunnelGetsTheTimeouts(t *testing.T) {
	bin := fakeHev(t, "tcp-read-write-timeout", "udp-read-write-timeout")

	for _, m := range []model.Mode{model.ModeTUN, model.ModeMixed} {
		if !m.NeedsTunProcess() {
			t.Fatalf("%s does not run a tunnel; this test is about the ones that do", m)
		}
		s := settings(14400, bin)
		s.Mode = m
		out := (&Tun{Settings: s}).renderConfig()

		if got := valueOf(t, out, "tcp-read-write-timeout"); got != 14400000 {
			t.Fatalf("%s mode: TCP timeout is %d", m, got)
		}
		if got := valueOf(t, out, "udp-read-write-timeout"); got != hevUDPCeilingSec*1000 {
			t.Fatalf("%s mode: UDP timeout is %d", m, got)
		}
	}
}

// --- and the rest of the file is untouched ----------------------------------

func TestTheConfigIsStillWhatTheTunnelExpects(t *testing.T) {
	bin := fakeHev(t, "tcp-read-write-timeout")
	tun := &Tun{Settings: settings(14400, bin)}
	out := tun.renderConfig()

	for _, want := range []string{
		"tunnel:", "  name: xwrt0", "  mtu: 8500", "  ipv4: 198.18.0.1",
		"socks5:", "  port: 10808", "  address: 127.0.0.1", "  udp: 'udp'",
		"misc:", "  task-stack-size: 86016", "  log-file: stderr",
		"  limit-nofile: 65535",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("%q went missing from the tunnel config:\n%s", want, out)
		}
	}
	// The timeouts belong to misc, not to a section of their own.
	misc := out[strings.Index(out, "misc:"):]
	if !strings.Contains(misc, "read-write-timeout") {
		t.Fatalf("the timeout landed outside the misc section:\n%s", out)
	}
}

func valueOf(t *testing.T, config, key string) int {
	t.Helper()
	for _, line := range strings.Split(config, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, key+":") {
			continue
		}
		n := 0
		for _, c := range strings.TrimSpace(strings.TrimPrefix(line, key+":")) {
			if c < '0' || c > '9' {
				t.Fatalf("%s is not a number: %q", key, line)
			}
			n = n*10 + int(c-'0')
		}
		return n
	}
	t.Fatalf("%s is not in the config:\n%s", key, config)
	return 0
}
