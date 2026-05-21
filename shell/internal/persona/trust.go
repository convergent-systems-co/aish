package persona

import (
	"crypto/ed25519"
	"encoding/hex"
	"errors"
)

// PersonaTrustAnchor pairs a persona-bundle signer's Ed25519 public
// key with metadata used at verification time. Compiled into the
// aish binary at build time; the runtime list (personaTrustAnchors)
// is the trust boundary, NOT any on-disk file. Rotation requires an
// aish release — the right blast radius for a trust root.
type PersonaTrustAnchor struct {
	// SignerID matches BundleManifest.SignerID.
	SignerID string
	// PublicKeyHex is the hex-encoded 32-byte Ed25519 public key.
	PublicKeyHex string
	// Revoked, when true, causes verification to fail regardless of
	// signature validity. Flip + ship a new release to revoke.
	Revoked bool
	// Notes is informational; unused by the verifier.
	Notes string
}

// personaTrustAnchors is the compiled-in list. v0.3-5.1 ships ONLY
// the development anchor (used by tests + cmd/aish-persona).
// Production anchors land via PR alongside the actual
// key-management process.
//
// IMPORTANT: the development anchor's private seed lives in this
// file as PersonaDevSigningSeed. It MUST NOT be used to sign any
// persona bundle distributed to real users — the seed is publicly
// known.
var personaTrustAnchors = []PersonaTrustAnchor{
	{
		SignerID:     "aish-persona-dev",
		PublicKeyHex: PersonaDevPublicKeyHex,
		Revoked:      false,
		Notes:        "Development-only signing key used by tests + cmd/aish-persona. NOT for production use.",
	},
}

// PersonaDevSigningSeed is the 32-byte seed used by
// ed25519.NewKeyFromSeed to produce the development persona-bundle
// keypair. Pinned in source so cmd/aish-persona and the test suite
// agree on the dev signer without managing a separate fixture file.
//
// Trade-off identical to community.DevSigningSeed: storing the seed
// in source means the dev key is publicly known. That is intentional
// — the trust-anchor list calling it out as "NOT for production
// use" is the policy fence.
const PersonaDevSigningSeed = "aish-persona-dev-bundle-signer!!"

// PersonaDevPublicKeyHex is the public half of the development
// signing key derived from PersonaDevSigningSeed. Encoded inline so
// the runtime can verify dev-signed bundles without reading the
// disk.
const PersonaDevPublicKeyHex = "52f760eaddc50581f456c30e9054adc05a96bbff8acce1844bddb35a4ddf4712"

// PersonaDevPrivateKey returns the development Ed25519 private key
// derived from PersonaDevSigningSeed. Used by cmd/aish-persona and
// the test suite to sign synthetic bundles. NOT for production use.
func PersonaDevPrivateKey() ed25519.PrivateKey {
	seed := []byte(PersonaDevSigningSeed)
	if len(seed) != ed25519.SeedSize {
		// Programmer error: the seed constant is the wrong length.
		// Panic — this is build-time-detectable.
		panic("persona: PersonaDevSigningSeed must be ed25519.SeedSize (32) bytes")
	}
	return ed25519.NewKeyFromSeed(seed)
}

// findPersonaAnchor returns the trust anchor matching signerID.
// Returns (zero, false) when no anchor matches. A revoked anchor
// still returns ok=true so the verifier can distinguish "unknown"
// from "revoked" in the error it emits.
func findPersonaAnchor(signerID string) (PersonaTrustAnchor, bool) {
	for _, a := range personaTrustAnchors {
		if a.SignerID == signerID {
			return a, true
		}
	}
	return PersonaTrustAnchor{}, false
}

// decodePublicKey returns the raw 32-byte Ed25519 public key for the
// anchor. Returns an error if the hex is malformed or the wrong
// length; that's a programmer error in the compiled-in anchor list,
// not a runtime condition.
func (a PersonaTrustAnchor) decodePublicKey() ([]byte, error) {
	raw, err := hex.DecodeString(a.PublicKeyHex)
	if err != nil {
		return nil, errors.New("persona: trust anchor public key is not valid hex")
	}
	if len(raw) != 32 {
		return nil, errors.New("persona: trust anchor public key wrong length")
	}
	return raw, nil
}

// PersonaTrustAnchorsForTest returns the compiled-in anchor list.
// Exposed for tests in this package so they can verify that the
// development anchor exists.
func PersonaTrustAnchorsForTest() []PersonaTrustAnchor {
	out := make([]PersonaTrustAnchor, len(personaTrustAnchors))
	copy(out, personaTrustAnchors)
	return out
}

// SetPersonaTrustAnchorsForTest replaces the compiled-in anchor list
// with the supplied slice and returns a restore function. ONLY for
// tests in this package.
func SetPersonaTrustAnchorsForTest(anchors []PersonaTrustAnchor) func() {
	saved := personaTrustAnchors
	personaTrustAnchors = anchors
	return func() { personaTrustAnchors = saved }
}
