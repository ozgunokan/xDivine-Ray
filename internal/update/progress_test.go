package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// What an install window gets to show while the bundle comes down.
//
// The window exists because an update used to be a button followed by silence:
// no way to tell a download in progress from one that had stalled, or either
// from an install that had quietly failed. These check that the download says
// what it is doing, in order, and that the numbers it gives add up.

func serveBundle(t *testing.T, bundle []byte, sums string) *Release {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bundle.tar.gz":
			w.Write(bundle)
		case "/sha256sums":
			w.Write([]byte(sums))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return &Release{
		Version: "9.9.9", AssetName: "xwrt-" + Arch() + ".tar.gz",
		Asset: srv.URL + "/bundle.tar.gz", Checksums: srv.URL + "/sha256sums",
		Size: int64(len(bundle)),
	}
}

func digest(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func TestTheDownloadSaysWhatItIsDoingInOrder(t *testing.T) {
	bundle := []byte(strings.Repeat("xwrt bundle ", 20000)) // ~240 KB, several reads
	rel := serveBundle(t, bundle, digest(bundle)+"  xwrt-"+Arch()+".tar.gz\n")

	var stages []string
	var last, total int64
	calls := 0
	_, err := FetchWith(context.Background(), rel, t.TempDir(), Progress{
		Stage: func(s string) { stages = append(stages, s) },
		Bytes: func(done, tot int64) { last, total = done, tot; calls++ },
	})
	if err != nil {
		t.Fatalf("a good bundle was refused: %v", err)
	}

	if strings.Join(stages, ",") != "downloading,verifying" {
		t.Fatalf("stages %v, want downloading then verifying", stages)
	}
	if last != int64(len(bundle)) {
		t.Fatalf("the count ended at %d of %d bytes", last, len(bundle))
	}
	if total != int64(len(bundle)) {
		t.Fatalf("the total was given as %d, want %d", total, len(bundle))
	}
	// More than one report, or a progress bar has nothing to move between.
	if calls < 2 {
		t.Fatalf("progress reported %d time(s) for a %d byte download", calls, len(bundle))
	}
}

// A bundle that does not match its checksum still goes through verifying —
// that is where it fails, and the window has to show the failure at the step
// it happened on rather than at the download.
func TestAMismatchFailsAtVerifying(t *testing.T) {
	bundle := []byte("the real bundle")
	rel := serveBundle(t, bundle, digest([]byte("something else"))+"  xwrt-"+Arch()+".tar.gz\n")

	var stages []string
	_, err := FetchWith(context.Background(), rel, t.TempDir(), Progress{
		Stage: func(s string) { stages = append(stages, s) },
	})
	if err == nil {
		t.Fatalf("a mismatched bundle was accepted")
	}
	if len(stages) == 0 || stages[len(stages)-1] != "verifying" {
		t.Fatalf("the failure did not happen at verifying: %v", stages)
	}
}

// Fetch without progress is exactly what it was: no reporter, no panic.
func TestFetchWithoutAReporterStillWorks(t *testing.T) {
	bundle := []byte("quiet bundle")
	rel := serveBundle(t, bundle, digest(bundle)+"  xwrt-"+Arch()+".tar.gz\n")
	if _, err := Fetch(context.Background(), rel, t.TempDir()); err != nil {
		t.Fatalf("plain Fetch broke: %v", err)
	}
}
