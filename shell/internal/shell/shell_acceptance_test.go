package shell

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestCwdInitialisedToProcessDir verifies that a freshly constructed Shell
// reports the current process working directory. Gates sub-issue #7.
func TestCwdInitialisedToProcessDir(t *testing.T) {
	want, err := os.Getwd()
	if err != nil {
		t.Skipf("os.Getwd failed (env-dependent): %v", err)
	}
	s := New()
	got := s.Cwd()
	if got != want {
		t.Errorf("Cwd() = %q, want %q (os.Getwd)", got, want)
	}
}

// TestCdAbsolutePathUpdatesCwd verifies that `cd /tmp` (or its platform
// equivalent) updates the shell's cwd. Gates sub-issue #7.
func TestCdAbsolutePathUpdatesCwd(t *testing.T) {
	target := "/tmp"
	if runtime.GOOS == "windows" {
		t.Skipf("test uses POSIX /tmp; runtime=%s", runtime.GOOS)
	}
	if _, err := os.Stat(target); err != nil {
		t.Skipf("%q not present (env-dependent): %v", target, err)
	}
	s := New()
	if err := s.Cd(target); err != nil {
		t.Fatalf("Cd(%q) returned err: %v", target, err)
	}
	// Resolve symlinks both sides — /tmp is /private/tmp on macOS.
	gotResolved, _ := filepath.EvalSymlinks(s.Cwd())
	wantResolved, _ := filepath.EvalSymlinks(target)
	if gotResolved != wantResolved {
		t.Errorf("after Cd(%q): Cwd() = %q (resolved %q), want %q (resolved %q)",
			target, s.Cwd(), gotResolved, target, wantResolved)
	}
}

// TestCdRelativePath verifies that a relative path resolves against the
// current cwd.
func TestCdRelativePath(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "subdir")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	s := New()
	if err := s.Cd(dir); err != nil {
		t.Fatalf("Cd(parent) returned err: %v", err)
	}
	if err := s.Cd("subdir"); err != nil {
		t.Fatalf("Cd(relative) returned err: %v", err)
	}
	gotResolved, _ := filepath.EvalSymlinks(s.Cwd())
	wantResolved, _ := filepath.EvalSymlinks(sub)
	if gotResolved != wantResolved {
		t.Errorf("after Cd(rel): Cwd() = %q, want %q", gotResolved, wantResolved)
	}
}

// TestCdNonexistentReturnsError is the negative path: cd to a missing
// directory must surface as a non-nil error, not silently succeed.
func TestCdNonexistentReturnsError(t *testing.T) {
	s := New()
	missing := filepath.Join(t.TempDir(), "does-not-exist-12345")
	err := s.Cd(missing)
	if err == nil {
		t.Errorf("Cd(%q) returned nil err, want non-nil", missing)
	}
}

// TestEnvSetAndGet verifies the SetEnv/GetEnv round trip. Gates #8.
func TestEnvSetAndGet(t *testing.T) {
	s := New()
	if err := s.SetEnv("AISH_TEST_FOO", "bar"); err != nil {
		t.Fatalf("SetEnv returned err: %v", err)
	}
	got, ok := s.GetEnv("AISH_TEST_FOO")
	if !ok {
		t.Fatal("GetEnv returned ok=false after SetEnv")
	}
	if got != "bar" {
		t.Errorf("GetEnv = %q, want %q", got, "bar")
	}
}

// TestLastExitDefaultZero verifies LastExit() is 0 before any command runs.
// Gates #9.
func TestLastExitDefaultZero(t *testing.T) {
	s := New()
	if got := s.LastExit(); got != 0 {
		t.Errorf("LastExit() before any command = %d, want 0", got)
	}
}

// TestSetLastExitRoundTrip verifies SetLastExit + LastExit pair. The REPL
// uses this seam to record each pipeline's code for `$?` expansion.
func TestSetLastExitRoundTrip(t *testing.T) {
	s := New()
	s.SetLastExit(42)
	if got := s.LastExit(); got != 42 {
		t.Errorf("LastExit after SetLastExit(42) = %d, want 42", got)
	}
	// Negative codes occur (e.g., signaled processes). They round-trip too.
	s.SetLastExit(-1)
	if got := s.LastExit(); got != -1 {
		t.Errorf("LastExit after SetLastExit(-1) = %d, want -1", got)
	}
}

// TestPromptShape verifies the v0.1-1 prompt format: "<cwd-shortened> > "
// with ~ substituting for $HOME prefix. Gates #11.
//
// Isolation note: shell.New() reads ~/.aish/config.toml from $HOME to
// restore the persisted active theme. Without isolation the developer's
// real `theme set` (e.g. nord-powerline with `❯` glyph) leaks into
// the test, breaking the " > " suffix assertion. t.Setenv points $HOME
// at a tempdir (with no config.toml) so New() falls through to the
// "default" theme whose prompt_char IS ">".
func TestPromptShape(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	s := New()
	if err := s.Cd(dir); err != nil {
		t.Fatalf("Cd: %v", err)
	}
	got := s.Prompt()
	// The prompt must end with the ` > ` separator (literal: space, >, space).
	if !strings.HasSuffix(got, " > ") {
		t.Errorf("Prompt() = %q, want suffix %q", got, " > ")
	}
	// The prompt body must mention the cwd.
	if !strings.Contains(got, filepath.Base(dir)) {
		t.Errorf("Prompt() = %q, want to contain cwd basename %q",
			got, filepath.Base(dir))
	}
}

