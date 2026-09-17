package selftest

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// Every verdict this package can return has a sentence in the interface.
//
// The daemon sends a code and the numbers; the wording lives in xwrt.js, with
// the rest of the interface's language. That split is right — it is what lets
// the page speak Turkish — but it means a verdict added on this side and not
// on that one falls through to `default`, where the page prints the daemon's
// English sentence instead. Nothing fails, nothing logs, and the only symptom
// is one line of English in the middle of a Turkish page, which is exactly the
// kind of thing that ships.
//
// So the two lists are compared. This is the same guard the failure catalog
// has, for the same reason.
const viewPath = "../../luci-app-xwrt/htdocs/luci-static/resources/xwrt.js"

func TestEveryVerdictHasASentenceInTheInterface(t *testing.T) {
	blob, err := os.ReadFile(viewPath)
	if err != nil {
		t.Skipf("the interface is not in this checkout: %v", err)
	}
	view := string(blob)

	// The codes as the interface knows them: `case 'loss-beyond':` inside
	// verdictText. Reading the file rather than keeping a second list here is
	// the whole point — a list kept by hand drifts exactly like the thing it
	// was meant to catch.
	start := strings.Index(view, "verdictText:")
	if start < 0 {
		t.Fatalf("verdictText is not in %s any more; this check needs rewriting",
			viewPath)
	}
	body := view[start:]
	if end := strings.Index(body, "\n\t},"); end > 0 {
		body = body[:end]
	}
	inView := map[string]bool{}
	for _, m := range regexp.MustCompile(`case '([a-z-]+)':`).FindAllStringSubmatch(body, -1) {
		inView[m[1]] = true
	}
	if len(inView) == 0 {
		t.Fatal("no verdict cases found in the interface; this check is not working")
	}

	// And the codes this package can actually produce.
	mine := []string{
		VerdictUnreachable, VerdictNoBaseline, VerdictFailures,
		VerdictLossBeyond, VerdictLoss, VerdictOverhead, VerdictHealthy,
		VerdictTunnelSteady, VerdictTunnelJitter, VerdictTunnelFailures,
	}
	for _, code := range mine {
		if !inView[code] {
			t.Errorf("the self-test can return %q and the interface has no "+
				"sentence for it: the page would print the daemon's English",
				code)
		}
		delete(inView, code)
	}
	for code := range inView {
		t.Errorf("the interface has a sentence for %q, which nothing returns "+
			"any more", code)
	}
}

// And the one that prompted the check: with the router's own traffic proxied,
// a run in which connections failed must say so. It used to be folded into the
// jitter reading, whose sentence mentions only the median and the maximum — so
// a run where one connection in ten never completed was reported as "usually
// 186 ms", which is true about the nine that worked and silent about the one
// that did not.
func TestFailuresAreNotHiddenBehindAMedian(t *testing.T) {
	r := &Report{
		SkipCode: SkipProxyRouter,
		Tunnel: &Stat{
			Attempts: 10, Failures: 1,
			Samples: []float64{136, 186, 206},
			Median:  186, Min: 136, Max: 206,
		},
	}
	code, text := verdict(r)
	if code != VerdictTunnelFailures {
		t.Errorf("a run with a failed connection is reported as %q", code)
	}
	if !strings.Contains(text, "failed") {
		t.Errorf("the sentence does not mention the failure: %q", text)
	}
	if !strings.Contains(text, "1 of 10") {
		t.Errorf("the sentence does not say how many failed: %q", text)
	}

	// With nothing failing, the steady reading is unchanged.
	r.Tunnel.Failures = 0
	if code, _ := verdict(r); code != VerdictTunnelSteady {
		t.Errorf("a clean steady run now reports %q", code)
	}
}
