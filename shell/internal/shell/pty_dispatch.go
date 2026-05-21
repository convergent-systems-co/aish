// Package shell — PTY dispatch seam.
//
// v0.2-2: decide per-pipeline whether to allocate a pseudo-terminal
// for the child or use the existing stdio pipe path. The decision is
// surgical and lives here (not inside runExternal) so the boundary
// between "decide PTY?" and "run pipeline" stays auditable.
//
// Routing rules (all must hold for PTY):
//  1. Pipeline has exactly one command.
//  2. Caller's stdin AND stdout are *os.File (i.e. real fds, not
//     bytes.Buffers from tests or strings.Readers from scripts).
//  3. Parent stdin is a real TTY (term.IsTerminal).
//  4. The command's first token is on the curated interactive list
//     (exec.IsInteractive).
//
// Failure to satisfy any rule routes to exec.Run (stdio). On any
// PTY-specific error from exec.RunPTY (errPTYUnsupported on Windows,
// allocation failure under load), we degrade silently to exec.Run
// rather than fail the user's command.
package shell

import (
	"context"
	"errors"
	"io"
	"os"

	"golang.org/x/term"

	"github.com/convergent-systems-co/aish/shell/internal/exec"
	"github.com/convergent-systems-co/aish/shell/internal/parser"
)

// runPipeline is the dispatch seam between PTY and stdio paths.
// Returns (exitCode, runErr) matching exec.Run's contract.
func (s *Shell) runPipeline(
	pipeline parser.Pipeline,
	stdin io.Reader,
	stdout, stderr io.Writer,
) (int, error) {
	if shouldUsePTY(pipeline, stdin, stdout) {
		stdinFile, _ := stdin.(*os.File)
		stdoutFile, _ := stdout.(*os.File)
		exitCode, err := exec.RunPTY(
			context.Background(),
			pipeline,
			s.env.Environ(),
			stdinFile,
			stdoutFile,
			stderr,
		)
		// errPTYUnsupported is the platform-unavailability sentinel
		// (Windows today). Fall back to stdio so the command still
		// runs — vim won't render correctly without a PTY, but the
		// shell as a whole stays usable. Other errors (failed
		// allocation, missing binary, etc.) surface to the caller.
		if !errors.Is(err, exec.ErrPTYUnsupported) {
			return exitCode, err
		}
	}
	return exec.Run(
		context.Background(),
		pipeline,
		s.env.Environ(),
		stdin,
		stdout,
		stderr,
	)
}

// shouldUsePTY applies the four routing rules from the file-level
// doc. Pure function (no side effects, no env reads) so tests can
// exercise the policy in isolation from the runtime.
func shouldUsePTY(
	pipeline parser.Pipeline,
	stdin io.Reader,
	stdout io.Writer,
) bool {
	if len(pipeline.Commands) != 1 {
		return false
	}
	stdinFile, ok := stdin.(*os.File)
	if !ok {
		return false
	}
	if _, ok := stdout.(*os.File); !ok {
		return false
	}
	if !term.IsTerminal(int(stdinFile.Fd())) {
		return false
	}
	return exec.IsInteractive(pipeline.Commands[0].Name)
}
