package shell

import (
	"bytes"
	"strings"
	"testing"
)

func identityTestShell(t *testing.T) (*Shell, string) {
	t.Helper()
	home := t.TempDir()
	s := New()
	if err := s.env.Set("HOME", home); err != nil {
		t.Fatalf("set HOME: %v", err)
	}
	return s, home
}

// TestIdentity_Usage — bare `identity` prints usage and exits 0.
func TestIdentity_Usage(t *testing.T) {
	s, _ := identityTestShell(t)
	var stdout, stderr bytes.Buffer
	code := s.identityBuiltin(nil, nil, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("identity (no args) exit = %d; stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "use") || !strings.Contains(stdout.String(), "list") {
		t.Errorf("usage missing expected subcommands:\n%s", stdout.String())
	}
}

// TestIdentity_CreateThenList — create writes a profile, list shows
// it.
func TestIdentity_CreateThenList(t *testing.T) {
	s, _ := identityTestShell(t)
	var stdout, stderr bytes.Buffer
	// No stdin lines → no gateway / signer set.
	if code := s.identityBuiltin([]string{"create", "work"}, bytes.NewBufferString("\n\n"), &stdout, &stderr); code != 0 {
		t.Fatalf("create exit = %d; stderr=%q", code, stderr.String())
	}
	stdout.Reset()
	if code := s.identityBuiltin([]string{"list"}, nil, &stdout, &stderr); code != 0 {
		t.Fatalf("list exit = %d; stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "work") {
		t.Errorf("list missing 'work':\n%s", stdout.String())
	}
}

// TestIdentity_UseSetsActive — use marks the profile active; list's
// active marker (`* `) flips.
func TestIdentity_UseSetsActive(t *testing.T) {
	s, _ := identityTestShell(t)
	var stdout, stderr bytes.Buffer
	for _, n := range []string{"work", "personal"} {
		if code := s.identityBuiltin([]string{"create", n}, bytes.NewBufferString("\n\n"), &stdout, &stderr); code != 0 {
			t.Fatalf("create %s: exit=%d %s", n, code, stderr.String())
		}
		stdout.Reset()
	}
	if code := s.identityBuiltin([]string{"use", "work"}, nil, &stdout, &stderr); code != 0 {
		t.Fatalf("use work exit=%d %s", code, stderr.String())
	}
	stdout.Reset()
	if code := s.identityBuiltin([]string{"list"}, nil, &stdout, &stderr); code != 0 {
		t.Fatalf("list exit=%d %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "* work") {
		t.Errorf("active marker missing for work:\n%s", stdout.String())
	}
}

// TestIdentity_UseRefusesMissingProfile — using an unknown name MUST
// fail loudly without writing the active pointer.
func TestIdentity_UseRefusesMissingProfile(t *testing.T) {
	s, _ := identityTestShell(t)
	var stdout, stderr bytes.Buffer
	if code := s.identityBuiltin([]string{"use", "ghost"}, nil, &stdout, &stderr); code == 0 {
		t.Fatalf("use ghost exit=0; want non-zero")
	}
}

// TestIdentity_ResolveTier — `identity` is a built-in.
func TestIdentity_ResolveTier(t *testing.T) {
	s, _ := identityTestShell(t)
	if got := s.ResolveTier("identity"); got != tierBuiltinExpected() {
		t.Errorf("ResolveTier(identity) = %v; want builtin", got)
	}
}
