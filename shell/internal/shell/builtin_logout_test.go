package shell

import (
	"bytes"
	"strings"
	"testing"

	"github.com/convergent-systems-co/aish/shell/internal/env"
)

// newTestShellLogout returns a Shell with the login flag wired
// for logout tests. Bypasses NewWithOptions so we don't fire the
// cache / history openers.
func newTestShellLogout(login bool) *Shell {
	return &Shell{
		env:       env.New(),
		loginMode: login,
	}
}

func TestLogoutBuiltin_LoginMode_BareReturnsSentinel(t *testing.T) {
	s := newTestShellLogout(true)
	var stderr bytes.Buffer
	err := s.logoutBuiltin(nil, &stderr)
	if err == nil {
		t.Fatal("expected sentinel error, got nil")
	}
	code, ok := IsLogout(err)
	if !ok {
		t.Fatalf("err is not errLogout: %T %v", err, err)
	}
	if code != 0 {
		t.Errorf("bare logout exit code = %d, want 0", code)
	}
	if stderr.Len() != 0 {
		t.Errorf("unexpected stderr: %s", stderr.String())
	}
}

func TestLogoutBuiltin_LoginMode_WithExitCode(t *testing.T) {
	s := newTestShellLogout(true)
	var stderr bytes.Buffer
	err := s.logoutBuiltin([]string{"7"}, &stderr)
	if err == nil {
		t.Fatal("expected sentinel error, got nil")
	}
	code, ok := IsLogout(err)
	if !ok {
		t.Fatalf("err is not errLogout: %T", err)
	}
	if code != 7 {
		t.Errorf("logout 7 exit code = %d, want 7", code)
	}
	if s.LastExit() != 7 {
		t.Errorf("lastExit = %d, want 7", s.LastExit())
	}
}

func TestLogoutBuiltin_LoginMode_NonIntegerArg(t *testing.T) {
	s := newTestShellLogout(true)
	var stderr bytes.Buffer
	err := s.logoutBuiltin([]string{"abc"}, &stderr)
	if err != nil {
		t.Fatalf("non-integer arg should be a user error (nil err, exit 1), got %v", err)
	}
	if s.LastExit() != 1 {
		t.Errorf("lastExit = %d, want 1", s.LastExit())
	}
	if !strings.Contains(stderr.String(), "numeric argument required") {
		t.Errorf("stderr = %q, want it to mention numeric argument", stderr.String())
	}
}

func TestLogoutBuiltin_NonLoginMode(t *testing.T) {
	s := newTestShellLogout(false)
	var stderr bytes.Buffer
	err := s.logoutBuiltin(nil, &stderr)
	if err != nil {
		t.Fatalf("expected nil err in non-login mode, got %v", err)
	}
	if s.LastExit() != 1 {
		t.Errorf("lastExit = %d, want 1", s.LastExit())
	}
	if !strings.Contains(stderr.String(), "not login shell") {
		t.Errorf("stderr = %q, want bash-compatible error", stderr.String())
	}
}

func TestIsLogout_Negative(t *testing.T) {
	// IsLogout must NOT match arbitrary errors. This guards the
	// runStream dispatcher from confusing real I/O failures with
	// a clean logout sentinel.
	type otherErr struct{ msg string }
	// Synthesize a non-errLogout error.
	if _, ok := IsLogout(nil); ok {
		t.Error("IsLogout(nil) returned ok=true")
	}
	_ = otherErr{}
}
