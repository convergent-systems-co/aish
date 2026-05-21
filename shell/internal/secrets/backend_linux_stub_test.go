//go:build !linux

package secrets

import (
	"errors"
	"testing"
)

// TestOpenLinuxBackend_StubReturnsUnsupported exercises the
// non-linux sentinel path for OpenLinuxBackend. darwin / Windows CI
// runs this to confirm dispatch code can rely on ErrUnsupported
// instead of guarding every call site with runtime.GOOS.
func TestOpenLinuxBackend_StubReturnsUnsupported(t *testing.T) {
	be, err := OpenLinuxBackend("aish", "")
	if be != nil {
		t.Fatalf("expected nil backend on non-linux host, got %T", be)
	}
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("expected ErrUnsupported, got %v", err)
	}
}
