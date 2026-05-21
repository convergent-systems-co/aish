package secrets

import (
	"bytes"
	"crypto/rand"
	"errors"
	"testing"
)

// TestSeal_Open_RoundTrip — the round-trip primitive: Seal then Open
// must reproduce the original plaintext byte-for-byte.
func TestSeal_Open_RoundTrip(t *testing.T) {
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		t.Fatalf("rand: %v", err)
	}
	plaintext := []byte("test-fake-value-A")

	nonce, ct, err := Seal(key[:], plaintext)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if len(nonce) != NonceSize {
		t.Fatalf("nonce length = %d; want %d", len(nonce), NonceSize)
	}
	if bytes.Contains(ct, plaintext) {
		t.Fatalf("ciphertext contains the plaintext literally")
	}

	got, err := Open(key[:], nonce, ct)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("Open returned %q; want %q", got, plaintext)
	}
}

// TestSeal_DifferentNoncesForEachCall — Seal MUST NOT reuse a nonce
// across calls for the same key (catastrophic for AES-GCM).
func TestSeal_DifferentNoncesForEachCall(t *testing.T) {
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		t.Fatalf("rand: %v", err)
	}
	n1, _, err := Seal(key[:], []byte("test-fake-value-B"))
	if err != nil {
		t.Fatalf("Seal #1: %v", err)
	}
	n2, _, err := Seal(key[:], []byte("test-fake-value-B"))
	if err != nil {
		t.Fatalf("Seal #2: %v", err)
	}
	if bytes.Equal(n1, n2) {
		t.Fatalf("Seal reused a nonce across calls — fatal for AES-GCM")
	}
}

// TestOpen_TamperedCiphertextFails — flipping a single byte in the
// ciphertext MUST cause Open to fail. AES-GCM gives us this for free
// via the auth tag; the test pins it.
func TestOpen_TamperedCiphertextFails(t *testing.T) {
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		t.Fatalf("rand: %v", err)
	}
	nonce, ct, err := Seal(key[:], []byte("test-fake-value-C"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	ct[0] ^= 0xFF // flip one byte
	got, err := Open(key[:], nonce, ct)
	if err == nil {
		t.Fatalf("Open accepted tampered ciphertext (got %q)", got)
	}
	if !errors.Is(err, ErrDecrypt) {
		t.Errorf("Open error should be ErrDecrypt-wrapped; got %v", err)
	}
}

// TestOpen_WrongKeyFails — Open with a different key MUST fail without
// leaking the plaintext.
func TestOpen_WrongKeyFails(t *testing.T) {
	var k1, k2 [32]byte
	if _, err := rand.Read(k1[:]); err != nil {
		t.Fatalf("rand k1: %v", err)
	}
	if _, err := rand.Read(k2[:]); err != nil {
		t.Fatalf("rand k2: %v", err)
	}
	nonce, ct, err := Seal(k1[:], []byte("test-fake-value-D"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if got, err := Open(k2[:], nonce, ct); err == nil {
		t.Fatalf("Open with wrong key returned %q; want error", got)
	}
}

// TestSeal_RejectsShortKey — keys MUST be exactly 32 bytes (AES-256).
func TestSeal_RejectsShortKey(t *testing.T) {
	short := make([]byte, 16)
	if _, _, err := Seal(short, []byte("test-fake-value-E")); err == nil {
		t.Fatalf("Seal accepted a 16-byte key; want error")
	}
}

// TestZero_OverwritesBuffer — Zero MUST clear every byte. Used to wipe
// plaintext + derived keys after use.
func TestZero_OverwritesBuffer(t *testing.T) {
	buf := []byte{1, 2, 3, 4, 5}
	Zero(buf)
	for i, b := range buf {
		if b != 0 {
			t.Errorf("Zero left buf[%d] = %d", i, b)
		}
	}
}
