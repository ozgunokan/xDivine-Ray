package proc

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// The tail is what turns "the core exited with status 23" into something
// diagnosable, so these tests check the two cases that matter: a short-lived
// process whose reason is in its output, and a chatty one whose early lines
// have to be dropped in favour of the recent ones.

func TestRecentOutputKeepsTheLastLines(t *testing.T) {
	p := &Process{
		Name: "sh",
		Path: "sh",
		Args: []string{"-c", "echo first; echo second; echo third 1>&2; exit 3"},
	}
	exited := make(chan error, 1)
	p.OnExit = func(err error, intentional bool) {
		select {
		case exited <- err:
		default:
		}
	}
	if err := p.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer p.Stop()

	select {
	case err := <-exited:
		if err == nil {
			t.Fatal("want a non-zero exit")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the process never exited")
	}

	lines := strings.Join(p.RecentOutput(15), "\n")
	for _, want := range []string{"first", "second", "third"} {
		if !strings.Contains(lines, want) {
			t.Fatalf("tail is missing %q, got:\n%s", want, lines)
		}
	}
}

func TestRecentOutputDropsTheOldestWhenFull(t *testing.T) {
	p := &Process{}
	for i := 0; i < tailSize*2; i++ {
		p.recordTail(strings.Repeat("x", 1) + " line " + itoa(i))
	}
	got := p.RecentOutput(0)
	if len(got) != tailSize {
		t.Fatalf("want %d lines, got %d", tailSize, len(got))
	}
	// Oldest first, and the oldest kept line is the one tailSize back.
	if !strings.HasSuffix(got[0], "line "+itoa(tailSize)) {
		t.Fatalf("wrong window: first kept line is %q", got[0])
	}
	if !strings.HasSuffix(got[len(got)-1], "line "+itoa(tailSize*2-1)) {
		t.Fatalf("wrong window: last line is %q", got[len(got)-1])
	}
	// A request for fewer lines takes them from the end, which is where the
	// reason for a failure is.
	if last := p.RecentOutput(2); len(last) != 2 ||
		!strings.HasSuffix(last[1], "line "+itoa(tailSize*2-1)) {
		t.Fatalf("RecentOutput(2) = %v", last)
	}
}

func TestRecentOutputSkipsBlankLines(t *testing.T) {
	p := &Process{}
	p.recordTail("real line")
	p.recordTail("")
	p.recordTail("   ")
	if got := p.RecentOutput(0); len(got) != 1 {
		t.Fatalf("want only the real line, got %v", got)
	}
}

func TestRecentOutputOnAFreshProcess(t *testing.T) {
	p := &Process{}
	if got := p.RecentOutput(10); got != nil {
		t.Fatalf("want nil before anything ran, got %v", got)
	}
}

// A stop must be distinguishable from a crash, or every disconnect files a
// spurious failure.
func TestOnExitReportsAnIntentionalStop(t *testing.T) {
	p := &Process{
		Name: "sleep",
		Path: "sh",
		Args: []string{"-c", "sleep 30"},
	}
	var mu sync.Mutex
	var calls []bool
	p.OnExit = func(err error, intentional bool) {
		mu.Lock()
		calls = append(calls, intentional)
		mu.Unlock()
	}
	if err := p.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	p.Stop()

	mu.Lock()
	defer mu.Unlock()
	if len(calls) == 0 {
		t.Fatal("OnExit was never called")
	}
	for i, intentional := range calls {
		if !intentional {
			t.Fatalf("call %d reported a crash for a deliberate stop", i)
		}
	}
}

func TestStartFailsFastOnAMissingBinary(t *testing.T) {
	p := &Process{Name: "nope", Path: "xwrt-no-such-binary-ever"}
	err := p.Start()
	if err == nil {
		t.Fatal("want an error rather than a silent crash loop")
	}
	if p.Running() {
		t.Fatal("a failed start must not leave the process marked running")
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// A child that spawned its own helpers must not be able to hold a disconnect
// open. The helper here keeps the inherited stderr pipe alive after its parent
// shell exits, which is exactly the shape that used to stall Stop for as long
// as the grandchild lived.
func TestStopKillsTheWholeProcessGroup(t *testing.T) {
	p := &Process{
		Name: "sh",
		Path: "sh",
		Args: []string{"-c", "sleep 60 & echo helper started; wait"},
	}
	if err := p.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	done := make(chan struct{})
	go func() {
		p.Stop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Stop did not return: a grandchild is still holding the pipes")
	}
	if p.Running() {
		t.Fatal("still marked running after Stop")
	}
}
