package update

import "testing"

// Version comparison, because getting it wrong is either an update nobody is
// offered or an update offered forever.
func TestNewerComparesVersionsRatherThanStrings(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
		why             string
	}{
		{"0.25.0", "0.24.0", true, "an ordinary release"},
		{"v0.25.0", "0.24.0", true, "the tag carries a v and the binary does not"},
		{"0.24.1", "0.24.0", true, "a patch release"},
		{"0.24.0", "0.24.0", false, "the same version is not newer than itself"},
		{"0.23.0", "0.24.0", false, "going backwards is not an update"},
		// The one string comparison gets wrong, and the reason this is not a
		// string comparison.
		{"0.10.0", "0.9.0", true, "10 is after 9, though \"0.10.0\" sorts before \"0.9.0\""},
		{"0.9.0", "0.10.0", false, "and not the other way round"},
		{"1.0.0", "0.99.99", true, "a major release"},
		// Anything that cannot be read is not an update. An update offered
		// because two strings differed is worse than none offered at all.
		{"", "0.24.0", false, "an empty tag"},
		{"latest", "0.24.0", false, "a tag that is not a version"},
		{"0.25.0", "dev", false, "a development build is not compared"},
		{"0.25.0-rc1", "0.24.0", true, "a pre-release suffix is ignored, not refused"},
	}
	for _, c := range cases {
		if got := Newer(c.latest, c.current); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v — %s",
				c.latest, c.current, got, c.want, c.why)
		}
	}
}

// The checksum file is the only thing standing between a release page and
// something running as root on a router, so reading it has to be exact.
func TestSumForReadsTheChecksumFile(t *testing.T) {
	sums := "" +
		"aaaa1111  xwrt-aarch64.tar.gz\n" +
		"bbbb2222 *xwrt-x86_64.tar.gz\n" +
		"cccc3333  release/xwrt-armv7.tar.gz\n"

	if got := sumFor(sums, "xwrt-aarch64.tar.gz"); got != "aaaa1111" {
		t.Errorf("plain line: got %q", got)
	}
	// sha256sum writes a star for binary mode.
	if got := sumFor(sums, "xwrt-x86_64.tar.gz"); got != "bbbb2222" {
		t.Errorf("binary-mode line: got %q", got)
	}
	// And a path, if the file was generated from a directory above.
	if got := sumFor(sums, "xwrt-armv7.tar.gz"); got != "cccc3333" {
		t.Errorf("line with a directory: got %q", got)
	}
	// A file that is not listed has no checksum, and must not borrow one.
	if got := sumFor(sums, "xwrt-mipsel.tar.gz"); got != "" {
		t.Errorf("an unlisted file got the checksum %q", got)
	}
}

// A release that cannot be verified cannot be installed. This is the rule the
// whole updater rests on: it downloads a tarball and hands it to something
// that unpacks it into /usr/sbin and runs it as root.
func TestAReleaseWithoutAChecksumIsNotInstallable(t *testing.T) {
	full := &Release{Asset: "https://example/x.tar.gz", Checksums: "https://example/sha256sums"}
	if !full.Installable() {
		t.Error("a release with a bundle and checksums should be installable")
	}
	noSums := &Release{Asset: "https://example/x.tar.gz"}
	if noSums.Installable() {
		t.Error("a release with no checksum file was reported as installable")
	}
	noAsset := &Release{Checksums: "https://example/sha256sums"}
	if noAsset.Installable() {
		t.Error("a release with no bundle for this architecture was reported as installable")
	}
	if (*Release)(nil).Installable() {
		t.Error("no release at all was reported as installable")
	}
}

// The asset name has to match what release.sh writes, or every device looks
// for a file that is not there.
func TestArchNamesMatchTheReleaseAssets(t *testing.T) {
	// The names in build.sh's target list.
	known := map[string]bool{
		"aarch64": true, "x86_64": true, "armv7": true,
		"mipsel": true, "mips64el": true, "riscv64": true,
	}
	if !known[Arch()] {
		t.Errorf("this build calls its architecture %q, which is not one of the "+
			"names release.sh publishes; the updater would look for a bundle "+
			"that does not exist", Arch())
	}
}
