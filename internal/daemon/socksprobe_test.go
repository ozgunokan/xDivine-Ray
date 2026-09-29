package daemon

import (
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

// The thing being tested here is not really the daemon. It is what the daemon
// leaves behind in somebody else's log.
//
// So the test needs a server that reacts to a botched handshake the way the
// core does: by treating it as a rejected request and recording it. fakeSocks
// is that server, kept deliberately close to the shape of the real one — read
// the methods, answer, read the request, answer, then let the association live
// as long as the connection does.

type fakeSocks struct {
	ln net.Listener

	mu       sync.Mutex
	rejected []error // what the core would have written as ERROR lines
	ended    int     // sessions that ended the way a session is meant to
	replyTo  byte    // address type to answer an association with
	refuse   bool    // answer the association with a failure
	mute     bool    // accept the connection and then say nothing at all
}

func newFakeSocks(t *testing.T) *fakeSocks {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := &fakeSocks{ln: ln, replyTo: 0x01}
	go s.serve()
	t.Cleanup(func() { ln.Close() })
	return s
}

func (s *fakeSocks) addr() string { return s.ln.Addr().String() }

func (s *fakeSocks) reject(err error) {
	s.mu.Lock()
	s.rejected = append(s.rejected, err)
	s.mu.Unlock()
}

func (s *fakeSocks) counts() (rejected int, ended int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.rejected), s.ended
}

func (s *fakeSocks) serve() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.session(conn)
	}
}

func (s *fakeSocks) session(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	if s.mute {
		<-make(chan struct{})
	}

	// Everything up to the reply is one handshake, and any failure inside it is
	// the one the core reports as "failed to read request".
	var hello [2]byte
	if _, err := io.ReadFull(conn, hello[:]); err != nil {
		s.reject(err)
		return
	}
	methods := make([]byte, int(hello[1]))
	if _, err := io.ReadFull(conn, methods); err != nil {
		s.reject(err)
		return
	}
	if _, err := conn.Write([]byte{0x05, 0x00}); err != nil {
		s.reject(err)
		return
	}

	req := make([]byte, 4)
	if _, err := io.ReadFull(conn, req); err != nil {
		s.reject(err)
		return
	}
	if _, err := io.ReadFull(conn, make([]byte, 4+2)); err != nil { // IPv4 target
		s.reject(err)
		return
	}
	if s.refuse {
		conn.Write([]byte{0x05, 0x07, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}
	conn.Write(s.associationReply())

	// An association lasts as long as the connection carrying it. The client
	// closing it is the ordinary end of the session, not a fault.
	io.Copy(io.Discard, conn)
	s.mu.Lock()
	s.ended++
	s.mu.Unlock()
}

func (s *fakeSocks) associationReply() []byte {
	switch s.replyTo {
	case 0x04:
		return append([]byte{0x05, 0x00, 0x00, 0x04}, make([]byte, 16+2)...)
	case 0x03:
		return append([]byte{0x05, 0x00, 0x00, 0x03, 9},
			[]byte("localhost\x04\x38")...)
	}
	return []byte{0x05, 0x00, 0x00, 0x01, 127, 0, 0, 1, 0x04, 0x38}
}

func greet(t *testing.T, addr string) error {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	return greetSocks(conn, 2*time.Second)
}

// --- the bug this file exists for -------------------------------------------

// What the old probe did, kept as a test so that going back to it is a visible
// change rather than a quiet one. Connecting and hanging up is not free: it
// costs an ERROR line in the log of the very thing being started, on every
// connect, for as long as nobody notices.
func TestConnectingAndHangingUpIsRecordedAgainstTheCore(t *testing.T) {
	s := newFakeSocks(t)
	conn, err := net.DialTimeout("tcp", s.addr(), 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	conn.Close()

	waitFor(t, func() bool { r, _ := s.counts(); return r == 1 })
	if _, ended := s.counts(); ended != 0 {
		t.Fatalf("a hang-up should not look like a finished session")
	}
}

func TestTheProbeLeavesNothingForTheCoreToComplainAbout(t *testing.T) {
	s := newFakeSocks(t)
	if err := greet(t, s.addr()); err != nil {
		t.Fatalf("greeting a SOCKS server should succeed: %v", err)
	}

	waitFor(t, func() bool { _, ended := s.counts(); return ended == 1 })
	if rejected, _ := s.counts(); rejected != 0 {
		t.Fatalf("the server rejected %d session(s); the probe is still rude", rejected)
	}
}

// --- and it is still a check, not a courtesy --------------------------------

func TestAStrangerOnThePortIsNotMistakenForTheCore(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		conn.Write([]byte("HTTP/1.1 400 Bad Request\r\n\r\n"))
		conn.Close()
	}()

	if err := greet(t, ln.Addr().String()); !errors.Is(err, errNotSocks) {
		t.Fatalf("a web server on the SOCKS port should be named as such, got %v", err)
	}
}

// A listener can get the easy half right and the rest wrong: two bytes of
// agreement are cheap to emit by accident, and the reply to the request is
// where a pretender stops sounding like a SOCKS server.
func TestAReplyThatIsNotSOCKSIsNotAcceptedEither(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		io.ReadFull(conn, make([]byte, 3))
		conn.Write([]byte{0x05, 0x00})
		io.ReadFull(conn, make([]byte, 10))
		conn.Write([]byte("HTTP/1.1 200 OK\r\n\r\n"))
	}()

	if err := greet(t, ln.Addr().String()); !errors.Is(err, errNotSocks) {
		t.Fatalf("want errNotSocks for a reply in another protocol, got %v", err)
	}
}

func TestARefusedAssociationIsNotReportedAsSuccess(t *testing.T) {
	s := newFakeSocks(t)
	s.refuse = true
	if err := greet(t, s.addr()); !errors.Is(err, errNoAssociate) {
		t.Fatalf("want a refused association, got %v", err)
	}
}

func TestAnUnauthenticatedSessionThatIsRefusedIsNamed(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		io.ReadFull(conn, make([]byte, 3))
		conn.Write([]byte{0x05, 0xFF}) // no acceptable methods
		conn.Close()
	}()

	if err := greet(t, ln.Addr().String()); !errors.Is(err, errNoMethod) {
		t.Fatalf("want the refusal to be named, got %v", err)
	}
}

// The reply's length depends on the address type it declares, and a reply read
// to the wrong length is a reply never really checked. All three shapes.
func TestEveryShapeOfReplyIsReadToItsEnd(t *testing.T) {
	for _, kind := range []byte{0x01, 0x03, 0x04} {
		s := newFakeSocks(t)
		s.replyTo = kind
		if err := greet(t, s.addr()); err != nil {
			t.Fatalf("address type %#x: %v", kind, err)
		}
		waitFor(t, func() bool { _, ended := s.counts(); return ended == 1 })
	}
}

func TestAnAddressTypeNobodyDefinedIsRefused(t *testing.T) {
	if _, err := addrRest(0x09, nil); !errors.Is(err, errNotSocks) {
		t.Fatalf("want errNotSocks, got %v", err)
	}
}

// A port that accepts and then says nothing must not hold the probe open: the
// deadline is what stops a silent listener from eating the whole connect
// budget.
func TestASilentListenerDoesNotHoldTheProbe(t *testing.T) {
	s := newFakeSocks(t)
	s.mute = true

	done := make(chan error, 1)
	go func() {
		conn, err := net.DialTimeout("tcp", s.addr(), 2*time.Second)
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		done <- greetSocks(conn, 200*time.Millisecond)
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatalf("a listener that never answered should not pass")
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("the probe did not give up on a silent listener")
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("condition never came true")
}