// TestPromptHomeTilde verifies that when cwd is under $HOME, the prompt
// substitutes ~ for the $HOME prefix. Gates #11.
func TestPromptHomeTilde(t *testing.T) {
	home := os.Getenv("HOME")
	if home == "" {
		t.Skip("$HOME not set (env-dependent)")
	}
	if _, err := os.Stat(home); err != nil {
		t.Skipf("$HOME=%q not accessible: %v", home, err)
	}
	s := New()
	if err := s.Cd(home); err != nil {
		t.Fatalf("Cd(home): %v", err)
	}
	got := s.Prompt()
	if !strings.Contains(got, "~") {
		t.Errorf("Prompt() in $HOME = %q, want to contain `~`", got)
	}
	// Must NOT contain the full $HOME path literally.
	if strings.Contains(got, home) && home != "~" {
		t.Errorf("Prompt() = %q, leaks full $HOME=%q (should be `~`)", got, home)
	}
}

// TestRunCdBuiltin runs an end-to-end scripted REPL session. Feeds `cd
// <tmpdir>` then EOF; asserts Run returns nil and cwd updated. Gates #3.
func TestRunCdBuiltin(t *testing.T) {
	dir := t.TempDir()
	s := New()
	script := strings.NewReader("cd " + dir + "\n")
	var out, errBuf bytes.Buffer
	if err := s.Run(script, &out, &errBuf); err != nil {
		t.Fatalf("Run returned err: %v", err)
	}
	gotResolved, _ := filepath.EvalSymlinks(s.Cwd())
	wantResolved, _ := filepath.EvalSymlinks(dir)
	if gotResolved != wantResolved {
		t.Errorf("after `cd %s`: Cwd = %q, want %q", dir, gotResolved, wantResolved)
	}
}

// TestRunExportThenEcho is the canonical sub-issue #8 acceptance:
// `export FOO=bar; echo $FOO` prints `bar`. Scripts a multi-line REPL
// session and asserts on stdout.
func TestRunExportThenEcho(t *testing.T) {
	if _, err := lookOnPath("echo"); err != nil {
		t.Skipf("echo missing on PATH (env-dependent): %v", err)
	}
	s := New()
	script := strings.NewReader("export AISH_REPL_VAR=carried\necho $AISH_REPL_VAR\n")
	var out, errBuf bytes.Buffer
	if err := s.Run(script, &out, &errBuf); err != nil {
		t.Fatalf("Run returned err: %v", err)
	}
	if !strings.Contains(out.String(), "carried") {
		t.Errorf("stdout did not contain expanded $AISH_REPL_VAR value\nstdout: %q\nstderr: %q",
			out.String(), errBuf.String())
	}
}

// TestRunExitCodeCaptured verifies sub-issue #9 end-to-end: a failing
// command sets `$?` to non-zero, observable via `echo $?`.
func TestRunExitCodeCaptured(t *testing.T) {
	if _, err := lookOnPath("false"); err != nil {
		t.Skipf("false missing on PATH (env-dependent): %v", err)
	}
	if _, err := lookOnPath("echo"); err != nil {
		t.Skipf("echo missing on PATH (env-dependent): %v", err)
	}
	s := New()
	script := strings.NewReader("false\necho $?\n")
	var out, errBuf bytes.Buffer
	if err := s.Run(script, &out, &errBuf); err != nil {
		t.Fatalf("Run returned err: %v", err)
	}
	trimmed := strings.TrimSpace(out.String())
	if trimmed == "" || trimmed == "0" {
		t.Errorf("after `false; echo $?`, stdout = %q, want non-empty non-zero code (stderr=%q)",
			out.String(), errBuf.String())
	}
}

// TestRunEmptyInputClean confirms the seed-level contract is preserved:
// empty stdin yields a nil error. This is a re-statement of TestRunSeed
// inside the acceptance file so the production stub keeps this invariant.
func TestRunEmptyInputClean(t *testing.T) {
	s := New()
	if err := s.Run(strings.NewReader(""), io.Discard, io.Discard); err != nil {
		t.Errorf("Run on empty stdin returned err: %v (want nil)", err)
	}
}

// lookOnPath is a tiny local helper so this test file does not import
// os/exec just for LookPath — keeps the failing-test surface small.
func lookOnPath(name string) (string, error) {
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		p := filepath.Join(dir, name)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p, nil
		}
	}
	return "", &os.PathError{Op: "lookpath", Path: name, Err: os.ErrNotExist}
}
