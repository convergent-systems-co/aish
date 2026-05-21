//go:build linux

package secrets

import (
	"bytes"
	"errors"
	"os"
	"testing"
)

// TestOpenLinuxBackend_NoSessionBus exercises the "platform can't run
// this" path. CI containers without a session bus surface this branch
// (DBUS_SESSION_BUS_ADDRESS unset and no autolaunched bus). The test
// passes either when the constructor returns ErrUnsupported OR when a
// live bus is available — Linux CI hosts vary too much to assert on
// one outcome here. The point is to make sure the error type is
// recoverable for the dispatch layer.
func TestOpenLinuxBackend_ErrorIsRecoverable(t *testing.T) {
	prev := os.Getenv("DBUS_SESSION_BUS_ADDRESS")
	t.Cleanup(func() {
		if prev != "" {
			_ = os.Setenv("DBUS_SESSION_BUS_ADDRESS", prev)
		} else {
			_ = os.Unsetenv("DBUS_SESSION_BUS_ADDRESS")
		}
	})
	// Forcibly clear the env var so the open path falls through to
	// the unsupported branch on hosts that don't autolaunch a bus.
	_ = os.Unsetenv("DBUS_SESSION_BUS_ADDRESS")

	be, err := OpenLinuxBackend("aish:test", nil)
	if err == nil {
		// Some hosts still autolaunch a bus despite the env clear;
		// that's OK — Close and skip the assertion.
		if be == nil {
			t.Fatal("nil backend with nil error")
		}
		_ = be.Close()
		t.Skip("session bus auto-launched on this host; live path covered by gated test")
	}
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("expected ErrUnsupported (recoverable), got %v", err)
	}
	if be != nil {
		t.Fatalf("expected nil backend on error, got %T", be)
	}
}

// TestLinuxBackend_LiveRoundTrip exercises the real D-Bus path. Gated
// by AISH_LINUX_BACKEND_LIVE=1 so CI without a keyring daemon is not
// noisy.
//
// The test stores a sentinel under a uniquely-named entry, reads it
// back, lists it, removes it, and confirms the post-remove state.
// Sentinel pattern follows the §4 rule: [REDACTED:test-value] form
// so a grep across the codebase doesn't pick up secret-looking
// literals.
func TestLinuxBackend_LiveRoundTrip(t *testing.T) {
	if os.Getenv("AISH_LINUX_BACKEND_LIVE") != "1" {
		t.Skip("AISH_LINUX_BACKEND_LIVE not set — skipping live Secret Service round-trip")
	}
	be, err := OpenLinuxBackend("aish:test", nil)
	if err != nil {
		t.Fatalf("OpenLinuxBackend: %v (no session bus or no keyring daemon?)", err)
	}
	defer be.Close()

	name := "TEST_LINUX_BACKEND_RT"
	sentinel := []byte("[REDACTED:test-value-roundtrip]")
	t.Cleanup(func() { _ = be.Rm(name) })

	if err := be.Set(name, sentinel); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := be.Get(name)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !bytes.Equal(got, sentinel) {
		t.Fatalf("Get returned %q, want sentinel", string(got))
	}
	names, err := be.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	found := false
	for _, n := range names {
		if n == name {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("List did not include %q (got %v)", name, names)
	}
	has, err := be.Has(name)
	if err != nil {
		t.Fatalf("Has: %v", err)
	}
	if !has {
		t.Fatalf("Has returned false for an existing entry")
	}
	if err := be.Rm(name); err != nil {
		t.Fatalf("Rm: %v", err)
	}
	has2, err := be.Has(name)
	if err != nil {
		t.Fatalf("Has after Rm: %v", err)
	}
	if has2 {
		t.Fatalf("Has returned true after Rm")
	}
}

// TestLinuxBackend_NameValidation rejects names that don't match the
// shared name regex. This path is purely package-internal — it runs
// on any Linux host because no D-Bus call is reached.
func TestLinuxBackend_NameValidation(t *testing.T) {
	// Build a fake backend with no conn — guard() short-circuits on
	// the regex before any D-Bus call.
	be := &linuxBackend{prefix: "aish:test", service: "aish:test"}
	bad := []string{
		"",            // empty
		"lowercase",   // not upper
		"WITH SPACE",  // contains space
		"X" + string(make([]byte, 200)), // too long
	}
	for _, n := range bad {
		if err := be.Set(n, []byte("v")); err == nil {
			t.Fatalf("Set(%q): expected validation error", n)
		}
	}
}
