package daemon

import (
	"strings"
	"testing"
	"time"

	"xwrt/internal/model"
)

// The lines below are real. They came off a router where the tunnel was up,
// the status page said connected, and the phones were not getting their
// notifications — and finding them took reading a log by hand because by the
// time anybody looked, the ring had turned over twice.

const (
	dialFail1 = `2026/09/20 21:02:11 [Error] [3921831] app/proxyman/outbound: failed to process outbound traffic > proxy/vless/outbound: failed to find an available destination > common/retry: all retry attempts failed > transport/internet/websocket: failed to dial WebSocket > EOF`
	dialFail2 = `2026/09/20 21:04:57 [Error] [1174226] app/proxyman/outbound: failed to process outbound traffic > proxy/vless/outbound: failed to find an available destination > common/retry: all retry attempts failed > transport/internet/websocket: failed to dial WebSocket > EOF`
	socksEOF  = `2026/09/20 21:04:16 [Error] [728072] app/proxyman/inbound: connection ends > proxy/socks: failed to read request > EOF`
	infoLine  = `2026/09/20 21:04:16 [Info] [728072] app/proxyman/inbound: connection ends > EOF`
)

var midnight = time.Date(2026, 9, 20, 21, 0, 0, 0, time.UTC)

// feed delivers n occurrences of one fault, gap apart, starting at start, and
// returns what the last one amounted to.
func feed(tally *faultTally, line string, start time.Time, n int, gap time.Duration) seen {
	var last seen
	for i := 0; i < n; i++ {
		last = tally.add(model.SourceCore, line, start.Add(time.Duration(i)*gap))
	}
	return last
}

// --- what counts as the same fault ------------------------------------------

// The whole feature turns on this. Two occurrences of one fault are never
// identical: different connection id, different source port, different second.
// If those count as two faults, four hundred failures are four hundred entries
// and nothing can ever be judged fast or slow.
func TestTheSameFaultTwiceIsOneFaultCountedTwice(t *testing.T) {
	tally := newFaultTally(40)
	tally.add(model.SourceCore, dialFail1, midnight)
	tally.add(model.SourceCore, dialFail2, midnight.Add(3*time.Minute))

	if n := len(tally.items); n != 1 {
		t.Fatalf("want one fault, got %d", n)
	}
	for _, f := range tally.items {
		if f.Count != 2 {
			t.Fatalf("want it counted twice, got %d", f.Count)
		}
		// The connection id is exactly what must not be in there — neither its
		// digits nor the empty brackets they came in.
		if strings.Contains(f.Message, "3921831") || strings.Contains(f.Message, "[") {
			t.Fatalf("the connection id survived into the shape: %q", f.Message)
		}
	}
}

func TestTwoDifferentFaultsStayTwoFaults(t *testing.T) {
	tally := newFaultTally(40)
	tally.add(model.SourceCore, dialFail1, midnight)
	tally.add(model.SourceCore, socksEOF, midnight.Add(time.Minute))
	if n := len(tally.items); n != 2 {
		t.Fatalf("want two faults, got %d", n)
	}
}

// A port and an address change per connection and mean nothing to the fault;
// a path and a verb are the fault.
func TestTheParticularsOfOneOccurrenceAreTakenOut(t *testing.T) {
	a := faultKey(`2026/09/20 21:04:16 [Error] [728072] from tcp:127.0.0.1:37974 rejected  proxy/socks: failed to read request > EOF`)
	b := faultKey(`2026/09/20 22:11:02 [Error] [991233] from tcp:127.0.0.1:41880 rejected  proxy/socks: failed to read request > EOF`)
	if a != b {
		t.Fatalf("the same fault reduced to two shapes:\n  %q\n  %q", a, b)
	}
	if !strings.Contains(a, "proxy/socks: failed to read request") {
		t.Fatalf("reduction ate the fault: %q", a)
	}
}

