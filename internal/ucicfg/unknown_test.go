package ucicfg

import (
	"strings"
	"testing"
)

// The point of this check is that a typo stops being invisible, so the test is
// about the message a person reads.
func TestUnknownOptionsAreNamedWithASuggestion(t *testing.T) {
	pkg := &Package{Name: PackageName, Sections: []*Section{
		{Name: "main", Type: typeMain, Options: map[string]string{
			"mode": "mixed", "loglevel": "info",
		}},
		{Name: "p1", Type: typeProfile, Options: map[string]string{
			"address": "a.example.com", "network": "ws", "fingerprint": "chrome",
		}},
	}}

	warnings := checkUnknownOptions(pkg)
	joined := strings.Join(warnings, "\n")

	for _, want := range []string{
		`option "network" is not recognised`, `did you mean "net"?`,
		`option "fingerprint" is not recognised`, `did you mean "fp"?`,
		`option "loglevel" is not recognised`, `did you mean "log_level"?`,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, `"mode"`) || strings.Contains(joined, `"address"`) {
		t.Errorf("a recognised option was reported:\n%s", joined)
	}
}

func TestKnownOptionsProduceNoWarnings(t *testing.T) {
	pkg := &Package{Name: PackageName, Sections: []*Section{
		{Name: "main", Type: typeMain, Options: map[string]string{
			"mode": "tproxy", "log_level": "warning", "fwmark": "0x1e0",
		}, Lists: map[string][]string{"bypass_ip": {"10.0.0.0/8"}}},
		{Name: "p1", Type: typeProfile, Options: map[string]string{
			"proto": "vless", "net": "ws", "fp": "chrome", "pinned_cert": "ab",
		}},
		{Name: "g1", Type: typeGroup, Options: map[string]string{"strategy": "leastPing"},
			Lists: map[string][]string{"member": {"p1"}}},
		{Name: "r1", Type: typeRule, Options: map[string]string{"action": "direct"},
			Lists: map[string][]string{"domain": {"example.com"}}},
		{Name: "s1", Type: typeSub, Options: map[string]string{"url": "https://x/y"}},
	}}
	if got := checkUnknownOptions(pkg); len(got) != 0 {
		t.Fatalf("unexpected warnings: %v", got)
	}
}

// A list written as a list must not be reported just because it is not an
// option, and vice versa: both spellings reach the loader.
func TestListOptionsAreRecognised(t *testing.T) {
	pkg := &Package{Name: PackageName, Sections: []*Section{
		{Name: "r1", Type: typeRule,
			Lists: map[string][]string{"domains": {"example.com"}}},
	}}
	warnings := checkUnknownOptions(pkg)
	if len(warnings) != 1 || !strings.Contains(warnings[0], `did you mean "domain"?`) {
		t.Fatalf("unexpected: %v", warnings)
	}
}

func TestUnknownSectionTypeIsReported(t *testing.T) {
	pkg := &Package{Name: PackageName, Sections: []*Section{
		{Name: "x", Type: "profil", Options: map[string]string{"address": "a"}},
	}}
	warnings := checkUnknownOptions(pkg)
	if len(warnings) != 1 || !strings.Contains(warnings[0], `section type "profil"`) {
		t.Fatalf("unexpected: %v", warnings)
	}
}
