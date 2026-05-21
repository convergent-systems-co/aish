// Package community implements the v0.2-3 L3 community cache.
//
// The L3 cache is a read-only, Ed25519-signed SQLite file ("bundle.db")
// distributed alongside an aish release. On first run the shell verifies
// the bundle's signature against compiled-in trust anchors and copies
// it under ~/.aish/community-bundle.db. The cache orchestrator
// (shell/internal/cache.Cache.Resolve) consults the bundle on L1 miss
// before walking the inference plugin path; an L3 hit is promoted to
// L1 with source='community' provenance so subsequent calls land on
// the existing exact-hash path.
//
// Layout of this subpackage:
//
//	bundle.go  — Manifest + Bundle types, file-layout constants.
//	verify.go  — Ed25519 + SHA-256 signature verification.
//	trust.go   — Compiled-in trust anchors (signer pubkeys + revocation).
//	loader.go  — Discovery, install-on-first-run, sidecar manifest.
//	lookup.go  — Read-only intent lookup against an opened bundle.
//
// The L3 bundle is append-only and read-only; users never write back
// to it. Promotion to L1 is the only path by which an L3 row enters
// the user's per-user store.
package community

// Layout constants for the on-disk bundle directory. A community
// bundle is a directory containing exactly three files:
//
//	<dir>/
//	├── manifest.json   — JSON manifest (version, signer_id, signature, …)
//	├── bundle.db       — SQLite file (the actual L3 contents)
//	└── trust-anchors.toml  — informational; the binary's compiled-in
//	                          anchors are the trust boundary, NOT this
//	                          file. Shipped for human auditing.
const (
	// ManifestFileName is the canonical name of the JSON manifest
	// file inside a bundle directory.
	ManifestFileName = "manifest.json"
	// BundleDBFileName is the canonical name of the SQLite bundle
	// inside a bundle directory.
	BundleDBFileName = "bundle.db"
	// TrustAnchorsFileName is the informational human-readable copy
	// of the trust anchors that come compiled into aish. It is NOT
	// consulted at verify time — the compiled-in anchors win — but
	// it ships so an auditor can read what signed the bundle.
	TrustAnchorsFileName = "trust-anchors.toml"

	// SidecarFileName is the JSON file aish writes under ~/.aish/
	// alongside the installed community-bundle.db to record the
	// installed version + signer_id. Used for downgrade protection.
	SidecarFileName = "community-bundle.json"

	// InstalledBundleFileName is the name aish gives the bundle.db
	// after it has been verified and copied under ~/.aish/.
	InstalledBundleFileName = "community-bundle.db"

	// StaleAfterDays is the loader's "warn but do not reject"
	// threshold for bundle freshness. A bundle whose manifest
	// created_at is older than this many days yields a stderr warning
	// on Open; the bundle is still loaded because an offline user
	// without a fresh release still benefits from a stale L3.
	StaleAfterDays = 90
)

// Manifest is the on-disk JSON description of a community bundle.
// Embedded as `manifest.json` inside the bundle directory. The
// Signature is base64(ed25519.Sign(privKey, sha256(bundle.db))) — see
// verify.go.
//
// Fields are exported so the build tool (cmd/aish-community) can
// marshal them. The shell side unmarshals via encoding/json.
type Manifest struct {
	// FormatVersion pins the on-disk format. v0.2-3 ships format
	// version 1; bump on any breaking change to the JSON shape or
	// the bundle.db schema.
	FormatVersion int `json:"format_version"`
	// BundleVersion identifies the corpus version. Monotonic per
	// signer. Used for downgrade protection in the loader.
	BundleVersion int `json:"bundle_version"`
	// CreatedAt is the bundle build time in RFC3339 UTC. The
	// loader emits a stderr warning when CreatedAt is older than
	// StaleAfterDays.
	CreatedAt string `json:"created_at"`
	// IntentCount is the count of rows in bundle.db at sign time.
	// Informational only — the loader does NOT re-count.
	IntentCount int `json:"intent_count"`
	// SignerID is the human-readable identifier of the keypair
	// that signed this bundle. Looked up against the compiled-in
	// trust anchor list at verify time.
	SignerID string `json:"signer_id"`
	// Signature is base64(ed25519.Sign(priv, sha256(bundle.db))).
	// 64 raw bytes → 88 base64 chars.
	Signature string `json:"signature"`
	// SHA256 is hex(sha256(bundle.db)). Carried in the manifest as a
	// belt-and-braces check against in-flight corruption; the
	// signature itself is the trust mechanism. The verifier
	// recomputes the hash anyway.
	SHA256 string `json:"sha256"`
}

// Bundle is an opened, verified community bundle. The dbPath points
// at the on-disk SQLite file (either the original location for a
// freshly-verified bundle or ~/.aish/community-bundle.db for the
// installed copy). The db handle is opened lazily on the first
// Lookup; Close releases it.
//
// All fields are unexported because callers should treat Bundle as
// opaque. Use the loader functions (OpenInstalled, OpenFromDir) to
// construct it.
type Bundle struct {
	manifest Manifest
	dbPath   string
	db       *bundleDB // nil until first Lookup
}

// Manifest returns the parsed manifest. Exposed for the
// `aish community info` built-in. Returns the zero Manifest if the
// receiver is nil.
func (b *Bundle) Manifest() Manifest {
	if b == nil {
		return Manifest{}
	}
	return b.manifest
}

// Path returns the on-disk path to the bundle.db file. Exposed for
// diagnostics and for the `aish community info` built-in.
func (b *Bundle) Path() string {
	if b == nil {
		return ""
	}
	return b.dbPath
}

// IsLoaded returns true when the bundle has a usable on-disk file
// open. Used by the shell to decide whether to surface "community
// cache not available" in `aish community info`.
func (b *Bundle) IsLoaded() bool {
	return b != nil && b.dbPath != ""
}
