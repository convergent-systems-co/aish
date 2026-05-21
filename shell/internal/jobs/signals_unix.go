//go:build !windows

// Package jobs — Unix signal-handling layer.
//
// Three responsibilities live here:
//
//  1. IgnoreShellSignals — the REPL goroutine ignores SIGINT, SIGQUIT,
//     SIGTSTP, SIGTTIN, SIGTTOU. Children inherit the default handlers
//     when they exec (the kernel resets ignored signals on exec only
//     for child processes that explicitly fork+exec via os/exec —
//     which is exactly our path).
//
//  2. TakeTTY / ReleaseTTY — wrappers over `tcsetpgrp(2)` (which Go's
//     stdlib does not expose). We use `golang.org/x/sys/unix.IoctlSetInt`
//     with `unix.TIOCSPGRP` against the controlling-TTY fd to hand the
//     terminal to a foreground job's pgrp, and to take it back on
//     return.
//
//  3. StartReaper — the long-lived SIGCHLD goroutine. Drains Wait4
//     results into the JobTable. Foreground pids are skipped so the
//     REPL's synchronous Wait owns them.
package jobs

import (
	"errors"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

// IgnoreShellSignals installs no-op handlers for the four signals a
// job-controlling shell must NOT receive: SIGINT, SIGQUIT, SIGTSTP,
// SIGTTIN, SIGTTOU. Children spawned via os/exec inherit DEFAULT
// behavior (the runtime resets ignored signals on fork+exec for the
// child), so this is safe at REPL startup.
//
// Returns a teardown function that restores the previous handlers.
// Called from Shell.Close() so non-interactive aish invocations
// (e.g. piped one-shots) don't leave global state altered.
//
// Implementation note: `signal.Ignore` on darwin/linux installs
// SIG_IGN at the C level, which IS inherited across fork — but
// os/exec calls execve immediately after fork, and execve resets
// ignored signals to default on the child. This is the standard
// POSIX shape and matches what bash does.
func IgnoreShellSignals() func() {
	sigs := []os.Signal{
		syscall.SIGINT,
		syscall.SIGQUIT,
		syscall.SIGTSTP,
		syscall.SIGTTIN,
		syscall.SIGTTOU,
	}
	signal.Ignore(sigs...)
	return func() { signal.Reset(sigs...) }
}

// TakeTTY hands the controlling terminal at fd over to process group
// pgid. Used by `fg` and the foreground-job path so the kernel routes
// Ctrl-C, Ctrl-Z, and TTY input/output signals to the job instead of
// the shell.
//
// Implementation: `ioctl(fd, TIOCSPGRP, &pgid)` — equivalent to the
// POSIX `tcsetpgrp(fd, pgid)` call. Go's stdlib does not wrap this;
// `golang.org/x/sys/unix.IoctlSetInt` does.
//
// Returns nil on success. On a non-TTY fd (the shell is running with
// a piped stdin, for example) the ioctl returns ENOTTY which we
// surface to the caller — fg/bg are no-ops in that case, by design.
func TakeTTY(fd int, pgid int) error {
	return unix.IoctlSetInt(fd, unix.TIOCSPGRP, pgid)
}

// ReleaseTTY restores the controlling terminal to the shell's own
// pgrp (ownPgid, typically the shell process's pgid). Caller MUST
// `defer ReleaseTTY` immediately after a successful TakeTTY, on
// every fg/bg path including panic recovery.
func ReleaseTTY(fd int, ownPgid int) error {
	return unix.IoctlSetInt(fd, unix.TIOCSPGRP, ownPgid)
}

// Reaper is the handle a Shell holds onto so it can deregister its
// JobTable from the package-global SIGCHLD goroutine on Close.
//
// We deliberately do NOT spawn a fresh goroutine per Shell — the
// kernel emits SIGCHLD process-wide, and `wait4(-1, ...)` races
// across goroutines for the same wait status. Multiple competing
// reapers therefore drop child events when more than one Shell
// exists in the same process (notably during `go test`, where many
// Shell instances coexist and their sleep-children share the parent
// pid). The singleton reaper, gated by registry membership, is the
// fix.
type Reaper struct {
	jt    *JobTable
	owner *globalReaper
}

// StartReaper registers jt with the package-global SIGCHLD reaper.
// The first call installs the signal handler and starts the
// goroutine; subsequent calls only add jt to the registry. Returns
// a *Reaper whose Stop deregisters jt (and tears down the goroutine
// when no JobTables remain). Idempotent.
func StartReaper(jt *JobTable) *Reaper {
	r := getGlobalReaper()
	r.register(jt)
	return &Reaper{jt: jt, owner: r}
}

// Stop deregisters this Shell's JobTable from the package-global
// reaper. Idempotent.
func (r *Reaper) Stop() {
	if r == nil || r.owner == nil {
		return
	}
	r.owner.deregister(r.jt)
	r.owner = nil
}

// globalReaper is the package-level singleton that owns the SIGCHLD
// signal channel and the goroutine. Multiple Shells share it.
type globalReaper struct {
	mu      sync.Mutex
	tables  map[*JobTable]struct{} // live registrations
	sigCh   chan os.Signal
	doneCh  chan struct{}
	running bool
}

var (
	reaperOnce sync.Once
	reaperInst *globalReaper
)

// getGlobalReaper lazily constructs the singleton.
func getGlobalReaper() *globalReaper {
	reaperOnce.Do(func() {
		reaperInst = &globalReaper{
			tables: make(map[*JobTable]struct{}),
		}
	})
	return reaperInst
}

// register adds jt to the live set and starts the goroutine if this
// is the first registration.
func (g *globalReaper) register(jt *JobTable) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.tables[jt] = struct{}{}
	if !g.running {
		g.sigCh = make(chan os.Signal, 8)
		g.doneCh = make(chan struct{})
		signal.Notify(g.sigCh, syscall.SIGCHLD)
		g.running = true
		go g.run()
	}
}

