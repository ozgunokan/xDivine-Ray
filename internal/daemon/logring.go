package daemon

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"xwrt/internal/model"
)

// LogRing is the daemon's log buffer.
//
// Two rings, not one. The main ring holds everything and churns quickly,
// because a core at info level produces a line per connection. The error
// journal holds only failures and churns slowly, so a failure from twenty
// minutes ago is still there after the main ring has turned over several
// times — which is exactly the case someone reading a log is usually in.
//
// Both live in RAM. Routers run from flash and a log written to it wears the
// device out; durability comes from mirroring the important entries into the
// system log instead, where `logread` finds them and where they survive a
// restart of this daemon.
type LogRing struct {
	mu      sync.RWMutex
	entries []model.LogEntry
	next    int
	full    bool

	errors    []model.ErrorEntry
	errNext   int
	errFull   bool
	lastError *model.LastError

	subs map[int]chan model.LogEntry
	seq  int

	sink *syslogSink
}

// NewLogRing returns a ring holding size entries and a journal of the last 100
// errors.
func NewLogRing(size int) *LogRing {
	if size <= 0 {
		size = 500
	}
	return &LogRing{
		entries: make([]model.LogEntry, size),
		errors:  make([]model.ErrorEntry, 100),
		subs:    map[int]chan model.LogEntry{},
		sink:    newSyslogSink("xwrt"),
	}
}

// Close releases the system log connection.
func (r *LogRing) Close() {
	if r.sink != nil {
		r.sink.Close()
	}
}

// --- writing ------------------------------------------------------------

// Debugf records a daemon debug message.
func (r *LogRing) Debugf(format string, args ...any) {
	r.emit(model.LevelDebug, model.SourceDaemon, model.StepNone, sprintf(format, args...))
}

// Infof records a daemon message.
func (r *LogRing) Infof(format string, args ...any) {
	r.emit(model.LevelInfo, model.SourceDaemon, model.StepNone, sprintf(format, args...))
}

// Warnf records a daemon warning: something is not right but the operation
// continued.
func (r *LogRing) Warnf(format string, args ...any) {
	r.emit(model.LevelWarn, model.SourceDaemon, model.StepNone, sprintf(format, args...))
}

// Step returns a logger bound to one stage of an operation, so every message
// it records says where in the sequence it came from.
func (r *LogRing) Step(step model.Step) *StepLog {
	return &StepLog{ring: r, step: step}
}

// StepLog is a logger carrying step context. It has no Fail: a failure always
// travels back to the connect sequence as an error, which files it once with
// its step, detail and hint — logging one here as well is how the same problem
// ends up in the log twice.
type StepLog struct {
	ring *LogRing
	step model.Step
}

func (s *StepLog) Infof(format string, args ...any) {
	s.ring.emit(model.LevelInfo, model.SourceDaemon, s.step, sprintf(format, args...))
}

func (s *StepLog) Warnf(format string, args ...any) {
	s.ring.emit(model.LevelWarn, model.SourceDaemon, s.step, sprintf(format, args...))
}

// Fault is a failure ready to be filed: what went wrong, why, what to do about
// it, and the keys that let the interface say all of that in another language.
type Fault struct {
	Source model.Source
	Step   model.Step
	Err    error
	Detail string
	Hint   string

	Code     string
	Args     []string
	HintCode string
	HintArgs []string
}

// Fail records a failure in both the log and the error journal.
//
// The journal entry is what makes a failure findable afterwards, and the
// detail field is what makes it explicable: for a core that would not start,
// the cause is in its own last lines, not in our message about it.
//
// The log line itself is always English. A log is read by whoever is helping,
// which is not always the person in front of the router, and a message that
// can be searched for is worth more there than one in the reader's language.
// The translation happens where a person is actually looking: the interface.
func (r *LogRing) Fail(f Fault) {
	if f.Err == nil {
		return
	}
	source, step := f.Source, f.Step
	msg := f.Err.Error()
	detail, hint := f.Detail, f.Hint

	if count := r.fileError(source, step, msg, detail, hint, f); count > 1 {
		// The same failure again: already collapsed into the journal entry, so
		// the log says so periodically rather than repeating itself. A crash
		// loop should be one line every so often, not the only thing in the log.
		if count%10 == 0 {
			r.emit(model.LevelWarn, source, step,
				fmt.Sprintf("%s (%d times now)", msg, count))
		}
		return
	}

	r.emit(model.LevelError, source, step, msg)
	if detail != "" {
		// Indented so a multi-line detail reads as belonging to the error
		// above it rather than as separate events.
		for _, line := range strings.Split(strings.TrimRight(detail, "\n"), "\n") {
			r.emit(model.LevelError, source, step, "    "+line)
		}
	}
	if hint != "" {
		r.emit(model.LevelWarn, source, step, hint)
	}
}

