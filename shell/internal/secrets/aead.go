package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
)

// KeySize is the AES-256 key length in bytes.
const KeySize = 32

// NonceSize is the GCM standard nonce length in bytes. AES-GCM
// REQUIRES a unique nonce per (key, plaintext) tuple — we generate
// one from crypto/rand for every Seal call.
const NonceSize = 12

// ErrDecrypt is returned when Open cannot recover the plaintext —
// either the key is wrong, the ciphertext is corrupt, or the auth
// tag does not match. The error is deliberately uniform so the
// caller cannot distinguish failure modes (no timing-leak surface).
var ErrDecrypt = errors.New("secrets: decryption failed")

// Seal encrypts plaintext under key using AES-256-GCM with a fresh
// random nonce. Returns (nonce, ciphertext) so the caller can persist
// them side-by-side. plaintext is NOT modified; callers that hold
// secret plaintext are responsible for calling Zero on their own
// buffer when done.
//
// key MUST be exactly KeySize (32) bytes. Anything else is a programmer
// error and returns an error rather than silently truncating.
func Seal(key, plaintext []byte) (nonce, ciphertext []byte, err error) {
	if len(key) != KeySize {
		return nil, nil, fmt.Errorf("secrets: key must be %d bytes, got %d", KeySize, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		// crypto/aes returns an error only for a wrong key size; we
		// already validated that, so this is unreachable in practice.
		return nil, nil, fmt.Errorf("secrets: aes: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, fmt.Errorf("secrets: gcm: %w", err)
	}
	nonce = make([]byte, NonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, fmt.Errorf("secrets: rand: %w", err)
	}
	ciphertext = gcm.Seal(nil, nonce, plaintext, nil)
	return nonce, ciphertext, nil
}

// Open decrypts ciphertext under key + nonce and returns the
// plaintext. On ANY failure (wrong key, tampered ciphertext, bad
// nonce length) it returns ErrDecrypt with no further detail.
//
// Callers MUST treat the returned plaintext as secret: pass it
// straight to the consumer (clipboard) and call Zero on the slice
// before it goes out of scope.
func Open(key, nonce, ciphertext []byte) ([]byte, error) {
	if len(key) != KeySize {
		return nil, ErrDecrypt
	}
	if len(nonce) != NonceSize {
		return nil, ErrDecrypt
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, ErrDecrypt
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, ErrDecrypt
	}
	pt, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, ErrDecrypt
	}
	return pt, nil
}

// Zero overwrites every byte of buf with 0. Used to wipe plaintext
// and derived keys before their backing slice falls out of scope.
// Go's GC does not guarantee zeroing, so we do it explicitly.
//
// Note: this does NOT mitigate swap-leak or process-dump attacks
// (see package doc.go). It does guarantee the slice's bytes are
// zero before reuse / GC.
func Zero(buf []byte) {
	for i := range buf {
		buf[i] = 0
	}
}
