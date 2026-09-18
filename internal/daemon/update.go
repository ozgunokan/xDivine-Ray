package daemon

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"xwrt/internal/fault"
	"xwrt/internal/model"
	"xwrt/internal/update"
)

// Updating this daemon from inside this daemon.
//
// The awkward part is not the download. It is that installing an update stops
// the service — which is this process — so whatever runs the installer cannot
// be a child of it, or it dies halfway through with the binary replaced and
// the service down. That is the same shape as every other way this project has
// taken someone's internet away, and it is handled the same way: the installer
// is detached into a session of its own before anything is touched, it writes
// to a log that survives, and the installer's own safety net (a pre-flight
// version check, a kept copy of the previous binary, a rollback, and
// reconnecting a tunnel that was up) does the rest.
//
// Nothing here installs anything on its own. The check runs on a timer; the
// install runs when someone asks for it.

// UpdateInfo is what the interface shows and the CLI prints.
type UpdateInfo struct {
	// Current is the running daemon's version, so a caller comparing them does
	// not have to ask twice.
	Current string `json:"current"`
	Latest  string `json:"latest,omitempty"`
	// Available is the answer to the only question most callers have.
	Available   bool   `json:"available"`
	Installable bool   `json:"installable"`
	Notes       string `json:"notes,omitempty"`
	URL         string `json:"url,omitempty"`
	AssetName   string `json:"asset_name,omitempty"`
	Size        int64  `json:"size,omitempty"`
	CheckedAt   string `json:"checked_at,omitempty"`
	// Deliberately not called "error". Every RPC answer goes through one
	// unwrapper in the interface, and that unwrapper treats an "error" key as a
	// call that failed — so a check that merely could not reach GitHub was
	// raised as a broken request, in English, in a red box, while the page
	// underneath still said no check had been made. It is a fact about the last
	// check, not a failed call, and it is named like one.
	CheckError string   `json:"check_error,omitempty"`
	ErrorCode  string   `json:"error_code,omitempty"`
	ErrorArgs  []string `json:"error_args,omitempty"`
	// Installing is set while an install is running, so the interface can say
	// so rather than offering the button twice.
	Installing bool   `json:"installing"`
	LogPath    string `json:"log_path,omitempty"`
	// How xwrt got onto this device: "apk", "opkg", or empty for a bundle.
	//
	// It is here because it changes what pressing the install button means. On
	// a bundle install, replacing /usr/sbin/xwrt is the whole operation. On a
	// package install it is a file apk believes it owns at a version it
	// records, and the next sysupgrade puts the old one back without saying so.
	// The button still works — someone building their own images can decide
	// that for themselves — but they are told before they press it, not weeks
	// later when the update appears to have undone itself.
	Origin        string `json:"origin,omitempty"`
	OriginVersion string `json:"origin_version,omitempty"`
	Managed       bool   `json:"managed"`
	// Drifted means it has already happened: the binary running is not the one
	// the package manager has on record.
	Drifted bool `json:"drifted"`
}

// updateInterval is how often the daemon looks, when looking is enabled. Once
// a day: a release is not urgent, and a router that asks more often is one
// more anonymous request against a shared rate limit.
const updateInterval = 24 * time.Hour

// updateLog is where a running install writes. Outside /tmp would survive a
// reboot, but an install that needs a reboot to be diagnosed has already gone
// so wrong that the log is not the problem.
const updateLog = "/tmp/xwrt-update.log"

type updater struct {
	mu         sync.Mutex
	info       UpdateInfo
	installing bool
	// The package manager is asked once rather than on every poll: the About
	// page polls this several times a minute, and each answer costs a
	// subprocess. It is refreshed on an explicit check, which is the only
	// moment it can plausibly have changed.
	origin      update.Origin
	originKnown bool
}

// origin answers how xwrt was installed, asking the device only the first time.
//
// The lock is not held across the question. Asking runs apk or opkg, which on a
// busy router is not instant, and every status poll would queue behind it.
func (e *Engine) origin() update.Origin {
	e.updater.mu.Lock()
	o, known := e.updater.origin, e.updater.originKnown
	e.updater.mu.Unlock()
	if known {
		return o
	}
	o = update.Installed()
	e.updater.mu.Lock()
	e.updater.origin, e.updater.originKnown = o, true
	e.updater.mu.Unlock()
	return o
}

