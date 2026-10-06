// Package update finds out whether a newer release exists, and fetches it.
//
// It does not install anything. That is deliberate: this package downloads and
// verifies a file, and the thing that then runs it as root lives elsewhere,
// where the consequences of getting it wrong are in plain sight.
//
// Two rules shape everything here.
//
// The first is that an update must never be installed unverified. This
// downloads a tarball over the internet and hands it to something that will
// unpack it into /usr/sbin and run it as root on a router. A release without a
// checksum file is refused rather than trusted, and a checksum that does not
// match is an error, not a warning.
//
// The second is that nothing here may hang. A router whose only way out is the
// tunnel does its update check through that tunnel, and a stalled connection to
// a censored or overloaded host must not leave a goroutine waiting forever or
// hold a lock the interface needs.
package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"xwrt/internal/fault"
)

// Every failure here is named, because every one of them ends up on a page in
// somebody's language. The English text next to the name is what the log and
// the command line show; the interface renders its own sentence from the name
// and the values. This is the same arrangement as the rest of the daemon's
// failures, and leaving it out was a mistake that put an English paragraph in
// the middle of a Turkish page.

// Codes is every name this package raises, so a test can check that each one
// has a sentence in both catalogs — and that the catalogs carry nothing this
// package stopped raising.
var Codes = []string{
	"update.bad_source", "update.unreachable", "update.no_releases",
	"update.rate_limited", "update.http", "update.unreadable", "update.draft",
	"update.no_bundle", "update.no_checksums", "update.not_listed",
	"update.checksum_mismatch", "update.download_failed",
}

// Arch is the release asset name for the architecture this binary was built
// for. The names are release.sh's, not Go's, because they are what appears on
// the release page.
func Arch() string {
	switch runtime.GOARCH {
	case "arm64":
		return "aarch64"
	case "amd64":
		return "x86_64"
	case "arm":
		return "armv7"
	case "mipsle":
		return "mipsel"
	case "mips64le":
		return "mips64el"
	case "riscv64":
		return "riscv64"
	}
	return runtime.GOARCH
}

// Release is what a release page says, reduced to what an update needs.
type Release struct {
	Version   string `json:"version"`
	Tag       string `json:"tag"`
	Notes     string `json:"notes,omitempty"`
	URL       string `json:"url,omitempty"`
	Published string `json:"published,omitempty"`

	// Asset is the bundle for this architecture, and Checksums the file that
	// vouches for it. Either being empty means this release cannot be
	// installed from here, and the reason is worth saying out loud.
	Asset     string `json:"asset,omitempty"`
	AssetName string `json:"asset_name,omitempty"`
	Checksums string `json:"checksums,omitempty"`
	Size      int64  `json:"size,omitempty"`
}

// Installable reports whether this release carries what it takes to install it
// on this device: a bundle for this architecture, and a checksum file.
func (r *Release) Installable() bool {
	return r != nil && r.Asset != "" && r.Checksums != ""
}

// route is how every request in this package reaches the network. It is read at
// dial time rather than captured in a transport, because what it should be
// changes while the daemon runs: the tunnel comes up and goes down, and an
// update started a minute after a connect must not still be using the route
// that was correct a minute before it.
//
// Nil means the ordinary one. See socks.go for why there is a choice at all.
var route struct {
	mu   sync.RWMutex
	dial func(ctx context.Context, network, addr string) (net.Conn, error)
}

// Route sets the dial function this package uses, or restores the ordinary one
// when given nil.
func Route(dial func(ctx context.Context, network, addr string) (net.Conn, error)) {
	route.mu.Lock()
	route.dial = dial
	route.mu.Unlock()
}

func dial(ctx context.Context, network, addr string) (net.Conn, error) {
	route.mu.RLock()
	via := route.dial
	route.mu.RUnlock()

	if via != nil {
		return via(ctx, network, addr)
	}
	var d net.Dialer
	return d.DialContext(ctx, network, addr)
}

// transport is shared by both clients below so that neither can be given a
// route the other does not have. Its settings are the standard ones; only the
// dial is this package's own.
func transport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.DialContext = dial
	// Proxy comes from the environment by default, which on a router is empty
	// but on a developer's machine is not — and an HTTP proxy would quietly
	// take precedence over the route chosen above.
	t.Proxy = nil
	return t
}

// client is deliberately modest. Ten seconds to find out whether a release
// exists is already generous on a link that is working, and a long timeout on
// a link that is not just means the interface waits.
var client = &http.Client{Timeout: 20 * time.Second, Transport: transport()}

