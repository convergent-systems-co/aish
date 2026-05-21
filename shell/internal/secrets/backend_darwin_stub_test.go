//go:build !darwin

package secrets

import (
	"errors"
	"testing"
)

// TestOpenDarwinBackend_StubReturnsUnsupported exercises the
// non-darwin sentinel path for OpenDarwinBackend. Linux / Windows CI
// runs this to confirm dispatch code can rely on ErrUnsupported
// instead of guarding every call site with runtime.GOOS.
func TestOpenDarwinBackend_StubReturnsUnsupported(t *testing.T) {
	be, err := OpenDarwinBackend("aish", "")
	if be != nil {
		t.Fatalf("expected nil backend on non-darwin host, got %T", be)
	}
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("expected ErrUnsupported, got %v", err)
	}
}
