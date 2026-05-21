package registry

import (
	"crypto/ed25519"
	"encoding/hex"
	"errors"
)

// TrustAnchor pairs a signer's Ed25519 public key with metadata used
// at verification time. Compiled into aish at build time; the runtime
// list (trustAnchors) is the trust boundary, NOT any on-disk file.
// Rotation requires an aish release — the right blast radius for a
// trust root.
//
// This mirrors community.TrustAnchor exactly; we keep them separate so
// the plugin-trust and bundle-trust surfaces can evolve independently
// (e.g. a plugin-signing anchor compromised in isolation can be
// revoked without forcing a community-bundle revocation).
type TrustAnchor struct {
	// SignerID matches Manifest.SignerID. Human-readable, stable
	// across rotations of the underlying key — bump SignerID when
	// rotating to a new key.
	SignerID string
	// PublicKeyHex is the hex-encoded 32-byte Ed25519 public key.
	// Decoded inside verify.go.
	PublicKeyHex string
	// Revoked, when true, causes verification to fail regardless of
	// signature validity. Flip + ship a new release to revoke a
	// compromised key.
	Revoked bool
	// Notes carries an informational reason for revocation or the
	// human owner of the key. Unused by the verifier.
	Notes string
}

// DevPublicKeyHex is the public half of the development signing key.
// Identical to community.DevPublicKeyHex by design: in v0.3-2 the
// development surface keeps a single dev keypair so tests + the
// `make bundle` and `make plugin-manifest` recipes can sign with the
// same private seed without an extra fixture. Production anchors land
// via a separate PR alongside the actual key-management process.
//
// IMPORTANT: for development + tests only. NOT for signing plugins
// distributed to real users.
const DevPublicKeyHex = "3d5b25c2999cd2b9717bf4dd23a23d84050957fda80c968a91edeaf14e07f496"

// DevSigningSeed is the 32-byte seed used by ed25519.NewKeyFromSeed to
// produce the development keypair. Identical to community.DevSigningSeed
// — see DevPublicKeyHex.
const DevSigningSeed = "aish-dev-community-bundle-signer"

// DevPrivateKey returns the development Ed25519 private key derived
// from DevSigningSeed. Used by cmd/aish-plugin's signing helper and by
// tests to sign synthetic manifests. NOT for production use.
func DevPrivateKey() ed25519.PrivateKey {
	seed := []byte(DevSigningSeed)
	if len(seed) != ed25519.SeedSize {
		// Programmer error: the seed constant is the wrong length.
		// Panic — this is build-time-detectable.
		panic("registry: DevSigningSeed must be ed25519.SeedSize (32) bytes")
	}
	return ed25519.NewKeyFromSeed(seed)
}

// trustAnchors is the compiled-in list. v0.3-2 ships ONLY the
// development anchor used by tests + `make plugin-manifest`;
// production anchors land via PR alongside the actual key-management
// process.
var trustAnchors = []TrustAnchor{
	{
		SignerID:     "aish-dev",
		PublicKeyHex: DevPublicKeyHex,
		Revoked:      false,
		Notes:        "Development-only signing key for plugin manifests + community bundles. NOT for production use.",
	},
}

// ErrUnknownSigner is returned by Verify* when manifest.SignerID does
// not appear in the compiled-in trust anchors.
var ErrUnknownSigner = errors.New("registry: signer not in trust anchors")

// ErrRevokedSigner is returned by Verify* when manifest.SignerID
// matches a trust anchor whose Revoked flag is true.
var ErrRevokedSigner = errors.New("registry: signer is revoked")

// findAnchor returns the trust anchor matching signerID. Returns
// (TrustAnchor{}, false) if no anchor matches. A revoked anchor still
// returns ok=true so the verifier can distinguish "unknown" from
// "revoked".
func findAnchor(signerID string) (TrustAnchor, bool) {
	for _, a := range trustAnchors {
		if a.SignerID == signerID {
			return a, true
		}
	}
	return TrustAnchor{}, false
}

// decodePublicKey returns the raw 32-byte Ed25519 public key for the
// anchor.
func (a TrustAnchor) decodePublicKey() ([]byte, error) {
	raw, err := hex.DecodeString(a.PublicKeyHex)
	if err != nil {
		return nil, errors.New("registry: trust anchor public key is not valid hex")
	}
	if len(raw) != 32 {
		return nil, errors.New("registry: trust anchor public key wrong length")
	}
	return raw, nil
}

// TrustAnchorsForTest returns the compiled-in anchor list. Exposed
// for tests in this package + downstream packages so they can
// inspect the dev anchor. Returns a defensive copy.
func TrustAnchorsForTest() []TrustAnchor {
	out := make([]TrustAnchor, len(trustAnchors))
	copy(out, trustAnchors)
	return out
}

// SetTrustAnchorsForTest replaces the compiled-in anchor list with
// the supplied slice and returns a restore function. ONLY for tests.
// The public API has no such hook.
func SetTrustAnchorsForTest(anchors []TrustAnchor) func() {
	saved := trustAnchors
	trustAnchors = anchors
	return func() { trustAnchors = saved }
}
