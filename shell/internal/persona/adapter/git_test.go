package adapter

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/convergent-systems-co/aish/shell/internal/persona"
)

// T4 — Git config adapter tests. Each test runs against a sandbox
// $HOME under t.TempDir() with the real `git` binary so we exercise
// the actual `git config --global` semantics (including `--unset-all`
// exit codes).

func personaWithGit(name, email, signing string) persona.Persona {
	p := persona.Persona{
		Name:         "git-test",
		Version:      persona.SchemaVersion,
		SystemPrompt: "test",
		Tone:         persona.Tone{Verbosity: "medium", Formality: "neutral"},
	}
	p.ExternalBindings.Git = &persona.GitBinding{
		Scope:      "global",
		UserName:   name,
		UserEmail:  email,
		SigningKey: signing,
	}
	return p
}

// gitSandboxHome creates a tempdir suitable for use as $HOME by `git
// config --global`. seed, if non-nil, is written verbatim to
// ~/.gitconfig before the test runs.
func gitSandboxHome(t *testing.T, seed []byte) string {
	t.Helper()
	dir := t.TempDir()
	if seed != nil {
		if err := os.WriteFile(filepath.Join(dir, ".gitconfig"), seed, 0o644); err != nil {
			t.Fatalf("seed gitconfig: %v", err)
		}
	}
	return dir
}

// requireGit skips the test if `git` is not on $PATH (CI shape we
// don't control everywhere).
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH — skipping (CI environment is responsible for installing git per the v0.3-3 plan §11.5 "+
			"acceptance; see issue #104 follow-up if this fires)")
	}
}

// readGitConfig reads a key via `git config --global --get` against
// the given sandbox HOME. Returns ("", false) if the key is unset.
func readGitConfig(t *testing.T, home, key string) (string, bool) {
	t.Helper()
	cmd := exec.Command("git", "config", "--global", "--get", key)
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "HOME="+home, "XDG_CONFIG_HOME="+filepath.Join(home, ".config"))
	out, err := cmd.Output()
	if err != nil {
		if ex, ok := err.(*exec.ExitError); ok && ex.ExitCode() == 1 {
			return "", false
		}
		t.Fatalf("git config --get %s: %v", key, err)
	}
	return strings.TrimRight(string(out), "\n"), true
}

// TestGitAdapter_ConfigSwap — sandbox $HOME; Apply with all three
// values; Rollback; asserted via `git config --get` calls reading
// the post-state.
func TestGitAdapter_ConfigSwap(t *testing.T) {
	requireGit(t)
	t.Parallel()
	seed := []byte("[user]\n\tname = Personal\n\temail = personal@example.test\n")
	home := gitSandboxHome(t, seed)
	ad := NewGitAdapterForHome(home)

	ctx := context.Background()
	snap, err := ad.Capture(ctx)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	p := personaWithGit("Work Identity", "work@example.test", "0xABCD1234")
	if err := ad.Apply(ctx, p); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got, _ := readGitConfig(t, home, "user.name"); got != "Work Identity" {
		t.Fatalf("post-apply user.name = %q; want Work Identity", got)
	}
	if got, _ := readGitConfig(t, home, "user.email"); got != "work@example.test" {
		t.Fatalf("post-apply user.email = %q; want work@example.test", got)
	}
	if got, _ := readGitConfig(t, home, "user.signingkey"); got != "0xABCD1234" {
		t.Fatalf("post-apply user.signingkey = %q; want 0xABCD1234", got)
	}
	if err := ad.Verify(ctx, p); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if err := ad.Rollback(ctx, snap); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if got, _ := readGitConfig(t, home, "user.name"); got != "Personal" {
		t.Fatalf("post-rollback user.name = %q; want Personal", got)
	}
	if got, _ := readGitConfig(t, home, "user.email"); got != "personal@example.test" {
		t.Fatalf("post-rollback user.email = %q; want personal@example.test", got)
	}
}