// refreshOrigin asks again. Called from an explicit check, because that is when
// someone is looking at the answer — and because an install done through the
// package manager since boot would otherwise never be noticed.
func (e *Engine) refreshOrigin() {
	o := update.Installed()
	e.updater.mu.Lock()
	e.updater.origin, e.updater.originKnown = o, true
	e.updater.mu.Unlock()
}

// withOrigin fills in the install-source fields. Every path that hands an
// UpdateInfo to a caller goes through it, so the interface cannot receive one
// that has an answer about releases but no answer about this device.
func (e *Engine) withOrigin(info UpdateInfo) UpdateInfo {
	o := e.origin()
	info.Origin = o.Manager
	info.OriginVersion = o.Version
	info.Managed = o.Managed()
	info.Drifted = o.DriftedFrom(Version)
	return info
}

// Update returns the last thing the daemon learned about releases.
func (e *Engine) Update() UpdateInfo {
	e.updater.mu.Lock()
	info := e.updater.info
	info.Current = Version
	info.Installing = e.updater.installing
	if info.Installing {
		info.LogPath = updateLog
	}
	e.updater.mu.Unlock()
	// Outside the lock: asking the package manager runs a subprocess, and
	// holding the updater's lock across it would stall every other caller.
	return e.withOrigin(info)
}

// CheckUpdate asks now, rather than waiting for the timer.
func (e *Engine) CheckUpdate(ctx context.Context) UpdateInfo {
	data, err := e.store.Load()
	repo := ""
	if err == nil {
		repo = data.Settings.UpdateRepo
	}
	if strings.TrimSpace(repo) == "" {
		repo = model.DefaultUpdateRepo
	}

	info := UpdateInfo{Current: Version, CheckedAt: time.Now().Format(time.RFC3339)}
	rel, err := update.Latest(ctx, repo)
	if err != nil {
		info.CheckError = err.Error()
		if code, args, ok := fault.CodeOf(err); ok {
			info.ErrorCode, info.ErrorArgs = code, args
		}
	} else {
		info.Latest = rel.Version
		info.Notes = rel.Notes
		info.URL = rel.URL
		info.AssetName = rel.AssetName
		info.Size = rel.Size
		info.Available = update.Newer(rel.Version, Version)
		info.Installable = rel.Installable()
		if info.Available && !info.Installable {
			// Worth saying rather than showing a button that cannot work. A
			// release with no bundle for this architecture, or with no
			// checksum file, is not installable from here by design.
			info.CheckError = fmt.Sprintf(
				"%s is out, but it has no verifiable bundle for %s, so it "+
					"cannot be installed from here", rel.Version, update.Arch())
			info.ErrorCode = "update.not_installable"
			info.ErrorArgs = []string{rel.Version, update.Arch()}
		}
	}

	e.updater.mu.Lock()
	e.updater.info = info
	e.updater.mu.Unlock()
	e.refreshOrigin()

	if info.Available {
		e.Log.Infof("a newer version is available: %s (running %s)",
			info.Latest, Version)
	}
	return e.Update()
}

// WatchUpdates checks once at startup and then daily, unless the setting says
// not to. It returns immediately; the work is on its own goroutine.
func (e *Engine) WatchUpdates() {
	go func() {
		// Not at the very moment of boot: the WAN is usually still coming up,
		// and a failed check would be the first thing in a fresh log.
		if !e.sleep(2 * time.Minute) {
			return
		}
		for {
			data, err := e.store.Load()
			if err == nil && data.Settings.UpdateCheck {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				e.CheckUpdate(ctx)
				cancel()
			}
			if !e.sleep(updateInterval) {
				return
			}
		}
	}()
}

