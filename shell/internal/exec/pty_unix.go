//go:build !windows

// Package exec — Unix PTY implementation.
//
// v0.2-2 (#52..#57). Allocates a pseudo-terminal pair via creack/pty,
// starts the child with the slave side as its controlling TTY, copies
// bytes between the parent's stdin/stdout and the master fd, and
// propagates SIGWINCH so the child re-renders on resize. When the
// parent's stdin is a real TTY, also flips it into raw mode for the
// child's lifetime and restores the original termios on exit — even
// on panic, even on child-killed-by-signal.
//
// Why this file is the right boundary: TL_UI owns parent-side TTY
// input (raw-mode keystroke parsing for the REPL); this file owns
// child-side PTY allocation and the bridge between them. The two
// touch the same parent fd only briefly (we save+restore termios
// around the child); they do not share state.
package exec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/creack/pty"
	"golang.org/x/term"

	"github.com/convergent-systems-co/aish/shell/internal/parser"
)

// runPTY is the Unix implementation behind RunPTY. See pty.go for
// the public contract; this function trusts its callers per that
// contract (single command, non-nil *os.File stdin/stdout).
//
// Lifecycle (in order):
//  1. Build osexec.Cmd. The ctx is wired so a parent cancellation
//     SIGKILLs the child (same semantic as exec.Run).
//  2. pty.Start(cmd) — allocate master/slave, set the slave as the
//     child's controlling TTY, fork+exec.
//  3. Set the initial master window size. Source: AISH_PTY_WS env
//     (for tests), else the parent stdin's actual size, else a
//     conservative 24x80 fallback.
//  4. If parent stdin is a TTY, put it in raw mode; defer Restore.
//  5. Install SIGWINCH handler that mirrors parent stdin's size to
//     the master.
//  6. Two goroutines copy bytes: stdin → master, master → stdout.
//  7. cmd.Wait() → exit code; tear down handlers + restore termios.
func runPTY(
	ctx context.Context,
	cmd parser.Command,
	env []string,
	stdin, stdout *os.File,
	stderr io.Writer,
) (int, error) {
	c := osexec.CommandContext(ctx, cmd.Name, cmd.Args...)
	c.Env = env
	// Stderr is a plain io.Writer — wire it through directly. Most
	// interactive programs write to the TTY (which is stdout in the
	// PTY world), but tools that explicitly use stderr (e.g. ssh
	// debug output) still need a destination.
	c.Stderr = stderr

	// Allocate the PTY and start the child. pty.Start does the
	// fork+exec dance, setsids the child, and assigns the slave as
	// its controlling TTY. master is the parent-side fd we read/write.
	master, err := pty.Start(c)
	if err != nil {
		return 0, fmt.Errorf("pty.Start %q: %w", cmd.Name, err)
	}
	// master is owned here; close on every exit path so the copy
	// goroutines unblock cleanly.
	defer func() { _ = master.Close() }()

	// Initial window size. Tests inject via AISH_PTY_WS=rows:cols;
	// in production we mirror the parent stdin's actual size.
	if err := setInitialSize(master, stdin, env); err != nil {
		// Non-fatal — the child still runs, it just doesn't know
		// its dimensions. Surface to stderr per Code.md §1.
		fmt.Fprintf(stderr, "aish: pty: set initial size: %v\n", err)
	}

	// Raw-mode the parent stdin if it's a TTY. The defer-restore
	// pattern is critical: a panic between here and the matching
	// defer would leave the user's terminal unusable.
	var restoreTermios func()
	if term.IsTerminal(int(stdin.Fd())) {
		oldState, terr := term.MakeRaw(int(stdin.Fd()))
		if terr != nil {
			fmt.Fprintf(stderr, "aish: pty: raw mode: %v\n", terr)
		} else {
			fd := int(stdin.Fd())
			old := oldState
			restoreTermios = func() { _ = term.Restore(fd, old) }
			defer restoreTermios()
		}
	}

	// SIGWINCH propagation. The handler runs for the child's
	// lifetime; signal.Stop + close drains the channel before we
	// return so the goroutine exits cleanly.
	winCh := make(chan os.Signal, 1)
	signal.Notify(winCh, syscall.SIGWINCH)
	doneWinch := make(chan struct{})
	go func() {
		for {
			select {
			case <-winCh:
				if err := pty.InheritSize(stdin, master); err != nil {
					// Best-effort; an ENOTTY here just means stdin
					// stopped being a TTY (rare). Log and continue.
					fmt.Fprintf(stderr, "aish: pty: resize: %v\n", err)
				}
			case <-doneWinch:
				return
			}
		}
	}()
	defer func() {
		signal.Stop(winCh)
		close(doneWinch)
	}()

	// Byte copy goroutines. Two-way bridge between parent and master.
	//
	// Direction A (parent stdin → master): blocks on parent read;
	// unblocks when stdin closes (EOF) or the parent process exits.
	// Direction B (master → parent stdout): blocks on master read;
	// unblocks when master closes (which we do in defer, after the
	// child exits).
	//
	// We don't block the main goroutine on the parent→master copy:
	// if stdin never closes (interactive user typing forever), the
	// goroutine would leak. Instead we rely on cmd.Wait() to be the
	// authoritative signal and let the goroutine exit when master
	// closes during teardown.
	copyDone := &sync.WaitGroup{}
	copyDone.Add(1)
	go func() {
		defer copyDone.Done()
		_, _ = io.Copy(stdout, master) // master closed → returns nil/EIO
	}()
	go func() {
		_, _ = io.Copy(master, stdin) // intentional leak on no-EOF stdin
	}()

	// Wait for the child to exit. cmd.Wait() also reaps the process.
	waitErr := c.Wait()

	// Drain the master→stdout copy so all final bytes (the editor's
	// terminal-restore escape sequence, in particular) reach the
	// parent terminal BEFORE we restore termios.
	_ = master.Close()
	copyDone.Wait()

	return extractPTYExitCode(waitErr), nil
}

