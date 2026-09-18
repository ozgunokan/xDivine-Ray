package daemon

// The catalogs have to agree with each other, and there is no way to notice
// that they have stopped agreeing by looking at either one.
//
// Three ways they can drift, all of them silent at runtime:
//
//   - a failure is raised with a code nobody put in the catalog, so the reader
//     gets the key itself instead of a sentence;
//   - a code has an English sentence but no Turkish one, so a Turkish
//     interface shows one English line in the middle of the page — and the
//     line it shows is the error, which is the worst one to lose;
//   - the two sentences disagree about how many values they interpolate, so
//     the translated one drops a port number or a file path and sends the
//     reader looking for something that was never named.
//
// None of these break a build, none produce a log line, and all of them are
// found immediately by comparing the two files.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// i18nPath is the file the interface translates from.
const i18nPath = "../../luci-app-xwrt/htdocs/luci-static/resources/xwrt/i18n.js"

// placeholders counts the values a format interpolates. %% is an escaped
// percent sign and takes nothing; %w is an error being wrapped, which the
// translation receives as text like any other value.
func placeholders(format string) int {
	n := 0
	for i := 0; i < len(format)-1; i++ {
		if format[i] != '%' {
			continue
		}
		switch format[i+1] {
		case '%':
			i++
		case 's', 'd', 'q', 'v', 'w':
			n++
			i++
		}
	}
	return n
}

var jsEntry = regexp.MustCompile(`'([a-z][a-z0-9_.]*)':\s*'((?:[^'\\]|\\.)*)'`)

// jsCatalog pulls one object literal out of the interface's catalog file. It
// parses rather than imports because the alternative — a shared JSON file
// loaded by both sides — would mean the interface fetching a second file over
// the network before it can show an error, which is exactly when the network
// is the thing that is broken.
func jsCatalog(t *testing.T, name string) map[string]string {
	t.Helper()
	blob, err := os.ReadFile(i18nPath)
	if err != nil {
		t.Fatalf("read %s: %v", filepath.Clean(i18nPath), err)
	}
	src := string(blob)
	start := strings.Index(src, "var "+name+" = {")
	if start < 0 {
		t.Fatalf("%s is not in %s any more; if it was renamed, this test has to "+
			"be told, because nothing else checks it", name, i18nPath)
	}
	end := strings.Index(src[start:], "\n};")
	if end < 0 {
		t.Fatalf("%s has no closing brace on its own line", name)
	}
	out := map[string]string{}
	for _, m := range jsEntry.FindAllStringSubmatch(src[start:start+end], -1) {
		out[m[1]] = strings.ReplaceAll(m[2], `\'`, `'`)
	}
	if len(out) == 0 {
		t.Fatalf("%s parsed as empty; the entry format probably changed", name)
	}
	return out
}

func TestEveryMessageIsTranslated(t *testing.T) {
	for _, c := range []struct {
		goCatalog map[string]string
		jsName    string
	}{
		{messages, "ERRORS_TR"},
		{hints, "HINTS_TR"},
	} {
		tr := jsCatalog(t, c.jsName)

		var missing, extra []string
		for code, en := range c.goCatalog {
			native, ok := tr[code]
			if !ok {
				missing = append(missing, code+"  →  "+en)
				continue
			}
			if got, want := placeholders(native), placeholders(en); got != want {
				t.Errorf("%s: the two sentences interpolate a different number of "+
					"values (%d here, %d in %s), so one of them drops a value the "+
					"reader needs:\n  en: %s\n  tr: %s",
					code, want, got, c.jsName, en, native)
			}
		}
		for code := range tr {
			if _, ok := c.goCatalog[code]; !ok {
				extra = append(extra, code)
			}
		}
		sort.Strings(missing)
		sort.Strings(extra)

		if len(missing) > 0 {
			t.Errorf("%s has no Turkish sentence for %d code(s); a Turkish "+
				"interface would show these in English:\n  %s",
				c.jsName, len(missing), strings.Join(missing, "\n  "))
		}
		if len(extra) > 0 {
			t.Errorf("%s translates %d code(s) the daemon no longer raises, so "+
				"they are dead weight that reads as coverage:\n  %s",
				c.jsName, len(extra), strings.Join(extra, "\n  "))
		}
	}
}

