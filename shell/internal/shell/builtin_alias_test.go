package shell

import (
	"bytes"
	"strings"
	"testing"

	"github.com/convergent-systems-co/aish/shell/internal/env"
)

// newAliasTestShell returns a Shell wired only with env (no cache /
// history side effects). Tests that need aliases pre-seeded should
// assign s.aliases directly.
func newAliasTestShell() *Shell {
	return &Shell{env: env.New()}
}

func TestAliasBuiltin_BareLists(t *testing.T) {
	s := newAliasTestShell()
	s.aliasSet("ll", "ls -la")
	s.aliasSet("gs", "git status")
	var stdout, stderr bytes.Buffer
	exit := s.aliasBuiltin(nil, &stdout, &stderr)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0", exit)
	}
	out := stdout.String()
	if !strings.Contains(out, "alias gs='git status'") {
		t.Errorf("output missing gs alias: %q", out)
	}
	if !strings.Contains(out, "alias ll='ls -la'") {
		t.Errorf("output missing ll alias: %q", out)
	}
	// Sorted: gs before ll.
	if strings.Index(out, "alias gs") > strings.Index(out, "alias ll") {
		t.Errorf("aliases not sorted: %q", out)
	}
}

func TestAliasBuiltin_SetAndLookup(t *testing.T) {
	s := newAliasTestShell()
	var stdout, stderr bytes.Buffer
	if exit := s.aliasBuiltin([]string{"ll=ls -la"}, &stdout, &stderr); exit != 0 {
		t.Fatalf("set exit = %d, stderr=%s", exit, stderr.String())
	}
	if v, _ := s.aliasGet("ll"); v != "ls -la" {
		t.Errorf("ll alias = %q, want %q", v, "ls -la")
	}
	stdout.Reset()
	if exit := s.aliasBuiltin([]string{"ll"}, &stdout, &stderr); exit != 0 {
		t.Fatalf("lookup exit = %d", exit)
	}
	if !strings.Contains(stdout.String(), "alias ll='ls -la'") {
		t.Errorf("lookup output = %q", stdout.String())
	}
}

func TestAliasBuiltin_LookupMissingExits1(t *testing.T) {
	s := newAliasTestShell()
	var stdout, stderr bytes.Buffer
	exit := s.aliasBuiltin([]string{"nope"}, &stdout, &stderr)
	if exit != 1 {
		t.Errorf("exit = %d, want 1", exit)
	}
	if !strings.Contains(stderr.String(), "not found") {
		t.Errorf("stderr = %q, want 'not found'", stderr.String())
	}
}

func TestAliasBuiltin_StripsQuotesOnSet(t *testing.T) {
	s := newAliasTestShell()
	var stdout, stderr bytes.Buffer
	// Single-quoted value — quotes belong in the alias listing, not
	// stored.
	if exit := s.aliasBuiltin([]string{"ll='ls -la'"}, &stdout, &stderr); exit != 0 {
		t.Fatalf("exit = %d", exit)
	}
	if v, _ := s.aliasGet("ll"); v != "ls -la" {
		t.Errorf("alias value = %q, want %q", v, "ls -la")
	}
}

func TestResolveAlias_FirstTokenRewrite(t *testing.T) {
	s := newAliasTestShell()
	s.aliasSet("ll", "ls -la")
	var stderr bytes.Buffer
	got := s.resolveAlias("ll /etc", &stderr)
	if got != "ls -la /etc" {
		t.Errorf("resolveAlias = %q, want %q", got, "ls -la /etc")
	}
}

func TestResolveAlias_CycleStops(t *testing.T) {
	s := newAliasTestShell()
	s.aliasSet("a", "b foo")
	s.aliasSet("b", "a bar")
	var stderr bytes.Buffer
	got := s.resolveAlias("a", &stderr)
	// After cycle detection we should have iterated at least twice
	// (a -> b foo -> a bar foo) then stopped on seeing `a` again.
	if !strings.Contains(stderr.String(), "cycle detected") {
		t.Errorf("expected cycle warning, stderr=%q", stderr.String())
	}
	// The exact intermediate form is implementation detail; we only
	// verify it didn't blow up and emitted SOMETHING different from
	// the bare input.
	if got == "a" {
		t.Errorf("resolveAlias didn't expand at all: %q", got)
	}
}

func TestResolveAlias_NoMatchIdentity(t *testing.T) {
	s := newAliasTestShell()
	s.aliasSet("ll", "ls -la")
	var stderr bytes.Buffer
	got := s.resolveAlias("echo hi", &stderr)
	if got != "echo hi" {
		t.Errorf("resolveAlias = %q, want unchanged", got)
	}
}

func TestSplitAliasArgs_PreservesQuotedStrings(t *testing.T) {
	got := splitAliasArgs(`ll='ls -la' gs="git status"`)
	want := []string{"ll=ls -la", "gs=git status"}
	if !equalStringSlices(got, want) {
		t.Errorf("splitAliasArgs = %v, want %v", got, want)
	}
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
