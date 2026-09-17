// Package proc supervises the external processes the daemon drives: the Xray
// core and, in TUN mode, hev-socks5-tunnel.
//
// Supervision is deliberately simple. A crashed core is restarted with a
// backoff, output is streamed into a callback so it can reach the log ring, and
// Stop is synchronous so a reconnect never races the old process.
package proc

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Process is a supervised child process.
type Process struct {
	// Name appears in log lines.
	Name string
	// Path is the executable, resolved through PATH when not absolute.
	Path string
	Args []string
	Env  []string
	Dir  string

	// OnLog receives each line the process writes to stdout or stderr.
	OnLog func(line string)
	// OnExit is called when the process exits, before any restart. intentional
	// is true when the exit follows a Stop, which is the difference between "we
	// shut it down" and "it died" — and only the second is a failure worth
	// reporting.
	OnExit func(err error, intentional bool)

	// MinRestartDelay and MaxRestartDelay bound the restart backoff.
	MinRestartDelay time.Duration
	MaxRestartDelay time.Duration

	mu      sync.Mutex
	cmd     *exec.Cmd
	cancel  context.CancelFunc
	running bool
	stopped bool
	done    chan struct{}
	lastErr error
	exits   int

	// tail keeps the process's most recent output. When a core exits, the
	// reason is almost always in its own last lines rather than in the exit
	// status, so those lines have to be available at the moment of failure.
	tail     []string
	tailNext int
	tailFull bool
}

// tailSize is how many output lines are kept for failure reporting. Enough to
// carry a core's startup complaint and its stack of causes, small enough to be
// free.
const tailSize = 30

// Start launches the process and supervises it until Stop is called. It returns
// once the process has been spawned, or with the error that stopped it from
// being spawned at all.
func (p *Process) Start() error {
	p.mu.Lock()
	if p.running {
		p.mu.Unlock()
		return errors.New(p.Name + " is already running")
	}
	if p.MinRestartDelay == 0 {
		p.MinRestartDelay = time.Second
	}
	if p.MaxRestartDelay == 0 {
		p.MaxRestartDelay = 30 * time.Second
	}

	// Fail fast on a missing binary rather than discovering it inside the
	// supervision loop, where the error would look like a crash loop.
	if _, err := exec.LookPath(p.Path); err != nil {
		p.mu.Unlock()
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.stopped = false
	p.running = true
	p.done = make(chan struct{})

	// The lock is released before waiting: the supervisor takes it to publish
	// the command it started, so holding it here would deadlock the two against
	// each other on every single start.
	p.mu.Unlock()

	started := make(chan error, 1)
	go p.supervise(ctx, started)

	err := <-started
	if err != nil {
		cancel()
		p.setRunning(false)
	}
	return err
}

func (p *Process) supervise(ctx context.Context, started chan<- error) {
	defer close(p.done)
	delay := p.MinRestartDelay
	first := true

	for {
		cmd := exec.CommandContext(ctx, p.Path, p.Args...)
		cmd.Env = p.Env
		cmd.Dir = p.Dir
		// A process group lets Stop take down anything the child spawned.
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

		stdout, err := cmd.StdoutPipe()
		if err != nil {
			if first {
				started <- err
				return
			}
			p.finish(err)
			return
		}
		stderr, err := cmd.StderrPipe()
		if err != nil {
			if first {
				started <- err
				return
			}
			p.finish(err)
			return
		}

		if err := cmd.Start(); err != nil {
			if first {
				p.setRunning(false)
				started <- err
				return
			}
			p.finish(err)
			return
		}

		p.mu.Lock()
		p.cmd = cmd
		p.mu.Unlock()

		if first {
			started <- nil
			first = false
		}

		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); p.pump(stdout) }()
		go func() { defer wg.Done(); p.pump(stderr) }()
		wg.Wait()

		waitErr := cmd.Wait()

		p.mu.Lock()
		stopped := p.stopped
		p.lastErr = waitErr
		if !stopped {
			// Counted, not just logged: "has it died since I started it?" is a
			// question the caller has to be able to ask. Running() cannot answer
			// it, because a process in a crash loop is restarted and reads as
			// running again a second later.
			p.exits++
		}
		p.mu.Unlock()

		if p.OnExit != nil {
			p.OnExit(waitErr, stopped || ctx.Err() != nil)
		}
		if stopped || ctx.Err() != nil {
			p.setRunning(false)
			return
		}

		// Crash: back off, then try again.
		select {
		case <-ctx.Done():
			p.setRunning(false)
			return
		case <-time.After(delay):
		}
		delay *= 2
		if delay > p.MaxRestartDelay {
			delay = p.MaxRestartDelay
		}
	}
}

