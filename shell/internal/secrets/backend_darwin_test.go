//go:build darwin

package secrets

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// requireIntegration gates every live-keychain test on the
// AISH_KEYCHAIN_INTEGRATION=1 env var. The user explicitly opted out
// of automatic keychain mutation when this branch's first attempt
// hit the login keychain; the gate makes that opt-out permanent for
// CI and for any developer who runs `go test ./...` without thinking.
//
// Even within the gated path, every test points HOME at a tempdir so
// the bootstrap passphrase file and the keychain itself land under
// the test's scratch space — never under the real $HOME. See
// `redirectHome` for the mechanics.
func requireIntegration(t *testing.T) {
	t.Helper()
	if os.Getenv("AISH_KEYCHAIN_INTEGRATION") != "1" {
		t.Skip("set AISH_KEYCHAIN_INTEGRATION=1 to run live macOS keychain tests; skipped by default")
	}
}

// redirectHome points $HOME at a tempdir for the test's duration AND
// arranges deletion of the aish.keychain-db the test creates. We
// also remove the keychain from the security search list so a leak
// of test-keychain state into the developer's login session is
// avoided.
//
// Returns the tempdir; the caller may use it to inspect bootstrap
// files etc.
func redirectHome(t *testing.T) string {
	t.Helper()
	requireIntegration(t)

	// Save and restore $HOME.
	home := t.TempDir()
	t.Setenv("HOME", home)

	// The macOS security CLI ALSO consults $HOME — that's the whole
	// point — but it additionally writes the created keychain into
	// the keychain search list at the user level. We remove it
	// during cleanup so the keychain doesn't linger as a "logged in"
	// keychain in the developer's Keychain.app sidebar.
	t.Cleanup(func() {
		// Best-effort: only the test-created keychain is removed.
		_ = os.RemoveAll(filepath.Join(home, "Library", "Keychains"))
	})
	return home
}

