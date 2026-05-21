package community

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
	"path/filepath"
	"strings"
)

// ErrSignatureInvalid is returned by Verify when the manifest's
// signature does not match a SHA-256 over the bundle.db bytes under
// the signer's trust-anchor public key.
var ErrSignatureInvalid = errors.New("community: signature verification failed")

// ErrManifestMalformed is returned by ReadManifest / Verify when the
// manifest JSON is missing required fields, has malformed base64, or
// otherwise fails the static-shape check.
var ErrManifestMalformed = errors.New("community: manifest malformed")

// ErrBundleHashMismatch is returned by Verify when manifest.SHA256
// does not match the recomputed SHA-256 of bundle.db. The signature
// covers the recomputed hash, so this is strictly belt-and-braces;
// surfacing it separately lets the loader emit a clearer error than
// the generic signature-invalid one when in-flight corruption is the
// actual cause.
var ErrBundleHashMismatch = errors.New("community: bundle.db hash does not match manifest")

// ReadManifest parses the manifest at path and returns the typed
// Manifest. Returns ErrManifestMalformed (wrapped) on any structural
// problem; the loader uses errors.Is to surface a uniform error to
// the user.
func ReadManifest(path string) (Manifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("community: read manifest: %w", err)
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

// Validate runs the static-shape check on a Manifest. Used by
// ReadManifest and by the verifier to short-circuit obviously-broken
// inputs before walking the crypto path.
func (m Manifest) Validate() error {
	if m.FormatVersion <= 0 {
		return fmt.Errorf("%w: format_version must be > 0", ErrManifestMalformed)
	}
	if m.BundleVersion <= 0 {
		return fmt.Errorf("%w: bundle_version must be > 0", ErrManifestMalformed)
	}
	if strings.TrimSpace(m.SignerID) == "" {
		return fmt.Errorf("%w: signer_id is empty", ErrManifestMalformed)
	}
	if strings.TrimSpace(m.Signature) == "" {
		return fmt.Errorf("%w: signature is empty", ErrManifestMalformed)
	}
	if _, err := base64.StdEncoding.DecodeString(m.Signature); err != nil {
		return fmt.Errorf("%w: signature is not valid base64: %v", ErrManifestMalformed, err)
	}
	if strings.TrimSpace(m.SHA256) == "" {
		return fmt.Errorf("%w: sha256 is empty", ErrManifestMalformed)
	}
	if _, err := hex.DecodeString(m.SHA256); err != nil {
		return fmt.Errorf("%w: sha256 is not valid hex: %v", ErrManifestMalformed, err)
	}
	if strings.TrimSpace(m.CreatedAt) == "" {
		return fmt.Errorf("%w: created_at is empty", ErrManifestMalformed)
	}
	return nil
}

// HashBundleDB returns the SHA-256 hex digest of the file at path.
// Used by the build tool (cmd/aish-community) at sign time and by
// Verify at load time.
func HashBundleDB(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("community: open bundle.db: %w", err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("community: hash bundle.db: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// VerifyBundleDir runs the full verification pipeline against a
// directory containing manifest.json + bundle.db:
//
//  1. Read + structural-validate the manifest.
//  2. Look up the manifest.SignerID in compiled-in trust anchors.
//     ErrUnknownSigner or ErrRevokedSigner on failure.
//  3. Recompute SHA-256 over bundle.db; reject on mismatch with
//     ErrBundleHashMismatch.
//  4. ed25519.Verify(anchorPubKey, sha256_bytes, signature). Reject
//     with ErrSignatureInvalid on failure.
//
// Returns the validated Manifest on success.
//
// dir is treated as untrusted: the loader's discovery code passes
// the canonicalised path here, but VerifyBundleDir defends in depth
// by re-canonicalising and rejecting symlinks that escape the
// directory.
func VerifyBundleDir(dir string) (Manifest, error) {
	// Canonicalise dir + verify the two expected files live inside
	// it (no symlink escape). EvalSymlinks would resolve through any
	// symlink at the leaf; we want the leaf itself to be a real file
	// inside the canonical bundle dir.
	canon, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return Manifest{}, fmt.Errorf("community: resolve bundle dir: %w", err)
	}
	manifestPath := filepath.Join(canon, ManifestFileName)
	bundlePath := filepath.Join(canon, BundleDBFileName)
	if err := assertInside(canon, manifestPath); err != nil {
		return Manifest{}, err
	}
	if err := assertInside(canon, bundlePath); err != nil {
		return Manifest{}, err
	}

	m, err := ReadManifest(manifestPath)
	if err != nil {
		return Manifest{}, err
	}

	// Resolve trust anchor.
	anchor, ok := findAnchor(m.SignerID)
	if !ok {
		return Manifest{}, fmt.Errorf("%w: %s", ErrUnknownSigner, m.SignerID)
	}
	if anchor.Revoked {
		return Manifest{}, fmt.Errorf("%w: %s", ErrRevokedSigner, m.SignerID)
	}

	// Recompute hash + assert manifest hash matches.
	gotHash, err := HashBundleDB(bundlePath)
	if err != nil {
		return Manifest{}, err
	}
	if gotHash != m.SHA256 {
		return Manifest{}, fmt.Errorf("%w (manifest=%s got=%s)", ErrBundleHashMismatch, m.SHA256, gotHash)
	}

	// Decode signature + public key, verify.
	sig, err := base64.StdEncoding.DecodeString(m.Signature)
	if err != nil {
		return Manifest{}, fmt.Errorf("%w: signature decode: %v", ErrManifestMalformed, err)
	}
	pub, err := anchor.decodePublicKey()
	if err != nil {
		return Manifest{}, err
	}
	hashBytes, _ := hex.DecodeString(gotHash) // gotHash was just produced by hex.EncodeToString
	if !ed25519.Verify(ed25519.PublicKey(pub), hashBytes, sig) {
		return Manifest{}, ErrSignatureInvalid
	}
	return m, nil
}

// assertInside returns nil iff candidate, after symlink resolution,
// lies inside dir (which is already canonicalised). Protects against
// a bundle directory that contains a symlink pointing outside.
//
// We do NOT EvalSymlinks(candidate) because that fails if candidate
// does not exist yet; the caller has already opened the file in
// ReadManifest / HashBundleDB. Instead we resolve only when the
// candidate is itself a symlink.
func assertInside(dir, candidate string) error {
	info, err := os.Lstat(candidate)
	if err != nil {
		return fmt.Errorf("community: stat %s: %w", candidate, err)
	}
	resolved := candidate
	if info.Mode()&os.ModeSymlink != 0 {
		r, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			return fmt.Errorf("community: resolve symlink %s: %w", candidate, err)
		}
		resolved = r
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	absRes, err := filepath.Abs(resolved)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(absDir, absRes)
	if err != nil {
		return err
	}
	if strings.HasPrefix(rel, "..") || rel == ".." {
		return fmt.Errorf("community: %s escapes bundle directory", candidate)
	}
	return nil
}

// SignBundleDB is the inverse of VerifyBundleDir's signature step:
// given a private key + the path to a bundle.db, returns the base64
// signature suitable for embedding in Manifest.Signature. Used by
// cmd/aish-community at build time; exposed here so tests can build
// synthetic bundles without depending on the cmd module.
func SignBundleDB(privKey ed25519.PrivateKey, bundlePath string) (sigB64, sha256Hex string, err error) {
	sha256Hex, err = HashBundleDB(bundlePath)
	if err != nil {
		return "", "", err
	}
	hashBytes, err := hex.DecodeString(sha256Hex)
	if err != nil {
		return "", "", fmt.Errorf("community: hex decode hash: %w", err)
	}
	sig := ed25519.Sign(privKey, hashBytes)
	return base64.StdEncoding.EncodeToString(sig), sha256Hex, nil
}
