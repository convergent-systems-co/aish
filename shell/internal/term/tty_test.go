package term

import (
	"bytes"
	"testing"
)

// TestIsTTY_NonTTY — a bytes.Buffer (or anything that isn't a real
// terminal file descriptor) MUST return false.
func TestIsTTY_NonTTY(t *testing.T) {
	if IsTTY(&bytes.Buffer{}) {
		t.Fatalf("bytes.Buffer is not a TTY")
	}
}

// TestIsTTY_NilReader — a nil reader returns false without panic.
func TestIsTTY_NilReader(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("IsTTY panicked on nil: %v", r)
		}
	}()
	if IsTTY(nil) {
		t.Fatalf("nil reader is not a TTY")
	}
}
