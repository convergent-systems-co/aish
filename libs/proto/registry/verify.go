package registry

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

// ErrSignatureInvalid is returned by Verify* when the manifest's
// signature does not match the SHA-256 over the binary bytes under
// the signer's trust-anchor public key.
var ErrSignatureInvalid = errors.New("registry: signature verification failed")

// ErrBinaryHashMismatch is returned by VerifyManifestAgainstBinary
// when manifest.SHA256 does not match the recomputed SHA-256 of the
// binary file at manifest.BinaryPath. The signature covers the
// recomputed hash, so this surfaces "the binary on disk is not what
// the signer signed" as a distinct error from generic signature
// failure.
var ErrBinaryHashMismatch = errors.New("registry: binary sha256 does not match manifest")

// HashBinary returns the SHA-256 hex digest of the file at path. Used
// by the admin CLI at sign time and by VerifyManifestAgainstBinary at
// load time.
func HashBinary(path string) (string, error) {
	f, err := os.Open(path) // #nosec G304 — path is supplied by caller; the CLI / loader is responsible for upstream validation.
	if err != nil {
		return "", fmt.Errorf("registry: open binary: %w", err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("registry: hash binary: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// SignBinary is the inverse of VerifyManifestAgainstBinary's signature
// step: given a private key + the path to a binary, returns the
// base64 signature suitable for embedding in Manifest.Signature plus
// the hex sha256 of the binary. Used by cmd/aish-plugin's install
// path; exposed here so tests can build synthetic manifests.
func SignBinary(privKey ed25519.PrivateKey, binaryPath string) (sigB64, sha256Hex string, err error) {
	sha256Hex, err = HashBinary(binaryPath)
	if err != nil {
		return "", "", err
	}
	hashBytes, err := hex.DecodeString(sha256Hex)
	if err != nil {
		return "", "", fmt.Errorf("registry: hex decode hash: %w", err)
	}
	sig := ed25519.Sign(privKey, hashBytes)
	return base64.StdEncoding.EncodeToString(sig), sha256Hex, nil
}

// VerifyManifestSignature checks the signer + signature math only. It
// does NOT touch the filesystem — it operates on Manifest values
// already in memory. The caller must independently verify the binary
// hash matches manifest.SHA256 (see VerifyManifestAgainstBinary).
//
// Returns ErrUnknownSigner if SignerID is not in trustAnchors.
// Returns ErrRevokedSigner if the signer is revoked.
// Returns ErrSignatureInvalid if the signature math fails.
// Returns ErrManifestMalformed (wrapped) if Validate() fails.
func VerifyManifestSignature(m Manifest) error {
	if err := m.Validate(); err != nil {
		return err
	}
	anchor, ok := findAnchor(m.SignerID)
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownSigner, m.SignerID)
	}
	if anchor.Revoked {
		return fmt.Errorf("%w: %s", ErrRevokedSigner, m.SignerID)
	}
	sig, err := base64.StdEncoding.DecodeString(m.Signature)
	if err != nil {
		return fmt.Errorf("%w: signature decode: %v", ErrManifestMalformed, err)
	}
	pub, err := anchor.decodePublicKey()
	if err != nil {
		return err
	}
	hashBytes, err := hex.DecodeString(m.SHA256)
	if err != nil {
		// Validate already ran; this should not happen, but guard.
		return fmt.Errorf("%w: sha256 hex decode: %v", ErrManifestMalformed, err)
	}
	if !ed25519.Verify(ed25519.PublicKey(pub), hashBytes, sig) {
		return ErrSignatureInvalid
	}
	return nil
}

// VerifyManifestAgainstBinary runs the full verification pipeline:
//
//  1. Static validation (Validate).
//  2. Trust + signature math (VerifyManifestSignature).
//  3. Recompute sha256 over the file at manifest.BinaryPath; reject
//     ErrBinaryHashMismatch on mismatch.
//
// This is what the installer and the registry loader call before
// trusting a manifest. The CLI's `verify` subcommand exposes the same
// pipeline.
func VerifyManifestAgainstBinary(m Manifest) error {
	if err := VerifyManifestSignature(m); err != nil {
		return err
	}
	gotHash, err := HashBinary(m.BinaryPath)
	if err != nil {
		return err
	}
	if gotHash != m.SHA256 {
		return fmt.Errorf("%w (manifest=%s got=%s)", ErrBinaryHashMismatch, m.SHA256, gotHash)
	}
	return nil
}

// ReadManifest parses the JSON manifest at path and returns the typed
// Manifest. Returns ErrManifestMalformed (wrapped) on any structural
// problem.
func ReadManifest(path string) (Manifest, error) {
	raw, err := os.ReadFile(path) // #nosec G304 — caller is the loader walking a registry directory.
	if err != nil {
		return Manifest{}, fmt.Errorf("registry: read manifest: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return Manifest{}, fmt.Errorf("%w: %v", ErrManifestMalformed, err)
	}
	if err := m.Validate(); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

// WriteManifest marshals m to indented JSON and writes it to path via
// a temp-then-rename to avoid a torn write. Mode 0o644.
func WriteManifest(path string, m Manifest) error {
	if err := m.Validate(); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("registry: marshal manifest: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return fmt.Errorf("registry: write manifest: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("registry: rename manifest: %w", err)
	}
	return nil
}
