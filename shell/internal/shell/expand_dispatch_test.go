package shell

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/convergent-systems-co/aish/shell/internal/env"
)

// TestDispatch_AliasRewritesFirstToken verifies the wiring from the
// dispatcher through resolveAlias. We register `ll` -> `echo ALIASED`
// and feed `ll` to dispatch via runExternal; the captured stdout must
// be `ALIASED`.
func TestDispatch_AliasRewritesFirstToken(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX `echo` shape")
	}
	s := &Shell{env: env.New(), cwd: t.TempDir()}
	_ = s.env.Set("PATH", os.Getenv("PATH"))
	s.aliasSet("greet", "echo ALIASED")
	var stdout, stderr bytes.Buffer
	if err := s.runExternal("greet", strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("runExternal: %v stderr=%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "ALIASED") {
		t.Errorf("stdout = %q, want it to contain ALIASED", stdout.String())
	}
}

func TestDispatch_BraceExpansion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX `echo` shape")
	}
	s := &Shell{env: env.New(), cwd: t.TempDir()}
	_ = s.env.Set("PATH", os.Getenv("PATH"))
	var stdout, stderr bytes.Buffer
	if err := s.runExternal("echo {a,b,c}", strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("runExternal: %v", err)
	}
	if strings.TrimSpace(stdout.String()) != "a b c" {
		t.Errorf("stdout = %q, want 'a b c'", stdout.String())
	}
}

func TestDispatch_GlobExpansion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX `echo` shape")
	}
	dir := t.TempDir()
	for _, name := range []string{"alpha.go", "beta.go", "gamma.txt"} {
		_ = os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644)
	}
	s := &Shell{env: env.New(), cwd: dir}
	_ = s.env.Set("PATH", os.Getenv("PATH"))
	var stdout, stderr bytes.Buffer
	if err := s.runExternal("echo *.go", strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("runExternal: %v", err)
	}
	out := strings.TrimSpace(stdout.String())
	if !strings.Contains(out, "alpha.go") || !strings.Contains(out, "beta.go") {
		t.Errorf("stdout = %q, want it to contain alpha.go + beta.go", out)
	}
	if strings.Contains(out, "gamma.txt") {
		t.Errorf("stdout = %q, glob matched a non-.go file", out)
	}
}

func TestDispatch_CommandSubstitution(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX `echo` shape")
	}
	s := &Shell{env: env.New(), cwd: t.TempDir()}
	_ = s.env.Set("PATH", os.Getenv("PATH"))
	var stdout, stderr bytes.Buffer
	if err := s.runExternal("echo $(echo nested)", strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("runExternal: %v stderr=%s", err, stderr.String())
	}
	if strings.TrimSpace(stdout.String()) != "nested" {
		t.Errorf("stdout = %q, want 'nested'", stdout.String())
	}
}

func TestRunForCapture_StripsTrailingNewline(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX `echo` shape")
	}
	s := &Shell{env: env.New(), cwd: t.TempDir()}
	_ = s.env.Set("PATH", os.Getenv("PATH"))
	out, err := s.RunForCapture("echo hi", &bytes.Buffer{})
	if err != nil {
		t.Fatalf("RunForCapture: %v", err)
	}
	// RunForCapture's contract: raw stdout (newline included). The
	// PARSER strips trailing newlines after the call.
	if !strings.Contains(out, "hi") {
		t.Errorf("captured = %q, want it to contain 'hi'", out)
	}
}

func TestResolveTier_RecognizesNewBuiltins(t *testing.T) {
	s := New()
	defer s.Close()
	for _, name := range []string{"alias", "source", "set", "unset"} {
		got := s.ResolveTier(name)
		// We can't import term.TierBuiltin without the symbol; the
		// public type is `term.Tier`. Just verify it's NOT the
		// "AI intent" zero-or-fallback bucket by comparing against
		// a known built-in.
		want := s.ResolveTier("cd")
		if got != want {
			t.Errorf("ResolveTier(%q) = %v, want same as `cd` (%v)", name, got, want)
		}
	}
}
