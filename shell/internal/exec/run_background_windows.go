//go:build windows

// Package exec — background-job stub for Windows.
//
// POSIX-style job control with process groups + SIGCHLD doesn't map
// to Windows. JobObject-based job control is a separate v1.0
// follow-up; for now RunBackground returns ErrBackgroundUnsupported
// and the built-ins surface a clear message to the user.
package exec

import (
	"errors"
	"io"
	"os"
	osexec "os/exec"

	"github.com/convergent-systems-co/aish/shell/internal/parser"
)

// BackgroundJob mirrors the Unix struct so callers compile cleanly
// on Windows. Fields are populated only on Unix; Windows callers
// MUST treat RunBackground's error as authoritative.
type BackgroundJob struct {
	Pgid      int
	LeaderPid int
	Procs     []*os.Process
	LastCmd   *osexec.Cmd
}

// ErrBackgroundUnsupported is returned by RunBackground on Windows.
var ErrBackgroundUnsupported = errors.New("background jobs not supported on this platform")

// RunBackground is a stub on Windows.
func RunBackground(
	p parser.Pipeline,
	env []string,
	stdout, stderr io.Writer,
) (*BackgroundJob, error) {
	return nil, ErrBackgroundUnsupported
}
