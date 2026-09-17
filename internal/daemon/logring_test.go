package daemon

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"xwrt/internal/model"
)

// newTestRing builds a ring with no syslog sink, so tests never depend on
// /dev/log existing.
func newTestRing(size int) *LogRing {
	r := NewLogRing(size)
	r.sink = nil
	return r
}

func TestEntriesFilterByLevel(t *testing.T) {
	r := newTestRing(50)
	r.Debugf("a debug line")
	r.Infof("an info line")
	r.Warnf("a warning")
	r.Fail(Fault{Source: model.SourceDaemon, Step: model.StepCore, Err: errors.New("boom")})

	all := r.Entries(Query{})
	if len(all) != 4 {
		t.Fatalf("want 4 entries unfiltered, got %d", len(all))
	}

	// A filter asks for a minimum severity, so "warning" must include errors.
	warn := r.Entries(Query{MinLevel: model.LevelWarn})
	if len(warn) != 2 {
		t.Fatalf("want 2 entries at warning and above, got %d: %v", len(warn), warn)
	}
	only := r.Entries(Query{MinLevel: model.LevelError})
	if len(only) != 1 || only[0].Message != "boom" {
		t.Fatalf("want just the error, got %v", only)
	}
}

func TestEntriesFilterBySourceAndStep(t *testing.T) {
	r := newTestRing(50)
	r.Step(model.StepFirewall).Infof("installing rules")
	r.Step(model.StepCore).Infof("starting core")
	r.AddProcessLine(model.SourceCore, "2026/09/12 [Info] core started")

	if got := r.Entries(Query{Step: model.StepFirewall}); len(got) != 1 {
		t.Fatalf("want 1 firewall entry, got %d", len(got))
	}
	core := r.Entries(Query{Source: model.SourceCore})
	if len(core) != 1 || !strings.Contains(core[0].Message, "core started") {
		t.Fatalf("want the core's own line, got %v", core)
	}
	// A step logger must actually tag its entries, or filtering by step is a
	// filter over nothing.
	fw := r.Entries(Query{Step: model.StepFirewall})
	if fw[0].Step != model.StepFirewall || fw[0].Source != model.SourceDaemon {
		t.Fatalf("step context lost: %+v", fw[0])
	}
}

func TestEntriesLimitKeepsTheNewest(t *testing.T) {
	r := newTestRing(50)
	for i := 0; i < 10; i++ {
		r.Infof("line %d", i)
	}
	got := r.Entries(Query{Limit: 3})
	if len(got) != 3 {
		t.Fatalf("want 3, got %d", len(got))
	}
	if got[2].Message != "line 9" {
		t.Fatalf("want the newest line last, got %q", got[2].Message)
	}
}

func TestRingWrapsWithoutEmptyEntries(t *testing.T) {
	r := newTestRing(4)
	for i := 0; i < 9; i++ {
		r.Infof("line %d", i)
	}
	got := r.Entries(Query{})
	if len(got) != 4 {
		t.Fatalf("want the ring size, got %d", len(got))
	}
	if got[0].Message != "line 5" || got[3].Message != "line 8" {
		t.Fatalf("wrong window after wrapping: %q .. %q", got[0].Message, got[3].Message)
	}
}

// The error journal exists so that a failure outlives the flood of ordinary
// lines that follows it. That is the property worth testing.
func TestErrorJournalSurvivesRingTurnover(t *testing.T) {
	r := newTestRing(5)
	r.Fail(Fault{Source: model.SourceCore, Step: model.StepCore, Err: errors.New("core would not start"), Detail: "listen tcp 127.0.0.1:10808: address already in use", Hint: "change socks_port"})

	for i := 0; i < 50; i++ {
		r.Infof("chatter %d", i)
	}

	if got := r.Entries(Query{MinLevel: model.LevelError}); len(got) != 0 {
		t.Fatalf("the main ring should have scrolled past the error, got %v", got)
	}
	errs := r.Errors(0)
	if len(errs) != 1 {
		t.Fatalf("want the failure still in the journal, got %d", len(errs))
	}
	if errs[0].Detail == "" || errs[0].Hint == "" {
		t.Fatalf("the journal must keep detail and hint: %+v", errs[0])
	}
	if errs[0].Step != model.StepCore || errs[0].Source != model.SourceCore {
		t.Fatalf("attribution lost: %+v", errs[0])
	}
}

func TestFailWritesDetailIntoTheLog(t *testing.T) {
	r := newTestRing(50)
	r.Fail(Fault{Source: model.SourceCore, Step: model.StepConfig, Err: errors.New("rejected"), Detail: "line one\nline two", Hint: "fix the thing"})

	got := r.Entries(Query{})
	var joined []string
	for _, e := range got {
		joined = append(joined, e.Message)
	}
	all := strings.Join(joined, "\n")
	for _, want := range []string{"rejected", "line one", "line two", "fix the thing"} {
		if !strings.Contains(all, want) {
			t.Fatalf("log is missing %q:\n%s", want, all)
		}
	}
	// The hint is advice, not a second failure, so it must not be logged at
	// error level — otherwise every failure counts twice.
	for _, e := range got {
		if strings.Contains(e.Message, "fix the thing") && e.Level != model.LevelWarn {
			t.Fatalf("hint logged at %s, want warning", e.Level)
		}
	}
}

