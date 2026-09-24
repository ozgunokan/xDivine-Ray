package api

import (
	"encoding/json"
	"strings"
	"testing"

	"xwrt/internal/model"
	"xwrt/internal/ucicfg"
)

// The whole configuration as one editable document.
//
// This is the only route that can empty a device in a single request, so the
// tests below are mostly about the ways a hand-typed document is wrong and
// what each of them costs if it is accepted.

func liveConfig() *ucicfg.Data {
	return &ucicfg.Data{
		Settings: model.Settings{Active: "p1", Enabled: true},
		Profiles: []model.Profile{{
			ID: "p1", Name: "amsterdam", Address: "a.example.com", Port: 443,
			Proto: "vless", UUID: "11111111-1111-1111-1111-111111111111",
			Network: "tcp", Security: "tls",
		}},
	}
}

// A document that is complete and correct, used as the base for the ones that
// are not.
func goodDoc(t *testing.T) map[string]any {
	t.Helper()
	return map[string]any{
		"settings": map[string]any{"active": "p1", "mode": "mixed"},
		"profiles": []any{map[string]any{
			"id": "p1", "name": "amsterdam", "address": "a.example.com",
			"port": 443, "proto": "vless",
			"uuid":    "11111111-1111-1111-1111-111111111111",
			"network": "tcp", "security": "tls",
		}},
		"groups": []any{}, "rules": []any{}, "subscriptions": []any{},
	}
}