// checksumName is the file a release must carry for its bundles to be
// installable. release.sh writes it.
const checksumName = "sha256sums"

// apiBase is where release information is asked for. A variable rather than a
// constant so that the tests can point it at a server they control, and
// overridable by the environment for the same reason at the other end: an
// end-to-end test runs the real daemon and has to be able to give it a release
// page without publishing one.
var apiBase = func() string {
	if v := os.Getenv("XWRT_UPDATE_API"); v != "" {
		return strings.TrimRight(v, "/")
	}
	return "https://api.github.com"
}()

// Latest asks GitHub for the newest release of a repository, named "owner/name".
func Latest(ctx context.Context, repo string) (*Release, error) {
	repo = strings.Trim(strings.TrimSpace(repo), "/")
	if repo == "" || strings.Count(repo, "/") != 1 {
		return nil, fault.Tagf("update.bad_source", []any{repo},
			"update source %q is not an owner/name repository", repo)
	}

	url := apiBase + "/repos/" + repo + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	// Named rather than anonymous, so a rate-limited router is identifiable in
	// somebody's logs as this program rather than as noise.
	req.Header.Set("User-Agent", "xwrt-updater")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fault.Tagf("update.unreachable", []any{err},
			"could not reach the release page: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		// Either the repository has no releases yet, or it is not the right
		// repository — and from here those look identical, so the answer names
		// the setting rather than guessing which it was.
		return nil, fault.Tagf("update.no_releases", []any{repo},
			"%s has no releases, or is not the right repository; check the "+
				"update source in Settings", repo)
	case http.StatusForbidden:
		// GitHub rate-limits by address, and a router behind a busy exit node
		// shares that address with everyone else behind it.
		return nil, fault.Tagf("update.rate_limited", nil,
			"the release page refused the request (rate limited); try again later")
	default:
		return nil, fault.Tagf("update.http", []any{resp.Status},
			"the release page answered %s", resp.Status)
	}

	var raw struct {
		TagName     string `json:"tag_name"`
		Name        string `json:"name"`
		Body        string `json:"body"`
		HTMLURL     string `json:"html_url"`
		Draft       bool   `json:"draft"`
		Prerelease  bool   `json:"prerelease"`
		PublishedAt string `json:"published_at"`
		Assets      []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
			Size int64  `json:"size"`
		} `json:"assets"`
	}
	// Bounded: this is parsed on a router with 128 MB of RAM, and the body is
	// whatever the other end sends.
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&raw); err != nil {
		return nil, fault.Tagf("update.unreadable", []any{err},
			"the release page sent something unreadable: %w", err)
	}
	if raw.Draft {
		return nil, fault.Tagf("update.draft", nil, "the newest release is still a draft")
	}

	rel := &Release{
		Tag:       raw.TagName,
		Version:   strings.TrimPrefix(raw.TagName, "v"),
		Notes:     raw.Body,
		URL:       raw.HTMLURL,
		Published: raw.PublishedAt,
	}

	want := "xwrt-" + Arch() + ".tar.gz"
	for _, a := range raw.Assets {
		switch {
		case a.Name == want:
			rel.Asset = a.URL
			rel.AssetName = a.Name
			rel.Size = a.Size
		case a.Name == checksumName:
			rel.Checksums = a.URL
		}
	}
	return rel, nil
}

// Newer compares two versions the way a release page writes them: dotted
// numbers, optionally prefixed with v, optionally with a suffix nobody should
// have to parse.
//
// Unparseable is not newer. An update offered because two strings sorted
// differently is worse than no update offered at all.
func Newer(latest, current string) bool {
	l, okL := parse(latest)
	c, okC := parse(current)
	if !okL || !okC {
		return false
	}
	for i := 0; i < 3; i++ {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	return false
}

func parse(v string) ([3]int, bool) {
	var out [3]int
	v = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(v), "v"))
	// A build that was never given a version says "dev"; everything is newer
	// than nothing, but saying so would offer an update on every developer
	// machine, so it is treated as unknown instead.
	if v == "" || v == "dev" {
		return out, false
	}
	// Anything after a dash is a pre-release marker and is not compared.
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) == 0 || len(parts) > 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// Fetch downloads the release bundle into dir and returns the path, having
// checked it against the release's own checksum file.
//
// The order matters: the checksums are fetched first, so a release that cannot
// be verified costs one small request rather than a router's worth of bandwidth
// on a file that is then thrown away.
func Fetch(ctx context.Context, rel *Release, dir string) (string, error) {
	return FetchWith(ctx, rel, dir, Progress{})
}

