package secrets

import (
	"os"
	"path/filepath"
	"testing"
)

// TestIdentity_NoActive_NoFile — when identity.toml does not exist,
// LoadActive returns (zero-value, nil) so the caller can render "no
// active identity" without error.
func TestIdentity_NoActive_NoFile(t *testing.T) {
	home := t.TempDir()
	id, err := LoadActive(home)
	if err != nil {
		t.Fatalf("LoadActive: %v", err)
	}
	if id.Name != "" {
		t.Errorf("LoadActive returned Name=%q on missing file; want empty", id.Name)
	}
}

// TestIdentity_CreateThenUse — Create writes
// ~/.aish/identities/<name>.toml; SetActive writes
// ~/.aish/identity.toml; LoadActive reads it back.
func TestIdentity_CreateThenUse(t *testing.T) {
	home := t.TempDir()
	if err := CreateProfile(home, Identity{Name: "work", GatewayURL: "https://aish.example.com", SignerPubkeySHA256: "abc123"}); err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}
	if err := SetActive(home, "work"); err != nil {
		t.Fatalf("SetActive: %v", err)
	}
	id, err := LoadActive(home)
	if err != nil {
		t.Fatalf("LoadActive: %v", err)
	}
	if id.Name != "work" {
		t.Errorf("LoadActive Name = %q; want %q", id.Name, "work")
	}
	if id.GatewayURL != "https://aish.example.com" {
		t.Errorf("LoadActive GatewayURL = %q; want %q", id.GatewayURL, "https://aish.example.com")
	}
}

// TestIdentity_List — ListProfiles returns sorted profile names.
func TestIdentity_List(t *testing.T) {
	home := t.TempDir()
	for _, n := range []string{"work", "personal", "demo"} {
		if err := CreateProfile(home, Identity{Name: n}); err != nil {
			t.Fatalf("CreateProfile %q: %v", n, err)
		}
	}
	got, err := ListProfiles(home)
	if err != nil {
		t.Fatalf("ListProfiles: %v", err)
	}
	want := []string{"demo", "personal", "work"}
	if len(got) != len(want) {
		t.Fatalf("ListProfiles len = %d; want %d (%v)", len(got), len(want), got)
	}
	for i, n := range want {
		if got[i] != n {
			t.Errorf("ListProfiles[%d] = %q; want %q", i, got[i], n)
		}
	}
}

// TestIdentity_CreateRejectsInvalidName — name regex is the same as
// shell identifiers: alphanumeric + underscore + hyphen, no whitespace.
func TestIdentity_CreateRejectsInvalidName(t *testing.T) {
	home := t.TempDir()
	bad := []string{"", "has space", "../escape", "..", "/abs"}
	for _, n := range bad {
		if err := CreateProfile(home, Identity{Name: n}); err == nil {
			t.Errorf("CreateProfile accepted invalid name %q", n)
		}
	}
}

// TestIdentity_FilesAre0600 — identity files contain a pubkey hash
// which is not secret in itself, but the active-identity pointer is
// a privacy signal. We lock them at 0600 to be consistent with the
// vault.
func TestIdentity_FilesAre0600(t *testing.T) {
	home := t.TempDir()
	if err := CreateProfile(home, Identity{Name: "work"}); err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}
	if err := SetActive(home, "work"); err != nil {
		t.Fatalf("SetActive: %v", err)
	}
	for _, p := range []string{
		filepath.Join(home, ".aish", "identity.toml"),
		filepath.Join(home, ".aish", "identities", "work.toml"),
	} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatalf("stat %s: %v", p, err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("%s perm = %o; want 0600", p, perm)
		}
	}
}

// TestIdentity_SetActiveRequiresProfile — pointing active at a
// non-existent profile MUST fail.
func TestIdentity_SetActiveRequiresProfile(t *testing.T) {
	home := t.TempDir()
	if err := SetActive(home, "missing"); err == nil {
		t.Fatalf("SetActive accepted missing profile; want error")
	}
}
