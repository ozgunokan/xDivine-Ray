package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// What the install window reads while an update runs.

func TestAnInstallStartsAtCheckingWithAClearSlate(t *testing.T) {
	e := &Engine{Log: newTestRing(10)}
	// A failure left over from an earlier attempt.
	e.updater.info.CheckError = "the last one failed"
	e.updater.info.ErrorCode = "update.download_failed"
	e.updater.stage = StageFailed

	if err := e.claimInstall(); err != nil {
		t.Fatal(err)
	}
	u := e.Update()
	if u.Stage != StageChecking || !u.Installing {
		t.Fatalf("a fresh install should read as checking, got %q installing=%v",
			u.Stage, u.Installing)
	}
	// The window would otherwise open on the previous attempt's error.
	if u.CheckError != "" || u.ErrorCode != "" {
		t.Fatalf("an old failure survived into a new install: %q %q", u.CheckError, u.ErrorCode)
	}
}

// Two presses, or two tabs, must not start two installers.
func TestASecondInstallIsAConflictNotAFailure(t *testing.T) {
	e := &Engine{Log: newTestRing(10)}
	if err := e.claimInstall(); err != nil {
		t.Fatal(err)
	}
	err := e.claimInstall()
	if err == nil {
		t.Fatalf("a second install was allowed to start")
	}
	if !IsAlreadyInstalling(err) {
		t.Fatalf("the second install was refused, but not as a conflict: %v", err)
	}
}

// The daily check replaces the release information wholesale. If it lands in
// the middle of an install, the window must keep showing the install — not go
// blank halfway through a download because a timer went off.
func TestACheckDuringAnInstallDoesNotEraseItsProgress(t *testing.T) {
	e := &Engine{Log: newTestRing(10)}
	if err := e.claimInstall(); err != nil {
		t.Fatal(err)
	}
	e.updater.mu.Lock()
	e.updater.stage = StageDownloading
	e.updater.target = "1.0.26"
	e.updater.done, e.updater.total = 1200000, 3000000
	e.updater.mu.Unlock()

	// Exactly what CheckUpdate does with its result.
	e.updater.mu.Lock()
	e.updater.info = UpdateInfo{Latest: "1.0.26", Available: true}
	e.updater.mu.Unlock()

	u := e.Update()
	if u.Stage != StageDownloading || u.Target != "1.0.26" ||
		u.Done != 1200000 || u.Total != 3000000 {
		t.Fatalf("a check erased the install's progress: %+v", u)
	}
}

// --- the log, for when it did not end where it was going ---------------------

func TestTheLogTailIsTheEndOfTheLog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "update.log")
	var lines []string
	for i := 1; i <= 100; i++ {
		lines = append(lines, "line "+itoa(i))
	}
	os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644)

	old := updateLogPath
	updateLogPath = path
	defer func() { updateLogPath = old }()

	got := strings.Split(UpdateLog(5), "\n")
	if len(got) != 5 || got[0] != "line 96" || got[4] != "line 100" {
		t.Fatalf("want the last five lines, got %q", got)
	}
}

func TestNoLogIsNotAnError(t *testing.T) {
	old := updateLogPath
	updateLogPath = filepath.Join(t.TempDir(), "absent")
	defer func() { updateLogPath = old }()
	if got := UpdateLog(10); got != "" {
		t.Fatalf("want nothing, got %q", got)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	s := ""
	for ; i > 0; i /= 10 {
		s = string(rune('0'+i%10)) + s
	}
	return s
}