var (
	failCall = regexp.MustCompile(`\bfail\(model\.Step\w+,\s*"([^"]+)"`)
	// The dot is not part of the pattern on purpose: a chained call is usually
	// wrapped onto its own line, which leaves the dot at the end of the line
	// before it.
	hintCall = regexp.MustCompile(`withHint\("([^"]+)"`)
	// Failures decided outside the daemon name themselves through fault.Tag,
	// and the daemon adopts the name. Those codes are in the same catalog and
	// have to be checked the same way, or a translation goes missing in the
	// one direction nothing else looks.
	tagCall = regexp.MustCompile(`fault\.Tagf?\((?:[^,]+,\s*)?"([a-z][a-z0-9_.]*)"`)
	// And the one kind of name that is not raised as an error at all: the
	// update check records why it could not offer an update, which the page
	// then translates the same way. It is a code with a sentence, so it is
	// held to the same rule.
	codeField = regexp.MustCompile(`ErrorCode\s*=\s*"([a-z][a-z0-9_.]*)"`)
)

// TestEveryCodeHasASentence reads the engine itself. The catalogs can be
// complete and consistent while a call site still passes a key that is in
// neither of them — a typo, or a code renamed in one place.
func TestEveryCodeHasASentence(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	// Plus every other package, for the failures that name themselves where
	// they are decided rather than where they are reported.
	elsewhere, err := filepath.Glob("../*/*.go")
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, elsewhere...)

	seen := map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		blob, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src := string(blob)
		for _, m := range failCall.FindAllStringSubmatch(src, -1) {
			seen[m[1]] = true
			if _, ok := messages[m[1]]; !ok {
				t.Errorf("%s raises %q, which is in no catalog; the reader would "+
					"see the key itself as the error", f, m[1])
			}
		}
		for _, m := range hintCall.FindAllStringSubmatch(src, -1) {
			seen[m[1]] = true
			if _, ok := hints[m[1]]; !ok {
				t.Errorf("%s attaches the hint %q, which is in no catalog", f, m[1])
			}
		}
		for _, m := range tagCall.FindAllStringSubmatch(src, -1) {
			seen[m[1]] = true
			if _, ok := messages[m[1]]; !ok {
				t.Errorf("%s tags a failure %q, which is in no catalog; the daemon "+
					"would adopt a name the interface cannot translate", f, m[1])
			}
		}
		for _, m := range codeField.FindAllStringSubmatch(src, -1) {
			seen[m[1]] = true
			if _, ok := messages[m[1]]; !ok {
				t.Errorf("%s records the code %q, which is in no catalog", f, m[1])
			}
		}
	}

	// The other direction: a message nobody raises any more. It is not a bug,
	// but it is a sentence somebody will keep translating for no reason.
	for code := range messages {
		if !seen[code] {
			t.Errorf("no code raises %q any more; delete it or find out what "+
				"stopped reporting it", code)
		}
	}
	for code := range hints {
		if !seen[code] {
			t.Errorf("nothing attaches the hint %q any more", code)
		}
	}
}

// TestTranslatedSentencesStayUsable guards the parts of a translation that a
// key-by-key comparison cannot see: a sentence that is empty, or one that was
// pasted in still carrying its English.
func TestTranslatedSentencesStayUsable(t *testing.T) {
	for _, name := range []string{"ERRORS_TR", "HINTS_TR"} {
		for code, native := range jsCatalog(t, name) {
			if strings.TrimSpace(native) == "" {
				t.Errorf("%s: %s is empty", name, code)
			}
			if native == messages[code] || native == hints[code] {
				t.Errorf("%s: %s is still the English sentence", name, code)
			}
		}
	}
}

// Example output, so the shape of a failure as the interface receives it is
// visible in the test file rather than only in a browser.
func ExampleFault() {
	f := fail("core", "config.port_in_use", 10808, "SOCKS proxy").
		withHint("hint.port_change", "socks_port", "127.0.0.1:10808")
	fmt.Println(f.Error())
	fmt.Println(f.code, f.args)
	fmt.Println(f.hintCode, f.hintArgs)
	// Output:
	// port 10808 is already in use, so the SOCKS proxy cannot start
	// config.port_in_use [10808 SOCKS proxy]
	// hint.port_change [socks_port 127.0.0.1:10808]
}
