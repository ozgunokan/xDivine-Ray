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

	// What a running install is doing, for the window that shows it. Stage is
	// one of the Stage* names below; Target is the version being installed;
	// Done and Total are the download in bytes, Total 0 when unknown.
	//
	// These describe this process's part of the install only. The last part —
	// the installer stopping this service and starting the new one — cannot be
	// reported by the thing being replaced, and the interface infers it from
	// the daemon going quiet and coming back with a different version.
	Stage  string `json:"stage,omitempty"`
	Target string `json:"target,omitempty"`
	Done   int64  `json:"done,omitempty"`
	Total  int64  `json:"total,omitempty"`
}

// The stages of an install, in order, as the interface names them.
const (
	StageChecking    = "checking"
	StageDownloading = "downloading"
	StageVerifying   = "verifying"
	StageUnpacking   = "unpacking"
	StageInstalling  = "installing"
	StageFailed      = "failed"
)

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

	// Progress, kept apart from info on purpose. The daily check replaces info
	// wholesale, and a check that lands in the middle of an install must not
	// wipe out what the install is showing.
	stage       string
	target      string
	done, total int64
}

// setStage records where the install has got to.
func (e *Engine) setStage(stage string) {
	e.updater.mu.Lock()
	e.updater.stage = stage
	e.updater.mu.Unlock()
}

// Update returns the last thing the daemon learned about releases.
func (e *Engine) Update() UpdateInfo {
	e.updater.mu.Lock()
	defer e.updater.mu.Unlock()
	info := e.updater.info
	info.Current = Version
	info.Installing = e.updater.installing
	if info.Installing {
		info.LogPath = updateLog
	}
	info.Stage = e.updater.stage
	info.Target = e.updater.target
	info.Done, info.Total = e.updater.done, e.updater.total
	return info
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
	if err := e.claimInstall(); err != nil {
		return err
	}
	return e.runInstall()
}

// StartUpdate is InstallUpdate for a caller that will not wait: it claims the
// install, starts it, and returns. The interface uses this, because the
// download can take longer than a browser's RPC call is allowed to, and a call
// that times out there reads as a failure while the install carries on
// regardless. The interface asks Update for progress instead.
func (e *Engine) StartUpdate() error {
	if err := e.claimInstall(); err != nil {
		return err
	}
	go func() { _ = e.runInstall() }()
	return nil
}

// errAlreadyInstalling is its own value so the API can answer it as a conflict
// rather than a failure: the install the caller wanted is already happening.
var errAlreadyInstalling = fmt.Errorf("an update is already being installed; see %s", updateLog)

// IsAlreadyInstalling reports whether err is the conflict above.
func IsAlreadyInstalling(err error) bool { return err == errAlreadyInstalling }

func (e *Engine) claimInstall() error {
	e.updater.mu.Lock()
	defer e.updater.mu.Unlock()
	if e.updater.installing {
		return errAlreadyInstalling
	}
	e.updater.installing = true
	e.updater.stage = StageChecking
	e.updater.target = ""
	e.updater.done, e.updater.total = 0, 0
	// A failure from an earlier attempt is not this attempt's failure.
	e.updater.info.CheckError = ""
	e.updater.info.ErrorCode, e.updater.info.ErrorArgs = "", nil
	return nil
}

func (e *Engine) runInstall() error {
	// Its own deadline rather than the caller's. The request that asked for
	// this is going to be cut off partway through — the installer stops the
	// service, which closes the connection the answer would have travelled on —
	// so a context tied to that request would cancel the download it just
	// started.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	done := func(err error) error {
		if err != nil {
			e.updater.mu.Lock()
			e.updater.installing = false
			e.updater.stage = StageFailed
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
	e.updater.mu.Lock()
	e.updater.target = rel.Version
	e.updater.total = rel.Size
	e.updater.mu.Unlock()

	// Somewhere with room. /tmp is RAM on most routers and a bundle is a few
	// megabytes, which is why the size is checked against what is free rather
	// than assumed to fit.
	dir := filepath.Join(os.TempDir(), "xwrt-update")
	os.RemoveAll(dir)

	e.Log.Infof("downloading %s (%s)", rel.Version, rel.AssetName)
	path, err := update.FetchWith(ctx, rel, dir, update.Progress{
		Stage: e.setStage,
		Bytes: func(n, total int64) {
			e.updater.mu.Lock()
			e.updater.done = n
			if total > 0 {
				e.updater.total = total
			}
			e.updater.mu.Unlock()
		},
	})
	if err != nil {
		return done(err)
	}
	e.setStage(StageUnpacking)

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
	e.setStage(StageInstalling)

	e.Log.Infof("installing %s; the service will restart. Progress: %s",
		rel.Version, updateLog)
	return nil
}

// updateLogPath is the log a detached install writes to; a variable only so a
// test can point it somewhere of its own.
var updateLogPath = updateLog

// UpdateLog returns the last lines an install wrote.
//
// This is what the interface shows when an update did not end on the version
// it was going to. The installer runs detached and outlives the process that
// started it, so its log is the only account there is of what happened in the
// part nobody could watch — and a rollback, in particular, is worth reading
// rather than guessing at.
func UpdateLog(lines int) string {
	b, err := os.ReadFile(updateLogPath)
	if err != nil {
		return ""
	}
	text := strings.TrimRight(string(b), "\n")
	all := strings.Split(text, "\n")
	if lines > 0 && len(all) > lines {
		all = all[len(all)-lines:]
	}
	return strings.Join(all, "\n")
}
