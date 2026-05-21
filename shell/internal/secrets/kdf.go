package secrets

import (
	"errors"
	"fmt"

	"golang.org/x/crypto/argon2"
)

// KDFParams holds the Argon2id cost parameters. Defaults are tuned
// for "single guess takes a few hundred ms on modern hardware" per
// OWASP 2023+ password-storage guidance.
//
// Time is the number of iterations (Argon2id "t").
// Memory is in KiB (Argon2id "m").
// Parallelism is the number of threads (Argon2id "p").
// KeyLen is the output length in bytes; this package always uses 32
// because the AEAD layer wants AES-256 keys.
type KDFParams struct {
	Time        uint32 // iterations
	Memory      uint32 // KiB
	Parallelism uint8  // threads
	KeyLen      uint32 // output bytes (always KeySize for this package)
}

// DefaultKDFParams returns the recommended Argon2id parameters used
// for new vaults. Tuned per OWASP 2023+ guidance — 64 MiB memory,
// 3 iterations, parallelism 4. A single guess takes ~250–500ms on
// modern desktop hardware; brute-force a strong passphrase is
// intractable.
func DefaultKDFParams() KDFParams {
	return KDFParams{
		Time:        3,
		Memory:      64 * 1024, // 64 MiB
		Parallelism: 4,
		KeyLen:      KeySize,
	}
}

// DescribeCost returns a one-line human-readable description of the
// cost parameters. The vault built-in prints this on first init so
// the user sees what they're paying for unlock latency.
//
// Format: "argon2id: memory=64MiB iterations=3 parallelism=4"
func (p KDFParams) DescribeCost() string {
	return fmt.Sprintf("argon2id: memory=%dMiB iterations=%d parallelism=%d",
		p.Memory/1024, p.Time, p.Parallelism)
}

// minSaltBytes is the lower bound for an Argon2id salt. The RFC
// recommends 16; we accept 8 as the floor to be defensive against
// future code paths that pass a short value while still rejecting
// the obvious mistakes.
const minSaltBytes = 8

// Derive runs Argon2id over passphrase + salt with the given params
// and returns a KeyLen-byte derived key. Empty passphrases and short
// salts are rejected — this is the last line of defense if a caller
// upstream forgot to validate.
//
// The returned slice is the secret material; callers MUST call Zero
// on it before the slice goes out of scope.
func Derive(passphrase, salt []byte, p KDFParams) ([]byte, error) {
	if len(passphrase) == 0 {
		return nil, errors.New("secrets: empty passphrase")
	}
	if len(salt) < minSaltBytes {
		return nil, fmt.Errorf("secrets: salt too short (%d bytes, want >= %d)", len(salt), minSaltBytes)
	}
	if p.KeyLen == 0 {
		p.KeyLen = KeySize
	}
	return argon2.IDKey(passphrase, salt, p.Time, p.Memory, p.Parallelism, p.KeyLen), nil
}
