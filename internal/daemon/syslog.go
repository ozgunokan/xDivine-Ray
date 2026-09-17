package daemon

import (
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"xwrt/internal/model"
)

// The in-memory log dies with the process, which is the wrong behaviour for
// exactly the entries someone wants afterwards: the error that stopped the
// service at three in the morning. Mirroring warnings and errors into the
// system log fixes that, and it puts them where an OpenWrt operator already
// looks — `logread`.
//
// This writes to /dev/log directly rather than using log/syslog, for two
// reasons: the datagram socket is reconnectable, so logd restarting does not
// permanently break logging, and it keeps the tag exactly as chosen rather
// than derived from the binary name.

// syslogSink writes to the local syslog socket.
type syslogSink struct {
	tag string

	mu      sync.Mutex
	conn    net.Conn
	failed  bool
	nextTry time.Time
}

// syslog facility 16 (local0), shifted as the wire format requires.
const syslogFacility = 16 << 3

func newSyslogSink(tag string) *syslogSink {
	s := &syslogSink{tag: tag}
	s.connect()
	return s
}

// connect opens the socket. Both the datagram and stream forms are tried
// because logd and syslog-ng disagree about which they offer.
func (s *syslogSink) connect() {
	for _, path := range []string{"/dev/log", "/var/run/log", "/var/run/syslog"} {
		for _, network := range []string{"unixgram", "unix"} {
			c, err := net.DialTimeout(network, path, time.Second)
			if err == nil {
				s.conn = c
				s.failed = false
				return
			}
		}
	}
	// No system log here. That is normal off-router, so it is not an error;
	// the in-memory ring still has everything.
	s.failed = true
	s.nextTry = time.Now().Add(time.Minute)
}

// Write sends one entry. Failures are swallowed: logging must never be the
// reason an operation fails.
func (s *syslogSink) Write(level model.Level, msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.conn == nil {
		// Retry occasionally, so a daemon that started before logd still ends
		// up logging once logd is there.
		if s.failed && time.Now().Before(s.nextTry) {
			return
		}
		s.connect()
		if s.conn == nil {
			return
		}
	}

	pri := syslogFacility + severity(level)
	// RFC 3164: <priority>timestamp hostname tag[pid]: message
	line := fmt.Sprintf("<%d>%s %s[%d]: %s",
		pri, time.Now().Format(time.Stamp), s.tag, os.Getpid(), msg)

	if _, err := s.conn.Write([]byte(line)); err != nil {
		// A dropped connection is worth one reconnect and retry; logd
		// restarting should not silence us until the next daemon restart.
		s.conn.Close()
		s.conn = nil
		s.connect()
		if s.conn != nil {
			_, _ = s.conn.Write([]byte(line))
		}
	}
}

func (s *syslogSink) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn != nil {
		s.conn.Close()
		s.conn = nil
	}
}

// severity maps our levels onto syslog's, so `logread -l` filtering and any
// log forwarding behave as an operator expects.
func severity(l model.Level) int {
	switch l {
	case model.LevelError:
		return 3 // err
	case model.LevelWarn:
		return 4 // warning
	case model.LevelDebug:
		return 7 // debug
	default:
		return 6 // info
	}
}
