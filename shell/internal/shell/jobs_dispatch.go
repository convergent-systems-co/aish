// Package shell — job-control dispatch helpers.
//
// v0.3-1 follow-up #83/#84. This file wires the parser's
// `Pipeline.Background` flag into `exec.RunBackground` and provides
// the prompt-time "Done" / "Stopped" notice renderer the REPL calls
// before every prompt.
//
// Why this lives here and not inside jobs/: the integration is
// inherently shell-scoped — it touches the JobTable, the parser
// output, and the user-facing stderr stream. The jobs package
// stays library-shaped (no I/O, no shell coupling).
package shell

import (
	"fmt"
	"io"
	"strings"

	"github.com/convergent-systems-co/aish/shell/internal/exec"
	"github.com/convergent-systems-co/aish/shell/internal/jobs"
	"github.com/convergent-systems-co/aish/shell/internal/parser"
)

// runBackground spawns pipeline as a background job and records it
// in the JobTable. Returns 0 on successful spawn (bash semantics:
// `cmd &` always reports exit 0 immediately) or 1 on spawn failure.
//
// On platforms where RunBackground returns ErrBackgroundUnsupported
// (Windows today), prints a clear stderr message and returns 1.
func (s *Shell) runBackground(pipeline parser.Pipeline, cmdline string, stdout, stderr io.Writer) int {
	bg, err := exec.RunBackground(pipeline, s.env.Environ(), stdout, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "aish: background: %v\n", err)
		return 1
	}
	cleanLine := strings.TrimRight(strings.TrimSpace(cmdline), "&")
	cleanLine = strings.TrimSpace(cleanLine)
	job := s.jobTable.Add(bg.Pgid, bg.LeaderPid, cleanLine, jobs.StatusRunning)
	// Match bash's stderr report: `[1] 12345`.
	fmt.Fprintf(stderr, "[%d] %d\n", job.ID, bg.Pgid)
	// We do NOT call `cmd.Wait()` ourselves for background jobs —
	// the reaper goroutine's `Wait4(-1, WNOHANG|WUNTRACED|WCONTINUED)`
	// loop in jobs/signals_unix.go owns reaping for the whole shell.
	// Calling cmd.Wait here would race the reaper for the wait
	// status (Linux's waitpid is racy across threads/goroutines on
	// the same pid) and only one would win. The reaper is the
	// authoritative source.
	_ = job
	return 0
}

// drainJobNotices emits any pending "Done" / "Stopped" notifications
// to stderr and reaps the Done entries from the JobTable. Called from
// the REPL just before each prompt render so backgrounded job
// completions surface without the user needing to type `jobs`.
//
// Format matches bash: `[1]+  Done   sleep 30` for an exit-0 job;
// `[1]+  Exit 42   sleep 30` for a non-zero exit; `[1]+  Stopped sleep 30`
// for a stop.
func (s *Shell) drainJobNotices(stderr io.Writer) {
	if s.jobTable == nil {
		return
	}
	for _, n := range s.jobTable.PendingNotices() {
		switch n.Status {
		case jobs.StatusDone:
			if n.ExitCode == 0 {
				fmt.Fprintf(stderr, "[%d]%s  Done       %s\n", n.ID, n.Flag, n.Cmdline)
			} else {
				fmt.Fprintf(stderr, "[%d]%s  Exit %-3d  %s\n", n.ID, n.Flag, n.ExitCode, n.Cmdline)
			}
		case jobs.StatusStopped:
			fmt.Fprintf(stderr, "[%d]%s  Stopped    %s\n", n.ID, n.Flag, n.Cmdline)
		}
	}
	_ = s.jobTable.Reap()
}
