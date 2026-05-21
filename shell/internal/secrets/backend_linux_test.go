//go:build linux

package secrets

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"sort"
	"testing"
)

// uniqueService returns a fresh Secret Service "service" attribute
// per test run so parallel runs and stale entries from prior
// failures cannot collide.
func uniqueService(t *testing.T) string {
	t.Helper()
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return "aish-test-" + hex.EncodeToString(b[:])
}

// integrationOn reports whether Linux Secret Service integration
// tests are permitted. Default-off; the gate avoids failures on
// hosts without a daemon and avoids interactive prompts.
func integrationOn(t *testing.T) bool {
	t.Helper()
	return os.Getenv("AISH_SECRET_SERVICE_INTEGRATION") == "1"
}

// TestLinuxBackend_NoDaemonReturnsUnsupported verifies that with no
// session bus reachable, OpenLinuxBackend returns ErrUnsupported.
// We force the negative path by clearing DBUS_SESSION_BUS_ADDRESS
// (and the runtime-dir fallback the dbus library consults).
//
// Skipped on hosts where integration mode is on, since the user has
// explicitly stated they have a working daemon and we don't want to
// fight their environment.
func TestLinuxBackend_NoDaemonReturnsUnsupported(t *testing.T) {
	if integrationOn(t) {
		t.Skip("AISH_SECRET_SERVICE_INTEGRATION=1 set; skipping negative-path test")
	}
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "")
	t.Setenv("XDG_RUNTIME_DIR", "")
	be, err := OpenLinuxBackend(uniqueService(t), "")
	if be != nil {
		_ = be.Close()
		t.Fatalf("expected nil backend, got %T", be)
	}
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("expected ErrUnsupported, got %v", err)
	}
}

// openTestBackend returns a linuxBackend with a unique service
// attribute. Cleanup deletes any items the test wrote.
func openTestBackend(t *testing.T) Backend {
	t.Helper()
	be, err := OpenLinuxBackend(uniqueService(t), "")
	if err != nil {
		t.Fatalf("OpenLinuxBackend: %v", err)
	}
	t.Cleanup(func() {
		names, err := be.List()
		if err != nil {
			t.Logf("cleanup List failed (continuing): %v", err)
		}
		for _, n := range names {
			if err := be.Rm(n); err != nil && !errors.Is(err, ErrNotFound) {
				t.Logf("cleanup Rm %q failed: %v", n, err)
			}
		}
		_ = be.Close()
	})
	return be
}

// --- Integration tests (require a running Secret Service daemon) ---

func TestLinuxBackend_Integration_SetGetRoundTrip(t *testing.T) {
	if !integrationOn(t) {
		t.Skip("set AISH_SECRET_SERVICE_INTEGRATION=1 to run Linux Secret Service integration tests")
	}
	be := openTestBackend(t)
	value := []byte("test-fake-value")
	if err := be.Set("DEMO_KEY", value); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := be.Get("DEMO_KEY")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer Zero(got)
	if !bytes.Equal(got, value) {
		t.Fatalf("round-trip mismatch: got %q want %q", got, value)
	}
}

func TestLinuxBackend_Integration_GetNotFound(t *testing.T) {
	if !integrationOn(t) {
		t.Skip("set AISH_SECRET_SERVICE_INTEGRATION=1 to run Linux Secret Service integration tests")
	}
	be := openTestBackend(t)
	_, err := be.Get("NEVER_SET")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestLinuxBackend_Integration_RmNotFound(t *testing.T) {
	if !integrationOn(t) {
		t.Skip("set AISH_SECRET_SERVICE_INTEGRATION=1 to run Linux Secret Service integration tests")
	}
	be := openTestBackend(t)
	err := be.Rm("NEVER_SET")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestLinuxBackend_Integration_HasReportsExistence(t *testing.T) {
	if !integrationOn(t) {
		t.Skip("set AISH_SECRET_SERVICE_INTEGRATION=1 to run Linux Secret Service integration tests")
	}
	be := openTestBackend(t)
	if ok, err := be.Has("ABSENT"); err != nil || ok {
		t.Fatalf("Has(absent) = (%v, %v); want (false, nil)", ok, err)
	}
	if err := be.Set("PRESENT", []byte("test-fake-value")); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if ok, err := be.Has("PRESENT"); err != nil || !ok {
		t.Fatalf("Has(present) = (%v, %v); want (true, nil)", ok, err)
	}
}

func TestLinuxBackend_Integration_ListSorted(t *testing.T) {
	if !integrationOn(t) {
		t.Skip("set AISH_SECRET_SERVICE_INTEGRATION=1 to run Linux Secret Service integration tests")
	}
	be := openTestBackend(t)
	want := []string{"ALPHA", "BRAVO", "CHARLIE"}
	for _, n := range want {
		if err := be.Set(n, []byte("test-fake-value-"+n)); err != nil {
			t.Fatalf("Set %q: %v", n, err)
		}
	}
	got, err := be.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("List length = %d, want %d (got=%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("List[%d] = %q, want %q (got=%v)", i, got[i], want[i], got)
		}
	}
}

func TestLinuxBackend_Integration_SetOverwrites(t *testing.T) {
	if !integrationOn(t) {
		t.Skip("set AISH_SECRET_SERVICE_INTEGRATION=1 to run Linux Secret Service integration tests")
	}
	be := openTestBackend(t)
	if err := be.Set("OVERK", []byte("v1")); err != nil {
		t.Fatalf("Set v1: %v", err)
	}
	if err := be.Set("OVERK", []byte("v2-longer")); err != nil {
		t.Fatalf("Set v2: %v", err)
	}
	got, err := be.Get("OVERK")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer Zero(got)
	if string(got) != "v2-longer" {
		t.Fatalf("expected overwritten value, got %q", got)
	}
}

func TestLinuxBackend_Integration_RejectsInvalidName(t *testing.T) {
	if !integrationOn(t) {
		t.Skip("set AISH_SECRET_SERVICE_INTEGRATION=1 to run Linux Secret Service integration tests")
	}
	be := openTestBackend(t)
	bad := []string{"lowercase", "has-dash", "has space", "", "12LEAD_DIGIT"}
	for _, n := range bad {
		if err := be.Set(n, []byte("test-fake-value")); err == nil {
			t.Errorf("Set(%q) expected name-validation error, got nil", n)
		}
	}
}

func TestLinuxBackend_Integration_RejectsEmptyValue(t *testing.T) {
	if !integrationOn(t) {
		t.Skip("set AISH_SECRET_SERVICE_INTEGRATION=1 to run Linux Secret Service integration tests")
	}
	be := openTestBackend(t)
	if err := be.Set("EMPTY", nil); err == nil {
		t.Fatal("Set with empty value should fail")
	}
}

func TestLinuxBackend_Integration_ClosedIsIdempotent(t *testing.T) {
	if !integrationOn(t) {
		t.Skip("set AISH_SECRET_SERVICE_INTEGRATION=1 to run Linux Secret Service integration tests")
	}
	be, err := OpenLinuxBackend(uniqueService(t), "")
	if err != nil {
		t.Fatalf("OpenLinuxBackend: %v", err)
	}
	if err := be.Close(); err != nil {
		t.Fatalf("Close#1: %v", err)
	}
	if err := be.Close(); err != nil {
		t.Fatalf("Close#2 (idempotent): %v", err)
	}
	if err := be.Set("X", []byte("test-fake-value")); err == nil {
		t.Fatal("Set after Close should fail")
	}
}