func TestTwoFaultsThatDifferInWordsAreNotMerged(t *testing.T) {
	a := faultKey(`2026/09/20 21:04:16 [Error] transport/internet/websocket: failed to dial WebSocket > EOF`)
	b := faultKey(`2026/09/20 21:04:16 [Error] transport/internet/websocket: failed to dial WebSocket > context deadline exceeded`)
	if a == b {
		t.Fatalf("two different causes reduced to one shape: %q", a)
	}
}

// Two servers failing the same way is one fault happening twice. The shape
// keeps what went wrong; which address it went wrong to is in the log, and a
// summary that splits per address stops being a summary the moment somebody
// runs a group of five.
func TestTheSameFailureToTwoServersIsOneFault(t *testing.T) {
	a := faultKey(`2026/09/20 21:04:16 [Error] transport/internet/websocket: failed to dial WebSocket > dial tcp 185.199.108.1:443: i/o timeout`)
	b := faultKey(`2026/09/20 21:05:01 [Error] transport/internet/websocket: failed to dial WebSocket > dial tcp 198.51.100.20:443: i/o timeout`)
	if a != b {
		t.Fatalf("one fault, two shapes:\n  %q\n  %q", a, b)
	}
}

// --- fast is news, slow is weather ------------------------------------------
//
// This is the rule the whole thing now turns on, and it is a judgement about
// what a person should be shown, not about what happened. Ten of the same
// failure inside eight minutes is a link that has gone. The same ten spread
// across a night is a mobile line behaving like a mobile line: every one of
// them was retried, every retry worked, and nobody noticed anything.

func TestTenFailuresInAFewMinutesAreReported(t *testing.T) {
	tally := newFaultTally(40)
	last := feed(tally, dialFail1, midnight, burstSize, 20*time.Second)
	if !last.Burst {
		t.Fatalf("ten failures in three minutes should be reported")
	}
	if last.Span != 9*20*time.Second {
		t.Fatalf("the span is wrong: %v", last.Span)
	}
}

func TestTheSameTenSpreadAcrossANightAreNot(t *testing.T) {
	tally := newFaultTally(40)
	last := feed(tally, dialFail1, midnight, burstSize, 20*time.Minute)
	if last.Burst {
		t.Fatalf("ten failures over three hours are not news")
	}
	// And nothing is owed later either. Nothing accumulates towards a report.
	for i := 0; i < 50; i++ {
		if s := tally.add(model.SourceCore, dialFail1,
			midnight.Add(time.Duration(10+i)*20*time.Minute)); s.Burst {
			t.Fatalf("a slow trickle eventually reported itself anyway")
		}
	}
}

// Nine is not ten, however fast. The threshold has to be a threshold.
func TestNineFailuresAreNotEnough(t *testing.T) {
	tally := newFaultTally(40)
	if last := feed(tally, dialFail1, midnight, burstSize-1, time.Second); last.Burst {
		t.Fatalf("nine failures should not have been reported")
	}
}

// The window slides. Nine slow failures followed by ten quick ones is a link
// that has just gone, and the slow ones must not be holding the window open
// against it.
func TestASlowStartDoesNotSpoilARealBurst(t *testing.T) {
	tally := newFaultTally(40)
	feed(tally, dialFail1, midnight, burstSize-1, 30*time.Minute)
	start := midnight.Add(time.Duration(burstSize) * 30 * time.Minute)
	if last := feed(tally, dialFail1, start, burstSize, 10*time.Second); !last.Burst {
		t.Fatalf("a real burst after a quiet stretch should still be reported")
	}
}

// A link that stays down reports once per burst, not once per failure. Thirty
// rapid failures are three lines, not thirty and not one.
func TestALinkThatStaysDownReportsOncePerBurst(t *testing.T) {
	tally := newFaultTally(40)
	bursts := 0
	for i := 0; i < 3*burstSize; i++ {
		if tally.add(model.SourceCore, dialFail1,
			midnight.Add(time.Duration(i)*time.Second)).Burst {
			bursts++
		}
	}
	if bursts != 3 {
		t.Fatalf("want 3 reports for 30 rapid failures, got %d", bursts)
	}
}

// --- what gets written ------------------------------------------------------