// TestGitAdapter_UnsetIsRestoredAsUnset — sandbox starts with no
// user.signingkey set; Apply sets one; Rollback unsets cleanly.
func TestGitAdapter_UnsetIsRestoredAsUnset(t *testing.T) {
	requireGit(t)
	t.Parallel()
	// Seed with name+email but no signingkey.
	seed := []byte("[user]\n\tname = X\n\temail = x@y.test\n")
	home := gitSandboxHome(t, seed)
	ad := NewGitAdapterForHome(home)

	ctx := context.Background()
	snap, err := ad.Capture(ctx)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	p := personaWithGit("X", "x@y.test", "0xKEYY")
	if err := ad.Apply(ctx, p); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got, set := readGitConfig(t, home, "user.signingkey"); !set || got != "0xKEYY" {
		t.Fatalf("post-apply user.signingkey = %q set=%v; want 0xKEYY set", got, set)
	}
	if err := ad.Rollback(ctx, snap); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if got, set := readGitConfig(t, home, "user.signingkey"); set {
		t.Fatalf("post-rollback user.signingkey = %q set=%v; want unset", got, set)
	}
}

// TestGitAdapter_ScopeRefuseLocal — scope = "local" is rejected at
// schema-validation time. Apply returns wrapped ErrSchema without
// invoking git.
func TestGitAdapter_ScopeRefuseLocal(t *testing.T) {
	t.Parallel()
	// Stub runner so we can assert it was NEVER called.
	stub := &countingGitRunner{available: true}
	ad := NewGitAdapterWithRunner(stub)
	p := persona.Persona{
		Name:         "x",
		Version:      persona.SchemaVersion,
		SystemPrompt: "x",
		Tone:         persona.Tone{Verbosity: "medium", Formality: "neutral"},
	}
	p.ExternalBindings.Git = &persona.GitBinding{
		Scope: "local", UserName: "X", UserEmail: "x@y.test",
	}
	err := ad.Apply(context.Background(), p)
	if err == nil {
		t.Fatal("expected ErrSchema for scope=local")
	}
	if !errors.Is(err, ErrSchema) {
		t.Fatalf("expected wrapped ErrSchema; got %v", err)
	}
	if stub.calls != 0 {
		t.Fatalf("git must not be invoked when schema fails; got %d calls", stub.calls)
	}
}

// TestGitAdapter_EmptyUserNameRejectedAsSchema — [external.git] with
// user_name = "" is rejected at schema validation.
func TestGitAdapter_EmptyUserNameRejectedAsSchema(t *testing.T) {
	t.Parallel()
	stub := &countingGitRunner{available: true}
	ad := NewGitAdapterWithRunner(stub)
	p := persona.Persona{
		Name:         "x",
		Version:      persona.SchemaVersion,
		SystemPrompt: "x",
		Tone:         persona.Tone{Verbosity: "medium", Formality: "neutral"},
	}
	p.ExternalBindings.Git = &persona.GitBinding{
		Scope: "global", UserName: "", UserEmail: "x@y.test",
	}
	err := ad.Apply(context.Background(), p)
	if err == nil {
		t.Fatal("expected ErrSchema for empty user_name")
	}
	if !errors.Is(err, ErrSchema) {
		t.Fatalf("expected wrapped ErrSchema; got %v", err)
	}
	if stub.calls != 0 {
		t.Fatalf("git must not be invoked when schema fails; got %d calls", stub.calls)
	}
}

// TestGitAdapter_GitNotOnPATHReturnsErrNoCLI — if `git` is not on
// $PATH, Capture returns wrapped ErrNoCLI.
func TestGitAdapter_GitNotOnPATHReturnsErrNoCLI(t *testing.T) {
	t.Parallel()
	stub := &countingGitRunner{available: false}
	ad := NewGitAdapterWithRunner(stub)
	_, err := ad.Capture(context.Background())
	if err == nil {
		t.Fatal("expected ErrNoCLI when git is missing")
	}
	if !errors.Is(err, ErrNoCLI) {
		t.Fatalf("expected wrapped ErrNoCLI; got %v", err)
	}
}

// countingGitRunner is a stub for tests that assert "git was not
// invoked when schema validation refuses early."
type countingGitRunner struct {
	available bool
	calls     int
}

func (c *countingGitRunner) Available(ctx context.Context) bool { return c.available }
func (c *countingGitRunner) Run(ctx context.Context, args ...string) (string, error) {
	c.calls++
	return "", nil
}
