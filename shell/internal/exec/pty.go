// Package exec — PTY entrypoint.
//
// v0.2-2 (issues #52..#57): allocate a real pseudo-terminal for
// interactive children so programs that check `isatty(0|1)` (vim,
// less, top, htop, ssh, az login) render and read input correctly.
//
// This file declares the cross-platform public API and the sentinel
// error set. The actual allocation, byte-copy, and signal forwarding
// live in `pty_unix.go` (Unix) and `pty_windows.go` (Windows stub).
package exec

import (
	"context"
	"errors"
	"io"
	"os"

	"github.com/convergent-systems-co/aish/shell/internal/parser"
)

// Sentinel errors returned by RunPTY. They are distinguishable via
// errors.Is so callers can degrade cleanly to the stdio path
// (exec.Run) when a PTY is unavailable.
var (
	// errPTYUnsupported is returned on platforms (currently Windows)
	// where no PTY implementation is wired up yet.
	errPTYUnsupported = errors.New("PTY not supported on this platform")

	// errPTYPipeline is returned when len(p.Commands) != 1. PTY-piping
	// a `cmd1 | cmd2` chain is out of scope for v0.2-2; callers should
	// route pipelines through exec.Run.
	errPTYPipeline = errors.New("RunPTY accepts only single-command pipelines")

	// errPTYNeedFile is returned when stdin or stdout is not an
	// *os.File. The raw-mode dance and SIGWINCH propagation both
	// require a real fd.
	errPTYNeedFile = errors.New("RunPTY requires *os.File for stdin and stdout")

	// errPTYNoCommand is returned when len(p.Commands) == 0. The
	// stdio Run() returns (0, nil) for an empty pipeline; RunPTY is
	// stricter because there is no sensible PTY behavior to default to.
	errPTYNoCommand = errors.New("RunPTY requires at least one command")
)

// ErrPTYUnsupported exposes the platform-unavailability sentinel to
// callers in `shell/` so they can decide between "fall back to
// exec.Run" and "surface a clear error." All other RunPTY errors are
// programming defects on the caller side and do not need a public
// identity.
var ErrPTYUnsupported = errPTYUnsupported

// RunPTY runs a single-command pipeline with a real pseudo-terminal.
// The child sees a controlling TTY pointed at the PTY slave; the
// parent reads/writes the master.
//
// Contract:
//
//	len(p.Commands) MUST equal 1; multi-stage pipelines stay on
//	exec.Run.
//	stdin and stdout MUST be *os.File; stderr may be any io.Writer.
//	stdin SHOULD be a TTY for user-facing use (vim et al). If it
//	  isn't, RunPTY still allocates a PTY for the child but does NOT
//	  put stdin into raw mode and does NOT install SIGWINCH.
//
// Returns (exitCode, nil) for "child ran and exited" — the exit code
// matches POSIX: 128+signum if the child was killed by a signal, the
// child's own exit code otherwise. Returns (0, err) for setup
// failures (missing binary, PTY allocation failure, contract
// violation).
//
// On Windows this currently returns (0, errPTYUnsupported); the
// dispatch seam in shell/runExternal falls back to exec.Run.
func RunPTY(
	ctx context.Context,
	p parser.Pipeline,
	env []string,
	stdin, stdout *os.File,
	stderr io.Writer,
) (exitCode int, err error) {
	if len(p.Commands) == 0 {
		return 0, errPTYNoCommand
	}
	if len(p.Commands) != 1 {
		return 0, errPTYPipeline
	}
	if stdin == nil || stdout == nil {
		return 0, errPTYNeedFile
	}
	return runPTY(ctx, p.Commands[0], env, stdin, stdout, stderr)
}
