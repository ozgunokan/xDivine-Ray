package daemon

import (
	"os/exec"
	"syscall"
)

// detach puts a command in a process group of its own.
//
// It matters for exactly one command: the installer. Stopping this service
// signals the whole process group, and the installer is the one thing that
// must survive being stopped by what it is installing.
func detach(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setsid = true
}
