//go:build !windows

// Package shell — `fg` built-in.
//
// v0.3-1 follow-up #83. Resumes a job in the foreground.
//
// The flow:
//
//  1. Look up the target job in the JobTable (`fg %n`, `fg`, etc).
//  2. Hand the controlling TTY to the job's pgrp via TIOCSPGRP.
//  3. Mark the job Foreground (so the reaper still updates its
//     status — we WANT that — but the REPL is the conceptual owner).
//  4. Send SIGCONT to the pgrp so a stopped job resumes.
//  5. Poll the JobTable for a status change from Running to Stopped
//     or Done. The reaper goroutine updates the table; we just
//     consume those updates here so the REPL blocks until the
//     foreground job finishes (or stops).
//  6. Hand the TTY back to the shell's own pgrp.
//
// Step 5 is a short sleep loop rather than a condvar because the
// JobTable's notice channel deliberately suppresses notices for
// foreground jobs (we don't want a "Done" message printed for a
// foreground exit — that's just a normal command finishing). A
// 25ms poll is invisible to humans; the reaper itself fires within
// microseconds of the child transitioning.
package shell

import (
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/convergent-systems-co/aish/shell/internal/jobs"
)

// goTermIsTerminal is a tiny indirection that lets fg call the
// real `term.IsTerminal` without naming-conflicting with the
// internal/term package used elsewhere in shell.
var goTermIsTerminal = term.IsTerminal

func (s *Shell) fgBuiltin(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if s.jobTable == nil {
		fmt.Fprintln(stderr, "aish: fg: job control not available")
		return 1
	}
	spec := ""
	if len(args) > 0 {
		spec = args[0]
	}
	if len(args) > 1 {
		fmt.Fprintln(stderr, "aish: fg: usage: fg [%n]")
		return 2
	}
	job, ok := s.jobTable.Find(spec)
	if !ok {
		fmt.Fprintf(stderr, "aish: fg: no such job: %q\n", spec)
		return 1
	}
	// Print the resumed command so the user sees what's about to run
	// (bash does the same — `fg` echoes `sleep 30` before the job runs).
	fmt.Fprintln(stdout, job.Cmdline)

	// Find a TTY fd for TakeTTY. If stdin is not a *os.File, or the
	// underlying fd is not a real TTY (TIOCSPGRP returns ENOTTY /
	// EBADF / EFAULT when called on a pipe), skip the TTY-ownership
	// dance. The job still runs to completion in the foreground;
	// SIGINT/SIGTSTP routing is degraded but the shell remains
	// usable. This matches how bash treats non-interactive sessions.
	ttyFd, ttyOK := ttyFromIO(stdin, stdout)
	if ttyOK && s.shellPgrp > 0 && isTTY(ttyFd) {
		if err := jobs.TakeTTY(ttyFd, job.Pgid); err == nil {
			defer func() {
				_ = jobs.ReleaseTTY(ttyFd, s.shellPgrp)
			}()
		}
		// On TakeTTY error we DO NOT log — the test harness pipes
		// stdin in, and the resulting ENOTTY is expected, not an
		// error the user should see.
	}

	// Mark foreground for table bookkeeping; clear on every exit
	// path so subsequent SIGCHLD events are reaped normally.
	jobID := job.ID
	jobPid := job.LeaderPid
	jobPgid := job.Pgid
	s.jobTable.SetCurrent(jobID)
	s.jobTable.MarkForeground(jobID)
	defer s.jobTable.ClearForeground(jobPid)

	// Resume the job if it was stopped. SIGCONT to the whole pgrp
	// (negative pid in kill(2) semantics).
	if err := jobs.SendSignal(jobPgid, syscall.SIGCONT); err != nil {
		if !errors.Is(err, syscall.ESRCH) {
			fmt.Fprintf(stderr, "aish: fg: SIGCONT: %v\n", err)
			return 1
		}
		// ESRCH means the process is already gone — treat as Done.
	}

	// Block until the JobTable shows the job no longer Running.
	// The reaper updates state on every transition.
	pollInterval := 25 * time.Millisecond
	for {
		j, ok := s.jobTable.Find(fmt.Sprintf("%%%d", jobID))
		if !ok {
			// Job evicted while we were waiting — treat as Done.
			return 0
		}
		switch j.Status {
		case jobs.StatusDone:
			return j.ExitCode
		case jobs.StatusStopped:
			// Print the bash-style "Stopped" line so the user knows
			// the job is suspended and `bg`/`fg` can be invoked.
			fmt.Fprintf(stderr, "\n[%d]+  Stopped    %s\n", j.ID, j.Cmdline)
			return 128 + int(syscall.SIGTSTP)
		}
		time.Sleep(pollInterval)
	}
}

// ttyFromIO finds an *os.File from stdin or stdout that the caller
// can use as the controlling-TTY fd. Returns (fd, true) when found;
// (-1, false) when neither is a *os.File. Production callers from
// the REPL always have one — tests with bytes.Buffers do not, and
// the caller skips the TTY-ownership dance.
func ttyFromIO(stdin io.Reader, stdout io.Writer) (int, bool) {
	if f, ok := stdin.(*os.File); ok {
		return int(f.Fd()), true
	}
	if f, ok := stdout.(*os.File); ok {
		return int(f.Fd()), true
	}
	return -1, false
}

// isTTY reports whether fd is a real terminal. Used by fg to skip
// the TIOCSPGRP dance when stdin is a pipe or file (which would
// otherwise return ENOTTY and surface a confusing error to the user).
func isTTY(fd int) bool {
	return goTermIsTerminal(fd)
}
