package update

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"
)

// Where the updater's own traffic goes.
//
// Everything in this package talks to GitHub, and until now it did so the way
// any program on the router does: straight out of the WAN. That is wrong
// whenever the tunnel is up. The whole reason this device exists is that the
// line it sits on cannot reach some of what is on the internet, and the release
// page is on the internet like everything else — so an update check from a
// router whose clients are all tunnelled would be the one request on the device
// still going out over the line that does not work.
//
// There was already a setting that fixed it by accident: "send the router's own
// traffic through the tunnel" captures the daemon's sockets along with
// everything else. But it is a separate choice, it is off by default, and
// somebody who leaves it off has not asked for their updates to be fetched over
// a line that cannot fetch them. The two questions are not the same question.
//
// So the updater decides for itself, and the rule is the simple one: tunnel up,
// go through the tunnel; tunnel down, go out the line. It asks the core's own
// SOCKS inbound, which exists in every capture mode, on the loopback address —
// which no capture rule touches, so this cannot loop back on itself whatever
// the firewall is doing.
//
// The hostname is handed to the proxy rather than resolved here. That is not an
// optimisation: on a device whose resolver points into the tunnel, resolving
// first and connecting second are two different lookups that can disagree, and
// the proxy is the only party that can resolve the name from the far end of the
// line that is actually going to carry the connection.

// Via returns a dial function that reaches the address through a SOCKS5 proxy.
func Via(proxy string) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		if network != "tcp" && network != "tcp4" && network != "tcp6" {
			return nil, fmt.Errorf("the updater cannot carry %s through a proxy", network)
		}
		var d net.Dialer
		conn, err := d.DialContext(ctx, "tcp", proxy)
		if err != nil {
			return nil, fmt.Errorf("the tunnel's proxy at %s did not accept a "+
				"connection: %w", proxy, err)
		}
		if err := socksConnect(ctx, conn, addr); err != nil {
			conn.Close()
			return nil, err
		}
		return conn, nil
	}
}

// socksHandshakeBudget bounds the handshake specifically, as opposed to the
// request as a whole. The proxy is on the loopback interface: it answers in
// microseconds or it is not there.
// It is a variable only so that a test can shrink it: the thing worth testing
// is that it is lifted again afterwards, and at ten seconds that test would
// take ten seconds.
var socksHandshakeBudget = 10 * time.Second

func socksConnect(ctx context.Context, conn net.Conn, addr string) error {
	deadline := time.Now().Add(socksHandshakeBudget)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return err
	}
	// Cleared on the way out: the deadline above is for the handshake, and
	// leaving it set would cut the download off in the middle.
	defer conn.SetDeadline(time.Time{})

	host, portText, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("%q is not a port", portText)
	}

	// One method offered, and it is "no authentication".
	if _, err := conn.Write([]byte{5, 1, 0}); err != nil {
		return err
	}
	var greeting [2]byte
	if _, err := io.ReadFull(conn, greeting[:]); err != nil {
		return fmt.Errorf("the proxy did not answer the greeting: %w", err)
	}
	if greeting[0] != 5 {
		return errors.New("the proxy answered something that is not SOCKS5")
	}
	if greeting[1] != 0 {
		return errors.New("the proxy wants authentication, which the core's own " +
			"inbound never does")
	}

	req := []byte{5, 1, 0}
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			req = append(req, 1)
			req = append(req, v4...)
		} else {
			req = append(req, 4)
			req = append(req, ip.To16()...)
		}
	} else {
		// A name longer than this cannot be expressed in the protocol, and no
		// release host has one.
		if len(host) > 255 {
			return fmt.Errorf("the host name is %d bytes, which SOCKS5 cannot "+
				"carry", len(host))
		}
		req = append(req, 3, byte(len(host)))
		req = append(req, host...)
	}
	req = append(req, byte(port>>8), byte(port))
	if _, err := conn.Write(req); err != nil {
		return err
	}

	var head [4]byte
	if _, err := io.ReadFull(conn, head[:]); err != nil {
		return fmt.Errorf("the proxy did not answer the connect request: %w", err)
	}
	if head[0] != 5 {
		return errors.New("the proxy's reply is not SOCKS5")
	}
	if head[1] != 0 {
		return fmt.Errorf("the tunnel refused to carry the connection: %s",
			socksReplyText(head[1]))
	}

	// The bound address has to be read even though nothing here wants it: it is
	// in front of the first byte of the actual conversation.
	rest, err := socksAddrLen(head[3], conn)
	if err != nil {
		return err
	}
	if rest > 0 {
		if _, err := io.ReadFull(conn, make([]byte, rest)); err != nil {
			return fmt.Errorf("the proxy's reply stopped early: %w", err)
		}
	}
	return nil
}

// socksAddrLen returns how many bytes of the reply are left to read, the two
// port bytes included.
func socksAddrLen(kind byte, r io.Reader) (int, error) {
	switch kind {
	case 1:
		return 4 + 2, nil
	case 4:
		return 16 + 2, nil
	case 3:
		var n [1]byte
		if _, err := io.ReadFull(r, n[:]); err != nil {
			return 0, err
		}
		return int(n[0]) + 2, nil
	}
	return 0, fmt.Errorf("the proxy named an address of unknown type %d", kind)
}

// socksReplyText turns the one byte that says no into the sentence a person can
// act on. The distinction that matters here is between "the tunnel is up but
// cannot reach GitHub" and "the tunnel itself is refusing", and the codes say
// which.
func socksReplyText(code byte) string {
	switch code {
	case 1:
		return "the proxy failed (general failure)"
	case 2:
		return "the proxy's own rules forbid it"
	case 3:
		return "the network is unreachable through the tunnel"
	case 4:
		return "the host is unreachable through the tunnel"
	case 5:
		return "the far end refused the connection"
	case 6:
		return "the attempt timed out"
	case 7:
		return "the proxy does not support this kind of connection"
	case 8:
		return "the proxy does not support this kind of address"
	}
	return "reply code " + strconv.Itoa(int(code))
}
