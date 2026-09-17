package app

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"xwrt/internal/api"
	"xwrt/internal/daemon"
	"xwrt/internal/netenv"
	"xwrt/internal/ucicfg"
)

// RunDaemon starts the daemon and blocks until it is told to stop. argv is the
// argument list with the program name removed.
func RunDaemon(argv []string) int {
	fs := flag.NewFlagSet("xwrtd", flag.ContinueOnError)
	var (
		showVersion = fs.Bool("version", false, "print the version and exit")
		apiPort     = fs.Int("port", 0, "override the API port from UCI")
		noRestore   = fs.Bool("no-restore", false, "do not reconnect at startup")
	)
	if err := fs.Parse(argv); err != nil {
		return 2
	}
	if *showVersion {
		fmt.Println(Version)
		return 0
	}

	uci := ucicfg.New()
	store := ucicfg.NewStore(uci)
	// 2000 lines is a few hundred kilobytes, which is a fair trade on a device
	// with at least 128 MB: it usually covers a whole connect/fail cycle, so a
	// user reporting a problem has the relevant output still in the buffer.
	// One version string, stamped by the build and shared with the daemon, so
	// the version the status reports is the binary that is actually running.
	daemon.Version = Version

	logRing := daemon.NewLogRing(2000)

	data, err := store.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "read configuration: %v\n", err)
		return 1
	}
	// A misspelled option in a hand-edited file is silently ignored by the
	// loader; saying so at startup is the difference between a five-minute fix
	// and an evening of wondering why a setting has no effect.
	for _, w := range data.Warnings {
		logRing.Warnf("%s", w)
	}

	port := data.Settings.APIPort
	if *apiPort > 0 {
		port = *apiPort
	}

	// The ring holds the system log connection, so it is closed last.
	defer logRing.Close()

	engine := daemon.New(store, uci, logRing)
	server := api.New(engine, store, logRing, port)

	logRing.Infof("xwrt %s starting (api on 127.0.0.1:%d, config backend: %s)",
		Version, port, uciBackend(uci))

	// Check the device against the supported floor once, at startup, rather
	// than letting an under-specified device fail later in a way that looks
	// like a bug in the proxy.
	env := netenv.Detect(netenv.Overrides{})
	// These are warnings rather than errors: nothing has failed yet, and a
	// permanent failure banner on the status page would be noise. They still
	// reach the system log, so a later "the core was killed" has context.
	if env.LowMemory() {
		logRing.Warnf("this device reports %d MB of RAM; xwrt targets %d MB and above, "+
			"so expect the core to be killed under load",
			env.MemTotalMB, netenv.MinMemoryMB)
	}
	if env.LowStorage() {
		logRing.Warnf("the filesystem holding xwrt is %d MB; xwrt targets %d MB and above. "+
			"Use extroot or a USB disk, or expect installs and upgrades to fail",
			env.StorageTotalMB, netenv.MinStorageMB)
	}

	// Clear anything a previous run left behind before doing anything else. A
	// daemon that was killed rather than stopped leaves capture rules pointing
	// at a core that no longer exists, and those rules would black-hole the LAN
	// for as long as they stay installed.
	engine.ClearStaleState()

	errCh := make(chan error, 1)
	go func() { errCh <- server.ListenAndServe() }()

	// Asks once, a couple of minutes in, and then daily — and only if the
	// setting allows it. Nothing is installed by this; it only makes the
	// answer available to whoever looks.
	engine.WatchUpdates()

	if !*noRestore {
		// Restoring blocks waiting for the WAN, so it must not hold up the
		// API server.
		go engine.Restore()
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	for {
		select {
		case err := <-errCh:
			if err != nil {
				fmt.Fprintf(os.Stderr, "api server: %v\n", err)
				return 1
			}
			return 0

		case sig := <-sigCh:
			if sig == syscall.SIGHUP {
				// procd sends SIGHUP when /etc/config/xwrt changes, which is
				// also how LuCI applies a settings edit. Reconnecting is the
				// only way a new port or mode can take effect.
				logRing.Infof("reload requested")
				// Reload reconnects, and a failed connect has already filed
				// itself with the step it failed at; this only records that the
				// trigger was a reload.
				if err := engine.Reload(); err != nil {
					logRing.Warnf("reload did not complete: %v", err)
				}
				continue
			}
			logRing.Infof("shutting down")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = server.Shutdown(ctx)
			cancel()
			// Tearing down on exit is deliberate: leaving capture rules
			// pointing at a core that is no longer running would black-hole
			// the LAN.
			engine.Close()
			return 0
		}
	}
}

func uciBackend(u *ucicfg.UCI) string {
	if u.UsesBinary() {
		return "uci"
	}
	return "file"
}
