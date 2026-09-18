package update

import (
	"errors"
	"testing"
)

// Fakes for the three ways detect reaches the outside world.
type fakeEnv struct {
	have  map[string]bool   // which tools are on PATH
	out   map[string]string // command line -> output
	fail  map[string]bool   // command line -> exits non-zero
	files map[string]bool
	calls []string
}

func (f *fakeEnv) lookPath(name string) (string, error) {
	if f.have[name] {
		return "/usr/bin/" + name, nil
	}
	return "", errors.New("not found")
}

func (f *fakeEnv) run(name string, args ...string) (string, error) {
	key := name
	for _, a := range args {
		key += " " + a
	}
	f.calls = append(f.calls, key)
	if f.fail[key] {
		return "", errors.New("exit 1")
	}
	return f.out[key], nil
}

func (f *fakeEnv) exists(path string) bool { return f.files[path] }

func (f *fakeEnv) detect() Origin { return detect(f.lookPath, f.run, f.exists) }

func TestBundleInstallHasNoManager(t *testing.T) {
	// Neither tool present: a device that was never packaged, or one where the
	// bundle put xwrt next to a package manager that has never heard of it.
	env := &fakeEnv{}
	if got := env.detect(); got.Managed() {
		t.Fatalf("a device with no package manager reported %+v; nothing owns "+
			"the binary there, so the update button has nothing to warn about", got)
	}

	// The tools are there, but xwrt is not one of their packages.
	env = &fakeEnv{
		have: map[string]bool{"apk": true, "opkg": true},
		out: map[string]string{
			"apk list --installed xwrt": "",
			"opkg status xwrt":          "",
		},
	}
	if got := env.detect(); got.Managed() {
		t.Fatalf("a bundle install alongside apk reported %+v", got)
	}
}

func TestApkInstallIsRecognised(t *testing.T) {
	env := &fakeEnv{
		have: map[string]bool{"apk": true},
		out: map[string]string{
			"apk list --installed xwrt": "xwrt-1.0.3-r1 aarch64_cortex-a53 {xwrt} (MIT) [installed]\n",
		},
	}
	got := env.detect()
	if got.Manager != "apk" || got.Version != "1.0.3" {
		t.Fatalf("apk install read as %+v, want apk/1.0.3 — the -r1 is the "+
			"package release, not part of the version the binary prints", got)
	}
	if got.DriftedFrom("1.0.3") {
		t.Fatal("a package install that matches the running binary was called drifted")
	}
	if !got.DriftedFrom("1.0.4") {
		t.Fatal("a binary newer than the package record is exactly the drift " +
			"a sysupgrade undoes, and it was not reported")
	}
}

func TestOpkgInstallIsRecognised(t *testing.T) {
	env := &fakeEnv{
		have: map[string]bool{"opkg": true},
		out: map[string]string{
			"opkg status xwrt": "Package: xwrt\n" +
				"Version: 1.0.3-1\n" +
				"Depends: libc, xray-core\n" +
				"Status: install user installed\n" +
				"Architecture: aarch64_cortex-a53\n",
		},
	}
	got := env.detect()
	if got.Manager != "opkg" || got.Version != "1.0.3" {
		t.Fatalf("opkg install read as %+v, want opkg/1.0.3", got)
	}
}

func TestAKnownButUninstalledPackageIsNotAnOrigin(t *testing.T) {
	// opkg knows about every package in its lists, installed or not. Reading
	// "Package: xwrt" and stopping there would put a drift warning on a device
	// that installed from a bundle and merely has the feed configured.
	env := &fakeEnv{
		have: map[string]bool{"opkg": true},
		out: map[string]string{
			"opkg status xwrt": "Package: xwrt\n" +
				"Version: 1.0.2-1\n" +
				"Status: unknown ok not-installed\n",
		},
	}
	if got := env.detect(); got.Managed() {
		t.Fatalf("a package opkg knows but has not installed reported %+v", got)
	}
}

func TestApkPrefixDoesNotMatchADifferentPackage(t *testing.T) {
	// `apk list` matches loosely. A package whose name starts with ours is not
	// ours, and reading its version would report a drift that does not exist.
	env := &fakeEnv{
		have: map[string]bool{"apk": true},
		out: map[string]string{
			"apk list --installed xwrt": "xwrt-extras-2.0.0-r1 aarch64_cortex-a53 [installed]\n" +
				"luci-app-xwrt-1.0.3-r1 all [installed]\n",
		},
	}
	if got := env.detect(); got.Managed() {
		t.Fatalf("a neighbouring package was read as xwrt itself: %+v", got)
	}
}

func TestApkWinsOnADeviceThatHasBoth(t *testing.T) {
	// An upgraded device keeps the opkg files around. apk is what will run at
	// the next upgrade, so apk is the answer.
	env := &fakeEnv{
		have: map[string]bool{"apk": true, "opkg": true},
		out: map[string]string{
			"apk list --installed xwrt": "xwrt-1.0.3-r1 aarch64_cortex-a53 [installed]\n",
			"opkg status xwrt":          "Package: xwrt\nVersion: 0.9.0-1\nStatus: install user installed\n",
		},
	}
	got := env.detect()
	if got.Manager != "apk" || got.Version != "1.0.3" {
		t.Fatalf("a device with both managers reported %+v, want apk/1.0.3", got)
	}
}

func TestOpkgRecordIsBelievedWhenTheToolCannotRun(t *testing.T) {
	// opkg's own list of what it installed is a file. If the binary is missing
	// from PATH, or refuses to run, that file is still the truth.
	env := &fakeEnv{
		files: map[string]bool{opkgList: true},
	}
	got := env.detect()
	if got.Manager != "opkg" {
		t.Fatalf("the opkg file record was ignored: %+v", got)
	}
	if got.Version != "" {
		t.Fatalf("a version was invented from a file that does not contain "+
			"one: %+v", got)
	}
	// And with no version there is nothing to compare, so nothing is claimed.
	if got.DriftedFrom("1.0.4") {
		t.Fatal("drift was reported against a version we never read")
	}
}

func TestAFailingToolDoesNotClaimAnOrigin(t *testing.T) {
	env := &fakeEnv{
		have: map[string]bool{"apk": true},
		fail: map[string]bool{"apk list --installed xwrt": true},
	}
	if got := env.detect(); got.Managed() {
		t.Fatalf("apk exited non-zero and the result was used anyway: %+v", got)
	}
}

func TestTrimRelease(t *testing.T) {
	cases := map[string]string{
		"1.0.3-r1":  "1.0.3",
		"1.0.3-1":   "1.0.3",
		"1.0.3-r12": "1.0.3",
		"1.0.3":     "1.0.3",
		// Not a release suffix: leave it alone rather than truncating a
		// version that genuinely has a dash in it.
		"1.0.3-beta": "1.0.3-beta",
		"1.0.3-r":    "1.0.3-r",
		"":           "",
	}
	for in, want := range cases {
		if got := trimRelease(in); got != want {
			t.Errorf("trimRelease(%q) = %q, want %q", in, got, want)
		}
	}
}