// fileError records the failure in the journal and returns how many times this
// same failure has now been seen in a row. A repeat updates the existing entry
// instead of adding another, so a crash loop cannot push everything else out of
// a hundred-entry journal.
func (r *LogRing) fileError(source model.Source, step model.Step, msg, detail, hint string, f Fault) int {
	now := time.Now().Format(time.RFC3339)

	r.mu.Lock()
	defer r.mu.Unlock()

	if idx, ok := r.newestErrorLocked(); ok {
		prev := &r.errors[idx]
		if prev.Source == source && prev.Step == step && prev.Message == msg {
			prev.Repeats++
			prev.Time = now
			if detail != "" {
				// Keep the latest detail: on a repeat it is the most recent
				// output, which is the one worth reading.
				prev.Detail = strings.TrimRight(detail, "\n")
			}
			r.lastError = &model.LastError{
				Time: now, Step: step, Message: msg, Hint: prev.Hint,
				Code: prev.Code, Args: prev.Args,
				HintCode: prev.HintCode, HintArgs: prev.HintArgs,
			}
			return prev.Repeats + 1
		}
	}

	r.errors[r.errNext] = model.ErrorEntry{
		Time:     now,
		Source:   source,
		Step:     step,
		Message:  msg,
		Detail:   strings.TrimRight(detail, "\n"),
		Hint:     hint,
		Code:     f.Code,
		Args:     f.Args,
		HintCode: f.HintCode,
		HintArgs: f.HintArgs,
	}
	r.errNext = (r.errNext + 1) % len(r.errors)
	if r.errNext == 0 {
		r.errFull = true
	}
	r.lastError = &model.LastError{
		Time: now, Step: step, Message: msg, Hint: hint,
		Code: f.Code, Args: f.Args,
		HintCode: f.HintCode, HintArgs: f.HintArgs,
	}
	return 1
}

// newestErrorLocked returns the index of the most recently filed error.
func (r *LogRing) newestErrorLocked() (int, bool) {
	if r.errNext == 0 && !r.errFull {
		return 0, false
	}
	idx := (r.errNext - 1 + len(r.errors)) % len(r.errors)
	if r.errors[idx].Time == "" {
		return 0, false
	}
	return idx, true
}

// AddProcessLine records a line a child process wrote. The level is inferred,
// because the core's format is its own; only our own messages carry an
// explicit level.
func (r *LogRing) AddProcessLine(source model.Source, line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	r.emit(classify(line), source, model.StepNone, line)
}

func (r *LogRing) emit(level model.Level, source model.Source, step model.Step, msg string) {
	e := model.LogEntry{
		Time:    time.Now().Format(time.RFC3339),
		Level:   level,
		Source:  source,
		Step:    step,
		Message: msg,
	}

	r.mu.Lock()
	r.entries[r.next] = e
	r.next = (r.next + 1) % len(r.entries)
	if r.next == 0 {
		r.full = true
	}
	subs := make([]chan model.LogEntry, 0, len(r.subs))
	for _, ch := range r.subs {
		subs = append(subs, ch)
	}
	r.mu.Unlock()

	for _, ch := range subs {
		// Never block a process output pump on a slow reader.
		select {
		case ch <- e:
		default:
		}
	}

	// Warnings and errors are mirrored to the system log so they outlive this
	// process. Info and debug are not: they would flood logd on a busy router
	// and they are the entries least likely to be wanted after a restart.
	if r.sink != nil && level.AtLeast(model.LevelWarn) {
		prefix := string(source)
		if step != model.StepNone {
			prefix += "/" + string(step)
		}
		r.sink.Write(level, prefix+": "+msg)
	}
}

// --- reading ------------------------------------------------------------

// Query filters the log.
type Query struct {
	// MinLevel drops anything less severe. The zero value keeps everything.
	MinLevel model.Level
	// Source, when set, keeps only entries from that component.
	Source model.Source
	// Step, when set, keeps only entries from that stage.
	Step model.Step
	// Limit caps the result to the most recent matches.
	Limit int
}