func TestOnlyABurstIsWritten(t *testing.T) {
	if _, ok := burstNote(seen{Nth: 1}); ok {
		t.Fatalf("a lone failure should not be written")
	}
	if _, ok := burstNote(seen{Nth: 9}); ok {
		t.Fatalf("a slow repeat should not be written")
	}
	note, ok := burstNote(seen{Nth: 10, Burst: true, Span: 222 * time.Second})
	if !ok {
		t.Fatalf("a burst must be written")
	}
	// It has to say both numbers, or the line reads as one failure and the
	// reader draws the wrong conclusion from it.
	if !strings.Contains(note, "×10") || !strings.Contains(note, "3m42s") {
		t.Fatalf("the note says neither how many nor how fast: %q", note)
	}
}

// A fault the table has no room for is not being counted anywhere, so judging
// it quiet is not something anything is in a position to do.
func TestAFaultNobodyIsCountingIsNeverHidden(t *testing.T) {
	if _, ok := burstNote(seen{}); !ok {
		t.Fatalf("an uncounted fault must still be written")
	}
}

func TestTenRapidFailuresLeaveOneLineInTheLog(t *testing.T) {
	r := NewLogRing(500)
	for i := 0; i < burstSize; i++ {
		r.AddProcessLine(model.SourceCore, dialFail1)
	}

	written := r.Entries(Query{Source: model.SourceCore})
	if len(written) != 1 {
		t.Fatalf("want one line for ten rapid failures, got %d", len(written))
	}
	if !strings.Contains(written[0].Message, "×10") {
		t.Fatalf("the line should say what it stands for: %q", written[0].Message)
	}
	// And the core's own words are still in it, because that is the string
	// somebody will search for.
	if !strings.Contains(written[0].Message, "failed to dial WebSocket") {
		t.Fatalf("the line lost the error itself: %q", written[0].Message)
	}
}

func TestAHandfulOfFailuresLeavesNothingInTheLog(t *testing.T) {
	r := NewLogRing(500)
	for i := 0; i < burstSize-1; i++ {
		r.AddProcessLine(model.SourceCore, dialFail1)
	}
	if got := len(r.Entries(Query{Source: model.SourceCore})); got != 0 {
		t.Fatalf("want silence for nine failures, got %d line(s)", got)
	}
}

// --- and only for the one transport that needed it --------------------------
//
// The rate rule would be just as true of every transport. It is applied to one
// because one produces this in bulk, and because a filter nobody asked for on
// the others is a filter that hides something somebody wanted.

func TestOnlyWebSocketFailuresAreEverHidden(t *testing.T) {
	r := NewLogRing(500)
	for i := 0; i < 3*burstSize; i++ {
		// Same rate, same shape, different transport.
		r.AddProcessLine(model.SourceCore,
			`2026/09/20 21:04:16 [Error] [728072] app/proxyman/outbound: failed to process outbound traffic > proxy/vless/outbound: failed to find an available destination > common/retry: all retry attempts failed > dial tcp 203.0.113.10:443: i/o timeout`)
	}
	if got := len(r.Entries(Query{Source: model.SourceCore})); got != 3*burstSize {
		t.Fatalf("a non-WebSocket failure must be written every time, got %d", got)
	}
}

func TestTheTunnelsOwnErrorsAreNeverHidden(t *testing.T) {
	r := NewLogRing(500)
	for i := 0; i < 3*burstSize; i++ {
		r.AddProcessLine(model.SourceTunnel, "failed to write to tun device: no buffer space")
	}
	if got := len(r.Entries(Query{Source: model.SourceTunnel})); got != 3*burstSize {
		t.Fatalf("the tunnel's errors must be written every time, got %d", got)
	}
}

// The match is on the core's package path, not on the word, so a server named
// after the protocol is not mistaken for the protocol.
func TestAServerNamedAfterTheProtocolIsNotMistakenForIt(t *testing.T) {
	if isWebsocketFault(`[Error] dial tcp: lookup websocket.example.com: no such host`) {
		t.Fatalf("a hostname was taken for the transport")
	}
	if !isWebsocketFault(`[Error] transport/internet/websocket: failed to dial > EOF`) {
		t.Fatalf("the transport was not recognised")
	}
	if !isWebsocketFault(`[Error] ... > transport/internet/websocket: failed to dial WebSocket > EOF`) {
		t.Fatalf("the transport was not recognised inside a chain")
	}
}