// setInitialSize sets master's window size. Source order:
//  1. AISH_PTY_WS=rows:cols env var (test injection)
//  2. Parent stdin's actual TTY size
//  3. Conservative fallback 24x80
func setInitialSize(master, stdin *os.File, env []string) error {
	if rows, cols, ok := parseWinSizeEnv(env); ok {
		return pty.Setsize(master, &pty.Winsize{Rows: rows, Cols: cols})
	}
	if term.IsTerminal(int(stdin.Fd())) {
		if err := pty.InheritSize(stdin, master); err == nil {
			return nil
		}
	}
	return pty.Setsize(master, &pty.Winsize{Rows: 24, Cols: 80})
}

// parseWinSizeEnv looks for AISH_PTY_WS=rows:cols. Returns ok=false
// when absent or malformed; the caller falls back to InheritSize.
func parseWinSizeEnv(env []string) (rows, cols uint16, ok bool) {
	const prefix = "AISH_PTY_WS="
	for _, kv := range env {
		if !strings.HasPrefix(kv, prefix) {
			continue
		}
		spec := strings.TrimPrefix(kv, prefix)
		r, c, found := strings.Cut(spec, ":")
		if !found {
			return 0, 0, false
		}
		ri, err := strconv.Atoi(r)
		if err != nil || ri <= 0 || ri > 65535 {
			return 0, 0, false
		}
		ci, err := strconv.Atoi(c)
		if err != nil || ci <= 0 || ci > 65535 {
			return 0, 0, false
		}
		return uint16(ri), uint16(ci), true
	}
	return 0, 0, false
}

// extractPTYExitCode collapses cmd.Wait()'s error into a POSIX exit
// code. A nil error means the child exited 0. A signal-killed child
// is encoded as 128+signum per shell convention. Any other error
// (I/O failure during Wait) collapses to -1.
func extractPTYExitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *osexec.ExitError
	if errors.As(err, &ee) {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok {
			if ws.Signaled() {
				return 128 + int(ws.Signal())
			}
			return ws.ExitStatus()
		}
		return ee.ExitCode()
	}
	return -1
}
