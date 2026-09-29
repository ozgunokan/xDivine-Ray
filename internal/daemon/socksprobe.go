package daemon

import (
	"errors"
	"io"
	"net"
	"time"
)

// How the readiness probe says hello.
//
// The daemon has to know when the core has finished starting, and the only
// thing it can ask is the port. For a long time it asked by opening a TCP
// connection and hanging up — which works, and costs one line of the operator's
// log every single time:
//
//	ERROR xray  from tcp:127.0.0.1:37974 rejected  proxy/socks: failed to read request > EOF
//
// That line is true. A client connected to the SOCKS inbound, said nothing, and
// went away; the core has no way to know it was us and no reason to keep quiet
// about it. But it is an ERROR in the log of a device whose whole purpose is to
// be diagnosed from that log, it appears on every connect, and it looks exactly
// like the transport errors somebody would be hunting. An hour went into
// chasing it once, which is an hour this file exists to give back.
//
// So the probe now behaves like a client instead of like a port scanner: it
// negotiates, asks for a UDP association it will not use, and closes the
// connection at a point where closing it is the normal end of a session rather
// than the middle of a sentence. The core logs nothing, because nothing went
// wrong.
//
// The exchange is RFC 1928, and short:
//
//	→ 05 01 00                     version 5, one method, no authentication
//	← 05 00                         agreed, no authentication
//	→ 05 03 00 01 00000000 0000     UDP ASSOCIATE, 0.0.0.0:0
//	← 05 00 00 01 <addr> <port>     here is where to send your datagrams
//	  (close)                       the association ends with the connection
//
// UDP ASSOCIATE rather than CONNECT deliberately: CONNECT would make the core
// dial the outside world through the tunnel to satisfy a probe, and a readiness
// check that sends real traffic somewhere is not a readiness check. An
// association costs the core a bookkeeping entry that it drops the moment the
// connection closes, and dials nothing.

var (
	errNotSocks    = errors.New("the listener did not answer as a SOCKS server")
	errNoMethod    = errors.New("the listener refused an unauthenticated session")
	errNoAssociate = errors.New("the listener refused a UDP association")
)

// greetSocks completes and ends one SOCKS session on an already open
// connection. Closing the connection is the caller's job, and may happen the
// moment this returns.
func greetSocks(conn net.Conn, timeout time.Duration) error {
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return err
	}

	if _, err := conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		return err
	}
	var method [2]byte
	if _, err := io.ReadFull(conn, method[:]); err != nil {
		return err
	}
	if method[0] != 0x05 {
		return errNotSocks
	}
	if method[1] != 0x00 {
		return errNoMethod
	}

	if _, err := conn.Write([]byte{0x05, 0x03, 0x00, 0x01, 0, 0, 0, 0, 0, 0}); err != nil {
		return err
	}
	var head [4]byte
	if _, err := io.ReadFull(conn, head[:]); err != nil {
		return err
	}
	if head[0] != 0x05 {
		return errNotSocks
	}
	if head[1] != 0x00 {
		return errNoAssociate
	}

	// Read the rest of the reply even though nothing is done with it, because
	// of what closing a socket with unread data in it does: the kernel sends a
	// reset instead of a clean shutdown, and the other end sees its session end
	// in an error rather than an ending. Draining the reply is what keeps this
	// whole exchange quiet at the far end, which is the entire point of it.
	rest, err := addrRest(head[3], conn)
	if err != nil {
		return err
	}
	if _, err := io.ReadFull(conn, rest); err != nil {
		return err
	}
	return nil
}

// addrRest sizes the address and port still to come, for the address type a
// SOCKS reply declared.
func addrRest(kind byte, r io.Reader) ([]byte, error) {
	switch kind {
	case 0x01: // IPv4
		return make([]byte, 4+2), nil
	case 0x04: // IPv6
		return make([]byte, 16+2), nil
	case 0x03: // name, length-prefixed
		var n [1]byte
		if _, err := io.ReadFull(r, n[:]); err != nil {
			return nil, err
		}
		return make([]byte, int(n[0])+2), nil
	}
	return nil, errNotSocks
}
