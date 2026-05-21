//go:build !windows

// Package exec — Unix process-group setup.
//
// v0.3-1 follow-up #84: every child the shell starts gets its own
// process group via `SysProcAttr.Setpgid: true`. Pipeline children
// after the first stage join the first stage's pgid (so the whole
// pipeline is one job for Ctrl-C / Ctrl-Z purposes). The shell stays
// in its own pgrp and remains in the foreground session.
//
// This file owns the OS-specific knob (Setpgid + Pgid). Callers
// pass firstPgid = 0 for the first stage (the kernel uses the
// child's own pid as the new pgid) and firstPgid = <first stage's
// pid> for every subsequent stage. The dispatcher in exec.Run
// patches firstPgid after Start succeeds on stage 0.
package exec

import (
	"os/exec"
	"syscall"
)

// applyPgroup configures cmd to start in a new process group when
// firstPgid == 0, or to join an existing one when firstPgid > 0.
//
// Setsid is intentionally NOT used — Setsid would detach the child
// from the controlling TTY, and we WANT the foreground job to
// receive Ctrl-C and Ctrl-Z routed via the kernel's TTY ownership.
// Setpgid is the bash/zsh-compatible approach.
//
// Foreground also needs Foreground: true on the SysProcAttr so the
// kernel binds the new pgrp to the controlling TTY; tcsetpgrp does
// the same job from userspace and we already need it for `fg`, so
// we leave Foreground=false here and let the shell's TakeTTY do
// the explicit hand-off.
func applyPgroup(cmd *exec.Cmd, firstPgid int) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.SysProcAttr.Pgid = firstPgid
}
