//go:build windows

// Package jobs — Windows stubs.
//
// POSIX-style job control with process groups, controlling-TTY
// ownership, and SIGCHLD does not map to Windows. The real
// equivalent — Windows JobObjects + console-control-event routing —
// is its own v1.0 follow-up. For now, every entry point either
// returns ErrUnsupported or is a no-op so the rest of the shell
// compiles on `GOOS=windows`.
package jobs

import (
	"errors"
	"syscall"
)

// ErrUnsupported is returned by every Unix-only entry point when
// compiled for Windows. The built-ins surface this verbatim ("job
// control not supported on Windows") and exit 1.
var ErrUnsupported = errors.New("job control not supported on this platform")

// IgnoreShellSignals is a no-op on Windows. POSIX-style signal
// handling does not apply; the runtime's default behavior is fine.
// Returns a no-op teardown.
func IgnoreShellSignals() func() {
	return func() {}
}

// TakeTTY is unsupported on Windows.
func TakeTTY(fd int, pgid int) error { return ErrUnsupported }

// ReleaseTTY is unsupported on Windows.
func ReleaseTTY(fd int, ownPgid int) error { return ErrUnsupported }

// Reaper is the Windows stub for the SIGCHLD reaper goroutine.
type Reaper struct{}

// StartReaper returns an empty Reaper on Windows; the rest of the
// shell sees an opaque handle whose Stop is a no-op.
func StartReaper(jt *JobTable) *Reaper { return &Reaper{} }

// Stop is a no-op on Windows.
func (r *Reaper) Stop() {}

// ShellPgrp is unsupported on Windows.
func ShellPgrp() (int, error) { return 0, ErrUnsupported }

// SendSignal is unsupported on Windows.
func SendSignal(pgid int, sig syscall.Signal) error { return ErrUnsupported }
