//go:build !windows

package exec

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/convergent-systems-co/aish/shell/internal/parser"
)

// TestRunPTYRejectsPipeline locks the v0.2-2 contract: PTY-piping
// multi-stage pipelines is out of scope; the seam in shell.go uses
// the existing stdio path for those.
func TestRunPTYRejectsPipeline(t *testing.T) {
	pin, pout, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer pin.Close()
	defer pout.Close()

	p := parser.Pipeline{
		Commands: []parser.Command{
			{Name: "echo", Args: []string{"a"}},
			{Name: "cat"},
		},
	}
	var stderr bytes.Buffer
	_, runErr := RunPTY(context.Background(), p, nil, pin, pout, &stderr)
	if !errors.Is(runErr, errPTYPipeline) {
		t.Fatalf("err = %v, want errPTYPipeline", runErr)
	}
}

// TestRunPTYRejectsEmptyPipeline locks the "must have at least one
// command" contract. exec.Run() treats empty as a no-op; RunPTY is
// stricter because there is no sensible default behavior.
func TestRunPTYRejectsEmptyPipeline(t *testing.T) {
	pin, pout, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer pin.Close()
	defer pout.Close()

	var stderr bytes.Buffer
	_, runErr := RunPTY(context.Background(), parser.Pipeline{}, nil, pin, pout, &stderr)
	if !errors.Is(runErr, errPTYNoCommand) {
		t.Fatalf("err = %v, want errPTYNoCommand", runErr)
	}
}

// TestRunPTYRequiresFile locks the "stdin/stdout must be *os.File"
// contract. A nil stdin should surface the sentinel cleanly rather
// than panicking.
func TestRunPTYRequiresFile(t *testing.T) {
	p := parser.Pipeline{Commands: []parser.Command{{Name: "echo"}}}
	var stderr bytes.Buffer
	_, runErr := RunPTY(context.Background(), p, nil, nil, nil, &stderr)
	if !errors.Is(runErr, errPTYNeedFile) {
		t.Fatalf("err = %v, want errPTYNeedFile", runErr)
	}
}

// TestRunPTYMissingBinary asserts parity with exec.Run: a binary not
// on PATH surfaces a non-nil error rather than silently returning
// exit-0. Required by Code.md §1 (no silent error swallowing) and
// the v0.1-1 Run() contract this code inherits.
func TestRunPTYMissingBinary(t *testing.T) {
	pin, pout, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer pin.Close()
	defer pout.Close()

	p := parser.Pipeline{
		Commands: []parser.Command{{Name: "this-binary-does-not-exist-aish-pty-test"}},
	}
	var stderr bytes.Buffer
	_, runErr := RunPTY(context.Background(), p, nil, pin, pout, &stderr)
	if runErr == nil {
		t.Fatal("RunPTY with missing binary returned nil error, want non-nil")
	}
	if errors.Is(runErr, errPTYUnsupported) {
		t.Fatalf("RunPTY surfaced errPTYUnsupported on a Unix host; the impl is missing")
	}
}

// TestRunPTYIsATTY gates issue #54 (PTY allocation). The child sees
// a controlling TTY pointed at the PTY slave — `tty(1)` resolves
// the calling fd's path on disk. On macOS that's /dev/ttys*; on
// Linux that's /dev/pts/*. We assert the prefix, not the index.
func TestRunPTYIsATTY(t *testing.T) {
	requireBinary(t, "tty")

	pin, _, ptyErr := mustPipePair(t)
	defer pin.Close()
	parentOut := captureFile(t)
	defer parentOut.cleanup()

	p := parser.Pipeline{Commands: []parser.Command{{Name: "tty"}}}
	var stderr bytes.Buffer
	exit, runErr := RunPTY(context.Background(), p, nil, pin, parentOut.f, &stderr)
	if errors.Is(runErr, errPTYUnsupported) {
		t.Fatalf("RunPTY returned errPTYUnsupported on a Unix host; impl is missing")
	}
	if runErr != nil {
		t.Fatalf("RunPTY: %v (stderr=%s)", runErr, stderr.String())
	}
	if exit != 0 {
		t.Fatalf("exit = %d, want 0 (stderr=%s)", exit, stderr.String())
	}
	out := parentOut.read()
	// PTY line discipline rewrites \n → \r\n on output; trim both.
	got := strings.TrimSpace(out)
	pat := regexp.MustCompile(`^/dev/(pts/\d+|ttys?\d+|pty\w+)$`)
	if !pat.MatchString(got) {
		t.Errorf("child tty = %q, want a /dev/{pts,ttys} device", got)
	}
	_ = ptyErr // silence — variable kept for symmetry with helper
}

