package update

import (
	"os"
	"os/exec"
	"strings"
)

// How xwrt got onto this device, and why anyone should care.
//
// There are two ways it arrives: unpacked from a bundle by install.sh, or
// installed by the device's package manager because it was built into the
// firmware from the feed. The daemon behaves identically either way — until
// someone presses the update button.
//
// The updater replaces /usr/sbin/xwrt. On a bundle install that is the whole
// story. On a package install it is a file that apk (or opkg) believes it owns
// and believes is a particular version. Replacing it works, and the new version
// runs, but the package database is now wrong: it still records the old
// version, `apk list -I` still reports it, and the next sysupgrade or package
// upgrade quietly puts the old binary back. Nothing warns anyone, and the
// symptom arrives weeks later as "the update undid itself".
//
// So the origin is detected and shown. The button still works — someone who
// builds their own images knows what they are doing, and taking the choice away
// from them would be worse than the drift. But they are told, once, in the
// place where they are about to make the decision.
//
// Detection asks the package manager rather than guessing from file paths,
// because the package manager is the thing whose opinion will matter later.
type Origin struct {
	// Manager is "apk", "opkg", or empty when nothing owns the binary — which
	// means it came from a bundle.
	Manager string
	// Version is what that manager has on record, with the packaging suffix
	// removed: apk reports 1.0.3-r1 and opkg 1.0.3-1, and neither is what the
	// binary prints.
	Version string
}

// Managed is the question every caller actually has.
func (o Origin) Managed() bool { return o.Manager != "" }

// DriftedFrom reports whether the running version and the package manager's
// record disagree — which is exactly what an in-place update leaves behind, and
// what a sysupgrade will later undo.
func (o Origin) DriftedFrom(running string) bool {
	if !o.Managed() || o.Version == "" || running == "" {
		return false
	}
	return o.Version != running
}

// opkgList is the file opkg writes listing what a package installed. It is
// consulted when the opkg binary itself cannot be run, so that a device whose
// PATH is unusual is not misreported as a bundle install.
const opkgList = "/usr/lib/opkg/info/xwrt.list"

// Installed works out how the running xwrt got here.
func Installed() Origin {
	return detect(exec.LookPath, runTool, func(path string) bool {
		_, err := os.Stat(path)
		return err == nil
	})
}

func runTool(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).Output()
	return string(out), err
}

// detect is the testable half: every way it reaches the outside world is an
// argument, because a test cannot install a package manager.
func detect(
	lookPath func(string) (string, error),
	run func(string, ...string) (string, error),
	exists func(string) bool,
) Origin {
	// apk first: it is the newer of the two, and a device that has both (an
	// upgraded one) is managed by apk.
	if _, err := lookPath("apk"); err == nil {
		if out, err := run("apk", "list", "--installed", "xwrt"); err == nil {
			if v := apkVersion(out); v != "" {
				return Origin{Manager: "apk", Version: v}
			}
		}
	}

	if _, err := lookPath("opkg"); err == nil {
		if out, err := run("opkg", "status", "xwrt"); err == nil {
			if v := opkgVersion(out); v != "" {
				return Origin{Manager: "opkg", Version: v}
			}
		}
	}

	// The tool could not be run, but opkg's own record of what it installed is
	// a file, and it is still there. Without a version — guessing one would be
	// worse than admitting to none.
	if exists(opkgList) {
		return Origin{Manager: "opkg"}
	}

	return Origin{}
}

// apkVersion reads the version out of a line like
//
//	xwrt-1.0.3-r1 aarch64_cortex-a53 {xwrt} (MIT) [installed]
//
// Only lines for this exact package count: `apk list` matches loosely enough
// that a package merely depending on xwrt can appear in the same output.
func apkVersion(out string) string {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		name := line
		if i := strings.IndexByte(name, ' '); i >= 0 {
			name = name[:i]
		}
		rest, ok := strings.CutPrefix(name, "xwrt-")
		if !ok {
			continue
		}
		// The first character after the name has to start a version, or this
		// is a different package whose name happens to begin with ours —
		// luci-app-xwrt does not, but xwrt-extras would.
		if rest == "" || rest[0] < '0' || rest[0] > '9' {
			continue
		}
		// Not every listing says [installed]; when it does say something else,
		// believe it.
		if strings.Contains(line, "[") && !strings.Contains(line, "[installed]") {
			continue
		}
		return trimRelease(rest)
	}
	return ""
}

// opkgVersion reads `opkg status xwrt`, which is a block of Key: value lines.
// A package that is known but not installed has a Status line saying so, and
// reporting it as installed would put a warning on a device that does not need
// one.
func opkgVersion(out string) string {
	version, installed := "", false
	for _, line := range strings.Split(out, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.TrimSpace(key) {
		case "Package":
			if value != "xwrt" {
				return ""
			}
		case "Version":
			version = trimRelease(value)
		case "Status":
			// "install ok installed" against "unknown ok not-installed": the
			// state is the last of three fields, and a substring test for
			// "installed" matches both of them. It did, and a device that had
			// only ever seen the package in a feed listing was told its binary
			// was owned by opkg.
			fields := strings.Fields(value)
			installed = len(fields) > 0 && fields[len(fields)-1] == "installed"
		}
	}
	if !installed {
		return ""
	}
	return version
}

// trimRelease removes the packaging suffix. PKG_RELEASE is appended as -1 by
// opkg and as -r1 by apk, and neither is part of the version the binary
// prints — so leaving it on makes every managed install look like it has
// drifted.
func trimRelease(v string) string {
	i := strings.LastIndexByte(v, '-')
	if i <= 0 {
		return v
	}
	suffix := v[i+1:]
	suffix = strings.TrimPrefix(suffix, "r")
	if suffix == "" {
		return v
	}
	for _, c := range suffix {
		if c < '0' || c > '9' {
			return v
		}
	}
	return v[:i]
}
