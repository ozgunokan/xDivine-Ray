//go:build linux

package netmark

import "syscall"

// Control returns a net.Dialer Control hook that marks the socket.
//
// It has to happen before connect, which is why it is a Control hook rather
// than something done to the connection afterwards: the mark decides which
// rules the outgoing packets meet, and the first of those packets is the SYN.
//
// A mark of zero returns nil — SO_MARK 0 means "no mark", so asking for it is
// a syscall that does nothing, and a nil hook is what net.Dialer wants for
// "leave the socket alone".
//
// Setting SO_MARK needs CAP_NET_ADMIN. This daemon has it — it writes nftables
// rules — but a failure is deliberately ignored: the measurement is still
// worth taking on a device where the mark cannot be set, it is just the
// captured path that gets measured then, and refusing to measure at all would
// hide a whole page for the sake of a footnote.
func Control(mark int) func(network, address string, c syscall.RawConn) error {
	if mark <= 0 {
		return nil
	}
	return func(_, _ string, c syscall.RawConn) error {
		_ = c.Control(func(fd uintptr) {
			_ = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET,
				syscall.SO_MARK, mark)
		})
		return nil
	}
}
