package mode

import (
	"bytes"
	"io"
	"os"
	"strconv"
	"strings"
)

// The timeout that was closing idle connections in TUN mode.
//
// hev-socks5-tunnel gives every session an I/O timeout, and its defaults are
// five minutes for TCP and one minute for UDP. Nothing here set them, so
// nothing here knew about them — and in TUN mode every connection on the
// network passes through it.
//
// Five minutes is the wrong number for a home network, and wrong in a way that
// is invisible. A phone holds one connection to its push service open for as
// long as it is switched on and sends nothing down it between messages; the
// heartbeat that keeps it alive comes every fifteen to thirty minutes. The
// tunnel closed that connection at five, the phone did not find out until its
// next heartbeat, and everything sent in between arrived in a batch when it
// reconnected. What the owner saw was a message sent at 22:37 arriving at
// 22:47.
//
// It cost weeks, because every property of it points somewhere else: it
// survives changing the transport, changing the server, changing the DNS mode,
// and it does not happen in mixed mode — where TCP goes through the firewall
// instead and never meets this timeout at all. The daemon's own connIdle had
// already been raised to four hours for exactly this reason, and it never got
// the chance to apply: this closed the connection first.
//
// So the settings' idle timeout now reaches both. One number, one meaning:
// how long a connection may carry no data before something closes it.

const (
	// The UDP ceiling. TCP follows the setting exactly, but a UDP session is a
	// mapping held open in userspace with no close to tell it otherwise, and a
	// household's worth of them held for four hours is memory this class of
	// device does not have to spare. Ten minutes is far longer than any UDP
	// protocol's own keepalive and short enough that the table drains.
	hevUDPCeilingSec = 600
	// Nothing is served by a timeout longer than a day, and the value is
	// multiplied by a thousand before it is written.
	hevMaxSec = 86400
)

// hevIdleMillis is what to write for a given idle setting, in milliseconds.
func hevIdleMillis(connIdleSec int) (tcp, udp int) {
	if connIdleSec <= 0 {
		return 0, 0
	}
	if connIdleSec > hevMaxSec {
		connIdleSec = hevMaxSec
	}
	udpSec := connIdleSec
	if udpSec > hevUDPCeilingSec {
		udpSec = hevUDPCeilingSec
	}
	return connIdleSec * 1000, udpSec * 1000
}

// hevKeyForm is how the installed tunnel spells these settings.
type hevKeyForm int

const (
	// hevSplitKeys is the current spelling: tcp-read-write-timeout and
	// udp-read-write-timeout.
	hevSplitKeys hevKeyForm = iota
	// hevSingleKey is the older one: a single read-write-timeout covering both.
	hevSingleKey
)

// hevKeys asks the installed binary which spelling it understands.
//
// By reading it, not by running it. The binary carries its own option names as
// plain strings, so looking for them settles the question without starting a
// process, without creating a device, and without a version number that a
// distribution may have patched underneath.
//
// It matters because writing a key the binary does not know is a gamble on
// what its parser does with one, and the stake is a tunnel that will not start
// on a device whose only way out is the tunnel. Reading first costs a few
// milliseconds once per connect and removes the gamble.
//
// A binary that cannot be read at all falls back to the current spelling,
// which is what a tunnel recent enough to be installed today will understand.
func hevKeys(path string) hevKeyForm {
	f, err := os.Open(path)
	if err != nil {
		return hevSplitKeys
	}
	defer f.Close()
	return hevKeysIn(f)
}

// hevKeysIn is the search itself, over any reader, so that it can be tested
// against a handful of bytes rather than against a binary.
func hevKeysIn(r io.Reader) hevKeyForm {
	// Chunked, with an overlap, because these binaries are megabytes and a
	// name can land across a boundary. The overlap is longer than the longest
	// name being looked for.
	const chunk = 1 << 16
	const overlap = 64

	split := []byte("tcp-read-write-timeout")
	single := []byte("read-write-timeout")

	buf := make([]byte, chunk+overlap)
	held := 0
	sawSingle := false

	for {
		n, err := r.Read(buf[held:])
		if n > 0 {
			window := buf[:held+n]
			// The split spelling contains the single one, so it is checked
			// first and answers on its own.
			if bytes.Contains(window, split) {
				return hevSplitKeys
			}
			if bytes.Contains(window, single) {
				sawSingle = true
			}
			if len(window) > overlap {
				held = copy(buf, window[len(window)-overlap:])
			} else {
				held = copy(buf, window)
			}
		}
		if err != nil {
			break
		}
	}
	if sawSingle {
		return hevSingleKey
	}
	return hevSplitKeys
}

// hevIdleLines renders the timeout settings for the misc section, already
// indented, or nothing when there is no timeout to set.
func hevIdleLines(form hevKeyForm, tcpMS, udpMS int) string {
	if tcpMS <= 0 {
		return ""
	}
	var b strings.Builder
	if form == hevSingleKey {
		// One key for both, so the connection that must survive decides it.
		b.WriteString("  read-write-timeout: " + strconv.Itoa(tcpMS) + "\n")
		return b.String()
	}
	b.WriteString("  tcp-read-write-timeout: " + strconv.Itoa(tcpMS) + "\n")
	b.WriteString("  udp-read-write-timeout: " + strconv.Itoa(udpMS) + "\n")
	return b.String()
}
