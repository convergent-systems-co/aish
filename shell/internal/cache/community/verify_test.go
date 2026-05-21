package community

import (
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// makeBundleDB writes a fresh bundle.db with the given (intent, os,
// invocation) rows. Returns the on-disk path. Used by every test
// here that needs a signable artifact.
func makeBundleDB(t *testing.T, dir string, rows []seedRow) string {
	t.Helper()
	path := filepath.Join(dir, BundleDBFileName)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open bundle.db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(BundleSchema); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	stmt, err := db.Prepare(
		`INSERT INTO intents (intent_hash, os, intent, invocation, confidence) VALUES (?, ?, ?, ?, ?)`,
	)
	if err != nil {
		t.Fatalf("prepare insert: %v", err)
	}
	defer stmt.Close()
	for _, r := range rows {
		if _, err := stmt.Exec(hashIntent(r.intent), r.os, r.intent, r.invocation, 1.0); err != nil {
			t.Fatalf("insert %s/%s: %v", r.intent, r.os, err)
		}
	}
	return path
}

type seedRow struct {
	intent     string
	os         string
	invocation string
}

// signBundleDir generates a manifest.json next to bundle.db, signed
// with the dev key (or with override when non-nil). Returns the
// directory path.
func signBundleDir(t *testing.T, rows []seedRow, override ed25519.PrivateKey, signerID string) string {
	t.Helper()
	dir := t.TempDir()
	bundlePath := makeBundleDB(t, dir, rows)
	priv := override
	if priv == nil {
		priv = DevPrivateKey()
	}
	if signerID == "" {
		signerID = "aish-dev"
	}
	sigB64, sha, err := SignBundleDB(priv, bundlePath)
	if err != nil {
		t.Fatalf("SignBundleDB: %v", err)
	}
	m := Manifest{
		FormatVersion: 1,
		BundleVersion: 1,
		CreatedAt:     time.Now().UTC().Format(time.RFC3339),
		IntentCount:   len(rows),
		SignerID:      signerID,
		Signature:     sigB64,
		SHA256:        sha,
	}
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ManifestFileName), raw, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return dir
}

func TestSignAndVerifyRoundtrip(t *testing.T) {
	rows := []seedRow{
		{intent: "list files", os: "darwin", invocation: "ls -la"},
		{intent: "show date", os: "linux", invocation: "date -u"},
	}
	dir := signBundleDir(t, rows, nil, "")
	m, err := VerifyBundleDir(dir)
	if err != nil {
		t.Fatalf("VerifyBundleDir: %v", err)
	}
	if m.SignerID != "aish-dev" {
		t.Errorf("SignerID = %q, want aish-dev", m.SignerID)
	}
	if m.IntentCount != 2 {
		t.Errorf("IntentCount = %d, want 2", m.IntentCount)
	}
}

func TestVerifyRejectsTamperedBundle(t *testing.T) {
	rows := []seedRow{{intent: "list files", os: "darwin", invocation: "ls -la"}}
	dir := signBundleDir(t, rows, nil, "")
	// Append a single byte to bundle.db — alters the hash + breaks
	// signature verification.
	bp := filepath.Join(dir, BundleDBFileName)
	f, err := os.OpenFile(bp, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open bundle.db: %v", err)
	}
	if _, err := f.Write([]byte{0xff}); err != nil {
		t.Fatalf("append byte: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close bundle.db: %v", err)
	}
	_, err = VerifyBundleDir(dir)
	// First the hash mismatch is checked; that's the user-visible
	// error for an appended byte. ErrSignatureInvalid is the
	// fallback if the manifest hash field is corrupted to match.
	if !errors.Is(err, ErrBundleHashMismatch) && !errors.Is(err, ErrSignatureInvalid) {
		t.Errorf("err = %v, want ErrBundleHashMismatch or ErrSignatureInvalid", err)
	}
}

func TestVerifyRejectsTamperedSignature(t *testing.T) {
	rows := []seedRow{{intent: "list files", os: "darwin", invocation: "ls -la"}}
	dir := signBundleDir(t, rows, nil, "")
	// Re-read + flip one byte in the signature, but keep manifest
	// SHA256 correct so the verifier reaches the signature step.
	mpath := filepath.Join(dir, ManifestFileName)
	raw, err := os.ReadFile(mpath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	sig, err := base64.StdEncoding.DecodeString(m.Signature)
	if err != nil {
		t.Fatalf("decode sig: %v", err)
	}
	sig[0] ^= 0xff
	m.Signature = base64.StdEncoding.EncodeToString(sig)
	out, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(mpath, out, 0o644); err != nil {
		t.Fatalf("rewrite manifest: %v", err)
	}
	_, err = VerifyBundleDir(dir)
	if !errors.Is(err, ErrSignatureInvalid) {
		t.Errorf("err = %v, want ErrSignatureInvalid", err)
	}
}

func TestVerifyRejectsUnknownSigner(t *testing.T) {
	// Generate an off-anchor keypair. The manifest's signer_id is
	// not in the compiled-in trust list.
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	_ = pub
	rows := []seedRow{{intent: "list files", os: "darwin", invocation: "ls -la"}}
	dir := signBundleDir(t, rows, priv, "evil-signer")
	_, err = VerifyBundleDir(dir)
	if !errors.Is(err, ErrUnknownSigner) {
		t.Errorf("err = %v, want ErrUnknownSigner", err)
	}
}

func TestVerifyRejectsRevokedSigner(t *testing.T) {
	rows := []seedRow{{intent: "list files", os: "darwin", invocation: "ls -la"}}
	dir := signBundleDir(t, rows, nil, "aish-dev")

	// Replace the trust-anchor list with a revoked dev anchor.
	restore := SetTrustAnchorsForTest([]TrustAnchor{{
		SignerID:     "aish-dev",
		PublicKeyHex: DevPublicKeyHex,
		Revoked:      true,
		Notes:        "test revocation",
	}})
	defer restore()

	_, err := VerifyBundleDir(dir)
	if !errors.Is(err, ErrRevokedSigner) {
		t.Errorf("err = %v, want ErrRevokedSigner", err)
	}
}

func TestManifestMalformedRejects(t *testing.T) {
	dir := t.TempDir()
	makeBundleDB(t, dir, nil)
	// Write a manifest missing required fields.
	bad := `{"format_version": 1}`
	if err := os.WriteFile(filepath.Join(dir, ManifestFileName), []byte(bad), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := VerifyBundleDir(dir)
	if !errors.Is(err, ErrManifestMalformed) {
		t.Errorf("err = %v, want ErrManifestMalformed", err)
	}
}

func TestHashIntentNormalizesInput(t *testing.T) {
	// TrimSpace + ToLower must collapse cosmetic differences to one
	// key. If this contract changes, the L1<->L3 cross-package
	// hashIntent test (TestHashIntentMatchesL1 in
	// shell/internal/cache) will also fail — that's the canary.
	if hashIntent("  List Files  ") != hashIntent("list files") {
		t.Error("hashIntent did not normalize whitespace + case")
	}
	// Spot-check: known SHA-256(list files) is determined; rather
	// than pin a magic hex constant here, just assert determinism.
	if hashIntent("foo") == hashIntent("bar") {
		t.Error("hashIntent collision on distinct inputs")
	}
}
