// Command xwrt is both the daemon and its command line client.
//
// Which one runs is decided by how the binary is invoked: as `xwrtd` (the
// installed symlink, which is what procd starts) or as `xwrt daemon` it is the
// daemon; anything else is a CLI command. Shipping one binary instead of two
// halves the flash footprint, which is what makes the package installable on
// 16 MB devices.
package main

import (
	"os"
	"path/filepath"
	"strings"

	"xwrt/internal/app"
)

func main() {
	argv := os.Args[1:]

	if isDaemonInvocation(os.Args[0], argv) {
		if len(argv) > 0 && argv[0] == "daemon" {
			argv = argv[1:]
		}
		os.Exit(app.RunDaemon(argv))
	}
	os.Exit(app.RunCLI(argv))
}

func isDaemonInvocation(arg0 string, argv []string) bool {
	if len(argv) > 0 && argv[0] == "daemon" {
		return true
	}
	name := filepath.Base(arg0)
	// A build or packaging step may add a suffix; match the prefix so
	// xwrtd, xwrtd-aarch64 and the like all start the daemon.
	return strings.HasPrefix(name, "xwrtd")
}