// Progress receives what a download is doing, for an interface that wants to
// show it. Either function may be nil.
//
// Stage is told "downloading" before the bundle is fetched and "verifying"
// once it has arrived and is being checked. Bytes is told how much has been
// written so far and how much there is in all; total is 0 when the server did
// not say.
type Progress struct {
	Stage func(stage string)
	Bytes func(done, total int64)
}

func (p Progress) stage(s string) {
	if p.Stage != nil {
		p.Stage(s)
	}
}

// FetchWith is Fetch, reporting as it goes.
func FetchWith(ctx context.Context, rel *Release, dir string, p Progress) (string, error) {
	if !rel.Installable() {
		if rel.Asset == "" {
			return "", fault.Tagf("update.no_bundle", []any{Arch()},
				"this release has no bundle for %s", Arch())
		}
		return "", fault.Tagf("update.no_checksums", []any{checksumName},
			"this release has no %s file, so the download cannot be verified "+
				"and will not be installed", checksumName)
	}

	sums, err := fetchText(ctx, rel.Checksums, 1<<20)
	if err != nil {
		return "", fault.Tagf("update.download_failed", []any{checksumName, err},
			"could not fetch %s: %w", checksumName, err)
	}
	want := sumFor(sums, rel.AssetName)
	if want == "" {
		return "", fault.Tagf("update.not_listed", []any{rel.AssetName, checksumName},
			"%s does not mention %s", checksumName, rel.AssetName)
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, rel.AssetName)
	p.stage("downloading")
	got, err := download(ctx, rel.Asset, path, rel.Size, p.Bytes)
	if err != nil {
		os.Remove(path)
		return "", err
	}
	// The digest was computed as the file was written; comparing it is the
	// check, and it is a separate step because a mismatch is the one outcome
	// here that means somebody else's file arrived.
	p.stage("verifying")
	if !strings.EqualFold(got, want) {
		os.Remove(path)
		return "", fault.Tagf("update.checksum_mismatch", []any{rel.AssetName},
			"the download does not match the checksum in %s (expected %s, "+
				"got %s); it was deleted rather than installed",
			checksumName, want[:12], got[:12])
	}
	return path, nil
}

func fetchText(ctx context.Context, url string, limit int64) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "xwrt-updater")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("answered %s", resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	return string(b), err
}

// sumFor reads the line for one file out of a sha256sums file, in the format
// sha256sum writes: "<hex>  <name>".
func sumFor(sums, name string) string {
	for _, line := range strings.Split(sums, "\n") {
		f := strings.Fields(strings.TrimSpace(line))
		if len(f) < 2 {
			continue
		}
		// The name may carry a leading * (binary mode) or a directory.
		got := strings.TrimPrefix(f[len(f)-1], "*")
		if filepath.Base(got) == name {
			return f[0]
		}
	}
	return ""
}

// download writes the body to path and returns its SHA-256, computed as it is
// written rather than by reading the file back: a router does not have the
// memory to hold the bundle, and reading it twice is a second pass over flash.
func download(ctx context.Context, url, path string, sizeHint int64,
	onBytes func(done, total int64)) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "xwrt-updater")
	// Downloading a bundle over a slow link is a different proposition from
	// asking whether one exists.
	dl := &http.Client{Timeout: 10 * time.Minute, Transport: transport()}
	resp, err := dl.Do(req)
	if err != nil {
		return "", fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download answered %s", resp.Status)
	}

	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	total := resp.ContentLength
	if total <= 0 {
		total = sizeHint
	}
	count := &byteCounter{total: total, report: onBytes}
	// 64 MB is far above any bundle we ship and far below what would fill a
	// router's storage unnoticed.
	if _, err := io.Copy(io.MultiWriter(f, h, count), io.LimitReader(resp.Body, 64<<20)); err != nil {
		return "", fmt.Errorf("download interrupted: %w", err)
	}
	if err := f.Sync(); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// byteCounter reports how much has gone past it. It writes nowhere; it sits
// beside the file and the digest in a MultiWriter and only counts.
type byteCounter struct {
	done, total int64
	report      func(done, total int64)
}

func (c *byteCounter) Write(p []byte) (int, error) {
	c.done += int64(len(p))
	if c.report != nil {
		c.report(c.done, c.total)
	}
	return len(p), nil
}