// Two faults running at once must not hide each other: the WebSocket one folds
// to a single line, and the one beside it is written out as it always was.
func TestOneFaultDoesNotHideAnother(t *testing.T) {
	r := NewLogRing(500)
	for i := 0; i < burstSize; i++ {
		r.AddProcessLine(model.SourceCore, dialFail1)
		r.AddProcessLine(model.SourceCore, socksEOF)
	}

	written := r.Entries(Query{Source: model.SourceCore})
	if len(written) != burstSize+1 {
		t.Fatalf("want %d socks lines and one WebSocket burst, got %d",
			burstSize, len(written))
	}
}

// A fatal is the core saying it cannot continue, and it says it once. A rule
// that only reports what happens ten times in eight minutes would swallow it
// whole, which is the one outcome this whole feature must not produce.
func TestAFatalIsNeverSwallowed(t *testing.T) {
	r := NewLogRing(500)
	fatal := `2026/09/20 21:04:16 [Fatal] infra/conf: failed to read config file: unexpected end of JSON input`
	r.AddProcessLine(model.SourceCore, fatal)

	written := r.Entries(Query{Source: model.SourceCore})
	if len(written) != 1 {
		t.Fatalf("a fatal must be written the first time, got %d line(s)", len(written))
	}
	// And it is written as it came, with nothing appended: it stands for
	// itself, not for a run of anything.
	if written[0].Message != fatal {
		t.Fatalf("the fatal was altered: %q", written[0].Message)
	}
}

// Even a core that keeps dying the same way keeps saying so. The restart
// backoff is what bounds this, not a filter.
func TestAFatalThatRepeatsKeepsBeingWritten(t *testing.T) {
	r := NewLogRing(500)
	for i := 0; i < 3*burstSize; i++ {
		r.AddProcessLine(model.SourceCore,
			`2026/09/20 21:04:16 [Fatal] main: failed to start: address already in use`)
	}
	if got := len(r.Entries(Query{Source: model.SourceCore})); got != 3*burstSize {
		t.Fatalf("want every fatal written, got %d", got)
	}
}

// Ordinary traffic narration is left alone. It is not counted, so folding it
// would lose it, and `logs 400` has to keep showing what the core was doing.
func TestOrdinaryLinesAreNotFolded(t *testing.T) {
	r := NewLogRing(500)
	for i := 0; i < 20; i++ {
		r.AddProcessLine(model.SourceCore, infoLine)
	}
	if got := len(r.Entries(Query{Source: model.SourceCore})); got != 20 {
		t.Fatalf("want all 20 informational lines, got %d", got)
	}
}

// --- what the status page gets ----------------------------------------------

// The page and the log agree on what is news. Two dial failures in a night
// must not put a panel on the front screen of a device that is working.
func TestAQuietTrickleNeverReachesTheStatusPage(t *testing.T) {
	tally := newFaultTally(40)
	feed(tally, dialFail1, midnight, burstSize-1, 20*time.Minute)
	if got := tally.top(4); got != nil {
		t.Fatalf("want an empty page, got %#v", got)
	}
}

// But once a fault has earned its line, the whole of it is worth knowing —
// including the occurrences nobody was shown.
func TestAFaultThatBurstReportsItsWholeCount(t *testing.T) {
	tally := newFaultTally(40)
	feed(tally, dialFail1, midnight, 5, 30*time.Minute)             // quiet
	feed(tally, dialFail1, midnight.Add(4*time.Hour), burstSize, 0) // then a burst
	feed(tally, dialFail1, midnight.Add(5*time.Hour), 3, time.Hour) // quiet again

	got := tally.top(4)
	if len(got) != 1 {
		t.Fatalf("want the burst fault listed, got %#v", got)
	}
	if got[0].Count != 5+burstSize+3 {
		t.Fatalf("want every occurrence counted, got %d", got[0].Count)
	}
}

