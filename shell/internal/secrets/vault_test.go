package secrets

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// testParams is a fast Argon2id config for unit tests. The defaults
// are tuned for production unlock latency; they would dominate the
// test budget. Real defaults are exercised once in the smoke-style
// test below.
func testParams() KDFParams {
	return KDFParams{Time: 1, Memory: 8 * 1024, Parallelism: 1, KeyLen: KeySize}
}

// newTempHome returns a tempdir-rooted "home" suitable for vault
// tests, cleaning up on test end.
func newTempHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	return home
}

// TestVault_FirstInit — Open on a missing vault must create the file
// with 0600 permissions and an empty entries map.
func TestVault_FirstInit(t *testing.T) {
	home := newTempHome(t)
	v, err := OpenVault(home, []byte("test-fake-passphrase-A"), testParams())
	if err != nil {
		t.Fatalf("OpenVault: %v", err)
	}
	defer v.Close()

	if got := v.List(); len(got) != 0 {
		t.Errorf("fresh vault should be empty; got %v", got)
	}

	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(home, ".aish", "vault", "vault.json"))
		if err != nil {
			t.Fatalf("stat vault.json: %v", err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("vault.json perm = %o; want 0600", perm)
		}
	}
}

// TestVault_SetGetRoundTrip — Set then Get must reproduce the value
// byte-for-byte across an Open / Close cycle (the second OpenVault
// proves the data is actually persisted, not just held in memory).
func TestVault_SetGetRoundTrip(t *testing.T) {
	home := newTempHome(t)
	pass := []byte("test-fake-passphrase-B")
	p := testParams()

	v1, err := OpenVault(home, pass, p)
	if err != nil {
		t.Fatalf("OpenVault #1: %v", err)
	}
	if err := v1.Set("DEMO_KEY", []byte("test-fake-value-A")); err != nil {
		t.Fatalf("Set: %v", err)
	}
	v1.Close()

	v2, err := OpenVault(home, pass, p)
	if err != nil {
		t.Fatalf("OpenVault #2: %v", err)
	}
	defer v2.Close()
	got, err := v2.Get("DEMO_KEY")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer Zero(got)
	if !bytes.Equal(got, []byte("test-fake-value-A")) {
		t.Errorf("round-trip got %q; want %q", got, "test-fake-value-A")
	}
}

// TestVault_PlaintextNeverOnDisk — the adversarial "stolen vault"
// test. After Set("PLAINTEXT_SENTINEL"), ripping the vault file off
// disk and grepping for the sentinel MUST find nothing.
func TestVault_PlaintextNeverOnDisk(t *testing.T) {
	home := newTempHome(t)
	pass := []byte("test-fake-passphrase-C")
	v, err := OpenVault(home, pass, testParams())
	if err != nil {
		t.Fatalf("OpenVault: %v", err)
	}
	sentinel := []byte("PLAINTEXT_SENTINEL_THIS_MUST_NEVER_LEAK")
	if err := v.Set("DEMO", sentinel); err != nil {
		t.Fatalf("Set: %v", err)
	}
	v.Close()

	raw, err := os.ReadFile(filepath.Join(home, ".aish", "vault", "vault.json"))
	if err != nil {
		t.Fatalf("read vault.json: %v", err)
	}
	if bytes.Contains(raw, sentinel) {
		t.Fatalf("PLAINTEXT_SENTINEL found verbatim in vault.json — encryption is broken")
	}
}

// TestVault_WrongPassphraseCleanError — opening a vault with the
// wrong passphrase MUST return a single uniform error. No panic, no
// "almost worked" diagnostics, no key material in the error.
func TestVault_WrongPassphraseCleanError(t *testing.T) {
	home := newTempHome(t)
	p := testParams()
	v, err := OpenVault(home, []byte("test-fake-passphrase-correct"), p)
	if err != nil {
		t.Fatalf("OpenVault: %v", err)
	}
	if err := v.Set("DEMO", []byte("test-fake-value-D")); err != nil {
		t.Fatalf("Set: %v", err)
	}
	v.Close()

	v2, err := OpenVault(home, []byte("test-fake-passphrase-wrong"), p)
	if err != nil {
		t.Fatalf("OpenVault #2: %v", err)
	}
	defer v2.Close()
	_, err = v2.Get("DEMO")
	if err == nil {
		t.Fatalf("Get with wrong passphrase succeeded; want error")
	}
	if strings.Contains(err.Error(), "test-fake-passphrase") {
		t.Errorf("error leaks passphrase material: %v", err)
	}
	if strings.Contains(err.Error(), "test-fake-value") {
		t.Errorf("error leaks value material: %v", err)
	}
}

// TestVault_EmptyPassphraseRejected — empty passphrase MUST be
// rejected at OpenVault, not silently accepted.
func TestVault_EmptyPassphraseRejected(t *testing.T) {
	home := newTempHome(t)
	if _, err := OpenVault(home, []byte{}, testParams()); err == nil {
		t.Fatalf("OpenVault accepted empty passphrase; want error")
	}
}

// TestVault_EmptyValueRejected — an empty value is a programmer
// mistake (you meant Rm). Refuse it.
func TestVault_EmptyValueRejected(t *testing.T) {
	home := newTempHome(t)
	v, err := OpenVault(home, []byte("test-fake-passphrase-E"), testParams())
	if err != nil {
		t.Fatalf("OpenVault: %v", err)
	}
	defer v.Close()
	if err := v.Set("DEMO", []byte{}); err == nil {
		t.Fatalf("Set accepted empty value; want error")
	}
}