// Entries returns matching entries, oldest first.
func (r *LogRing) Entries(q Query) []model.LogEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()

	all := r.orderedLocked()
	out := make([]model.LogEntry, 0, len(all))
	for _, e := range all {
		if e.Time == "" {
			continue
		}
		if q.MinLevel != "" && !e.Level.AtLeast(q.MinLevel) {
			continue
		}
		if q.Source != "" && e.Source != q.Source {
			continue
		}
		if q.Step != "" && e.Step != q.Step {
			continue
		}
		out = append(out, e)
	}
	if q.Limit > 0 && len(out) > q.Limit {
		out = out[len(out)-q.Limit:]
	}
	return out
}

func (r *LogRing) orderedLocked() []model.LogEntry {
	out := make([]model.LogEntry, 0, len(r.entries))
	if r.full {
		out = append(out, r.entries[r.next:]...)
		out = append(out, r.entries[:r.next]...)
	} else {
		out = append(out, r.entries[:r.next]...)
	}
	return out
}

// Errors returns the error journal, most recent last.
func (r *LogRing) Errors(limit int) []model.ErrorEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]model.ErrorEntry, 0, len(r.errors))
	if r.errFull {
		out = append(out, r.errors[r.errNext:]...)
		out = append(out, r.errors[:r.errNext]...)
	} else {
		out = append(out, r.errors[:r.errNext]...)
	}
	// Skip unused slots.
	filtered := out[:0]
	for _, e := range out {
		if e.Time != "" {
			filtered = append(filtered, e)
		}
	}
	if limit > 0 && len(filtered) > limit {
		filtered = filtered[len(filtered)-limit:]
	}
	return filtered
}

// LastError returns the most recent failure, or nil.
func (r *LogRing) LastError() *model.LastError {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.lastError == nil {
		return nil
	}
	copied := *r.lastError
	return &copied
}

// ClearLastError forgets the most recent failure, which a successful connect
// does so the UI stops showing a stale banner. The journal keeps its copy.
func (r *LogRing) ClearLastError() {
	r.mu.Lock()
	r.lastError = nil
	r.mu.Unlock()
}

// ClearErrors empties the error journal and the banner. Someone who has read
// and dealt with a set of failures wants them gone; keeping them would make the
// next real failure harder to spot, which is the opposite of the point.
func (r *LogRing) ClearErrors() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	cleared := 0
	for i := range r.errors {
		if r.errors[i].Time != "" {
			cleared++
		}
		r.errors[i] = model.ErrorEntry{}
	}
	r.errNext = 0
	r.errFull = false
	r.lastError = nil
	return cleared
}

// Clear empties the message stream as well. The journal is cleared with it:
// leaving failures behind whose surrounding context is gone would be worse than
// starting clean.
//
// It returns how many lines were removed, which is the only proof the caller
// can show: the core writes a line per connection, so a stream that was just
// cleared can look untouched a second later.
func (r *LogRing) Clear() int {
	r.ClearErrors()
	r.mu.Lock()
	defer r.mu.Unlock()

	cleared := 0
	for i := range r.entries {
		if r.entries[i].Time != "" {
			cleared++
		}
		r.entries[i] = model.LogEntry{}
	}
	r.next = 0
	r.full = false
	return cleared
}

// Subscribe returns a channel of new entries and a cancel function.
func (r *LogRing) Subscribe() (<-chan model.LogEntry, func()) {
	ch := make(chan model.LogEntry, 64)
	r.mu.Lock()
	id := r.seq
	r.seq++
	r.subs[id] = ch
	r.mu.Unlock()

	return ch, func() {
		r.mu.Lock()
		if c, ok := r.subs[id]; ok {
			delete(r.subs, id)
			close(c)
		}
		r.mu.Unlock()
	}
}

// classify guesses the severity of a line written by a child process. The
// core brackets its level, which is the reliable signal; the word-based
// fallbacks are ordered so a line carrying an explicit level is never
// reclassified by a word appearing elsewhere in it.
func classify(line string) model.Level {
	l := strings.ToLower(line)
	switch {
	case strings.Contains(l, "[error]"), strings.Contains(l, "[fatal]"):
		return model.LevelError
	case strings.Contains(l, "[warning]"), strings.Contains(l, "[warn]"):
		return model.LevelWarn
	case strings.Contains(l, "[info]"):
		return model.LevelInfo
	case strings.Contains(l, "[debug]"):
		return model.LevelDebug
	case strings.Contains(l, "failed"), strings.Contains(l, "error:"),
		strings.Contains(l, "cannot "), strings.Contains(l, "refused"):
		return model.LevelError
	default:
		return model.LevelInfo
	}
}

func sprintf(format string, args ...any) string {
	if len(args) == 0 {
		return format
	}
	return fmt.Sprintf(format, args...)
}