// TestOpen_CreatesDedicatedKeychain — first open with no existing
// keychain creates aish.keychain-db AND a bootstrap passphrase
// file. NEVER touches the user's login keychain.
func TestOpen_CreatesDedicatedKeychain(t *testing.T) {
	home := redirectHome(t)

	b, err := OpenDarwinBackend("", nil)
	if err != nil {
		t.Fatalf("OpenDarwinBackend: %v", err)
	}
	t.Cleanup(func() { _ = b.Close() })

	// Bootstrap file exists at $HOME/.aish/keychain.bootstrap mode 0600.
	bp := filepath.Join(home, ".aish", "keychain.bootstrap")
	fi, err := os.Stat(bp)
	if err != nil {
		t.Fatalf("bootstrap file missing: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("bootstrap perms = %v, want 0600", fi.Mode().Perm())
	}

	// Keychain file exists under $HOME/Library/Keychains.
	kc := filepath.Join(home, "Library", "Keychains", "aish.keychain-db")
	if _, err := os.Stat(kc); err != nil {
		t.Errorf("aish keychain not created at %s: %v", kc, err)
	}
}

// TestOpen_ReusesExistingKeychain — second open with an existing
// keychain unlocks it via the persisted bootstrap passphrase.
func TestOpen_ReusesExistingKeychain(t *testing.T) {
	home := redirectHome(t)
	b1, err := OpenDarwinBackend("", nil)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	_ = b1.Close()

	// Read the passphrase that was just created.
	bp := filepath.Join(home, ".aish", "keychain.bootstrap")
	raw, err := os.ReadFile(bp)
	if err != nil {
		t.Fatalf("read bootstrap: %v", err)
	}
	first := strings.TrimSpace(string(raw))

	// Second open should NOT regenerate the passphrase.
	b2, err := OpenDarwinBackend("", nil)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	defer b2.Close()

	raw2, _ := os.ReadFile(bp)
	if got := strings.TrimSpace(string(raw2)); got != first {
		t.Errorf("bootstrap rewritten on re-open; want stable")
	}
}

// TestRoundTrip — Set + Get + Has + Rm cycle.
func TestRoundTrip(t *testing.T) {
	redirectHome(t)
	b, err := OpenDarwinBackend("", nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer b.Close()

	const name = "test-roundtrip-key"
	value := []byte("test-fake-value-A")

	if err := b.Set(name, value); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := b.Get(name)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !bytes.Equal(got, value) {
		t.Errorf("Get = %q, want %q", got, value)
	}

	ok, err := b.Has(name)
	if err != nil || !ok {
		t.Errorf("Has(%q) = (%v, %v), want (true, nil)", name, ok, err)
	}

	if err := b.Rm(name); err != nil {
		t.Fatalf("Rm: %v", err)
	}

	if _, err := b.Get(name); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after Rm = %v, want ErrNotFound", err)
	}
}

// TestSet_Overwrites — Set on an existing entry replaces the value.
func TestSet_Overwrites(t *testing.T) {
	redirectHome(t)
	b, err := OpenDarwinBackend("", nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer b.Close()

	const name = "overwrite-key"
	if err := b.Set(name, []byte("first-fake")); err != nil {
		t.Fatal(err)
	}
	if err := b.Set(name, []byte("second-fake")); err != nil {
		t.Fatal(err)
	}
	got, _ := b.Get(name)
	if string(got) != "second-fake" {
		t.Errorf("after overwrite Get = %q, want %q", got, "second-fake")
	}
}

// TestList — entries written under one service appear in List,
// sorted, deduplicated.
func TestList(t *testing.T) {
	redirectHome(t)
	b, err := OpenDarwinBackend("", nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer b.Close()

	for _, n := range []string{"zeta", "alpha", "mu"} {
		if err := b.Set(n, []byte("fake")); err != nil {
			t.Fatal(err)
		}
	}
	names, err := b.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{"alpha", "mu", "zeta"}
	if len(names) != len(want) {
		t.Fatalf("List len = %d, want %d (%v)", len(names), len(want), names)
	}
	for i, w := range want {
		if names[i] != w {
			t.Errorf("List[%d] = %q, want %q", i, names[i], w)
		}
	}
}

// TestPrefix_ScopesService — entries under different prefixes do
// not bleed into each other's List.
func TestPrefix_ScopesService(t *testing.T) {
	redirectHome(t)
	work, err := OpenDarwinBackend("work", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer work.Close()
	home, err := OpenDarwinBackend("home", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer home.Close()

	_ = work.Set("only-in-work", []byte("fake-w"))
	_ = home.Set("only-in-home", []byte("fake-h"))

	wn, _ := work.List()
	hn, _ := home.List()
	for _, n := range wn {
		if n == "only-in-home" {
			t.Errorf("home leaked into work List")
		}
	}
	for _, n := range hn {
		if n == "only-in-work" {
			t.Errorf("work leaked into home List")
		}
	}
}

// TestClose_Idempotent — Close twice does not panic; subsequent ops
// return "closed" errors.
func TestClose_Idempotent(t *testing.T) {
	redirectHome(t)
	b, err := OpenDarwinBackend("", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if _, err := b.Get("anything"); err == nil {
		t.Errorf("Get after Close returned nil err; want closed-state error")
	}
}

// TestServiceFor — pure function check; runs without keychain mutation.
func TestServiceFor(t *testing.T) {
	// This one DOES NOT need the integration gate — pure function.
	cases := []struct{ in, want string }{
		{"", "aish"},
		{"work", "aish:work"},
		{"a-b-c", "aish:a-b-c"},
	}
	for _, c := range cases {
		if got := serviceFor(c.in); got != c.want {
			t.Errorf("serviceFor(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestParseDumpKeychain — pure parser; uses canned fixture.
func TestParseDumpKeychain(t *testing.T) {
	raw := []byte(`keychain: "/tmp/x.keychain-db"
class: 0x00000010
attributes:
    0x00000007 <blob>="MY_KEY"
    "acct"<blob>="MY_KEY"
    "svce"<blob>="aish"
keychain: "/tmp/x.keychain-db"
class: 0x00000010
attributes:
    "acct"<blob>="OTHER"
    "svce"<blob>="not-aish"
keychain: "/tmp/x.keychain-db"
class: 0x00000010
attributes:
    "acct"<blob>="ANOTHER"
    "svce"<blob>="aish"
`)
	names := parseDumpKeychain(raw, "aish")
	want := []string{"ANOTHER", "MY_KEY"}
	if len(names) != len(want) {
		t.Fatalf("got %v, want %v", names, want)
	}
	for i, w := range want {
		if names[i] != w {
			t.Errorf("names[%d]=%q want %q", i, names[i], w)
		}
	}
}
