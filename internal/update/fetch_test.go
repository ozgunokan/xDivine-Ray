package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The download path, against a server that behaves like a release page.
//
// This is the part of the program where being wrong is worst: it takes a file
// from the internet and hands it to something that unpacks it into /usr/sbin
// and runs it as root. So what is checked here is not that the happy path
// works — it is that every unhappy one refuses.
func TestFetchRefusesAnythingItCannotVerify(t *testing.T) {
	bundle := []byte("this is not really a tarball, but it is the right bytes")
	sum := sha256.Sum256(bundle)
	good := hex.EncodeToString(sum[:])

	var serveSums string
	var serveBundle []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/sha256sums"):
			fmt.Fprint(w, serveSums)
		case strings.HasSuffix(r.URL.Path, ".tar.gz"):
			w.Write(serveBundle)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	rel := &Release{
		Version:   "9.9.9",
		AssetName: "xwrt-test.tar.gz",
		Asset:     srv.URL + "/xwrt-test.tar.gz",
		Checksums: srv.URL + "/sha256sums",
	}

	// 1. Everything as it should be.
	dir := t.TempDir()
	serveSums = good + "  xwrt-test.tar.gz\n"
	serveBundle = bundle
	path, err := Fetch(context.Background(), rel, dir)
	if err != nil {
		t.Fatalf("a correct download was refused: %v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(bundle) {
		t.Error("the file on disk is not what the server sent")
	}

	// 2. The bundle has been tampered with after the checksums were published.
	//    This is the case the whole design is for.
	dir = t.TempDir()
	serveBundle = append([]byte("x"), bundle...)
	if _, err := Fetch(context.Background(), rel, dir); err == nil {
		t.Error("a bundle that does not match its checksum was accepted")
	} else if !strings.Contains(err.Error(), "checksum") {
		t.Errorf("the refusal does not mention the checksum: %v", err)
	}
	// And it is not left lying around for something else to pick up.
	if _, err := os.Stat(filepath.Join(dir, rel.AssetName)); !os.IsNotExist(err) {
		t.Error("the rejected download was left on disk")
	}

	// 3. The checksum file does not mention this file at all.
	dir = t.TempDir()
	serveBundle = bundle
	serveSums = good + "  something-else.tar.gz\n"
	if _, err := Fetch(context.Background(), rel, dir); err == nil {
		t.Error("a bundle absent from the checksum file was accepted")
	}

	// 4. There is no checksum file. Refused before anything is downloaded.
	noSums := *rel
	noSums.Checksums = ""
	if _, err := Fetch(context.Background(), &noSums, t.TempDir()); err == nil {
		t.Error("a release with no checksum file was accepted")
	}
}

// And the release page itself: what it says becomes what the daemon offers, so
// a release with no bundle for this architecture has to come back saying so
// rather than looking installable.
func TestLatestReadsTheReleasePage(t *testing.T) {
	body := map[string]any{
		"tag_name": "v9.9.9",
		"body":     "notes",
		"html_url": "https://example/releases/9.9.9",
		"assets": []map[string]any{
			{"name": "xwrt-" + Arch() + ".tar.gz",
				"browser_download_url": "https://example/b.tar.gz", "size": 123},
			{"name": "sha256sums",
				"browser_download_url": "https://example/sha256sums"},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/someone/xwrt/releases/latest" {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(body)
	}))
	defer srv.Close()

	old := apiBase
	apiBase = srv.URL
	defer func() { apiBase = old }()

	rel, err := Latest(context.Background(), "someone/xwrt")
	if err != nil {
		t.Fatalf("a well-formed release page was rejected: %v", err)
	}
	if rel.Version != "9.9.9" {
		t.Errorf("version %q, want 9.9.9 (the v is the tag's, not the version's)", rel.Version)
	}
	if !rel.Installable() {
		t.Error("a release with a bundle for this architecture and checksums " +
			"was not considered installable")
	}

	// The same release, without the checksums: found, but not installable.
	body["assets"] = []map[string]any{
		{"name": "xwrt-" + Arch() + ".tar.gz",
			"browser_download_url": "https://example/b.tar.gz"},
	}
	rel, err = Latest(context.Background(), "someone/xwrt")
	if err != nil {
		t.Fatal(err)
	}
	if rel.Installable() {
		t.Error("a release with no checksum file was considered installable")
	}

	// A repository that has published nothing says so, rather than looking
	// like a failure of the network.
	if _, err := Latest(context.Background(), "someone/nothing"); err == nil {
		t.Error("a repository with no releases did not produce an error")
	}

	// And a source that is not a repository is refused before any request.
	for _, bad := range []string{"", "xwrt", "https://github.com/a/b", "a/b/c"} {
		if _, err := Latest(context.Background(), bad); err == nil {
			t.Errorf("%q was accepted as a repository", bad)
		}
	}
}
