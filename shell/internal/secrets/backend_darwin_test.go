//go:build darwin

package secrets

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"sort"
	"testing"
)

// uniqueService returns a fresh keychain service label per test run
// so parallel runs and stale entries from prior failures cannot
// collide. The label is also used by the test's cleanup hook to
// purge any items the test wrote.
func uniqueService(t *testing.T) string {
	t.Helper()
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return "aish-test-" + hex.EncodeToString(b[:])
}

// withHomeOverride sets HOME to a fresh temp directory for the
// duration of the test. The darwin backend's index file lives under
// $HOME/.aish; redirecting HOME isolates each test's index from the
// developer's real ~/.aish directory.
func withHomeOverride(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	return dir
}

// integrationOn reports whether macOS Keychain integration tests are
// permitted. Default-off; the gate avoids GUI unlock prompts in CI.
func integrationOn(t *testing.T) bool {
	t.Helper()
	return os.Getenv("AISH_KEYCHAIN_INTEGRATION") == "1"
}

// openTestBackend returns a darwinBackend bound to a unique service
// label and a temp HOME. The cleanup hook removes any entries
// written through the backend, including ones that might have leaked
// out of the index.
func openTestBackend(t *testing.T) Backend {
	t.Helper()
	withHomeOverride(t)
	be, err := OpenDarwinBackend(uniqueService(t), "")
	if err != nil {
		t.Fatalf("OpenDarwinBackend: %v", err)
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

// --- Unit tests (run on every macOS host; no keychain access) ------

// TestDarwinBackend_IndexRoundTrip exercises the index file read /
// write / add / rm logic without invoking /usr/bin/security.
func TestDarwinBackend_IndexRoundTrip(t *testing.T) {
	withHomeOverride(t)
	beIface, err := OpenDarwinBackend("test-svc", "")
	if err != nil {
		t.Fatalf("OpenDarwinBackend: %v", err)
	}
	be := beIface.(*darwinBackend)

	if err := be.indexAdd("FOO"); err != nil {
		t.Fatalf("indexAdd FOO: %v", err)
	}
	if err := be.indexAdd("BAR"); err != nil {
		t.Fatalf("indexAdd BAR: %v", err)
	}
	if err := be.indexAdd("FOO"); err != nil { // dup, no-op
		t.Fatalf("indexAdd FOO (dup): %v", err)
	}
	idx, err := be.indexRead()
	if err != nil {
		t.Fatalf("indexRead: %v", err)
	}
	want := []string{"BAR", "FOO"}
	if len(idx.Names) != len(want) {
		t.Fatalf("index Names = %v, want %v", idx.Names, want)
	}
	for i := range want {
		if idx.Names[i] != want[i] {
			t.Fatalf("index Names[%d] = %q, want %q", i, idx.Names[i], want[i])
		}
	}

	// Permission check on the index file: must be 0600.
	info, err := os.Stat(be.indexPath)
	if err != nil {
		t.Fatalf("stat index: %v", err)
	}
	if perm := info.Mode().Perm(); perm != vaultFilePerm {
		t.Fatalf("index perm = %#o, want %#o", perm, vaultFilePerm)
	}

	// Remove and verify.
	if err := be.indexRm("FOO"); err != nil {
		t.Fatalf("indexRm FOO: %v", err)
	}
	if err := be.indexRm("FOO"); err != nil { // already gone, no-op
		t.Fatalf("indexRm FOO (absent): %v", err)
	}
	idx, err = be.indexRead()
	if err != nil {
		t.Fatalf("indexRead after rm: %v", err)
	}
	if len(idx.Names) != 1 || idx.Names[0] != "BAR" {
		t.Fatalf("after rm: Names = %v, want [BAR]", idx.Names)
	}
}

// TestDarwinBackend_IndexMissingIsEmpty asserts that a missing index
// file yields an empty index, not an error.
func TestDarwinBackend_IndexMissingIsEmpty(t *testing.T) {
	withHomeOverride(t)
	beIface, err := OpenDarwinBackend("test-svc", "")
	if err != nil {
		t.Fatalf("OpenDarwinBackend: %v", err)
	}
	be := beIface.(*darwinBackend)
	idx, err := be.indexRead()
	if err != nil {
		t.Fatalf("indexRead empty: %v", err)
	}
	if len(idx.Names) != 0 {
		t.Fatalf("expected empty index, got %v", idx.Names)
	}
}

// TestDarwinBackend_FullAccountPrefix exercises the optional account
// prefix path that lets multiple identities share one service label.
func TestDarwinBackend_FullAccountPrefix(t *testing.T) {
	withHomeOverride(t)
	beIface, err := OpenDarwinBackend("test-svc", "persona-A")
	if err != nil {
		t.Fatalf("OpenDarwinBackend: %v", err)
	}
	be := beIface.(*darwinBackend)
	if got := be.fullAccount("FOO"); got != "persona-A:FOO" {
		t.Fatalf("fullAccount(FOO) = %q, want %q", got, "persona-A:FOO")
	}

	beIface2, err := OpenDarwinBackend("test-svc", "")
	if err != nil {
		t.Fatalf("OpenDarwinBackend: %v", err)
	}
	be2 := beIface2.(*darwinBackend)
	if got := be2.fullAccount("FOO"); got != "FOO" {
		t.Fatalf("fullAccount(FOO) without prefix = %q, want %q", got, "FOO")
	}
}

// TestDarwinBackend_GuardRejectsInvalidNames checks that the name
// regex gate fires before any keychain call.
func TestDarwinBackend_GuardRejectsInvalidNames(t *testing.T) {
	withHomeOverride(t)
	be, err := OpenDarwinBackend("test-svc", "")
	if err != nil {
		t.Fatalf("OpenDarwinBackend: %v", err)
	}
	bad := []string{"lowercase", "has-dash", "has space", "", "12LEAD_DIGIT"}
	for _, n := range bad {
		if err := be.Set(n, []byte("test-fake-value")); err == nil {
			t.Errorf("Set(%q) expected name-validation error, got nil", n)
		}
	}
}

// TestDarwinBackend_ClosedIsIdempotent verifies Close can be called
// multiple times and that post-Close operations fail.
func TestDarwinBackend_ClosedIsIdempotent(t *testing.T) {
	withHomeOverride(t)
	be, err := OpenDarwinBackend("test-svc", "")
	if err != nil {
		t.Fatalf("OpenDarwinBackend: %v", err)
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

// TestDarwinBackend_OpenRequiresHome asserts the constructor fails
// cleanly when no $HOME is set.
func TestDarwinBackend_OpenRequiresHome(t *testing.T) {
	t.Setenv("HOME", "")
	be, err := OpenDarwinBackend("test-svc", "")
	if err == nil {
		_ = be.Close()
		t.Fatal("expected error with empty HOME, got nil")
	}
}

// --- Integration tests (require keychain access) -------------------

// TestDarwinBackend_Integration_SetGetRoundTrip writes a fake value
// to the real macOS login keychain, reads it back, and verifies the
// round trip. Gated by AISH_KEYCHAIN_INTEGRATION=1 because it can
// pop a UI unlock prompt if the keychain is locked.
func TestDarwinBackend_Integration_SetGetRoundTrip(t *testing.T) {
	if !integrationOn(t) {
		t.Skip("set AISH_KEYCHAIN_INTEGRATION=1 to run macOS keychain integration tests")
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

func TestDarwinBackend_Integration_GetNotFound(t *testing.T) {
	if !integrationOn(t) {
		t.Skip("set AISH_KEYCHAIN_INTEGRATION=1 to run macOS keychain integration tests")
	}
	be := openTestBackend(t)
	_, err := be.Get("NEVER_SET")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestDarwinBackend_Integration_RmNotFound(t *testing.T) {
	if !integrationOn(t) {
		t.Skip("set AISH_KEYCHAIN_INTEGRATION=1 to run macOS keychain integration tests")
	}
	be := openTestBackend(t)
	err := be.Rm("NEVER_SET")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestDarwinBackend_Integration_HasReportsExistence(t *testing.T) {
	if !integrationOn(t) {
		t.Skip("set AISH_KEYCHAIN_INTEGRATION=1 to run macOS keychain integration tests")
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

func TestDarwinBackend_Integration_ListStrippedSorted(t *testing.T) {
	if !integrationOn(t) {
		t.Skip("set AISH_KEYCHAIN_INTEGRATION=1 to run macOS keychain integration tests")
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

func TestDarwinBackend_Integration_SetOverwrites(t *testing.T) {
	if !integrationOn(t) {
		t.Skip("set AISH_KEYCHAIN_INTEGRATION=1 to run macOS keychain integration tests")
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

// TestDarwinBackend_Integration_ListPrunesStale exercises the
// "deleted out of band" branch in List. The test writes an item,
// deletes it via the underlying `security` CLI (simulating the user
// doing it via Keychain Access), then calls List and expects the
// stale entry to be pruned from the index transparently.
func TestDarwinBackend_Integration_ListPrunesStale(t *testing.T) {
	if !integrationOn(t) {
		t.Skip("set AISH_KEYCHAIN_INTEGRATION=1 to run macOS keychain integration tests")
	}
	be := openTestBackend(t).(*darwinBackend)
	if err := be.Set("STALE", []byte("test-fake-value")); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := be.Set("KEEP", []byte("test-fake-value")); err != nil {
		t.Fatalf("Set: %v", err)
	}
	// Out-of-band delete via the same CLI the backend uses — the
	// index still contains STALE after this.
	cmd := execSecurity("delete-generic-password", "-s", be.service, "-a", be.fullAccount("STALE"))
	if err := cmd.Run(); err != nil {
		t.Fatalf("out-of-band delete: %v", err)
	}
	got, err := be.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0] != "KEEP" {
		t.Fatalf("List = %v, want [KEEP] (stale not pruned)", got)
	}
	// Index file should also have been pruned.
	idx, err := be.indexRead()
	if err != nil {
		t.Fatalf("indexRead: %v", err)
	}
	if len(idx.Names) != 1 || idx.Names[0] != "KEEP" {
		t.Fatalf("post-prune index = %v, want [KEEP]", idx.Names)
	}
}

// execSecurity is a thin helper used only by the prune test above.
// Lives in the test file (not the implementation) because it's a
// testing-only call path.
func execSecurity(args ...string) *exec.Cmd {
	return exec.Command(securityBin, args...)
}