// InstallUpdate downloads the newest release, verifies it, and hands it to the
// bundle's own installer — detached, because the installer stops this service.
//
// It returns as soon as the installer has been started. There is nothing
// useful to wait for: this process is about to be stopped by what it just
// started.
func (e *Engine) InstallUpdate() error {
	// Its own deadline rather than the caller's. The request that asked for
	// this is going to be cut off partway through — the installer stops the
	// service, which closes the connection the answer would have travelled on —
	// so a context tied to that request would cancel the download it just
	// started.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	e.updater.mu.Lock()
	if e.updater.installing {
		e.updater.mu.Unlock()
		return fmt.Errorf("an update is already being installed; see %s", updateLog)
	}
	e.updater.installing = true
	e.updater.mu.Unlock()

	done := func(err error) error {
		if err != nil {
			e.updater.mu.Lock()
			e.updater.installing = false
			e.updater.info.CheckError = err.Error()
			if code, args, ok := fault.CodeOf(err); ok {
				e.updater.info.ErrorCode, e.updater.info.ErrorArgs = code, args
			}
			e.updater.mu.Unlock()
			e.record(fail(model.StepConfig, "update.failed", err))
		}
		return err
	}

	data, err := e.store.Load()
	repo := model.DefaultUpdateRepo
	if err == nil && strings.TrimSpace(data.Settings.UpdateRepo) != "" {
		repo = data.Settings.UpdateRepo
	}

	rel, err := update.Latest(ctx, repo)
	if err != nil {
		return done(err)
	}
	if !update.Newer(rel.Version, Version) {
		return done(fmt.Errorf("%s is already the newest version", Version))
	}
	if !rel.Installable() {
		return done(fmt.Errorf("release %s carries no verifiable bundle for %s",
			rel.Version, update.Arch()))
	}

	// Said once, here, where it ends up in the system log next to the install
	// itself. The interface says it too, before the button is pressed; this is
	// for whoever reads the log afterwards wondering why a version they
	// installed is not the one running any more.
	if o := e.origin(); o.Managed() {
		e.Log.Warnf("this xwrt was installed by %s, which has %s on record. "+
			"Replacing the binary here leaves %s's database wrong, and a "+
			"sysupgrade or package upgrade will put %s back.",
			o.Manager, o.Version, o.Manager, o.Version)
	}

	// Somewhere with room. /tmp is RAM on most routers and a bundle is a few
	// megabytes, which is why the size is checked against what is free rather
	// than assumed to fit.
	dir := filepath.Join(os.TempDir(), "xwrt-update")
	os.RemoveAll(dir)

	e.Log.Infof("downloading %s (%s)", rel.Version, rel.AssetName)
	path, err := update.Fetch(ctx, rel, dir)
	if err != nil {
		return done(err)
	}

	unpacked := filepath.Join(dir, "unpacked")
	if err := os.MkdirAll(unpacked, 0o700); err != nil {
		return done(err)
	}
	if out, err := exec.Command("tar", "xzf", path, "-C", unpacked).CombinedOutput(); err != nil {
		return done(fmt.Errorf("could not unpack the bundle: %v: %s",
			err, strings.TrimSpace(string(out))))
	}

	// The bundle unpacks into one directory named for the architecture.
	entries, err := os.ReadDir(unpacked)
	if err != nil {
		return done(err)
	}
	root := ""
	for _, ent := range entries {
		if ent.IsDir() {
			root = filepath.Join(unpacked, ent.Name())
			break
		}
	}
	if root == "" {
		return done(fmt.Errorf("the bundle has no package directory in it"))
	}
	installer := filepath.Join(root, "install.sh")
	if _, err := os.Stat(installer); err != nil {
		return done(fmt.Errorf("the bundle has no install.sh"))
	}

	// And now the part that has to outlive this process.
	//
	// setsid puts the installer in a session of its own, so stopping this
	// service — which install.sh does, a second from now — does not take the
	// installer with it. Without that, the daemon is killed mid-install with
	// its binary half replaced, which is the exact failure this whole project
	// keeps being careful about.
	script := fmt.Sprintf("cd %q && sh install.sh >>%q 2>&1", root, updateLog)
	cmd := exec.Command("setsid", "sh", "-c", script)
	if _, err := exec.LookPath("setsid"); err != nil {
		// Without setsid, detach as far as a shell can: ignore the signals
		// that would arrive when this process's group is stopped.
		cmd = exec.Command("sh", "-c", "trap '' HUP INT TERM; "+script)
	}
	cmd.Stdout, cmd.Stderr = nil, nil
	cmd.Dir = root
	// Explicitly not inheriting this process's group, so a stop of the service
	// does not reach it.
	detach(cmd)

	if err := os.WriteFile(updateLog,
		[]byte(fmt.Sprintf("xwrt update %s -> %s, %s\n",
			Version, rel.Version, time.Now().Format(time.RFC3339))), 0o644); err != nil {
		e.Log.Warnf("could not open the update log: %v", err)
	}
	if err := cmd.Start(); err != nil {
		return done(fmt.Errorf("could not start the installer: %w", err))
	}
	// Not waited for on purpose: it is going to stop this process.
	go func() { _ = cmd.Wait() }()

	e.Log.Infof("installing %s; the service will restart. Progress: %s",
		rel.Version, updateLog)
	return nil
}
