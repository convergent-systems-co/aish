package integration

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestLogin_FlagLSourcesUserRC verifies that `-l` triggers RC sourcing
// from $HOME/.aish/aishrc.toml. The synthetic RC sets FOO=bar; the
// REPL echoes $FOO and we assert the value reached the env.
func TestLogin_FlagLSourcesUserRC(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("login-shell sourcing is POSIX-only in v0.3-1")
	}
	tempHome := t.TempDir()
	rcPath := filepath.Join(tempHome, ".aish", "aishrc.toml")
	if err := os.MkdirAll(filepath.Dir(rcPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(rcPath, []byte(`
[env]
FOO_FROM_RC = "loaded"
`), 0o644); err != nil {
		t.Fatalf("write rc: %v", err)
	}
	// Pivot HOME so the login loader finds the temp file. Inherit
	// the rest of the env so PATH etc. still work.
	env := append([]string{}, os.Environ()...)
	env = filterEnv(env, "HOME")
	env = append(env, "HOME="+tempHome)

	s := runWithEnv(t, "echo loaded=$FOO_FROM_RC\n", env, "-l")
	s.assertStdoutContains("loaded=loaded")
}

// TestLogin_DashArgv0SourcesUserRC verifies that argv[0] starting
// with `-` triggers login mode (the POSIX getty / login(8)
// convention) even without a -l/--login flag.
func TestLogin_DashArgv0SourcesUserRC(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("dash-argv[0] login detection is POSIX-only in v0.3-1")
	}
	tempHome := t.TempDir()
	rcPath := filepath.Join(tempHome, ".aish", "aishrc.toml")
	if err := os.MkdirAll(filepath.Dir(rcPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(rcPath, []byte(`
[env]
DASH_ARGV0 = "yes"
`), 0o644); err != nil {
		t.Fatalf("write rc: %v", err)
	}

	// Spawn aish with argv[0] = "-aish". exec.Command sets argv[0]
	// from the path; we override via cmd.Args[0].
	cmd := exec.Command(binaryPath)
	cmd.Args = []string{"-aish"} // argv[0] starts with '-' -> login mode
	env := append([]string{}, os.Environ()...)
	env = filterEnv(env, "HOME")
	env = append(env, "HOME="+tempHome)
	cmd.Env = env

	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer stdinR.Close()
	cmd.Stdin = stdinR
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	go func() {
		_, _ = io.WriteString(stdinW, "echo dash=$DASH_ARGV0\n")
		_ = stdinW.Close()
	}()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(defaultTimeout):
		_ = cmd.Process.Kill()
		t.Fatal("aish hung in dash-argv0 mode")
	}
	if !strings.Contains(stdout.String(), "dash=yes") {
		t.Fatalf("dash-argv0 did not source the RC. stdout:\n%s\nstderr:\n%s",
			stdout.String(), stderr.String())
	}
}

// TestLogin_AISHVersionPropagated verifies that AISH_VERSION is set
// in the login session's env. The build-time version may be "dev"
// in CI; we only assert the variable is non-empty.
func TestLogin_AISHVersionPropagated(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("login-shell defaults are POSIX-only in v0.3-1")
	}
	tempHome := t.TempDir()
	env := append([]string{}, os.Environ()...)
	env = filterEnv(env, "HOME", "AISH_VERSION")
	env = append(env, "HOME="+tempHome)
	s := runWithEnv(t, "echo VERSION=$AISH_VERSION\n", env, "--login")
	// VERSION= must appear AND must be followed by something other
	// than whitespace / end-of-line. Splitting on "VERSION=" lets us
	// inspect the bytes immediately after the marker without
	// caring whether the prompt's ANSI escape sits on the same line.
	idx := strings.Index(s.stdout, "VERSION=")
	if idx < 0 {
		t.Fatalf("no VERSION= marker in stdout:\n%s", s.stdout)
	}
	rest := s.stdout[idx+len("VERSION="):]
	// Take everything up to the next newline OR ANSI escape '\x1b'.
	end := len(rest)
	for i, r := range rest {
		if r == '\n' || r == '\x1b' {
			end = i
			break
		}
	}
	token := strings.TrimSpace(rest[:end])
	if token == "" {
		t.Fatalf("AISH_VERSION expanded to empty string in login session\nstdout:\n%s", s.stdout)
	}
}