func TestLastErrorLifecycle(t *testing.T) {
	r := newTestRing(50)
	if r.LastError() != nil {
		t.Fatal("a fresh ring has no last error")
	}
	r.Fail(Fault{Source: model.SourceDaemon, Step: model.StepDNS, Err: errors.New("dnsmasq restart failed"), Hint: "switch dns_mode to redirect"})

	last := r.LastError()
	if last == nil || last.Step != model.StepDNS || last.Hint == "" {
		t.Fatalf("unexpected last error: %+v", last)
	}
	// The caller gets a copy: mutating it must not corrupt the ring's state.
	last.Message = "mutated"
	if again := r.LastError(); again.Message == "mutated" {
		t.Fatal("LastError handed out its internal pointer")
	}

	r.ClearLastError()
	if r.LastError() != nil {
		t.Fatal("clearing should dismiss the banner")
	}
	if len(r.Errors(0)) != 1 {
		t.Fatal("clearing the banner must not empty the journal")
	}
}

func TestFailIgnoresNil(t *testing.T) {
	r := newTestRing(10)
	r.Fail(Fault{Source: model.SourceDaemon, Step: model.StepCore, Err: nil, Detail: "detail", Hint: "hint"})
	if len(r.Errors(0)) != 0 || len(r.Entries(Query{})) != 0 {
		t.Fatal("a nil error must record nothing")
	}
}

func TestClassifyReadsTheCoresOwnLevel(t *testing.T) {
	cases := []struct {
		line string
		want model.Level
	}{
		{"2026/09/12 10:00:00 [Error] failed to start: something", model.LevelError},
		{"2026/09/12 10:00:00 [Warning] unable to connect", model.LevelWarn},
		{"2026/09/12 10:00:00 [Info] connection established", model.LevelInfo},
		{"2026/09/12 10:00:00 [Debug] handshake", model.LevelDebug},
		// No bracketed level: fall back to the words.
		{"cannot open /dev/net/tun", model.LevelError},
		{"tunnel up on xwrt0", model.LevelInfo},
		// An explicit level wins over a word appearing elsewhere in the line,
		// which is why the bracket cases are checked first.
		{"2026/09/12 [Info] retrying after a failed probe", model.LevelInfo},
	}
	for _, c := range cases {
		if got := classify(c.line); got != c.want {
			t.Errorf("classify(%q) = %s, want %s", c.line, got, c.want)
		}
	}
}

func TestAddProcessLineSkipsBlanks(t *testing.T) {
	r := newTestRing(10)
	r.AddProcessLine(model.SourceCore, "   ")
	r.AddProcessLine(model.SourceCore, "")
	if got := r.Entries(Query{}); len(got) != 0 {
		t.Fatalf("blank process output should be dropped, got %v", got)
	}
}

func TestSubscribeDoesNotBlockOnASlowReader(t *testing.T) {
	r := newTestRing(10)
	_, cancel := r.Subscribe()
	defer cancel()
	// Far more than the channel buffer; a blocked send here would hang the
	// process output pump, which must never happen.
	for i := 0; i < 500; i++ {
		r.Infof("line %d", i)
	}
}

// A crash loop must not be able to push everything else out of the journal, and
// the repeat count is the interesting part of the second occurrence anyway.
func TestRepeatedFailuresCollapse(t *testing.T) {
	r := newTestRing(500)
	for i := 0; i < 5; i++ {
		r.Fail(Fault{Source: model.SourceCore, Step: model.StepCore, Err: errors.New("core exited: status 23"), Detail: "Failed to start: something", Hint: "check the config"})
	}
	errs := r.Errors(0)
	if len(errs) != 1 {
		t.Fatalf("want one collapsed entry, got %d", len(errs))
	}
	if errs[0].Repeats != 4 {
		t.Fatalf("want 4 repeats, got %d", errs[0].Repeats)
	}
	// Only the first occurrence is written out in full; the rest would be
	// identical lines.
	full := r.Entries(Query{MinLevel: model.LevelError})
	if len(full) != 2 { // message + one detail line
		t.Fatalf("repeats leaked into the log: %v", full)
	}
	if r.LastError() == nil {
		t.Fatal("the banner should still show the failure")
	}
}

func TestADifferentFailureStartsANewEntry(t *testing.T) {
	r := newTestRing(500)
	r.Fail(Fault{Source: model.SourceCore, Step: model.StepCore, Err: errors.New("first")})
	r.Fail(Fault{Source: model.SourceCore, Step: model.StepCore, Err: errors.New("first")})
	r.Fail(Fault{Source: model.SourceDaemon, Step: model.StepFirewall, Err: errors.New("second")})
	r.Fail(Fault{Source: model.SourceCore, Step: model.StepCore, Err: errors.New("first")})

	errs := r.Errors(0)
	if len(errs) != 3 {
		t.Fatalf("want three entries, got %d: %+v", len(errs), errs)
	}
	if errs[0].Repeats != 1 {
		t.Fatalf("first entry should have collapsed one repeat, got %d", errs[0].Repeats)
	}
	if errs[2].Message != "first" || errs[2].Repeats != 0 {
		t.Fatalf("a failure recurring after another one is a new entry: %+v", errs[2])
	}
}

// The journal keeps its newest entries when it fills, the same way the main
// ring does; losing the recent ones would defeat the point of having it.
func TestErrorJournalWrapsKeepingTheNewest(t *testing.T) {
	r := newTestRing(10)
	total := len(r.errors) + 20
	for i := 0; i < total; i++ {
		r.Fail(Fault{Source: model.SourceDaemon, Step: model.StepNone, Err: fmt.Errorf("failure %d", i)})
	}
	errs := r.Errors(0)
	if len(errs) != len(r.errors) {
		t.Fatalf("want a full journal of %d, got %d", len(r.errors), len(errs))
	}
	if errs[len(errs)-1].Message != fmt.Sprintf("failure %d", total-1) {
		t.Fatalf("newest entry is %q", errs[len(errs)-1].Message)
	}
	if errs[0].Message != fmt.Sprintf("failure %d", total-len(r.errors)) {
		t.Fatalf("oldest kept entry is %q", errs[0].Message)
	}
}
