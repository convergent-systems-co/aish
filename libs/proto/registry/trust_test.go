package registry

import (
	"crypto/ed25519"
	"encoding/hex"
	"testing"
)

func TestDevPrivateKey_DerivesDevPublicKey(t *testing.T) {
	priv := DevPrivateKey()
	pub := priv.Public().(ed25519.PublicKey)
	got := hex.EncodeToString(pub)
	if got != DevPublicKeyHex {
		t.Fatalf("dev pubkey mismatch: got %s want %s", got, DevPublicKeyHex)
	}
}

func TestTrustAnchors_ContainDev(t *testing.T) {
	anchors := TrustAnchorsForTest()
	found := false
	for _, a := range anchors {
		if a.SignerID == "aish-dev" {
			found = true
			if a.PublicKeyHex != DevPublicKeyHex {
				t.Fatalf("aish-dev anchor key mismatch: %s vs %s",
					a.PublicKeyHex, DevPublicKeyHex)
			}
			if a.Revoked {
				t.Fatalf("aish-dev anchor must not be revoked at v0.3-2")
			}
		}
	}
	if !found {
		t.Fatalf("trustAnchors must include aish-dev")
	}
}

func TestSetTrustAnchorsForTest_Roundtrip(t *testing.T) {
	original := TrustAnchorsForTest()
	restore := SetTrustAnchorsForTest([]TrustAnchor{
		{SignerID: "fake", PublicKeyHex: "00" + hex.EncodeToString(make([]byte, 31))},
	})
	got := TrustAnchorsForTest()
	if len(got) != 1 || got[0].SignerID != "fake" {
		t.Fatalf("override didn't take effect: %v", got)
	}
	restore()
	got = TrustAnchorsForTest()
	if len(got) != len(original) {
		t.Fatalf("restore didn't put anchors back: got %d want %d", len(got), len(original))
	}
}
