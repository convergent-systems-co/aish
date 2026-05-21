package history

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Signer signs the canonical bytes of an Event row before it is
// persisted. The contract is intentionally narrow so the v0.3-4 file-
// backed signer and the v0.3-4.1 secrets-engine-backed signer can both
// satisfy it.
//
// SignerID is opaque to the verifier: it identifies which key the
// signature was produced with. The on-disk default ("aish-local") is
// the per-install key file; future production identities will use the
// secrets engine's identity ID.
type Signer interface {
	// Sign returns a detached signature over msg. The returned byte
	// slice is the raw signature; Append base64-encodes it before
	// writing to SQLite.
	Sign(msg []byte) ([]byte, error)
	// SignerID returns the stable identifier this signer signs with.
	// Persisted in events.signer_id so a future verifier can route
	// signatures to the right public key.
	SignerID() string
	// PublicKey returns the raw 32-byte Ed25519 public key. The Verify
	// helper uses this to round-trip in tests; production verifiers
	// will resolve the key via SignerID against a trust store.
	PublicKey() ed25519.PublicKey
}

// FileSigner is the v0.3-4 default Signer. It lazily creates an
// Ed25519 keypair at the configured path (0600 permissions) on first
// use and reuses it on subsequent loads.
//
// The key file format is one line of hex: 64 bytes of seed (the
// ed25519.PrivateKey "seed" half — the full private key is derived
// deterministically from it). Reading 64 hex chars (32 bytes) yields a
// PrivateKey via ed25519.NewKeyFromSeed.
type FileSigner struct {
	priv     ed25519.PrivateKey
	pub      ed25519.PublicKey
	signerID string
}

// LocalSignerID is the SignerID stamped on every event signed by the
// per-install FileSigner. Stable across rotations of the key file
// (which is currently a manual "delete + restart" operation).
const LocalSignerID = "aish-local"

// NewFileSigner loads (or creates) the Ed25519 key at path. The
// directory must already exist — typically `~/.aish/`, which the
// shell creates on startup. Errors:
//   - path empty → returns an error rather than guessing a default.
//   - parent directory missing → returns the underlying os error.
//   - file exists but is malformed → returns a parse error rather
//     than silently overwriting (data-safety win over UX win).
func NewFileSigner(path string) (*FileSigner, error) {
	if path == "" {
		return nil, errors.New("history: NewFileSigner: empty path")
	}
	if data, err := os.ReadFile(path); err == nil {
		return parseKeyFile(data, path)
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("history: read signing key: %w", err)
	}
	seed := make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(seed); err != nil {
		return nil, fmt.Errorf("history: generate signing key: %w", err)
	}
	enc := hex.EncodeToString(seed) + "\n"
	if err := os.WriteFile(path, []byte(enc), 0o600); err != nil {
		return nil, fmt.Errorf("history: write signing key: %w", err)
	}
	// Belt-and-suspenders on the permission bits: some umasks would
	// otherwise leave the file group-readable.
	_ = os.Chmod(path, 0o600)
	priv := ed25519.NewKeyFromSeed(seed)
	return &FileSigner{
		priv:     priv,
		pub:      priv.Public().(ed25519.PublicKey),
		signerID: LocalSignerID,
	}, nil
}

func parseKeyFile(data []byte, path string) (*FileSigner, error) {
	// Strip surrounding whitespace; the file is single-line hex.
	s := string(data)
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r' || s[len(s)-1] == ' ' || s[len(s)-1] == '\t') {
		s = s[:len(s)-1]
	}
	if len(s) != 2*ed25519.SeedSize {
		return nil, fmt.Errorf("history: signing key at %s is malformed (len=%d, want %d)", path, len(s), 2*ed25519.SeedSize)
	}
	seed, err := hex.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("history: signing key at %s is not valid hex: %w", path, err)
	}
	priv := ed25519.NewKeyFromSeed(seed)
	return &FileSigner{
		priv:     priv,
		pub:      priv.Public().(ed25519.PublicKey),
		signerID: LocalSignerID,
	}, nil
}

// Sign produces a 64-byte Ed25519 signature over msg.
func (s *FileSigner) Sign(msg []byte) ([]byte, error) {
	if s == nil {
		return nil, errors.New("history: nil FileSigner")
	}
	return ed25519.Sign(s.priv, msg), nil
}

// SignerID returns the constant LocalSignerID. The file-backed signer
// does not produce per-key IDs because there is exactly one key per
// install.
func (s *FileSigner) SignerID() string {
	if s == nil {
		return ""
	}
	return s.signerID
}

// PublicKey returns the verifying key.
func (s *FileSigner) PublicKey() ed25519.PublicKey {
	if s == nil {
		return nil
	}
	return s.pub
}

// canonicalSigningMsg builds the byte stream that gets signed. We
// sign a JSON re-encoding of the event with Signature and SignerID
// blanked out — those fields are populated AFTER the signature is
// produced, so they cannot themselves be part of what gets signed.
//
// This is intentionally not a stable signing canonicalization (no
// sorted-keys, no normalized whitespace) because we control both the
// signer and verifier and round-trip through the same json.Marshal.
// If a future use-case needs cross-tool verification, this is where
// the canonicalization tightens.
func canonicalSigningMsg(e *Event) ([]byte, error) {
	clone := *e
	clone.Signature = ""
	clone.SignerID = ""
	return json.Marshal(&clone)
}

// Verify returns nil when sig is a valid Ed25519 signature on the
// canonical encoding of e under pub. Used by tests and by future
// audit tooling; the shell does not call Verify on every read (the
// signature is treated as audit metadata, not a gate on display).
func Verify(e *Event, sig []byte, pub ed25519.PublicKey) error {
	if e == nil {
		return errors.New("history: Verify: nil event")
	}
	if len(pub) != ed25519.PublicKeySize {
		return errors.New("history: Verify: public key wrong size")
	}
	if len(sig) != ed25519.SignatureSize {
		return errors.New("history: Verify: signature wrong size")
	}
	msg, err := canonicalSigningMsg(e)
	if err != nil {
		return fmt.Errorf("history: Verify: marshal: %w", err)
	}
	if !ed25519.Verify(pub, msg, sig) {
		return errors.New("history: Verify: signature does not match")
	}
	return nil
}

// DefaultKeyPath returns the conventional location of the per-install
// signing key inside a `.aish` directory. The shell wires this up at
// startup; tests pass a temp directory to keep the production file
// untouched.
func DefaultKeyPath(dotAish string) string {
	return filepath.Join(dotAish, "history.key")
}
