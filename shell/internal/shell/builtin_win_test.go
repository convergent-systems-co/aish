package shell

import (
	"bytes"
	"runtime"
	"strings"
	"testing"
)

// These tests exercise the dispatch + argv parsing of the five
// Windows-targeted built-ins. On non-Windows hosts the exec/win32
// stubs surface ErrUnsupported, which we assert the built-ins
// translate into a polite "not supported" message + exit 2. On a
// Windows runtime the Win32 path runs end-to-end; we keep the
// assertions tight enough that they hold under either branch.

func TestInstallBuiltinUsage(t *testing.T) {
	s := New()
	defer s.Close()
	var stdout, stderr bytes.Buffer
	code := s.installBuiltin([]string{}, &stdout, &stderr)
	if code != 0 {
		t.Errorf("install (no args) exit = %d, want 0 (usage)", code)
	}
	if !strings.Contains(stdout.String(), "usage:") {
		t.Errorf("install (no args) stdout = %q, want usage", stdout.String())
	}
}

func TestInstallBuiltinNonWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("non-windows path only")
	}
	s := New()
	defer s.Close()
	var stdout, stderr bytes.Buffer
	code := s.installBuiltin([]string{"git"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("install <pkg> on %s exit = %d, want 2", runtime.GOOS, code)
	}
	if !strings.Contains(stderr.String(), "not supported") {
		t.Errorf("install stderr = %q, want `not supported`", stderr.String())
	}
}

func TestServiceBuiltinNonWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("non-windows path only")
	}
	cases := []struct {
		args []string
		want string // substring expected in stderr
	}{
		{[]string{"list"}, "not supported"},
		{[]string{"status", "Spooler"}, "not supported"},
		{[]string{"start", "Spooler"}, "not supported"},
		{[]string{"stop", "Spooler"}, "not supported"},
	}
	s := New()
	defer s.Close()
	for _, tc := range cases {
		var stdout, stderr bytes.Buffer
		code := s.serviceBuiltin(tc.args, &stdout, &stderr)
		if code != 2 {
			t.Errorf("service %v exit = %d, want 2", tc.args, code)
		}
		if !strings.Contains(stderr.String(), tc.want) {
			t.Errorf("service %v stderr = %q, want %q", tc.args, stderr.String(), tc.want)
		}
	}
}

func TestServiceBuiltinUnknownSub(t *testing.T) {
	s := New()
	defer s.Close()
	var stdout, stderr bytes.Buffer
	code := s.serviceBuiltin([]string{"frobnicate"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("service frobnicate exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "unknown subcommand") {
		t.Errorf("service frobnicate stderr = %q, want `unknown subcommand`", stderr.String())
	}
}

func TestProcessBuiltinNonWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("non-windows path only")
	}
	s := New()
	defer s.Close()
	for _, args := range [][]string{{"list"}, {"kill", "1234"}} {
		var stdout, stderr bytes.Buffer
		code := s.processBuiltin(args, &stdout, &stderr)
		if code != 2 {
			t.Errorf("process %v exit = %d, want 2", args, code)
		}
	}
}

func TestProcessKillBadPID(t *testing.T) {
	s := New()
	defer s.Close()
	var stdout, stderr bytes.Buffer
	code := s.processBuiltin([]string{"kill", "not-a-number"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("process kill <not-a-number> exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "invalid pid") {
		t.Errorf("process kill bad-pid stderr = %q, want `invalid pid`", stderr.String())
	}
}

func TestEnvBuiltinSetGetUnset(t *testing.T) {
	s := New()
	defer s.Close()
	// set
	var sOut, sErr bytes.Buffer
	if code := s.envBuiltin([]string{"set", "AISH_TEST_VAR", "hello"}, &sOut, &sErr); code != 0 {
		t.Fatalf("env set exit = %d, want 0 (stderr=%q)", code, sErr.String())
	}
	// get
	var gOut, gErr bytes.Buffer
	if code := s.envBuiltin([]string{"get", "AISH_TEST_VAR"}, &gOut, &gErr); code != 0 {
		t.Fatalf("env get exit = %d, want 0", code)
	}
	if !strings.Contains(gOut.String(), "hello") {
		t.Errorf("env get stdout = %q, want `hello`", gOut.String())
	}
	// unset
	var uOut, uErr bytes.Buffer
	if code := s.envBuiltin([]string{"unset", "AISH_TEST_VAR"}, &uOut, &uErr); code != 0 {
		t.Fatalf("env unset exit = %d, want 0", code)
	}
	// get again — should miss (exit 1)
	var gOut2, gErr2 bytes.Buffer
	if code := s.envBuiltin([]string{"get", "AISH_TEST_VAR"}, &gOut2, &gErr2); code != 1 {
		t.Errorf("env get after unset exit = %d, want 1", code)
	}
}

func TestEnvBuiltinListSorted(t *testing.T) {
	s := New()
	defer s.Close()
	// Insert two vars in reverse-alphabetical order then list.
	_ = s.env.Set("AISH_TEST_Z", "z")
	_ = s.env.Set("AISH_TEST_A", "a")
	var stdout, stderr bytes.Buffer
	if code := s.envBuiltin([]string{"list"}, &stdout, &stderr); code != 0 {
		t.Fatalf("env list exit = %d", code)
	}
	out := stdout.String()
	aIdx := strings.Index(out, "AISH_TEST_A=")
	zIdx := strings.Index(out, "AISH_TEST_Z=")
	if aIdx < 0 || zIdx < 0 {
		t.Fatalf("env list missing entries: A=%d Z=%d\n%s", aIdx, zIdx, out)
	}
	if aIdx > zIdx {
		t.Errorf("env list not sorted: A at %d, Z at %d", aIdx, zIdx)
	}
}

func TestNetworkBuiltinNonWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("non-windows path only")
	}
	s := New()
	defer s.Close()
	var stdout, stderr bytes.Buffer
	code := s.networkBuiltin([]string{"interfaces"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("network interfaces exit = %d, want 2", code)
	}
}

func TestNetworkRoutesDeferred(t *testing.T) {
	s := New()
	defer s.Close()
	var stdout, stderr bytes.Buffer
	code := s.networkBuiltin([]string{"routes"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("network routes exit = %d, want 2 (deferred)", code)
	}
	if !strings.Contains(stderr.String(), "not yet implemented") {
		t.Errorf("network routes stderr = %q, want `not yet implemented`", stderr.String())
	}
}
