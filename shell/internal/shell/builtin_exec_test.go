package shell

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/convergent-systems-co/aish/shell/internal/env"
)

// newTestShellExec builds a Shell wired for exec tests. Injects
// an execFn that records the resolved binary + argv instead of
// actually calling syscall.Exec — production would replace the
// process and never return; tests would never finish.
func newTestShellExec() (*Shell, *execCapture) {
	cap := &execCapture{}
	s := &Shell{
		env:    env.New(),
		execFn: cap.fn,
	}
	return s, cap
}

type execCapture struct {
	called bool
	argv0  string
	argv   []string
	envv   []string
}

// fn is the injected execFn: record the call and return nil so
// the caller treats it as a successful exec.
func (c *execCapture) fn(argv0 string, argv, envv []string) error {
	c.called = true
	c.argv0 = argv0
	c.argv = argv
	c.envv = envv
	return nil
}

func TestExecBuiltin_NoArgs_SilentSuccess(t *testing.T) {
	s, cap := newTestShellExec()
	var stderr bytes.Buffer
	err := s.execBuiltin(nil, nil, &stderr)
	if err != nil {
		t.Errorf("bare exec returned %v, want nil", err)
	}
	if cap.called {
		t.Error("execFn called for bare exec — should be a no-op")
	}
	if s.LastExit() != 0 {
		t.Errorf("lastExit = %d, want 0", s.LastExit())
	}
	if stderr.Len() != 0 {
		t.Errorf("unexpected stderr: %s", stderr.String())
	}
}

func TestExecBuiltin_NotFound_ReturnsSentinel(t *testing.T) {
	s, cap := newTestShellExec()
	// Empty PATH guarantees lookup failure even on hosts with a
	// real /usr/bin/this-does-not-exist binary.
	_ = s.env.Set("PATH", "")
	var stderr bytes.Buffer
	err := s.execBuiltin([]string{"definitely-not-on-path-xyz"}, nil, &stderr)
	if err == nil {
		t.Fatal("expected errExecReplaced{Code:127}, got nil")
	}
	code, ok := IsExecReplaced(err)
	if !ok {
		t.Fatalf("err is not errExecReplaced: %T", err)
	}
	if code != 127 {
		t.Errorf("exit code = %d, want 127", code)
	}
	if s.LastExit() != 127 {
		t.Errorf("lastExit = %d, want 127", s.LastExit())
	}
	if !strings.Contains(stderr.String(), "command not found") {
		t.Errorf("stderr = %q, want 'command not found'", stderr.String())
	}
	if cap.called {
		t.Error("execFn should not be called when lookup fails")
	}
}

func TestExecBuiltin_ResolvesBinaryAndInvokesExecFn(t *testing.T) {
	s, cap := newTestShellExec()
	// /bin/sh exists on every POSIX test host (CI included).
	_ = s.env.Set("PATH", "/bin:/usr/bin")
	_ = s.env.Set("FOO", "bar")
	var stderr bytes.Buffer
	err := s.execBuiltin([]string{"sh", "-c", "true"}, nil, &stderr)
	if err != nil {
		t.Errorf("err = %v, want nil from the injected stub", err)
	}
	if !cap.called {
		t.Fatal("execFn was not called")
	}
	if !strings.HasSuffix(cap.argv0, "/sh") {
		t.Errorf("argv0 = %q, want path ending in /sh", cap.argv0)
	}
	wantArgv := []string{"sh", "-c", "true"}
	if !equalSlice(cap.argv, wantArgv) {
		t.Errorf("argv = %v, want %v", cap.argv, wantArgv)
	}
	// envv must include FOO=bar so the child sees the shell's env.
	foundFoo := false
	for _, kv := range cap.envv {
		if kv == "FOO=bar" {
			foundFoo = true
			break
		}
	}
	if !foundFoo {
		t.Error("envv did not contain FOO=bar from the shell env")
	}
}

func TestExecBuiltin_AbsolutePathBypassesLookup(t *testing.T) {
	s, cap := newTestShellExec()
	// Empty PATH would fail a name lookup, but absolute paths
	// must bypass that branch.
	_ = s.env.Set("PATH", "")
	var stderr bytes.Buffer
	err := s.execBuiltin([]string{"/bin/sh"}, nil, &stderr)
	if err != nil {
		t.Errorf("err = %v, want nil — absolute path should resolve", err)
	}
	if !cap.called {
		t.Fatal("execFn was not called for absolute path")
	}
	if cap.argv0 != "/bin/sh" {
		t.Errorf("argv0 = %q, want /bin/sh", cap.argv0)
	}
}

func TestParseExecLine_BareEmpty(t *testing.T) {
	args, err := parseExecLine("")
	if err != nil {
		t.Errorf("parseExecLine(\"\") err = %v, want nil", err)
	}
	if len(args) != 0 {
		t.Errorf("args = %v, want empty", args)
	}
}

func TestParseExecLine_SingleCommand(t *testing.T) {
	args, err := parseExecLine("/bin/sh -c true")
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	want := []string{"/bin/sh", "-c", "true"}
	if !equalSlice(args, want) {
		t.Errorf("args = %v, want %v", args, want)
	}
}

func TestIsExecReplaced_NegativeNil(t *testing.T) {
	if _, ok := IsExecReplaced(nil); ok {
		t.Error("IsExecReplaced(nil) returned ok=true")
	}
	if _, ok := IsExecReplaced(errors.New("random")); ok {
		t.Error("IsExecReplaced(random err) returned ok=true")
	}
}

// equalSlice is a small comparison helper to avoid pulling in
// reflect.DeepEqual for primitive string slices.
func equalSlice(a, b []string) bool {
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
