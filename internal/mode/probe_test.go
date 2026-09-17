package mode

import (
	"net"
	"testing"
)

// The probe exists so a resolver that does not answer is caught before the
// network is handed to it. It has to accept a real answer and reject silence.
func TestProbeResolver(t *testing.T) {
	// A socket that answers with NOERROR and the same id.
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	go func() {
		buf := make([]byte, 512)
		for {
			n, addr, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			if n < 12 {
				continue
			}
			reply := append([]byte(nil), buf[:n]...)
			reply[2] = 0x81 // response, recursion desired
			reply[3] = 0x80 // recursion available, rcode 0
			pc.WriteTo(reply, addr)
		}
	}()
	port := pc.LocalAddr().(*net.UDPAddr).Port
	if err := probeResolver(port); err != nil {
		t.Errorf("a resolver that answers must pass: %v", err)
	}

	// Nothing listening: the probe must fail rather than hang.
	dead, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	deadPort := dead.LocalAddr().(*net.UDPAddr).Port
	dead.Close()
	if err := probeResolver(deadPort); err == nil {
		t.Error("a silent port must not pass as a working resolver")
	}
}

// OpenWrt does not always use /tmp/dnsmasq.d. It commonly uses a per-instance
// subdirectory under it, and a build can put it anywhere — so the directory is
// read from the command line dnsmasq is actually running with, not guessed.
func TestConfDirFromCmdline(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
		ok   bool
	}{
		{
			name: "per-instance subdirectory with a filter",
			args: []string{"/usr/sbin/dnsmasq", "-C", "/var/etc/dnsmasq.conf.cfg01411c",
				"-k", "-x", "/var/run/dnsmasq/dnsmasq.cfg01411c.pid",
				"--conf-dir=/tmp/dnsmasq.d/cfg01411c,*"},
			want: "/tmp/dnsmasq.d/cfg01411c",
			ok:   true,
		},
		{
			name: "plain directory",
			args: []string{"dnsmasq", "--conf-dir=/etc/dnsmasq.d"},
			want: "/etc/dnsmasq.d",
			ok:   true,
		},
		{
			name: "dnsmasq without a conf-dir",
			args: []string{"/usr/sbin/dnsmasq", "-k"},
		},
		{
			// Another process must never be mistaken for dnsmasq, however its
			// arguments look.
			name: "some other process",
			args: []string{"/usr/sbin/odhcpd", "--conf-dir=/tmp/elsewhere"},
		},
		{
			name: "no arguments at all",
			args: nil,
		},
	}

	for _, c := range cases {
		got, ok := confDirFromCmdline(c.args)
		if ok != c.ok || got != c.want {
			t.Errorf("%s: got (%q, %v), want (%q, %v)", c.name, got, ok, c.want, c.ok)
		}
	}
}

// The state file is what makes the uci path reversible after a kill: the
// previous value of noresolv has to survive the daemon that recorded it, and
// "it was not set" has to survive as distinct from "it was set to something".
func TestParseNoresolvState(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
		had  bool
	}{
		{name: "it was set", raw: "noresolv=1\n", want: "1", had: true},
		{name: "it was set to zero", raw: "noresolv=0\n", want: "0", had: true},
		{name: "it was unset", raw: "noresolv=\n"},
		{name: "empty file", raw: ""},
		{name: "unrelated content", raw: "something=else\n"},
	}
	for _, c := range cases {
		got, had := parseNoresolvState(c.raw)
		if got != c.want || had != c.had {
			t.Errorf("%s: got (%q, %v), want (%q, %v)", c.name, got, had, c.want, c.had)
		}
	}
}