func TestTheWorstOffenderComesFirst(t *testing.T) {
	tally := newFaultTally(40)
	feed(tally, socksEOF, midnight, burstSize, time.Second)
	feed(tally, dialFail1, midnight, 5*burstSize, time.Second)

	got := tally.top(4)
	if len(got) != 2 || got[0].Count != 5*burstSize {
		t.Fatalf("want the frequent fault first, got %#v", got)
	}
	if !strings.Contains(got[0].Message, "dial WebSocket") {
		t.Fatalf("wrong fault on top: %q", got[0].Message)
	}
}

func TestTheListIsCappedToWhatWasAskedFor(t *testing.T) {
	tally := newFaultTally(40)
	for i := 0; i < 10; i++ {
		feed(tally, "[Error] fault number "+string(rune('a'+i)), midnight, burstSize, time.Second)
	}
	if got := tally.top(4); len(got) != 4 {
		t.Fatalf("want 4, got %d", len(got))
	}
}

// Both ends are reported, because "one hundred times" means something very
// different over four minutes than over four hours.
func TestBothEndsOfTheWindowAreKept(t *testing.T) {
	tally := newFaultTally(40)
	feed(tally, dialFail1, midnight, burstSize, 30*time.Second)
	last := midnight.Add(time.Duration(burstSize-1) * 30 * time.Second)

	got := tally.top(1)[0]
	if got.First != midnight.Format(time.RFC3339) {
		t.Fatalf("first seen moved: %q", got.First)
	}
	if got.Last != last.Format(time.RFC3339) {
		t.Fatalf("last seen did not move: %q", got.Last)
	}
}

// A device producing endless distinct errors must not turn the tally into an
// unbounded map. It stops recording new shapes and says so to itself.
func TestATableThatIsFullStopsGrowing(t *testing.T) {
	tally := newFaultTally(3)
	for i := 0; i < 9; i++ {
		tally.add(model.SourceCore, "[Error] distinct fault "+strings.Repeat("x", i), midnight)
	}
	if n := len(tally.items); n != 3 {
		t.Fatalf("the table grew past its limit: %d entries", n)
	}
	if tally.missed != 6 {
		t.Fatalf("want 6 missed, got %d", tally.missed)
	}
}

func TestAFreshCoreStartsFromZero(t *testing.T) {
	tally := newFaultTally(40)
	feed(tally, dialFail1, midnight, burstSize, time.Second)
	tally.reset()
	if got := tally.top(4); len(got) != 0 {
		t.Fatalf("want nothing after a reset, got %#v", got)
	}
}

// --- and what reaches it ----------------------------------------------------

func TestOnlyWhatWentWrongIsCounted(t *testing.T) {
	r := NewLogRing(500)
	for i := 0; i < burstSize; i++ {
		r.AddProcessLine(model.SourceCore, dialFail1)
		r.AddProcessLine(model.SourceCore, infoLine)
		// The daemon's own messages are written one per event and deliberately.
		r.Warnf("a warning the daemon chose to write")
	}

	got := r.Faults(4)
	if len(got) != 1 {
		t.Fatalf("want only the core error counted, got %#v", got)
	}
	if got[0].Source != string(model.SourceCore) {
		t.Fatalf("the fault lost its source: %q", got[0].Source)
	}
}

func TestTheTunnelIsCountedToo(t *testing.T) {
	r := NewLogRing(500)
	for i := 0; i < burstSize; i++ {
		r.AddProcessLine(model.SourceTunnel, "failed to open tun device")
	}
	if got := r.Faults(4); len(got) != 1 ||
		got[0].Source != string(model.SourceTunnel) {
		t.Fatalf("the tunnel's errors are not counted: %#v", got)
	}
}

func TestAnEmptyTallyReportsNothingRatherThanAnEmptyLine(t *testing.T) {
	r := NewLogRing(50)
	if got := r.Faults(4); got != nil {
		t.Fatalf("want nil, got %#v", got)
	}
}
