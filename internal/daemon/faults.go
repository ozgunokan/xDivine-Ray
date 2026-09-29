package daemon

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"xwrt/internal/model"
)

// Counting the errors the core repeats.
//
// The log ring holds two thousand entries. A core at info level writes a line
// per connection, so on a busy router two thousand entries is a few minutes —
// and the error somebody is hunting is usually the one that happened twenty
// minutes ago, four hundred times, and has long since been pushed out by the
// traffic it was failing to carry. Chasing one of those from the log means
// catching it live, which means watching a screen and hoping.
//
// That is the wrong shape for the question being asked. The question is almost
// never "what happened at 21:04:16" — it is "is this happening, and how often".
// A counter answers it and costs nothing to keep: the same fault arriving four
// hundred times is one entry that says four hundred, still there an hour later
// when somebody finally comes to look.
//
// What makes it work is deciding when two errors are the same error, which they
// never are literally: every one of them carries a different connection id, a
// different source port, a different timestamp. So the message is reduced to
// its shape first — the words the core chose, with the particulars of this one
// occurrence taken out — and the shape is what is counted.

var (
	// A core line arrives with its own timestamp and level marker, both of
	// which belong to the occurrence rather than to the fault.
	reLogTime = regexp.MustCompile(`^\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2}(\.\d+)?\s*`)
	reLogTag  = regexp.MustCompile(`\[(Error|Fatal|Warning|Warn|Info|Debug)\]\s*`)
	// The bracketed connection id, and then addresses and ports: the three
	// things that are different on every single occurrence of the same fault.
	// The port matters as much as the id — half these lines carry the client's
	// source port, which is new every time.
	reConnID = regexp.MustCompile(`\[\d+\]\s*`)
	reIPv4   = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)
	rePort   = regexp.MustCompile(`:\d{2,5}\b`)
	reSpace  = regexp.MustCompile(`\s+`)
)

// faultKey reduces one log line to the fault it is an instance of.
//
// Deliberately blunt. It cannot know which numbers in a message are incidental,
// so it treats all of the long ones as incidental — a fault that differs only
// in a large number is a fault this will merge, and merging two faults that
// happen to read alike costs a count being too high, while splitting one fault
// into four hundred costs the whole feature.
func faultKey(line string) string {
	s := reLogTime.ReplaceAllString(strings.TrimSpace(line), "")
	s = reLogTag.ReplaceAllString(s, "")
	s = reConnID.ReplaceAllString(s, "")
	s = reIPv4.ReplaceAllString(s, "#")
	s = rePort.ReplaceAllString(s, ":#")
	s = reSpace.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

// faultEntry is one counted fault.
//
// recent holds the times of the last few occurrences, which is what makes the
// difference between the two things that look identical in a total: ten
// failures in four minutes, and ten failures spread across a night. The first
// is a link that has gone; the second is a mobile line being a mobile line.
type faultEntry struct {
	model.CoreFault
	firstAt time.Time
	recent  []time.Time
	bursts  int
}

// faultTally counts distinct faults and remembers when each was last seen.
type faultTally struct {
	mu    sync.Mutex
	items map[string]*faultEntry
	limit int
	// missed counts faults that arrived after the table was full. A table that
	// quietly stops recording is worse than one that says it stopped.
	missed int
}

func newFaultTally(limit int) *faultTally {
	if limit <= 0 {
		limit = 40
	}
	return &faultTally{items: map[string]*faultEntry{}, limit: limit}
}

// seen is what one occurrence amounted to.
type seen struct {
	// Nth is 1 for the first of its kind and counts up from there. Zero means
	// the fault is not being counted at all, because the table was full.
	Nth int
	// Burst is true when this occurrence completed a run fast enough to mean
	// something: burstSize of the same fault inside burstWindow. It is the
	// only thing that puts a line in the log.
	Burst bool
	// Span is how long that run took, and goes into the line that reports it.
	Span time.Duration
}

// add records one occurrence and says what it amounted to.
func (t *faultTally) add(source model.Source, line string, now time.Time) seen {
	key := faultKey(line)
	if key == "" {
		return seen{}
	}
	stamp := now.Format(time.RFC3339)

	t.mu.Lock()
	defer t.mu.Unlock()

	f, ok := t.items[key]
	if !ok {
		if len(t.items) >= t.limit {
			t.missed++
			return seen{}
		}
		f = &faultEntry{
			CoreFault: model.CoreFault{
				Source:  string(source),
				Message: key,
				Count:   0,
				First:   stamp,
			},
			firstAt: now,
			recent:  make([]time.Time, 0, burstSize),
		}
		t.items[key] = f
	}
	f.Count++
	f.Last = stamp

	// Only the last burstSize occurrences are kept. Whether they all happened
	// inside burstWindow is the entire question, and older ones cannot change
	// the answer.
	f.recent = append(f.recent, now)
	if len(f.recent) > burstSize {
		f.recent = f.recent[1:]
	}
	if len(f.recent) < burstSize || now.Sub(f.recent[0]) > burstWindow {
		return seen{Nth: f.Count}
	}

	span := now.Sub(f.recent[0])
	// Start the run again rather than reporting on every occurrence after the
	// tenth. A link that stays down reports once per burst — roughly once every
	// few minutes while it is bad — instead of once a second.
	f.recent = f.recent[:0]
	f.bursts++
	return seen{Nth: f.Count, Burst: true, Span: span}
}

// top returns the faults worth showing, worst first.
//
// Only faults that have burst at least once. The status page and the log agree
// on what counts as news, or the page becomes the place the noise moved to:
// two dial failures in a night would put a red panel on the front screen of a
// device that is working perfectly well.
//
// The count reported for a fault that qualifies is its true total, every
// occurrence including the quiet ones. Once a fault has earned its line, the
// whole of it is worth knowing.
//
// Frequency rather than recency, because the one that matters on a device that
// is misbehaving is the one that keeps happening, and a page with room for four
// lines should spend them on that. Ties break on the most recent, so two faults
// that have each happened twice are ordered by which is still going.
func (t *faultTally) top(n int) []model.CoreFault {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.items) == 0 {
		return nil
	}
	out := make([]model.CoreFault, 0, len(t.items))
	for _, f := range t.items {
		if f.bursts == 0 {
			continue
		}
		out = append(out, f.CoreFault)
	}
	if len(out) == 0 {
		return nil
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		if out[i].Last != out[j].Last {
			return out[i].Last > out[j].Last
		}
		return out[i].Message < out[j].Message
	})
	if n > 0 && len(out) > n {
		out = out[:n]
	}
	return out
}