// deregister removes jt; tears down the goroutine on the last exit.
func (g *globalReaper) deregister(jt *JobTable) {
	g.mu.Lock()
	delete(g.tables, jt)
	stop := g.running && len(g.tables) == 0
	if stop {
		signal.Stop(g.sigCh)
		close(g.doneCh)
		g.running = false
	}
	g.mu.Unlock()
}

// run is the singleton's goroutine body. Exits on doneCh close.
func (g *globalReaper) run() {
	for {
		select {
		case <-g.sigCh:
			g.drain()
		case <-g.doneCh:
			return
		}
	}
}

// drain reaps every pending child status the kernel has buffered.
// Loops until Wait4 returns 0 (no more pending). Each reaped pid is
// attributed to the JobTable that registered it (we iterate every
// registered table and apply the update to whichever owns the pid).
// A pid that no registered table owns (e.g. the PTY child reaped by
// exec.RunPTY, or a child of a plugin we didn't register) is
// silently dropped.
//
// Foreground jobs receive the same status updates here as background
// jobs — the JobTable's notice channel suppresses foreground
// notifications, and the foreground-wait path polls the table
// directly, so race-free correctness is preserved without a
// separate foreground-vs-background reaping channel.
func (g *globalReaper) drain() {
	for {
		var status unix.WaitStatus
		var rusage unix.Rusage
		pid, err := unix.Wait4(-1, &status, unix.WNOHANG|unix.WUNTRACED|unix.WCONTINUED, &rusage)
		if pid == 0 {
			return
		}
		if err != nil {
			if errors.Is(err, syscall.ECHILD) || errors.Is(err, syscall.EINTR) {
				return
			}
			return
		}
		// Find the owning table. Most processes will have exactly
		// one Shell so this loop is effectively O(1); in `go test`
		// with N parallel-ish Shell instances it's O(N) per reap,
		// still negligible.
		g.mu.Lock()
		var target *JobTable
		var job Job
		for jt := range g.tables {
			if j, ok := jt.FindByPid(pid); ok {
				target = jt
				job = j
				break
			}
		}
		g.mu.Unlock()
		if target == nil {
			continue
		}
		switch {
		case status.Exited():
			target.SetStatus(job.ID, StatusDone, status.ExitStatus())
		case status.Signaled():
			target.SetStatus(job.ID, StatusDone, 128+int(status.Signal()))
		case status.Stopped():
			target.SetStatus(job.ID, StatusStopped, 0)
		case status.Continued():
			target.SetStatus(job.ID, StatusRunning, 0)
		}
	}
}

// ShellPgrp returns the shell process's own process group ID. Used by
// ReleaseTTY to reclaim the terminal after a foreground job exits.
func ShellPgrp() (int, error) {
	pgid, err := syscall.Getpgid(os.Getpid())
	if err != nil {
		return 0, err
	}
	return pgid, nil
}

// SendSignal sends sig to the process group pgid. Used by `bg` (SIGCONT)
// and `fg` (SIGCONT) and the foreground-resume path. Negative pid in
// `kill(2)` semantics targets the whole pgrp.
func SendSignal(pgid int, sig syscall.Signal) error {
	return syscall.Kill(-pgid, sig)
}