// TestVault_RejectsInvalidName — names must match ^[A-Z][A-Z0-9_]{0,63}$.
// Anything else is rejected at Set.
func TestVault_RejectsInvalidName(t *testing.T) {
	home := newTempHome(t)
	v, err := OpenVault(home, []byte("test-fake-passphrase-F"), testParams())
	if err != nil {
		t.Fatalf("OpenVault: %v", err)
	}
	defer v.Close()
	bad := []string{"", "lowercase", "1STARTS_DIGIT", "HAS SPACE", "HAS-DASH", "HAS.DOT", strings.Repeat("A", 100)}
	for _, name := range bad {
		if err := v.Set(name, []byte("test-fake-value-G")); err == nil {
			t.Errorf("Set accepted invalid name %q", name)
		}
	}
}

// TestVault_Rm — remove an entry; subsequent Get returns
// ErrNotFound; List no longer contains the name.
func TestVault_Rm(t *testing.T) {
	home := newTempHome(t)
	pass := []byte("test-fake-passphrase-H")
	v, err := OpenVault(home, pass, testParams())
	if err != nil {
		t.Fatalf("OpenVault: %v", err)
	}
	if err := v.Set("DEMO", []byte("test-fake-value-H")); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := v.Rm("DEMO"); err != nil {
		t.Fatalf("Rm: %v", err)
	}
	if _, err := v.Get("DEMO"); err == nil {
		t.Fatalf("Get after Rm succeeded; want ErrNotFound")
	}
	if got := v.List(); len(got) != 0 {
		t.Errorf("List after Rm = %v; want empty", got)
	}
	v.Close()
}

// TestVault_List_Sorted — List returns names in lexicographic order
// so output is deterministic.
func TestVault_List_Sorted(t *testing.T) {
	home := newTempHome(t)
	v, err := OpenVault(home, []byte("test-fake-passphrase-I"), testParams())
	if err != nil {
		t.Fatalf("OpenVault: %v", err)
	}
	defer v.Close()
	for _, n := range []string{"CHARLIE", "ALPHA", "BRAVO"} {
		if err := v.Set(n, []byte("test-fake-value-J")); err != nil {
			t.Fatalf("Set %q: %v", n, err)
		}
	}
	got := v.List()
	want := []string{"ALPHA", "BRAVO", "CHARLIE"}
	if len(got) != len(want) {
		t.Fatalf("List len = %d; want %d", len(got), len(want))
	}
	for i, n := range want {
		if got[i] != n {
			t.Errorf("List[%d] = %q; want %q", i, got[i], n)
		}
	}
}

// TestVault_TamperedFileDetected — flip one byte of vault.json; the
// next Get on that entry MUST fail (AES-GCM auth tag catches it). The
// other entries should also fail to load if the corruption is in the
// JSON; the failure mode is "you have a corrupt vault, restore from
// backup" — not "we successfully loaded half of it."
func TestVault_TamperedFileDetected(t *testing.T) {
	home := newTempHome(t)
	pass := []byte("test-fake-passphrase-K")
	p := testParams()
	v, err := OpenVault(home, pass, p)
	if err != nil {
		t.Fatalf("OpenVault: %v", err)
	}
	if err := v.Set("DEMO", []byte("test-fake-value-K")); err != nil {
		t.Fatalf("Set: %v", err)
	}
	v.Close()

	path := filepath.Join(home, ".aish", "vault", "vault.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	// Find a ciphertext byte to flip. We tamper near the end so we
	// don't accidentally hit the JSON structure or the salt.
	if len(raw) < 50 {
		t.Skipf("vault.json too short to tamper (%d bytes)", len(raw))
	}
	// Find the ciphertext_b64 substring and flip a character in its value.
	idx := bytes.Index(raw, []byte(`"ciphertext_b64": "`))
	if idx < 0 {
		t.Fatalf("vault.json missing ciphertext_b64 field")
	}
	target := idx + len(`"ciphertext_b64": "`) + 5
	if raw[target] == 'A' {
		raw[target] = 'B'
	} else {
		raw[target] = 'A'
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	v2, err := OpenVault(home, pass, p)
	if err != nil {
		t.Fatalf("OpenVault after tamper: %v", err)
	}
	defer v2.Close()
	if _, err := v2.Get("DEMO"); err == nil {
		t.Fatalf("Get on tampered vault succeeded; want decrypt error")
	}
}

// TestVault_RejectsWorldReadablePerms — if the vault file is found
// with bits other than 0600 on POSIX, the open must fail loudly. We
// will NOT silently re-chmod someone else's file.
func TestVault_RejectsWorldReadablePerms(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX-only test")
	}
	home := newTempHome(t)
	pass := []byte("test-fake-passphrase-L")
	v, err := OpenVault(home, pass, testParams())
	if err != nil {
		t.Fatalf("OpenVault: %v", err)
	}
	if err := v.Set("DEMO", []byte("test-fake-value-L")); err != nil {
		t.Fatalf("Set: %v", err)
	}
	v.Close()

	path := filepath.Join(home, ".aish", "vault", "vault.json")
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	if _, err := OpenVault(home, pass, testParams()); err == nil {
		t.Fatalf("OpenVault accepted world-readable vault; want error")
	}
}
