package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Backing the device up, and putting it back.
//
// The file is the configuration document, unchanged — no wrapper, no format of
// its own. That is what lets a backup be restored by any path that already
// exists, and what keeps a backup taken a year ago readable.

// configServer stands in for the daemon. It records what it was sent and
// answers the check and the save.
type configServer struct {
	doc      string
	checked  []byte
	saved    []byte
	problems []string
}

func (c *configServer) start(t *testing.T) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/config":
			_, _ = w.Write([]byte(c.doc))
		case r.Method == http.MethodPut && r.URL.Path == "/api/config":
			body := make([]byte, r.ContentLength)
			_, _ = r.Body.Read(body)
			if r.URL.Query().Get("check") == "1" {
				c.checked = body
				if len(c.problems) > 0 {
					w.WriteHeader(http.StatusBadRequest)
					out, _ := json.Marshal(map[string]any{
						"error": c.problems[0], "problems": c.problems, "saved": false,
					})
					_, _ = w.Write(out)
					return
				}
				_, _ = w.Write([]byte(`{"saved":false,"problems":[]}`))
				return
			}
			c.saved = body
			_, _ = w.Write([]byte(`{"saved":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	addr := strings.TrimPrefix(srv.URL, "http://")
	saved := apiAddr
	apiAddr = func() string { return addr }
	t.Cleanup(func() { apiAddr = saved })
}

const sampleDoc = `{"settings":{"mode":"mixed"},"profiles":[{"id":"p1","name":"a"}],` +
	`"groups":[],"rules":[],"subscriptions":[]}`

func TestBackupWritesTheDocumentAsItIs(t *testing.T) {
	c := &configServer{doc: sampleDoc}
	c.start(t)
	path := filepath.Join(t.TempDir(), "backup.json")

	if err := runBackup([]string{path}); err != nil {
		t.Fatal(err)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Same document, not a wrapper around it. A backup that needs unwrapping
	// is a backup only this version can read.
	var got, want map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("the backup is not valid JSON: %v\n%s", err, body)
	}
	_ = json.Unmarshal([]byte(sampleDoc), &want)
	for _, key := range []string{"settings", "profiles", "groups", "rules", "subscriptions"} {
		if _, ok := got[key]; !ok {
			t.Errorf("the backup has no %q, so it cannot be restored", key)
		}
	}
	if len(got) != len(want) {
		t.Errorf("the backup has %d top-level keys, the document has %d — "+
			"anything extra is refused on the way back in", len(got), len(want))
	}
}

// The file carries every server's credentials. It must not be world readable.
func TestTheBackupFileIsNotReadableByEveryone(t *testing.T) {
	c := &configServer{doc: sampleDoc}
	c.start(t)
	path := filepath.Join(t.TempDir(), "backup.json")
	if err := runBackup([]string{path}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Errorf("mode %v: this file holds every server's credentials in full",
			info.Mode().Perm())
	}
}

func TestRestoreChecksBeforeItWrites(t *testing.T) {
	c := &configServer{doc: sampleDoc, problems: []string{
		"the document has no rules", "profile p1 has no address",
	}}
	c.start(t)
	path := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(path, []byte(`{"settings":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	err := runRestore(path)

	if err == nil {
		t.Fatal("a document the daemon would refuse was restored anyway")
	}
	// Nothing was written. This is the assertion that matters: a restore that
	// fails after replacing the device is not a failed restore.
	if c.saved != nil {
		t.Error("it was saved despite failing the check")
	}
	// And every problem is reported, not the first. Somebody fixing a
	// hand-edited file one error per attempt gives up on the fourth.
	for _, want := range c.problems {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q: %v", want, err)
		}
	}
}

func TestRestoreSendsTheFileThroughUnchanged(t *testing.T) {
	c := &configServer{doc: sampleDoc}
	c.start(t)
	path := filepath.Join(t.TempDir(), "good.json")
	if err := os.WriteFile(path, []byte(sampleDoc), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := runRestore(path); err != nil {
		t.Fatal(err)
	}

	if c.saved == nil {
		t.Fatal("nothing was saved")
	}
	// Byte for byte what was in the file: a restore that reformats on the way
	// in is answering a different question from the one the check answered.
	if string(c.saved) != sampleDoc {
		t.Errorf("what was saved is not what was in the file:\n%s", c.saved)
	}
	if c.checked == nil {
		t.Error("it was saved without being checked first")
	}
}

// A round trip. The point of a backup is that it comes back.
func TestABackupRestores(t *testing.T) {
	c := &configServer{doc: sampleDoc}
	c.start(t)
	path := filepath.Join(t.TempDir(), "roundtrip.json")

	if err := runBackup([]string{path}); err != nil {
		t.Fatal(err)
	}
	if err := runRestore(path); err != nil {
		t.Fatal(err)
	}

	var sent, original map[string]any
	if err := json.Unmarshal(c.saved, &sent); err != nil {
		t.Fatalf("what came back is not valid JSON: %v", err)
	}
	_ = json.Unmarshal([]byte(sampleDoc), &original)
	if len(sent) != len(original) {
		t.Errorf("the round trip changed the document: %v vs %v", sent, original)
	}
}
