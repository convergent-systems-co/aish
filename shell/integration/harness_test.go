package integration

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// binaryPath is the path to the freshly-built aish binary used by every test
// in this package. Populated by TestMain.
var binaryPath string

const (
	defaultTimeout = 10 * time.Second
	cmdImport      = "../cmd/aish"
)

// TestMain builds aish once into a temp dir, then runs the tests. The
// binary is shared across all tests; each test spawns its own subprocess,
// so REPL state never leaks between tests.
func TestMain(m *testing.M) {
	tmpDir, err := os.MkdirTemp("", "aish-integration-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "integration: failed to create tempdir: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmpDir)

	binaryName := "aish"
	if runtime.GOOS == "windows" {
		binaryName = "aish.exe"
	}
	binaryPath = filepath.Join(tmpDir, binaryName)

	// -buildvcs=false: see plugins/cloud/integration/harness_test.go
	// and shell/internal/cache/plugin_test.go for the same fix.
	// Worktree checkouts (.git is a file) trip Go's VCS stamping.
	build := exec.Command("go", "build", "-buildvcs=false", "-o", binaryPath, cmdImport)
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "integration: go build failed:\n%s\n", out)
		os.Exit(1)
	}

	os.Exit(m.Run())
}

// session is the result of running aish once with scripted input.
type session struct {
	t        *testing.T
	stdout   string
	stderr   string
	exitCode int
}

// run starts aish, feeds it `input` on stdin, waits for it to exit, and
// returns a session. The session retains the testing.T for fluent asserts.
//
// Optional args... are passed as CLI flags to aish (e.g. "--version").
func run(t *testing.T, input string, args ...string) *session {
	t.Helper()
	return runWithEnv(t, input, nil, args...)
}

// runWithEnv is like run but lets the test override the environment.
// Passing nil for env inherits the parent's env unchanged.
//
// stdin is delivered via a real OS pipe so the aish process sees an
// *os.File on fd 0. Critical: when aish spawns a child and assigns
// cmd.Stdin to its own stdin (which is *os.File), os/exec dup2's the
// fd directly into the child WITHOUT spawning a goroutine to copy
// bytes. A goroutine would drain the source aggressively and break
// the issue-#167 contract that subsequent lines remain available for
// `cat`, `head`, and other stdin readers. A strings.Reader here would
// force os/exec into goroutine mode and the tests would not match
// production semantics.
func runWithEnv(t *testing.T, input string, env []string, args ...string) *session {
	t.Helper()

	cmd := exec.Command(binaryPath, args...)
	if env != nil {
		cmd.Env = env
	}

	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatalf("integration: os.Pipe for stdin: %v", err)
	}
	defer stdinR.Close()
	cmd.Stdin = stdinR

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		_ = stdinW.Close()
		t.Fatalf("integration: failed to start aish: %v", err)
	}

	// Feed scripted input through the pipe, then close the write end so
	// aish sees EOF on stdin and exits the REPL cleanly.
	go func() {
		_, _ = io.WriteString(stdinW, input)
		_ = stdinW.Close()
	}()

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		s := &session{
			t:      t,
			stdout: stdout.String(),
			stderr: stderr.String(),
		}
		switch e := err.(type) {
		case nil:
			s.exitCode = 0
		case *exec.ExitError:
			s.exitCode = e.ExitCode()
		default:
			t.Fatalf("integration: unexpected wait error: %v", e)
		}
		return s
	case <-time.After(defaultTimeout):
		_ = cmd.Process.Kill()
		t.Fatalf("integration: aish hung — killed after %s.\nstdout:\n%s\nstderr:\n%s",
			defaultTimeout, stdout.String(), stderr.String())
		return nil
	}
}

// requireBinary skips the test if `name` isn't on PATH. Use for integration
// tests that exec real binaries (echo, tr, cat, …).
func requireBinary(t *testing.T, names ...string) {
	t.Helper()
	for _, n := range names {
		if _, err := exec.LookPath(n); err != nil {
			t.Skipf("integration: required external binary %q not on PATH: %v", n, err)
		}
	}
}

// assertExit asserts the binary's exit code. aish itself exits 0 on clean
// REPL EOF; non-zero means an aish-level failure, not a command-level one.
// Use containsLine + a `echo EXIT=$?` pattern to assert on the LAST
// command's exit code.
func (s *session) assertExit(want int) {
	s.t.Helper()
	if s.exitCode != want {
		s.t.Fatalf("aish exit=%d, want %d\nstdout:\n%s\nstderr:\n%s",
			s.exitCode, want, s.stdout, s.stderr)
	}
}

// assertStdoutContains asserts stdout contains the substring anywhere.
// Useful for substring assertions on command output that might share lines
// with the prompt (e.g. "~/path > HELLO" — contains "HELLO" passes).
func (s *session) assertStdoutContains(want string) {
	s.t.Helper()
	if !strings.Contains(s.stdout, want) {
		s.t.Fatalf("stdout does not contain %q\nstdout:\n%s\nstderr:\n%s",
			want, s.stdout, s.stderr)
	}
}

// assertStdoutNotContains asserts stdout does NOT contain the substring.
func (s *session) assertStdoutNotContains(unwant string) {
	s.t.Helper()
	if strings.Contains(s.stdout, unwant) {
		s.t.Fatalf("stdout unexpectedly contains %q\nstdout:\n%s",
			unwant, s.stdout)
	}
}

// assertStderrContains asserts stderr contains the substring.
func (s *session) assertStderrContains(want string) {
	s.t.Helper()
	if !strings.Contains(s.stderr, want) {
		s.t.Fatalf("stderr does not contain %q\nstderr:\n%s\nstdout:\n%s",
			want, s.stderr, s.stdout)
	}
}

// assertStderrEmpty asserts stderr is empty (modulo trailing whitespace).
func (s *session) assertStderrEmpty() {
	s.t.Helper()
	if strings.TrimSpace(s.stderr) != "" {
		s.t.Fatalf("stderr expected empty, got:\n%s", s.stderr)
	}
}

// script joins lines with newlines and ensures a trailing newline so the
// REPL reads each line cleanly. Use as `script("cmd1", "cmd2", "echo done")`.
func script(lines ...string) string {
	return strings.Join(lines, "\n") + "\n"
}

// contains is a small wrapper around strings.Contains for in-test brevity.
func contains(haystack, needle string) bool {
	return strings.Contains(haystack, needle)
}
