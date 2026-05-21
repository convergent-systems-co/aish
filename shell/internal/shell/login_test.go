package shell

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/convergent-systems-co/aish/shell/internal/env"
)

// writeRCFile is a test helper: write `body` to <dir>/<name> with
// 0644 permissions. The parent directory is created as needed.
func writeRCFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// newLoginShellForTest constructs a Shell directly (bypassing
// NewWithOptions to avoid the openCache / openHistory side effects
// against a fresh tempdir HOME — those openers are tested in their
// own packages). loginMode is on and the version string is set to
// a fixed value so AISH_VERSION assertions are reproducible.
func newLoginShellForTest(t *testing.T, homeOverride string) *Shell {
	t.Helper()
	s := &Shell{
		env:           env.New(),
		loginMode:     true,
		versionString: "test-1.2.3",
	}
	if homeOverride != "" {
		_ = s.env.Set("HOME", homeOverride)
	}
	return s
}

func TestLoadRCFiles_MissingBothFiles_NoError(t *testing.T) {
	tempHome := t.TempDir()
	s := newLoginShellForTest(t, tempHome)
	// No system file under /etc (assumed absent on test host), no
	// user file under tempHome. Should be a silent no-op.
	var stderr bytes.Buffer
	s.loadRCFiles(&stderr)
	if stderr.Len() != 0 {
		t.Fatalf("expected silent skip, got stderr: %s", stderr.String())
	}
	// Sanity: no env was set.
	if v, ok := s.env.Get("FOO"); ok {
		t.Fatalf("FOO unexpectedly set to %q", v)
	}
}

func TestLoadRCFiles_UserFileOnly_AppliesEnv(t *testing.T) {
	tempHome := t.TempDir()
	s := newLoginShellForTest(t, tempHome)
	writeRCFile(t, tempHome, ".aish/aishrc.toml", `
[env]
FOO = "bar"
BAZ = "qux"
`)
	var stderr bytes.Buffer
	s.loadRCFiles(&stderr)
	if stderr.Len() != 0 {
		t.Fatalf("unexpected stderr: %s", stderr.String())
	}
	if v, _ := s.env.Get("FOO"); v != "bar" {
		t.Errorf("FOO = %q, want %q", v, "bar")
	}
	if v, _ := s.env.Get("BAZ"); v != "qux" {
		t.Errorf("BAZ = %q, want %q", v, "qux")
	}
}

func TestLoadRCFiles_MalformedTOML_WarnsButContinues(t *testing.T) {
	tempHome := t.TempDir()
	s := newLoginShellForTest(t, tempHome)
	writeRCFile(t, tempHome, ".aish/aishrc.toml", `
[env
FOO = "broken"
`) // missing closing bracket on the [env table header
	var stderr bytes.Buffer
	s.loadRCFiles(&stderr)
	if stderr.Len() == 0 {
		t.Fatal("expected a parse-error warning on stderr, got nothing")
	}
	// FOO must NOT be set — parse failure aborts the apply.
	if _, ok := s.env.Get("FOO"); ok {
		t.Error("FOO was set despite the malformed RC parse")
	}
}

func TestLoadRCFiles_InvalidUmask_WarnsButEnvStillApplied(t *testing.T) {
	tempHome := t.TempDir()
	s := newLoginShellForTest(t, tempHome)
	writeRCFile(t, tempHome, ".aish/aishrc.toml", `
[env]
APPLIED = "yes"

[shell]
umask = "not-octal"
`)
	var stderr bytes.Buffer
	s.loadRCFiles(&stderr)
	// stderr carries the warning…
	if stderr.Len() == 0 {
		t.Fatal("expected warning about invalid umask")
	}
	// …but the env that came BEFORE the umask field was still applied,
	// because applyRCFile loops the env map first.
	if v, _ := s.env.Get("APPLIED"); v != "yes" {
		t.Errorf("APPLIED = %q, want yes — env should land even when umask fails", v)
	}
}

func TestLoadRCFiles_AliasesStoredOnShell(t *testing.T) {
	tempHome := t.TempDir()
	s := newLoginShellForTest(t, tempHome)
	writeRCFile(t, tempHome, ".aish/aishrc.toml", `
[aliases]
ll = "ls -la"
gs = "git status"
`)
	var stderr bytes.Buffer
	s.loadRCFiles(&stderr)
	if s.aliases == nil {
		t.Fatal("aliases map is nil after RC load")
	}
	if got, want := s.aliases["ll"], "ls -la"; got != want {
		t.Errorf("alias ll = %q, want %q", got, want)
	}
	if got, want := s.aliases["gs"], "git status"; got != want {
		t.Errorf("alias gs = %q, want %q", got, want)
	}
}

func TestLoadRCFiles_ValidUmaskApplied(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("umask is a no-op on Windows")
	}
	tempHome := t.TempDir()
	s := newLoginShellForTest(t, tempHome)
	writeRCFile(t, tempHome, ".aish/aishrc.toml", `
[shell]
umask = "077"
`)
	var stderr bytes.Buffer
	s.loadRCFiles(&stderr)
	if stderr.Len() != 0 {
		t.Fatalf("unexpected stderr: %s", stderr.String())
	}
	// Create a file in tempHome — its permission should be 0600
	// (0666 & ~077 = 0600) which proves the umask landed.
	probePath := filepath.Join(tempHome, "probe")
	f, err := os.Create(probePath)
	if err != nil {
		t.Fatalf("create probe: %v", err)
	}
	_ = f.Close()
	defer os.Remove(probePath)
	st, err := os.Stat(probePath)
	if err != nil {
		t.Fatalf("stat probe: %v", err)
	}
	got := st.Mode().Perm()
	if got != 0o600 {
		t.Errorf("probe perm = %#o, want 0600 (umask 077 should have masked 0666)", got)
	}
	// Reset the umask so we don't pollute later tests in this
	// process (umask is per-process global state).
	applyUmask(0o022)
}

func TestApplyLoginEnvDefaults_SetsAISHVersion(t *testing.T) {
	s := newLoginShellForTest(t, t.TempDir())
	s.applyLoginEnvDefaults()
	got, _ := s.env.Get("AISH_VERSION")
	if got != "test-1.2.3" {
		t.Errorf("AISH_VERSION = %q, want %q", got, "test-1.2.3")
	}
}

func TestApplyLoginEnvDefaults_OverridesInheritedAISHVersion(t *testing.T) {
	s := newLoginShellForTest(t, t.TempDir())
	_ = s.env.Set("AISH_VERSION", "stale-from-parent")
	s.applyLoginEnvDefaults()
	got, _ := s.env.Get("AISH_VERSION")
	if got != "test-1.2.3" {
		t.Errorf("AISH_VERSION = %q, want %q — login mode must overwrite", got, "test-1.2.3")
	}
}

func TestApplyLoginEnvDefaults_PathDefaultWhenUnset(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PATH defaulting is POSIX-only")
	}
	s := newLoginShellForTest(t, t.TempDir())
	s.env.Unset("PATH")
	s.applyLoginEnvDefaults()
	got, _ := s.env.Get("PATH")
	if got != defaultPOSIXPath {
		t.Errorf("PATH = %q, want %q", got, defaultPOSIXPath)
	}
}

func TestApplyLoginEnvDefaults_PathLeftAloneWhenSet(t *testing.T) {
	s := newLoginShellForTest(t, t.TempDir())
	_ = s.env.Set("PATH", "/custom/bin")
	s.applyLoginEnvDefaults()
	got, _ := s.env.Get("PATH")
	if got != "/custom/bin" {
		t.Errorf("PATH = %q, want left alone", got)
	}
}
