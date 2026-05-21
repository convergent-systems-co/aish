//go:build !windows

// Package shell — `bg` built-in.
//
// v0.3-1 follow-up #83. Continues a Stopped job in the background.
//
// Behavior:
//
//   - `bg %n` resumes job %n (must be Stopped).
//   - `bg` (no arg) resumes the most recently stopped job (the `+`
//     entry).
//   - Sends SIGCONT to the job's process group via `kill(-pgid, SIGCONT)`.
//   - Flips the JobTable status from Stopped → Running.
//   - Prints `[<n>]+ <cmdline> &` to stdout so the user sees the
//     resumed command (matches bash).
//
// `bg` is a no-op on a Running job (we still print and return 0 —
// matches bash).
package shell

import (
	"errors"
	"fmt"
	"io"
	"syscall"

	"github.com/convergent-systems-co/aish/shell/internal/jobs"
)

func (s *Shell) bgBuiltin(args []string, stdout, stderr io.Writer) int {
	if s.jobTable == nil {
		fmt.Fprintln(stderr, "aish: bg: job control not available")
		return 1
	}
	spec := ""
	if len(args) > 0 {
		spec = args[0]
	}
	if len(args) > 1 {
		fmt.Fprintln(stderr, "aish: bg: usage: bg [%n]")
		return 2
	}
	job, ok := s.jobTable.Find(spec)
	if !ok {
		fmt.Fprintf(stderr, "aish: bg: no such job: %q\n", spec)
		return 1
	}
	// SIGCONT to the whole pgrp. ESRCH means the process already
	// exited — we report it Done implicitly via the next prompt.
	if err := jobs.SendSignal(job.Pgid, syscall.SIGCONT); err != nil {
		if !errors.Is(err, syscall.ESRCH) {
			fmt.Fprintf(stderr, "aish: bg: SIGCONT: %v\n", err)
			return 1
		}
	}
	s.jobTable.SetRunning(job.ID)
	fmt.Fprintf(stdout, "[%d]+ %s &\n", job.ID, job.Cmdline)
	return 0
}
