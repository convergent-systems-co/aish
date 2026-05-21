//go:build windows

package secrets

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sort"
	"testing"
)

// uniquePrefix returns a fresh Credential Manager prefix per test
// run so parallel runs and stale entries from prior failures cannot
// collide. The prefix is also used by the test's cleanup hook.
func uniquePrefix(t *testing.T) string {
	t.Helper()
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return "aish-test-" + hex.EncodeToString(b[:]) + ":"
}

// openTestBackend opens a windowsBackend with a fresh prefix and
// registers a cleanup that deletes every entry written under that
// prefix. Tests can call Set without worrying about leaving ghosts
// in Credential Manager.
func openTestBackend(t *testing.T) Backend {
	t.Helper()
	prefix := uniquePrefix(t)
	be, err := OpenWindowsBackend(prefix, []byte("aish-test-entropy"))
	if err != nil {
		t.Fatalf("OpenWindowsBackend: %v", err)
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

func TestWindowsBackend_SetGetRoundTrip(t *testing.T) {
	be := openTestBackend(t)
	value := []byte("hunter2-supersecret")
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

func TestWindowsBackend_GetNotFound(t *testing.T) {
	be := openTestBackend(t)
	_, err := be.Get("NEVER_SET")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestWindowsBackend_RmNotFound(t *testing.T) {
	be := openTestBackend(t)
	err := be.Rm("NEVER_SET")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestWindowsBackend_HasReportsExistence(t *testing.T) {
	be := openTestBackend(t)
	if ok, err := be.Has("ABSENT"); err != nil || ok {
		t.Fatalf("Has(absent) = (%v, %v); want (false, nil)", ok, err)
	}
	if err := be.Set("PRESENT", []byte("v")); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if ok, err := be.Has("PRESENT"); err != nil || !ok {
		t.Fatalf("Has(present) = (%v, %v); want (true, nil)", ok, err)
	}
}

func TestWindowsBackend_ListStrippedSorted(t *testing.T) {
	be := openTestBackend(t)
	want := []string{"ALPHA", "BRAVO", "CHARLIE"}
	for _, n := range want {
		if err := be.Set(n, []byte("v-"+n)); err != nil {
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

func TestWindowsBackend_SetOverwrites(t *testing.T) {
	be := openTestBackend(t)
	if err := be.Set("K", []byte("v1")); err != nil {
		t.Fatalf("Set v1: %v", err)
	}
	if err := be.Set("K", []byte("v2-longer")); err != nil {
		t.Fatalf("Set v2: %v", err)
	}
	got, err := be.Get("K")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer Zero(got)
	if string(got) != "v2-longer" {
		t.Fatalf("expected overwritten value, got %q", got)
	}
}

func TestWindowsBackend_RejectsInvalidName(t *testing.T) {
	be := openTestBackend(t)
	bad := []string{"lowercase", "has-dash", "has space", "", "12LEAD_DIGIT"}
	for _, n := range bad {
		if err := be.Set(n, []byte("v")); err == nil {
			t.Errorf("Set(%q) expected name-validation error, got nil", n)
		}
	}
}

func TestWindowsBackend_RejectsEmptyValue(t *testing.T) {
	be := openTestBackend(t)
	if err := be.Set("EMPTY", nil); err == nil {
		t.Fatal("Set with empty value should fail")
	}
}

func TestWindowsBackend_ClosedIsIdempotent(t *testing.T) {
	be, err := OpenWindowsBackend(uniquePrefix(t), nil)
	if err != nil {
		t.Fatalf("OpenWindowsBackend: %v", err)
	}
	if err := be.Close(); err != nil {
		t.Fatalf("Close#1: %v", err)
	}
	if err := be.Close(); err != nil {
		t.Fatalf("Close#2 (idempotent): %v", err)
	}
	if err := be.Set("X", []byte("v")); err == nil {
		t.Fatal("Set after Close should fail")
	}
}

func TestDPAPI_RoundTrip(t *testing.T) {
	plaintext := []byte("dpapi-only roundtrip without Credential Manager")
	entropy := []byte("xyzzy")
	blob, err := dpapiProtect(plaintext, entropy)
	if err != nil {
		t.Fatalf("dpapiProtect: %v", err)
	}
	if bytes.Contains(blob, plaintext) {
		t.Fatal("DPAPI ciphertext contains plaintext literal — wrap failed")
	}
	got, err := dpapiUnprotect(blob, entropy)
	if err != nil {
		t.Fatalf("dpapiUnprotect: %v", err)
	}
	defer Zero(got)
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("DPAPI round-trip mismatch: got %q want %q", got, plaintext)
	}
}

func TestDPAPI_WrongEntropyFails(t *testing.T) {
	plaintext := []byte("secret")
	blob, err := dpapiProtect(plaintext, []byte("entropy-A"))
	if err != nil {
		t.Fatalf("dpapiProtect: %v", err)
	}
	if _, err := dpapiUnprotect(blob, []byte("entropy-B")); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("expected ErrDecrypt with wrong entropy, got %v", err)
	}
}
