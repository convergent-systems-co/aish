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