func encode(t *testing.T, doc any) []byte {
	t.Helper()
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func TestACompleteDocumentIsAccepted(t *testing.T) {
	next, problems := parseConfig(encode(t, goodDoc(t)), liveConfig())
	if len(problems) > 0 {
		t.Fatalf("a valid document was refused: %v", problems)
	}
	if len(next.Profiles) != 1 || next.Profiles[0].ID != "p1" {
		t.Errorf("profiles = %+v", next.Profiles)
	}
}

func TestAMissingSectionIsRefusedRatherThanObeyed(t *testing.T) {
	// The dangerous case: someone pastes the half of the document they were
	// editing. Absent means "delete every one of these", and obeying that
	// silently is how a device loses its servers.
	doc := goodDoc(t)
	delete(doc, "profiles")

	_, problems := parseConfig(encode(t, doc), liveConfig())
	if len(problems) == 0 {
		t.Fatal("a document with no profiles section was accepted; it would " +
			"have deleted every server on the device")
	}
	if !strings.Contains(problems[0], "profiles") {
		t.Errorf("the refusal does not name the missing section: %q", problems[0])
	}
	if !strings.Contains(problems[0], "empty list") {
		t.Errorf("it refuses without saying how to actually delete them: %q",
			problems[0])
	}
}

func TestAnExplicitlyEmptyListIsObeyed(t *testing.T) {
	// The other half of the same rule: "[]" is a decision someone made, and
	// refusing it would leave no way to clear a section at all.
	doc := goodDoc(t)
	doc["profiles"] = []any{}
	doc["settings"] = map[string]any{"mode": "mixed"} // nothing to be active

	next, problems := parseConfig(encode(t, doc), liveConfig())
	if len(problems) > 0 {
		t.Fatalf("an explicitly emptied section was refused: %v", problems)
	}
	if len(next.Profiles) != 0 {
		t.Errorf("profiles = %+v, want none", next.Profiles)
	}
}

func TestAMisspelledKeyIsAnError(t *testing.T) {
	// "profile" for "profiles" parses perfectly, means nothing, and without
	// this would be reported as a successful save of an empty server list.
	doc := goodDoc(t)
	doc["profile"] = doc["profiles"]

	_, problems := parseConfig(encode(t, doc), liveConfig())
	if len(problems) == 0 {
		t.Fatal("an unrecognised key was ignored instead of reported")
	}
	if !strings.Contains(problems[0], "profile") {
		t.Errorf("the error does not name the key: %q", problems[0])
	}
}

func TestBrokenJSONSaysWhere(t *testing.T) {
	body := []byte("{\n  \"settings\": {},\n  \"profiles\": [,]\n}\n")
	_, problems := parseConfig(body, liveConfig())
	if len(problems) == 0 {
		t.Fatal("invalid JSON was accepted")
	}
	if !strings.Contains(problems[0], "line 3") {
		t.Errorf("the error does not say which line: %q", problems[0])
	}
}

func TestAGroupPointingAtNothingIsCaught(t *testing.T) {
	// The fault no single-field form would ever see: a group referring to a
	// server that is not in the file. It loads, reports nothing, and connects
	// to fewer servers than it claims.
	doc := goodDoc(t)
	doc["groups"] = []any{map[string]any{
		"id": "g1", "name": "avrupa", "strategy": "leastPing",
		"members": []any{"p1", "p9"},
	}}

	_, problems := parseConfig(encode(t, doc), liveConfig())
	if len(problems) == 0 {
		t.Fatal("a group referring to a missing server was accepted")
	}
	joined := strings.Join(problems, " | ")
	if !strings.Contains(joined, "p9") {
		t.Errorf("the problem does not name the missing member: %q", joined)
	}
}

func TestEveryProblemIsReportedAtOnce(t *testing.T) {
	// Someone fixing a hand-written file one error per attempt gives up on the
	// fourth attempt.
	doc := goodDoc(t)
	doc["profiles"] = []any{
		map[string]any{"id": "p1", "name": "a", "address": "a.example.com",
			"port": 443, "proto": "vless",
			"uuid": "11111111-1111-1111-1111-111111111111", "network": "tcp"},
		map[string]any{"id": "p1", "name": "b", "address": "b.example.com",
			"port": 443, "proto": "vless",
			"uuid": "22222222-2222-2222-2222-222222222222", "network": "tcp"},
	}
	doc["groups"] = []any{map[string]any{
		"id": "g1", "name": "g", "strategy": "leastPing",
		"members": []any{"nope"},
	}}

	_, problems := parseConfig(encode(t, doc), liveConfig())
	if len(problems) < 2 {
		t.Fatalf("only %d problem(s) reported: %v", len(problems), problems)
	}
	joined := strings.Join(problems, " | ")
	for _, want := range []string{"share the id", "nope"} {
		if !strings.Contains(joined, want) {
			t.Errorf("%q is not among the problems: %s", want, joined)
		}
	}
}

func TestRuntimeStateIsNotTakenFromTheDocument(t *testing.T) {
	// Whether the daemon dials out on its own is written by connect and
	// disconnect. A document carrying yesterday's value would silently reverse
	// a decision made since it was exported.
	doc := goodDoc(t)
	doc["settings"] = map[string]any{"active": "p1", "mode": "mixed", "enabled": false}

	live := liveConfig()
	live.Settings.Enabled = true

	next, problems := parseConfig(encode(t, doc), live)
	if len(problems) > 0 {
		t.Fatalf("refused: %v", problems)
	}
	if !next.Settings.Enabled {
		t.Error("the document turned the daemon's own enabled flag off")
	}
}

func TestAnActiveSelectionThatIsGoneIsReported(t *testing.T) {
	doc := goodDoc(t)
	doc["settings"] = map[string]any{"active": "vanished", "mode": "mixed"}

	_, problems := parseConfig(encode(t, doc), liveConfig())
	if len(problems) == 0 {
		t.Fatal("an active id naming nothing was accepted; the device would " +
			"come back from a reboot and connect to nothing")
	}
}

func TestAnEmptyBodyIsNotAnEmptyConfiguration(t *testing.T) {
	for _, body := range []string{"", "   \n "} {
		if _, problems := parseConfig([]byte(body), liveConfig()); len(problems) == 0 {
			t.Errorf("an empty body (%q) was read as a configuration", body)
		}
	}
}
