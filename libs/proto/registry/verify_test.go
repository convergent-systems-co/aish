package registry

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// signedFakeBinaryFixture writes a tiny "binary" file under dir, signs
// it with the dev key, and returns a Manifest pointing at it.
func signedFakeBinaryFixture(t *testing.T, dir string) (Manifest, string) {
	t.Helper()
	binPath := filepath.Join(dir, "fakebin")
	if err := os.WriteFile(binPath, []byte("fake-binary-bytes\n"), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}
	priv := DevPrivateKey()
	sigB64, shaHex, err := SignBinary(priv, binPath)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return Manifest{
		FormatVersion: CurrentFormatVersion,
		Name:          "fake",
		Version:       "0.0.1",
		BinaryPath:    binPath,
		Kinds:         []Kind{KindInference},
		SHA256:        shaHex,
		SignerID:      "aish-dev",
		Signature:     sigB64,
		CreatedAt:     time.Now().UTC().Format(time.RFC3339),
	}, binPath
}

func TestVerifyManifestSignature_HappyPath(t *testing.T) {
	m, _ := signedFakeBinaryFixture(t, t.TempDir())
	if err := VerifyManifestSignature(m); err != nil {
		t.Fatalf("dev-signed manifest should verify, got %v", err)
	}
}

func TestVerifyManifestAgainstBinary_HappyPath(t *testing.T) {
	m, _ := signedFakeBinaryFixture(t, t.TempDir())
	if err := VerifyManifestAgainstBinary(m); err != nil {
		t.Fatalf("dev-signed manifest should verify against binary, got %v", err)
	}
}

func TestVerifyManifestAgainstBinary_TamperedBinary(t *testing.T) {
	dir := t.TempDir()
	m, binPath := signedFakeBinaryFixture(t, dir)

	// Tamper with the binary AFTER signing.
	if err := os.WriteFile(binPath, []byte("tampered\n"), 0o755); err != nil {
		t.Fatalf("tamper: %v", err)
	}

	err := VerifyManifestAgainstBinary(m)
	if !errors.Is(err, ErrBinaryHashMismatch) {
		t.Fatalf("expected ErrBinaryHashMismatch, got %v", err)
	}
}

func TestVerifyManifestSignature_UnknownSigner(t *testing.T) {
	m, _ := signedFakeBinaryFixture(t, t.TempDir())
	m.SignerID = "stranger"
	err := VerifyManifestSignature(m)
	if !errors.Is(err, ErrUnknownSigner) {
		t.Fatalf("expected ErrUnknownSigner, got %v", err)
	}
}

func TestVerifyManifestSignature_RevokedSigner(t *testing.T) {
	m, _ := signedFakeBinaryFixture(t, t.TempDir())
	restore := SetTrustAnchorsForTest([]TrustAnchor{
		{SignerID: "aish-dev", PublicKeyHex: DevPublicKeyHex, Revoked: true, Notes: "test revoke"},
	})
	defer restore()

	err := VerifyManifestSignature(m)
	if !errors.Is(err, ErrRevokedSigner) {
		t.Fatalf("expected ErrRevokedSigner, got %v", err)
	}
}

func TestVerifyManifestSignature_TamperedSignature(t *testing.T) {
	m, _ := signedFakeBinaryFixture(t, t.TempDir())
	// Flip a byte in the signature.
	raw, err := base64.StdEncoding.DecodeString(m.Signature)
	if err != nil {
		t.Fatalf("decode sig: %v", err)
	}
	raw[0] ^= 0xff
	m.Signature = base64.StdEncoding.EncodeToString(raw)

	err = VerifyManifestSignature(m)
	if !errors.Is(err, ErrSignatureInvalid) {
		t.Fatalf("expected ErrSignatureInvalid, got %v", err)
	}
}

func TestVerifyManifestSignature_WrongKey(t *testing.T) {
	m, binPath := signedFakeBinaryFixture(t, t.TempDir())

	// Sign with a fresh (random) keypair but keep the dev SignerID
	// → the dev anchor pubkey is consulted, signature won't verify.
	_, otherPriv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate other key: %v", err)
	}
	sigB64, _, err := SignBinary(otherPriv, binPath)
	if err != nil {
		t.Fatalf("sign with other key: %v", err)
	}
	m.Signature = sigB64

	err = VerifyManifestSignature(m)
	if !errors.Is(err, ErrSignatureInvalid) {
		t.Fatalf("expected ErrSignatureInvalid for wrong-key signature, got %v", err)
	}
}

func TestHashBinary_DeterministicAcrossCalls(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bin")
	if err := os.WriteFile(path, []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	a, err := HashBinary(path)
	if err != nil {
		t.Fatalf("hash a: %v", err)
	}
	b, err := HashBinary(path)
	if err != nil {
		t.Fatalf("hash b: %v", err)
	}
	if a != b {
		t.Fatalf("non-deterministic: %s vs %s", a, b)
	}
	if _, err := hex.DecodeString(a); err != nil {
		t.Fatalf("hash not hex: %v", err)
	}
	if len(a) != 64 {
		t.Fatalf("sha256 hex length should be 64, got %d", len(a))
	}
}

func TestWriteThenReadManifest_Roundtrip(t *testing.T) {
	m, _ := signedFakeBinaryFixture(t, t.TempDir())
	dir := t.TempDir()
	path := filepath.Join(dir, ManifestFileName)
	if err := WriteManifest(path, m); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := ReadManifest(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.Name != m.Name || got.SHA256 != m.SHA256 || got.Signature != m.Signature {
		t.Fatalf("roundtrip mismatch:\n got %#v\nwant %#v", got, m)
	}
}

func TestReadManifest_MalformedJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ManifestFileName)
	if err := os.WriteFile(path, []byte("{ not json"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, err := ReadManifest(path)
	if !errors.Is(err, ErrManifestMalformed) {
		t.Fatalf("expected ErrManifestMalformed, got %v", err)
	}
}

func TestReadManifest_MissingFile(t *testing.T) {
	_, err := ReadManifest(filepath.Join(t.TempDir(), "nope.json"))
	if err == nil || !strings.Contains(err.Error(), "read manifest") {
		t.Fatalf("expected read-manifest error, got %v", err)
	}
}