func (p *Process) finish(err error) {
	p.mu.Lock()
	p.lastErr = err
	p.running = false
	stopped := p.stopped
	p.mu.Unlock()
	if p.OnExit != nil {
		p.OnExit(err, stopped)
	}
}

func (p *Process) setRunning(v bool) {
	p.mu.Lock()
	p.running = v
	p.mu.Unlock()
}

func (p *Process) pump(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 8*1024), 256*1024)
	for sc.Scan() {
		line := sc.Text()
		p.recordTail(line)
		if p.OnLog != nil {
			p.OnLog(line)
		}
	}
}

func (p *Process) recordTail(line string) {
	if strings.TrimSpace(line) == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.tail == nil {
		p.tail = make([]string, tailSize)
	}
	p.tail[p.tailNext] = line
	p.tailNext = (p.tailNext + 1) % tailSize
	if p.tailNext == 0 {
		p.tailFull = true
	}
}

// RecentOutput returns the last lines the process wrote, oldest first. It is
// what turns "the core exited with status 23" into something diagnosable.
func (p *Process) RecentOutput(n int) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.tail == nil {
		return nil
	}

	var out []string
	if p.tailFull {
		out = append(out, p.tail[p.tailNext:]...)
		out = append(out, p.tail[:p.tailNext]...)
	} else {
		out = append(out, p.tail[:p.tailNext]...)
	}
	// Drop unused slots.
	cleaned := out[:0]
	for _, l := range out {
		if l != "" {
			cleaned = append(cleaned, l)
		}
	}
	if n > 0 && len(cleaned) > n {
		cleaned = cleaned[len(cleaned)-n:]
	}
	return cleaned
}

// Stop terminates the process and waits for the supervisor to finish.
func (p *Process) Stop() {
	p.mu.Lock()
	if !p.running {
		p.mu.Unlock()
		return
	}
	p.stopped = true
	cancel := p.cancel
	cmd := p.cmd
	done := p.done
	p.mu.Unlock()

	// SIGTERM first so the core can close its listeners cleanly, and to the
	// whole process group rather than just the child.
	//
	// The group matters more than it looks. A child that spawned helpers leaves
	// them holding the output pipes open when it dies, and the supervisor waits
	// on those pipes — so signalling only the child can hang a disconnect for as
	// long as the grandchild lives. Setpgid was set at start precisely so this
	// signal can reach all of them.
	signalGroup(cmd, syscall.SIGTERM)

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		signalGroup(cmd, syscall.SIGKILL)
		if cancel != nil {
			cancel() // escalates to SIGKILL via CommandContext
		}
		select {
		case <-done:
		case <-time.After(3 * time.Second):
		}
	}
	if cancel != nil {
		cancel()
	}
	p.setRunning(false)
}

// signalGroup sends sig to the child's process group, falling back to the child
// alone if the group is gone.
func signalGroup(cmd *exec.Cmd, sig syscall.Signal) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	if pid <= 0 {
		return
	}
	if err := syscall.Kill(-pid, sig); err != nil {
		_ = cmd.Process.Signal(sig)
	}
}

// Running reports whether the process is currently supervised and alive.
func (p *Process) Running() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.running
}

// PID returns the current process ID, or 0.
func (p *Process) PID() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cmd != nil && p.cmd.Process != nil {
		return p.cmd.Process.Pid
	}
	return 0
}

// Exits returns how many times the process has died on its own. A caller that
// records this before a startup wait can tell an exited core from a slow one.
func (p *Process) Exits() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.exits
}

// LastError returns the most recent exit error.
func (p *Process) LastError() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastErr
}
