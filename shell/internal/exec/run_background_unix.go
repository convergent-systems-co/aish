//go:build !windows

// Package exec — background-job spawner.
//
// v0.3-1 follow-up #83: RunBackground starts a Pipeline as a fire-and-
// forget background job, returning immediately with the new process
// group's pgid and a slice of *os.Process handles so the caller can
// signal / wait later. Foreground vs background routing happens in
// `shell/runExternal`; this function does NOT block.
//
// Why it's a separate entry point (not a flag on Run): Run's contract
// is "wait for the pipeline and return its exit code." Background
// jobs invert that contract — the shell returns to the prompt while
// the job runs. Sharing the start path through a boolean flag would
// require Run to choose between two utterly different return shapes,
// which is the textbook smell that says "two functions."
//
// The pipes between stages are owned by the spawner: we close the
// parent's ends after Start so EOF propagates, and we wait on the
// *Process handles in a caller-controlled goroutine.
package exec

import (
	"errors"
	"fmt"
	"io"
	"os"
	osexec "os/exec"

	"github.com/convergent-systems-co/aish/shell/internal/parser"
)

// BackgroundJob is what RunBackground returns. The shell wires the
// fields onto a jobs.Job entry; Wait is the goroutine entry point
// the shell calls to await completion (in the foreground via `fg`
// or asynchronously to learn the exit code on the next prompt).
type BackgroundJob struct {
	// Pgid is the process group ID — the leader process's pid.
	Pgid int
	// LeaderPid is the first stage's pid (== Pgid for a fresh pgrp).
	LeaderPid int
	// Procs is every stage's underlying OS process handle, in
	// pipeline order. The shell only waits on the LAST stage (POSIX
	// pipeline semantics), but earlier handles are exposed so the
	// reaper can match Wait4 results to a job.
	Procs []*os.Process
	// LastCmd is the last stage's *exec.Cmd handle. The shell's
	// foreground-wait path uses it to retrieve the exit code via the
	// standard Wait API. RunBackground does NOT call Wait — that's
	// the caller's job.
	LastCmd *osexec.Cmd
}

// RunBackground starts pipeline as a background job. Returns
// immediately after every stage has been spawned successfully.
//
// stdin/stdout/stderr: the contract differs from Run. For a
// background job, stdin is `/dev/null` (POSIX bash sets background
// stdin to /dev/null so a SIGTTIN doesn't suspend the shell when the
// job tries to read). stdout/stderr point at the caller's terminal
// directly — bash does the same; the user sees the job's output
// inter-leaved with the prompt, exactly as on bash.
//
// Returns a *BackgroundJob on success. Errors collapse to a clean
// teardown: any started stage is killed and reaped, no zombies left.
func RunBackground(
	p parser.Pipeline,
	env []string,
	stdout, stderr io.Writer,
) (*BackgroundJob, error) {
	if len(p.Commands) == 0 {
		return nil, errors.New("RunBackground: empty pipeline")
	}
	n := len(p.Commands)

	// Background stdin is /dev/null. Opening once, closing on exit;
	// the os/exec runtime dup2's the fd into each child.
	devNull, err := os.OpenFile(os.DevNull, os.O_RDONLY, 0)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", os.DevNull, err)
	}
	// We close the parent's copy of devNull AFTER Start runs, so the
	// child has had a chance to inherit the fd.
	defer func() { _ = devNull.Close() }()

	// stderr is fanned out to every stage. When the caller's stderr is
	// a non-*os.File (test buffer, in-memory sink), os/exec spawns a
	// goroutine per stage that copies the child's stderr pipe into the
	// supplied writer — concurrent writes from N goroutines race on a
	// shared *bytes.Buffer. The syncWriter mutex serializes them; for
	// a real *os.File the wrap is harmless (os/exec hands the fd to
	// the child directly and no goroutine is spawned).
	sharedStderr := &syncWriter{w: stderr}
	cmds := make([]*osexec.Cmd, n)
	for i, c := range p.Commands {
		cmds[i] = osexec.Command(c.Name, c.Args...)
		cmds[i].Env = env
		cmds[i].Stderr = sharedStderr
		applyPgroup(cmds[i], 0) // patched to first pgid for i>0 below
	}
	cmds[0].Stdin = devNull
	cmds[n-1].Stdout = stdout

	pipeCloses := make([]*os.File, 0, 2*(n-1))
	for i := 0; i < n-1; i++ {
		pr, pw, perr := os.Pipe()
		if perr != nil {
			for _, f := range pipeCloses {
				_ = f.Close()
			}
			return nil, fmt.Errorf("pipe stage %d-%d: %w", i, i+1, perr)
		}
		cmds[i].Stdout = pw
		cmds[i+1].Stdin = pr
		pipeCloses = append(pipeCloses, pr, pw)
	}

	started := 0
	for i, cmd := range cmds {
		if i > 0 && cmds[0].Process != nil {
			applyPgroup(cmd, cmds[0].Process.Pid)
		}
		if startErr := cmd.Start(); startErr != nil {
			for _, f := range pipeCloses {
				_ = f.Close()
			}
			for j := 0; j < started; j++ {
				if cmds[j].Process != nil {
					_ = cmds[j].Process.Kill()
					_, _ = cmds[j].Process.Wait()
				}
			}
			return nil, fmt.Errorf("start %q (stage %d): %w", cmd.Path, i, startErr)
		}
		started++
	}

	// Close parent copies of pipe fds so EOF propagates between stages.
	for _, f := range pipeCloses {
		_ = f.Close()
	}

	procs := make([]*os.Process, 0, n)
	for _, c := range cmds {
		procs = append(procs, c.Process)
	}

	bj := &BackgroundJob{
		Pgid:      cmds[0].Process.Pid,
		LeaderPid: cmds[0].Process.Pid,
		Procs:     procs,
		LastCmd:   cmds[n-1],
	}
	return bj, nil
}

// ErrBackgroundUnsupported is returned by RunBackground on platforms
// where job control is not implemented (Windows today).
var ErrBackgroundUnsupported = errors.New("background jobs not supported on this platform")
