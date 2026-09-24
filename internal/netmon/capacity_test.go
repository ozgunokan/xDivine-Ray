package netmon

import (
	"os"
	"path/filepath"
	"testing"
)

// The connection table's fullness.
//
// This number exists because of a report that had no other explanation: TikTok
// plays fine in TUN mode and, in mixed mode, plays for a while and then stalls
// for a few seconds at a time, over and over, recovering by itself each time.
// That is the shape of a fixed-size table filling up — new connections dropped
// until old entries age out — and nothing in this project could see it. The
// kernel's own complaint goes to dmesg.

func files(t *testing.T, count, max string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	c := filepath.Join(dir, "count")
	m := filepath.Join(dir, "max")
	if count != "" {
		if err := os.WriteFile(c, []byte(count), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if max != "" {
		if err := os.WriteFile(m, []byte(max), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return c, m
}

func TestAnAlmostEmptyTableIsNotReported(t *testing.T) {
	c, m := files(t, "1200\n", "16384\n")
	got := readCapacityFrom(c, m)
	if !got.Known {
		t.Fatal("the numbers were there and were not read")
	}
	if got.Percent != 7 {
		t.Errorf("percent = %d, want 7", got.Percent)
	}
	if got.Tight() {
		t.Error("a table at 7% was called tight, which would put a warning " +
			"in the log of every healthy device")
	}
}

func TestAFillingTableIsReportedBeforeItIsFull(t *testing.T) {
	// At a hundred per cent the dropping has been going on for a while
	// already, and a warning that arrives only at the moment of failure is one
	// nobody can act on.
	c, m := files(t, "13200\n", "16384\n")
	got := readCapacityFrom(c, m)
	if got.Percent != 80 {
		t.Fatalf("percent = %d, want 80", got.Percent)
	}
	if !got.Tight() {
		t.Error("a table at 80% is not reported; by the time it is full the " +
			"connections have been failing for minutes")
	}
}

func TestAFullTableIsTight(t *testing.T) {
	c, m := files(t, "16384\n", "16384\n")
	if got := readCapacityFrom(c, m); !got.Tight() || got.Percent != 100 {
		t.Errorf("a full table reported %+v", got)
	}
}

func TestAKernelThatDoesNotPublishThemIsNotAFault(t *testing.T) {
	// Some kernels do not expose these. That is not a problem to report; it
	// only means the number is unavailable, and inventing a zero would put
	// "0 of 0" on the screen.
	c, m := files(t, "", "")
	got := readCapacityFrom(c, m)
	if got.Known {
		t.Errorf("missing files produced a known capacity: %+v", got)
	}
	if got.Tight() {
		t.Error("an unknown capacity was called tight, which would warn every " +
			"device that does not publish the numbers")
	}
}

func TestNonsenseIsNotRead(t *testing.T) {
	for _, bad := range []struct{ count, max string }{
		{"not a number\n", "16384\n"},
		{"1200\n", "zero\n"},
		{"1200\n", "0\n"}, // a zero limit would divide by zero
	} {
		c, m := files(t, bad.count, bad.max)
		if got := readCapacityFrom(c, m); got.Known {
			t.Errorf("count=%q max=%q was read as %+v", bad.count, bad.max, got)
		}
	}
}
