package model

import (
	"strings"
	"testing"
)

// What a profile offers in the TLS handshake.
//
// Two protocols get dropped, both for the same reason: advertising something
// the transport cannot actually speak. A server that takes us up on it leaves
// a connection neither side can use, and neither failure says so.

func alpnFor(network, alpn string) []string {
	return (&Profile{Network: network, ALPN: alpn}).ALPNList()
}

func joined(list []string) string { return strings.Join(list, ",") }

// The one this was written for. Panels emit exactly this on WebSocket
// profiles, and it is the default mistake rather than a rare one.
func TestWebsocketDoesNotOfferH2(t *testing.T) {
	got := alpnFor("ws", "h2,http/1.1")
	if joined(got) != "http/1.1" {
		t.Errorf("alpn = %q; WebSocket upgrades an HTTP/1.1 request, so a "+
			"server that picks h2 closes without answering and the dial fails "+
			"with a bare EOF", joined(got))
	}
}

// httpupgrade works the same way and has the same problem.
func TestHTTPUpgradeDoesNotOfferH2(t *testing.T) {
	if got := alpnFor("httpupgrade", "h2,http/1.1"); joined(got) != "http/1.1" {
		t.Errorf("alpn = %q", joined(got))
	}
}

// And the transports that really do speak h2 keep it. Dropping it everywhere
// would trade one silent failure for another.
func TestTransportsThatSpeakH2KeepIt(t *testing.T) {
	for _, network := range []string{"grpc", "xhttp", "splithttp", "h2", "tcp"} {
		if got := alpnFor(network, "h2,http/1.1"); joined(got) != "h2,http/1.1" {
			t.Errorf("%s: alpn = %q, want h2,http/1.1", network, joined(got))
		}
	}
}

func TestH3IsStillDroppedOffQUIC(t *testing.T) {
	if got := alpnFor("ws", "h3,h2,http/1.1"); joined(got) != "http/1.1" {
		t.Errorf("alpn = %q, want http/1.1", joined(got))
	}
	if got := alpnFor("tcp", "h3,h2"); joined(got) != "h2" {
		t.Errorf("alpn = %q, want h2", joined(got))
	}
	if got := alpnFor("quic", "h3,h2"); joined(got) != "h3,h2" {
		t.Errorf("alpn = %q; QUIC is where h3 belongs", joined(got))
	}
}

// A profile that never named an ALPN still names none: an empty list means the
// core's own default, and inventing one here would change what is negotiated on
// every connection that was working before.
func TestNothingIsInvented(t *testing.T) {
	if got := alpnFor("ws", ""); len(got) != 0 {
		t.Errorf("alpn = %q, want nothing", joined(got))
	}
}

// Dropping everything leaves nothing rather than a list with a hole in it.
func TestDroppingTheOnlyEntryLeavesNothing(t *testing.T) {
	if got := alpnFor("ws", "h2"); len(got) != 0 {
		t.Errorf("alpn = %q, want nothing", joined(got))
	}
}
