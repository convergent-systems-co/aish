package community

import (
	"crypto/ed25519"
	"encoding/hex"
	"errors"
)

// DevPrivateKey returns the development Ed25519 private key derived
// from DevSigningSeed. Used by cmd/aish-community and by the test
// suite to sign synthetic bundles. NOT for production use.
func DevPrivateKey() ed25519.PrivateKey {
	seed := []byte(DevSigningSeed)
	if len(seed) != ed25519.SeedSize {
		// Programmer error: the seed constant is the wrong length.
		// Panic — this is build-time-detectable.
		panic("community: DevSigningSeed must be ed25519.SeedSize (32) bytes")
	}
	return ed25519.NewKeyFromSeed(seed)
}

// TrustAnchor pairs a signer's Ed25519 public key with metadata used
// at verification time. Compiled into the aish binary at build time;
// the runtime list (trustAnchors) is the trust boundary, NOT any
// on-disk file. Rotation requires an aish release — the right blast
// radius for a trust root.
type TrustAnchor struct {
	// SignerID matches Manifest.SignerID. Human-readable, stable
	// across rotations of the underlying key — bump SignerID when
	// rotating to a new key.
	SignerID string
	// PublicKeyHex is the hex-encoded 32-byte Ed25519 public key.
	// Decoded once in init().
	PublicKeyHex string
	// Revoked, when true, causes verification to fail regardless of
	// signature validity. Flip + ship a new release to revoke a
	// compromised key.
	Revoked bool
	// Notes carries an informational reason for revocation or the
	// human owner of the key. Unused by the verifier.
	Notes string
}

// trustAnchors is the compiled-in list. v0.2-3 ships ONLY the
// development anchor used by tests + `make bundle`; production
// anchors land via PR alongside the actual key-management process.
//
// IMPORTANT: the development anchor's private key lives in
// shell/internal/cache/community/testdata/devkey.bin. It MUST NOT be
// used to sign any bundle distributed to real users — its only purpose
// is to make the test suite + the `make bundle` recipe exercise the
// signing path end-to-end.
var trustAnchors = []TrustAnchor{
	{
		SignerID:     "aish-dev",
		PublicKeyHex: DevPublicKeyHex,
		Revoked:      false,
		Notes:        "Development-only signing key used by tests + make bundle. NOT for production use.",
	},
}

// DevPublicKeyHex is the public half of the development signing key
// derived from DevSigningSeed via ed25519.NewKeyFromSeed. Encoded
// inline so the runtime can verify dev-signed bundles without
// reading the disk.
//
// The dev keypair is deterministic — same seed → same key — so
// every build of aish that includes this file accepts bundles
// signed by `make bundle` without requiring an out-of-band key
// distribution step.
//
// IMPORTANT: this is for development + tests only. Production
// anchors land via a separate PR alongside the actual key-management
// process (hardware-backed signer, rotation policy, audit).
const DevPublicKeyHex = "3d5b25c2999cd2b9717bf4dd23a23d84050957fda80c968a91edeaf14e07f496"

// DevSigningSeed is the 32-byte seed used by ed25519.NewKeyFromSeed
// to produce the development keypair. Pinned here so cmd/aish-community
// and the test suite agree on the dev signer without storing the
// private key as a separate fixture file.
//
// Trade-off: storing the seed in source means the dev key is
// publicly known. That's intentional — the dev key MUST NOT sign
// bundles distributed to real users. The trust-anchor list calling
// it out as "NOT for production use" is the policy fence.
const DevSigningSeed = "aish-dev-community-bundle-signer"

// ErrUnknownSigner is returned by Verify when manifest.SignerID
// does not appear in the compiled-in trust anchors.
var ErrUnknownSigner = errors.New("community: signer not in trust anchors")

// ErrRevokedSigner is returned by Verify when manifest.SignerID
// matches a trust anchor whose Revoked flag is true.
var ErrRevokedSigner = errors.New("community: signer is revoked")

// findAnchor returns the trust anchor matching signerID. Returns
// (TrustAnchor{}, false) if no anchor matches. A revoked anchor still
// returns ok=true so the verifier can distinguish "unknown" from
// "revoked" in the error it emits.
func findAnchor(signerID string) (TrustAnchor, bool) {
	for _, a := range trustAnchors {
		if a.SignerID == signerID {
			return a, true
		}
	}
	return TrustAnchor{}, false
}

// decodePublicKey returns the raw 32-byte Ed25519 public key for the
// anchor. Returns an error if the hex is malformed or the wrong
// length; that's a programmer error in the compiled-in anchor list,
// not a runtime condition.
func (a TrustAnchor) decodePublicKey() ([]byte, error) {
	raw, err := hex.DecodeString(a.PublicKeyHex)
	if err != nil {
		return nil, errors.New("community: trust anchor public key is not valid hex")
	}
	if len(raw) != 32 {
		return nil, errors.New("community: trust anchor public key wrong length")
	}
	return raw, nil
}

// TrustAnchorsForTest returns the compiled-in anchor list. Exposed
// for tests in this package so they can verify that the development
// anchor exists; not for use elsewhere.
func TrustAnchorsForTest() []TrustAnchor {
	out := make([]TrustAnchor, len(trustAnchors))
	copy(out, trustAnchors)
	return out
}

// SetTrustAnchorsForTest replaces the compiled-in anchor list with
// the supplied slice and returns a restore function. ONLY for tests
// in this package; the public API has no such hook.
func SetTrustAnchorsForTest(anchors []TrustAnchor) func() {
	saved := trustAnchors
	trustAnchors = anchors
	return func() { trustAnchors = saved }
}