// TestLogin_LogoutInLoginModeExitsCleanly verifies that `logout`
// in a `-l` session terminates the shell with the requested exit
// code.
func TestLogin_LogoutInLoginModeExitsCleanly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("logout sentinel reach POSIX-only in v0.3-1")
	}
	tempHome := t.TempDir()
	env := append([]string{}, os.Environ()...)
	env = filterEnv(env, "HOME")
	env = append(env, "HOME="+tempHome)

	s := runWithEnv(t, "logout 7\n", env, "-l")
	if s.exitCode != 7 {
		t.Fatalf("aish exit = %d, want 7 (logout 7)\nstdout:\n%s\nstderr:\n%s",
			s.exitCode, s.stdout, s.stderr)
	}
}

// TestLogin_LogoutInNonLoginModeErrors verifies the non-login
// behavior — bash prints "not login shell" and keeps running.
// The session ends on EOF; exit code should be 0 (clean REPL exit).
func TestLogin_LogoutInNonLoginModeErrors(t *testing.T) {
	tempHome := t.TempDir()
	env := append([]string{}, os.Environ()...)
	env = filterEnv(env, "HOME")
	env = append(env, "HOME="+tempHome)
	// No `-l` flag — interactive non-login session.
	s := runWithEnv(t, "logout\n", env)
	s.assertStderrContains("not login shell")
}

// TestLogin_ExecReplacesProcess uses the test-injected execFn path —
// we can't actually exec inside the test process, but we CAN drive
// `exec /usr/bin/true` and observe that the aish process exited 0.
// On POSIX, syscall.Exec replaces us with `true`, which exits 0.
func TestLogin_ExecReplacesProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("syscall.Exec semantics are POSIX-specific")
	}
	requireBinary(t, "true")
	tempHome := t.TempDir()
	env := append([]string{}, os.Environ()...)
	env = filterEnv(env, "HOME")
	env = append(env, "HOME="+tempHome)

	// We MUST run exec via a manually-driven pipe — the harness's
	// `runWithEnv` closes stdin after writing input, but with
	// `exec` the aish process is replaced before EOF would
	// naturally end the REPL.
	cmd := exec.Command(binaryPath, "-l")
	cmd.Env = env
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer stdinR.Close()
	cmd.Stdin = stdinR
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	go func() {
		_, _ = io.WriteString(stdinW, "exec /usr/bin/true\n")
		_ = stdinW.Close()
	}()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		// `true` exits 0 — and on POSIX syscall.Exec gives that
		// status to the original aish PID's parent (us).
		if err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				if ee.ExitCode() != 0 {
					t.Errorf("exit = %d, want 0\nstdout:\n%s\nstderr:\n%s",
						ee.ExitCode(), stdout.String(), stderr.String())
				}
			} else {
				t.Fatalf("wait: %v", err)
			}
		}
	case <-time.After(defaultTimeout):
		_ = cmd.Process.Kill()
		t.Fatal("aish hung on exec /usr/bin/true")
	}
}

// filterEnv returns env minus any KEY=… entries whose KEY is in keys.
// Used by login tests to scrub HOME / AISH_VERSION before pivoting.
func filterEnv(env []string, keys ...string) []string {
	out := env[:0]
nextEntry:
	for _, e := range env {
		eq := strings.IndexByte(e, '=')
		if eq < 0 {
			out = append(out, e)
			continue
		}
		name := e[:eq]
		for _, k := range keys {
			if name == k {
				continue nextEntry
			}
		}
		out = append(out, e)
	}
	return out
}
