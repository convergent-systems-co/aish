// Git config adapter — T4 of the v0.3-3 atomic-persona-switch plan.
//
// Strategy (Thomas-approved): shell-out to `git config --global …`.
// The plan documents the trade: git owns its own config file format
// including conditional-include rules; reimplementing it in Go for
// three keys is a tax we do not need to pay. The shell-out is a
// well-typed, stable interface (`git config` has documented exit
// codes and a contract that hasn't changed in decades).
//
// Scope is locked to "global" for v0.3-3. "local" / "system" are
// rejected at schema-validation time and deferred to v0.3-fu.
//
// Snapshot contents: {name, email, signingkey} tuple of captured
// values + per-key "was set?" flag. Rollback re-sets captured values
// or `--unset-all` for keys that were unset pre-Apply.

package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/convergent-systems-co/aish/shell/internal/persona"
)

// GitRunner abstracts `git config` invocations so tests can supply a
// recording stub. Production uses execGitRunner which shells out to
// the `git` binary on $PATH.
type GitRunner interface {
	// Available reports whether `git` is present on $PATH.
	Available(ctx context.Context) bool
	// Run executes `git <args...>` and returns stdout. Non-zero exit
	// is returned as an error; the GitAdapter relies on `git config
	// --get`'s exit code 1 (key not present) being distinguishable
	// from other errors via stderr substring matching.
	Run(ctx context.Context, args ...string) (string, error)
}

type execGitRunner struct {
	// extraEnv is appended to the child's env. Used by tests to set
	// HOME / XDG_CONFIG_HOME so `git config --global` writes to a
	// tempdir instead of the real ~/.gitconfig.
	extraEnv []string
}

func (e execGitRunner) Available(ctx context.Context) bool {
	_, err := exec.LookPath("git")
	return err == nil
}

func (e execGitRunner) Run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	if len(e.extraEnv) > 0 {
		cmd.Env = append(cmd.Environ(), e.extraEnv...)
		// When the runner was constructed with a sandbox HOME, pin
		// CWD to the same path so `git config --global` doesn't
		// inherit a stale CWD from the caller. (Real `git config
		// --global` cares about CWD because it walks up looking for
		// `.git/`; a missing CWD aborts with status 128.)
		for _, kv := range e.extraEnv {
			if strings.HasPrefix(kv, "HOME=") {
				cmd.Dir = strings.TrimPrefix(kv, "HOME=")
				break
			}
		}
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), wrapGitErr(err, args, stderr.String())
}

// ErrGitKeyNotPresent is returned by Run when `git config --get`
// exits with status 1 (the documented "key not present" signal). The
// adapter treats it as "captured value is empty" rather than a
// transaction failure. Exported so test-side GitRunner stubs can
// emit the same sentinel.
var ErrGitKeyNotPresent = errors.New("git: key not present")

// gitNotPresentErr is the internal alias retained for the existing
// uses inside this file. Will be folded away in a follow-up.
var gitNotPresentErr = ErrGitKeyNotPresent

func wrapGitErr(err error, args []string, stderr string) error {
	if err == nil {
		return nil
	}
	if ex, ok := err.(*exec.ExitError); ok {
		// `git config --get` exits 1 for "key not present" with no
		// stderr. Anything else is a real error.
		if ex.ExitCode() == 1 && stderr == "" && hasGet(args) {
			return gitNotPresentErr
		}
	}
	return fmt.Errorf("git %v: %w (stderr: %s)", args, err, strings.TrimSpace(stderr))
}

func hasGet(args []string) bool {
	for _, a := range args {
		if a == "--get" {
			return true
		}
	}
	return false
}

// GitAdapter implements PersonaAdapter for git config user.* keys.
type GitAdapter struct {
	runner GitRunner
}

// NewGitAdapter constructs an adapter wired to a real `git` binary.
func NewGitAdapter() *GitAdapter {
	return &GitAdapter{runner: execGitRunner{}}
}

// NewGitAdapterWithRunner is the test/integration constructor.
func NewGitAdapterWithRunner(r GitRunner) *GitAdapter {
	return &GitAdapter{runner: r}
}

// NewGitAdapterForHome returns an adapter that shells out to the real
// `git` but with HOME pointed at the given path, so `git config
// --global` reads/writes ~/.gitconfig under that path. Used by the
// T5 integration tests.
func NewGitAdapterForHome(home string) *GitAdapter {
	return &GitAdapter{runner: execGitRunner{
		extraEnv: []string{
			"HOME=" + home,
			"XDG_CONFIG_HOME=" + home + "/.config",
		},
	}}
}

// Name implements PersonaAdapter.
func (g *GitAdapter) Name() string { return "git" }

type gitSnapshot struct {
	NameSet       bool   `json:"name_set"`
	NameValue     string `json:"name_value"`
	EmailSet      bool   `json:"email_set"`
	EmailValue    string `json:"email_value"`
	SigningKeySet bool   `json:"signing_key_set"`
	SigningKey    string `json:"signing_key"`
}

// Capture reads the three keys via `git config --global --get`.
func (g *GitAdapter) Capture(ctx context.Context) (Snapshot, error) {
	if !g.runner.Available(ctx) {
		return nil, fmt.Errorf("%w: git not on PATH", ErrNoCLI)
	}
	snap := gitSnapshot{}
	for _, k := range []struct {
		key      string
		setFlag  *bool
		valueRef *string
	}{
		{"user.name", &snap.NameSet, &snap.NameValue},
		{"user.email", &snap.EmailSet, &snap.EmailValue},
		{"user.signingkey", &snap.SigningKeySet, &snap.SigningKey},
	} {
		out, err := g.runner.Run(ctx, "config", "--global", "--get", k.key)
		switch {
		case err == nil:
			*k.setFlag = true
			*k.valueRef = strings.TrimRight(out, "\n")
		case errors.Is(err, gitNotPresentErr):
			*k.setFlag = false
		default:
			return nil, fmt.Errorf("capture %s: %w", k.key, err)
		}
	}
	return json.Marshal(snap)
}

