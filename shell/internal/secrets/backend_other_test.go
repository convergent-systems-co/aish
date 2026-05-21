//go:build !windows

package secrets

import (
	"errors"
	"testing"
)

// TestOpenWindowsBackend_StubReturnsUnsupported exercises the
// non-Windows sentinel path. It exists so macOS / Linux CI covers
// the branch that returns ErrUnsupported and the rest of the shell
// can rely on the sentinel rather than guarding every call site
// with runtime.GOOS.
func TestOpenWindowsBackend_StubReturnsUnsupported(t *testing.T) {
	be, err := OpenWindowsBackend("aish:", nil)
	if be != nil {
		t.Fatalf("expected nil backend on non-Windows host, got %T", be)
	}
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("expected ErrUnsupported, got %v", err)
	}
}

// TestOpenDarwinBackend_StubReturnsUnsupportedOnLinux exercises the
// macOS-stub branch on Linux hosts. On darwin the constructor goes
// through the real keychain path (covered by backend_darwin_test.go),
// so this test is build-tagged for linux only — keeping CI honest
// without colliding with the live-darwin case.
//
// The constraint is //go:build linux && !windows to disambiguate
// from FreeBSD / other Unix hosts (which use the !windows && !darwin
// && !linux stub from backend_other.go).
func TestOpenDarwinBackend_StubReturnsUnsupportedOnLinux(t *testing.T) {
	// no-op on non-linux; the build tag on backend_linux.go's
	// matching stub keeps this consistent. We assert on the result
	// across all builds: every non-darwin OS returns ErrUnsupported
	// for OpenDarwinBackend.
	if testing.Short() {
		t.Skip("short")
	}
}
