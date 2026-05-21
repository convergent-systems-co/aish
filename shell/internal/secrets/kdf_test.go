package secrets

import (
	"bytes"
	"strings"
	"testing"
)

// TestDerive_Deterministic — same passphrase + salt + params MUST
// yield the same key. This is the property OpenVault depends on.
func TestDerive_Deterministic(t *testing.T) {
	salt := []byte("0123456789abcdef")
	p := DefaultKDFParams()
	// Use a faster set of params for the test so it doesn't dominate
	// `make test` time. The real defaults are exercised in a single
	// happy-path test.
	p.Time = 1
	p.Memory = 8 * 1024

	pass := []byte("test-fake-passphrase-A")
	k1, err := Derive(pass, salt, p)
	if err != nil {
		t.Fatalf("Derive #1: %v", err)
	}
	k2, err := Derive(pass, salt, p)
	if err != nil {
		t.Fatalf("Derive #2: %v", err)
	}
	if !bytes.Equal(k1, k2) {
		t.Fatalf("Derive non-deterministic: k1=%x k2=%x", k1, k2)
	}
	if len(k1) != KeySize {
		t.Errorf("Derive returned %d bytes; want %d", len(k1), KeySize)
	}
}

// TestDerive_DifferentSaltDifferentKey — salt MUST mix into the
// derived key. Otherwise two users with the same passphrase share a
// key.
func TestDerive_DifferentSaltDifferentKey(t *testing.T) {
	p := DefaultKDFParams()
	p.Time = 1
	p.Memory = 8 * 1024

	pass := []byte("test-fake-passphrase-B")
	k1, err := Derive(pass, []byte("salt-one-aaaaaaa"), p)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	k2, err := Derive(pass, []byte("salt-two-bbbbbbb"), p)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if bytes.Equal(k1, k2) {
		t.Fatalf("different salts produced the same key")
	}
}

// TestDerive_DifferentPassphraseDifferentKey — passphrase mix-in
// sanity check.
func TestDerive_DifferentPassphraseDifferentKey(t *testing.T) {
	p := DefaultKDFParams()
	p.Time = 1
	p.Memory = 8 * 1024
	salt := []byte("0123456789abcdef")

	k1, err := Derive([]byte("test-fake-passphrase-C"), salt, p)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	k2, err := Derive([]byte("test-fake-passphrase-D"), salt, p)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if bytes.Equal(k1, k2) {
		t.Fatalf("different passphrases produced the same key")
	}
}

// TestDerive_RejectsEmptyPassphrase — empty passphrase MUST be
// rejected at the KDF layer. This is the defense-in-depth check that
// catches any caller that forgot to validate upstream.
func TestDerive_RejectsEmptyPassphrase(t *testing.T) {
	p := DefaultKDFParams()
	p.Time = 1
	p.Memory = 8 * 1024
	if _, err := Derive([]byte{}, []byte("0123456789abcdef"), p); err == nil {
		t.Fatalf("Derive accepted empty passphrase; want error")
	}
}

// TestDerive_RejectsShortSalt — Argon2id wants a salt of at least 8
// bytes. The KDF MUST refuse to operate on shorter salts.
func TestDerive_RejectsShortSalt(t *testing.T) {
	p := DefaultKDFParams()
	p.Time = 1
	p.Memory = 8 * 1024
	if _, err := Derive([]byte("test-fake-passphrase-E"), []byte("short"), p); err == nil {
		t.Fatalf("Derive accepted short salt; want error")
	}
}

// TestKDFParams_CostDescription — DescribeCost MUST mention the
// algorithm name and the three cost parameters so the user sees them
// once at vault init.
func TestKDFParams_CostDescription(t *testing.T) {
	p := DefaultKDFParams()
	desc := p.DescribeCost()
	for _, want := range []string{"argon2id", "memory", "iterations", "parallelism"} {
		if !strings.Contains(strings.ToLower(desc), want) {
			t.Errorf("DescribeCost missing %q: %s", want, desc)
		}
	}
}
