// Package exec runs a parsed pipeline against the host OS via os/exec.
//
// v0.1-1 scope (sub-issues #5, #6): execute one or more commands wired
// via stdin/stdout/stderr; for `cmd1 | cmd2`, connect cmd1.Stdout to
// cmd2.Stdin via an os.Pipe. Returns the exit code of the LAST command
// in the pipeline (POSIX semantic).
//
// No PTY allocation (deferred to v0.2-2). No signal forwarding beyond
// what os/exec provides by default. No CGO.
package exec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"sync"

	"github.com/convergent-systems-co/aish/shell/internal/parser"
)

// syncWriter serializes writes to an underlying io.Writer so multiple
// concurrent producers (one per pipeline stage's stderr copier goroutine
// inside os/exec) cannot race on the shared buffer.
//
// This is the standard remedy for the os/exec contract: when a non-
// *os.File writer is assigned to cmd.Stderr or cmd.Stdout, os/exec
// launches a goroutine per Cmd to copy from a pipe into that writer.
// Two stages sharing the same caller-provided io.Writer therefore need
// external synchronization or undefined-order interleaved writes.
type syncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

// Run executes pipeline against the host. env is passed through to every
// child process as its os.Environ-shaped slice (key=value strings).
// stdin feeds the first command; stdout/stderr receive the last command's
// (and any pipeline-internal) output streams.
//
// Returns the exit code of the LAST command and a non-nil err only on
// pipeline-setup failures (e.g., missing binary, pipe creation failure).
// A non-zero exit code from a child program is reported via exitCode
// with err == nil — that is normal exec behavior, not an error.
func Run(
	ctx context.Context,
	p parser.Pipeline,
	env []string,
	stdin io.Reader,
	stdout, stderr io.Writer,
) (exitCode int, err error) {
	if len(p.Commands) == 0 {
		return 0, nil
	}

	n := len(p.Commands)
	// stderr is shared by every stage. When n > 1, multiple os/exec
	// copier goroutines would write to the same io.Writer concurrently —
	// wrap with a mutex to serialize. For n == 1 the wrap is harmless.
	sharedStderr := &syncWriter{w: stderr}

	cmds := make([]*osexec.Cmd, n)
	for i, c := range p.Commands {
		cmds[i] = osexec.CommandContext(ctx, c.Name, c.Args...)
		cmds[i].Env = env
		cmds[i].Stderr = sharedStderr
	}

	// First stage reads from caller-provided stdin.
	cmds[0].Stdin = stdin
	// Last stage writes to caller-provided stdout.
	cmds[n-1].Stdout = stdout

	// Inter-stage pipes: cmds[i].Stdout -> cmds[i+1].Stdin via os.Pipe.
	// Using os.Pipe rather than io.Pipe lets os/exec hand the file
	// descriptors directly to the child without an extra goroutine, and
	// makes parent-side close semantics explicit.
	pipeCloses := make([]*os.File, 0, 2*(n-1))
	for i := 0; i < n-1; i++ {
		pr, pw, perr := os.Pipe()
		if perr != nil {
			// Tear down any pipes we already created before bailing.
			for _, f := range pipeCloses {
				_ = f.Close()
			}
			return 0, fmt.Errorf("create pipe between stage %d and %d: %w", i, i+1, perr)
		}
		cmds[i].Stdout = pw
		cmds[i+1].Stdin = pr
		pipeCloses = append(pipeCloses, pr, pw)
	}

	// Start every command. If any fails to start (e.g., missing binary),
	// kill the ones that already started and report the setup error so the
	// caller sees a clean failure, not a hung pipeline.
	started := 0
	for i, cmd := range cmds {
		if startErr := cmd.Start(); startErr != nil {
			// Close all pipe fds so already-started children unblock and exit.
			for _, f := range pipeCloses {
				_ = f.Close()
			}
			for j := 0; j < started; j++ {
				if cmds[j].Process != nil {
					_ = cmds[j].Process.Kill()
					_ = cmds[j].Wait()
				}
			}
			return 0, fmt.Errorf("start command %q (stage %d): %w", cmd.Path, i, startErr)
		}
		started++
	}

	// Parent closes its copies of the pipe fds so that EOF actually
	// propagates when a producer exits — otherwise the consumer blocks
	// reading from a still-open writer.
	for _, f := range pipeCloses {
		if cerr := f.Close(); cerr != nil {
			// A close failure here is informative but not fatal — the
			// children own their own fds. Surface to stderr per Code.md §1
			// (no silent error swallowing) and continue.
			fmt.Fprintf(sharedStderr, "aish: pipe close: %v\n", cerr)
		}
	}

	// Wait on every command in order. Only the LAST command's exit
	// status is returned per POSIX pipeline semantics; intermediate
	// failures are not promoted to err (they are surfaced via stderr
	// already by the child itself).
	var lastExit int
	for i, cmd := range cmds {
		werr := cmd.Wait()
		if i == n-1 {
			lastExit = extractExitCode(werr)
		}
	}
	return lastExit, nil
}

// extractExitCode converts an *exec.ExitError into its POSIX exit code.
// A nil error means the child exited 0. A non-ExitError (e.g., I/O
// failure waiting on the process) collapses to -1 so the caller can
// distinguish "ran and failed" from "could not run at all".
func extractExitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *osexec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}