// reset empties the table. Called when the core is started, so that what the
// status page shows is always "since this connection came up" rather than an
// all-time total nobody can date.
func (t *faultTally) reset() {
	t.mu.Lock()
	t.items = map[string]*faultEntry{}
	t.missed = 0
	t.mu.Unlock()
}

// --- what is worth waking someone for ---------------------------------------
//
// A transport on a lossy line fails the same way over and over, and none of it
// means anything: the connection is retried, the retry works, nobody noticed.
// Other clients do exactly the same thing and are simply quieter about it. A
// log that reports each of those teaches its reader to stop reading it, and
// then the one failure that did matter goes past unread too.
//
// A rate is what separates the two. Ten of the same failure inside eight
// minutes is a link that has gone, and that is worth one line. Ten of the same
// failure spread across a night is weather, and is worth none — not even a
// summary at the end, because a summary of nothing is still something to read.
//
// So a fault is written when it arrives fast, and not otherwise. Nothing
// accumulates towards a report; a run that does not finish inside the window
// simply never becomes news.

const (
	burstSize   = 10
	burstWindow = 8 * time.Minute
)

// isWebsocketFault marks the one transport this filter applies to.
//
// Narrow on purpose, and narrower than the problem. The rate rule would be
// just as true of any transport — the odd failure retried and working is not
// news whatever carried it — but WebSocket is the one that produces this in
// bulk, because it waits for an HTTP answer before a connection counts as made
// and a lossy line turns that wait into a failure. The other transports fail
// rarely enough that their lines are worth reading, and the operator would
// rather read them.
//
// The match is on the core's package path rather than on the word, so a server
// named after the protocol is not mistaken for the protocol. If a future core
// renames that package the match stops working and these lines start appearing
// in full again — which is the right direction for a filter to fail in.
func isWebsocketFault(line string) bool {
	return strings.Contains(strings.ToLower(line), "internet/websocket")
}

// isFatal marks the one kind of line that is never judged by its rate.
//
// A fatal is the core saying it cannot continue — a config it will not accept,
// a port it cannot have, a file that is not there. It happens once, which under
// a rule that only reports what happens ten times in eight minutes means it
// would happen silently. Nothing about "this occurred only once" makes a fatal
// less worth seeing; the rule above exists for failures that are retried and
// work, and a fatal is the opposite of that.
func isFatal(line string) bool {
	return strings.Contains(strings.ToLower(line), "[fatal]")
}

// burstNote decides whether an occurrence is written, and what is added to it.
//
// The note goes on the end of the core's own line rather than on a line of its
// own, so that searching for the error still finds the one line that reports
// it.
func burstNote(s seen) (note string, ok bool) {
	// Nothing is counting this one, because the table is full — which takes
	// forty distinct faults and is itself a thing worth seeing. What cannot be
	// measured cannot be judged quiet, so it is written.
	if s.Nth == 0 {
		return "", true
	}
	if !s.Burst {
		return "", false
	}
	span := s.Span.Round(time.Second)
	if span < time.Second {
		span = time.Second
	}
	return fmt.Sprintf("  [×%d in %s]", burstSize, span), true
}
