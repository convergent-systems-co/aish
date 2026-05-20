// Package exec runs a parsed pipeline against the host OS via os/exec.
//
// v0.1-1 scope (sub-issues #5, #6): execute one or more commands wired
// via stdin/stdout/stderr; for `cmd1 | cmd2`, connect cmd1.Stdout to
// cmd2.Stdin via an io.Pipe or exec.Cmd.StdoutPipe(). Returns the exit
// code of the LAST command in the pipeline (POSIX semantic).
//
// No PTY allocation (deferred to v0.2-2). No signal forwarding beyond
// what os/exec provides by default. No CGO.
package exec

import (
	"context"
	"io"

	"github.com/convergent-systems-co/aish/shell/internal/parser"
)

// Run executes pipeline against the host. env is passed through to every
// child process as its os.Environ-shaped slice (key=value strings).
// stdin feeds the first command; stdout/stderr receive the last command's
// (and any pipeline-internal) output streams.
//
// Returns the exit code of the LAST command and a non-nil err only on
// pipeline-setup failures (e.g., missing binary, pipe creation failure).
// A non-zero exit code from a child program is reported via exitCode
// with err == nil — that is normal exec behavior, not an error.
//
// Implementation lives in the v0.1-1 coder T1 sub-task; this stub returns
// (0, nil) so the test file compiles.
func Run(
	ctx context.Context,
	p parser.Pipeline,
	env []string,
	stdin io.Reader,
	stdout, stderr io.Writer,
) (exitCode int, err error) {
	return 0, nil
}