// Apply sets the three keys via `git config --global`.
func (g *GitAdapter) Apply(ctx context.Context, p persona.Persona) error {
	if p.ExternalBindings.Git == nil {
		return fmt.Errorf("%w: persona %q has no [external.git]", ErrNoBinding, p.Name)
	}
	b := p.ExternalBindings.Git
	if err := validateGitBinding(b); err != nil {
		return err
	}
	if !g.runner.Available(ctx) {
		return fmt.Errorf("%w: git not on PATH", ErrNoCLI)
	}

	if _, err := g.runner.Run(ctx, "config", "--global", "user.name", b.UserName); err != nil {
		return fmt.Errorf("apply user.name: %w", err)
	}
	if _, err := g.runner.Run(ctx, "config", "--global", "user.email", b.UserEmail); err != nil {
		return fmt.Errorf("apply user.email: %w", err)
	}
	if b.SigningKey != "" {
		if _, err := g.runner.Run(ctx, "config", "--global", "user.signingkey", b.SigningKey); err != nil {
			return fmt.Errorf("apply user.signingkey: %w", err)
		}
	} else {
		// Per ExternalBindings doc: SigningKey == "" with the block
		// declared means "unset the signing key." Use --unset-all
		// which is a no-op if the key wasn't set.
		if _, err := g.runner.Run(ctx, "config", "--global", "--unset-all", "user.signingkey"); err != nil {
			// Exit code 5 from --unset-all means "no such key" — treat
			// as success. wrapGitErr surfaces it as a generic error;
			// detect by string match.
			if !strings.Contains(err.Error(), "exit status 5") {
				return fmt.Errorf("apply unset signingkey: %w", err)
			}
		}
	}
	return nil
}

// Verify reads back the three keys and confirms they match the
// persona's binding.
func (g *GitAdapter) Verify(ctx context.Context, p persona.Persona) error {
	if p.ExternalBindings.Git == nil {
		return nil
	}
	b := p.ExternalBindings.Git

	checks := []struct {
		key  string
		want string
	}{
		{"user.name", b.UserName},
		{"user.email", b.UserEmail},
	}
	for _, c := range checks {
		out, err := g.runner.Run(ctx, "config", "--global", "--get", c.key)
		if err != nil {
			return fmt.Errorf("verify %s: %w", c.key, err)
		}
		got := strings.TrimRight(out, "\n")
		if got != c.want {
			return fmt.Errorf("verify %s: have %q want %q", c.key, got, c.want)
		}
	}
	// signingkey: verify "set with right value" OR "unset" depending
	// on the binding.
	out, err := g.runner.Run(ctx, "config", "--global", "--get", "user.signingkey")
	switch {
	case err == nil:
		got := strings.TrimRight(out, "\n")
		if b.SigningKey == "" {
			return fmt.Errorf("verify signingkey: persona unsets but value present (%q)", got)
		}
		if got != b.SigningKey {
			return fmt.Errorf("verify signingkey: have %q want %q", got, b.SigningKey)
		}
	case errors.Is(err, gitNotPresentErr):
		if b.SigningKey != "" {
			return fmt.Errorf("verify signingkey: want %q, got unset", b.SigningKey)
		}
	default:
		return fmt.Errorf("verify signingkey: %w", err)
	}
	return nil
}

// Rollback restores the three keys to their captured state.
func (g *GitAdapter) Rollback(ctx context.Context, snap Snapshot) error {
	var s gitSnapshot
	if err := json.Unmarshal(snap, &s); err != nil {
		return fmt.Errorf("decode git snapshot: %w", err)
	}
	for _, k := range []struct {
		key string
		set bool
		val string
	}{
		{"user.name", s.NameSet, s.NameValue},
		{"user.email", s.EmailSet, s.EmailValue},
		{"user.signingkey", s.SigningKeySet, s.SigningKey},
	} {
		if k.set {
			if _, err := g.runner.Run(ctx, "config", "--global", k.key, k.val); err != nil {
				return fmt.Errorf("rollback set %s: %w", k.key, err)
			}
			continue
		}
		// Was unset pre-Apply — unset cleanly via --unset-all.
		if _, err := g.runner.Run(ctx, "config", "--global", "--unset-all", k.key); err != nil {
			// Exit 5 means "no such key" — fine.
			if !strings.Contains(err.Error(), "exit status 5") {
				return fmt.Errorf("rollback unset %s: %w", k.key, err)
			}
		}
	}
	return nil
}

// validateGitBinding enforces schema rules: scope must be "global",
// user_name + user_email are required.
func validateGitBinding(b *persona.GitBinding) error {
	if b.Scope != "" && b.Scope != "global" {
		return fmt.Errorf("%w: [external.git].scope %q not supported (use \"global\")",
			ErrSchema, b.Scope)
	}
	if b.UserName == "" {
		return fmt.Errorf("%w: [external.git].user_name is empty", ErrSchema)
	}
	if b.UserEmail == "" {
		return fmt.Errorf("%w: [external.git].user_email is empty", ErrSchema)
	}
	return nil
}

// Compile-time assertion.
var _ PersonaAdapter = (*GitAdapter)(nil)