// TestRunPTYWindowSize gates issue #56 (window-size propagation).
// We set the master PTY to a known size BEFORE the child reads
// `stty size`; the child must observe that size.
func TestRunPTYWindowSize(t *testing.T) {
	requireBinary(t, "stty")

	pin, _, _ := mustPipePair(t)
	defer pin.Close()
	parentOut := captureFile(t)
	defer parentOut.cleanup()

	// Override the initial window size for the test via env that the
	// impl reads (AISH_PTY_WS=rows:cols). Documented in pty_unix.go.
	env := append(os.Environ(), "AISH_PTY_WS=40:100")

	p := parser.Pipeline{Commands: []parser.Command{{Name: "stty", Args: []string{"size"}}}}
	var stderr bytes.Buffer
	exit, runErr := RunPTY(context.Background(), p, env, pin, parentOut.f, &stderr)
	if errors.Is(runErr, errPTYUnsupported) {
		t.Fatalf("RunPTY returned errPTYUnsupported on a Unix host; impl is missing")
	}
	if runErr != nil {
		t.Fatalf("RunPTY: %v (stderr=%s)", runErr, stderr.String())
	}
	if exit != 0 {
		t.Fatalf("exit = %d, want 0 (stderr=%s)", exit, stderr.String())
	}
	got := strings.TrimSpace(parentOut.read())
	if got != "40 100" {
		t.Errorf("stty size = %q, want %q", got, "40 100")
	}
}

// TestRunPTYStdinPassthrough gates issue #54 (byte flow through the
// master). Feed `cat` a line via the master input side; the same
// line should land on stdout (with the PTY line-discipline rewrite
// `\n → \r\n` baked in).
func TestRunPTYStdinPassthrough(t *testing.T) {
	requireBinary(t, "cat")

	parentIn, childInDriver, _ := mustPipePair(t)
	defer parentIn.Close()
	defer childInDriver.Close()

	parentOut := captureFile(t)
	defer parentOut.cleanup()

	// Push the input bytes into the parent-end pipe. PTY line
	// discipline doesn't propagate fd-close → child-EOF the way a
	// raw pipe does — `cat` reads from the slave side and only
	// stops on a Ctrl-D byte (0x04, the line-discipline EOF
	// terminator). After delivering the byte we still close to
	// unblock our own io.Copy goroutine.
	go func() {
		_, _ = childInDriver.WriteString("hello\n")
		// small delay so `hello` flushes the line buffer before EOF
		time.Sleep(50 * time.Millisecond)
		_, _ = childInDriver.Write([]byte{0x04})
		_ = childInDriver.Close()
	}()

	p := parser.Pipeline{Commands: []parser.Command{{Name: "cat"}}}
	var stderr bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	exit, runErr := RunPTY(ctx, p, nil, parentIn, parentOut.f, &stderr)
	if errors.Is(runErr, errPTYUnsupported) {
		t.Fatalf("RunPTY returned errPTYUnsupported on a Unix host; impl is missing")
	}
	if runErr != nil {
		t.Fatalf("RunPTY: %v (stderr=%s)", runErr, stderr.String())
	}
	if exit != 0 {
		t.Fatalf("exit = %d, want 0 (stderr=%s)", exit, stderr.String())
	}
	out := parentOut.read()
	// Line-discipline maps \n → \r\n on output. Accept either to
	// stay portable across BSD/Linux quirks; the *content* is what
	// matters for this test.
	got := strings.ReplaceAll(out, "\r\n", "\n")
	if !strings.Contains(got, "hello") {
		t.Errorf("stdout = %q, want to contain %q", out, "hello")
	}
}

// --- helpers -----------------------------------------------------

// mustPipePair returns an OS pipe (read, write). Used as a stand-in
// for a TTY in tests; the impl path that needs a real TTY (raw mode)
// detects non-TTY stdin and skips the termios dance.
func mustPipePair(t *testing.T) (*os.File, *os.File, error) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	return r, w, nil
}

// captureFile wraps a temp file used as the child's stdout sink.
// Reading os.Pipe() can deadlock when the writer never closes; a
// tempfile lets the impl write through without coordination and the
// test reads after the child exits.
type captured struct {
	f       *os.File
	path    string
	cleanFn func()
}

func captureFile(t *testing.T) *captured {
	t.Helper()
	f, err := os.CreateTemp("", "aish-pty-out-*")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	c := &captured{f: f, path: f.Name()}
	c.cleanFn = func() {
		_ = f.Close()
		_ = os.Remove(f.Name())
	}
	return c
}

func (c *captured) read() string {
	// Reopen-and-read so we get whatever the child / forwarder
	// wrote, independent of fd offset state.
	b, err := os.ReadFile(c.path)
	if err != nil {
		return ""
	}
	return string(b)
}

func (c *captured) cleanup() { c.cleanFn() }

// requireBinary is shared with exec_test.go (same package, no build
// tag on that file — it's safe to call from this !windows file).
// Listing it here as a doc anchor; the actual symbol lives in
// exec_test.go.
var _ = io.EOF // keep io import live for editor symmetry
