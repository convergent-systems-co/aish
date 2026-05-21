// Package registry defines the on-disk plugin manifest format for the
// v0.3-2 plugin registry plus the signature-verification primitives
// needed to validate it.
//
// A plugin in aish is a binary that speaks the libs/proto/inference
// NDJSON wire shape on stdin/stdout. The manifest declares the binary
// (absolute path), the kinds it implements (inference today, more
// later), the binary's SHA-256 digest, the signer that vouches for it,
// and an Ed25519 signature over the digest.
//
// The on-disk layout is one directory per plugin under
// ~/.aish/plugins/<name>/ containing exactly one file:
//
//	manifest.json
//
// The binary itself lives wherever the manifest's BinaryPath points;
// the manifest is the trust anchor, not the directory layout.
//
// This package carries types + verification ONLY — no transport, no
// filesystem walking, no spawn logic. The shell and plugins/cloud
// modules import these types and verify manifests at their own
// discretion.
//
// See GOALS.md §"Epic v0.3-2 — Plugin Registry" for the broader design
// and .artifacts/plans/v0.3-2.md for the chosen format trade-offs.
package registry

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// ManifestFileName is the canonical filename for a plugin manifest
// inside a plugin's directory.
const ManifestFileName = "manifest.json"

// CurrentFormatVersion is the on-disk format version the current code
// produces. Bumped on any breaking change to the JSON shape.
const CurrentFormatVersion = 1

// Kind is the capability a plugin advertises in its manifest. A plugin
// MAY advertise multiple kinds; the registry indexes by kind so the
// shell can pick "an inference plugin" without naming a specific one.
type Kind string

const (
	// KindInference covers the existing libs/proto/inference wire shape
	// — the cloud plugin, Ollama, future remote-inference plugins.
	KindInference Kind = "inference"
)

// AllKinds enumerates the kinds aish currently understands. Plugins
// advertising a kind not in this list are accepted by the registry
// (forward compatibility) but a shell that doesn't recognise the kind
// will simply ignore them.
func AllKinds() []Kind {
	return []Kind{KindInference}
}

// Manifest is the on-disk JSON description of one installed plugin.
//
// Fields are exported so cmd/aish-plugin (the admin CLI) and the
// shell's registry loader can marshal / unmarshal them. The Signature
// is base64(ed25519.Sign(privKey, sha256(binary_at_path))) — see
// verify.go.
type Manifest struct {
	// FormatVersion pins the on-disk format. v0.3-2 ships format
	// version 1; bump on any breaking change to the JSON shape.
	FormatVersion int `json:"format_version"`

	// Name is the human + system-readable plugin name. Used as the
	// directory key under ~/.aish/plugins/. MUST match the regex
	// [a-z0-9][a-z0-9-]{0,62} — restrictive on purpose to keep the
	// directory layout predictable across platforms.
	Name string `json:"name"`

	// Version is the plugin author's semver. Informational at this
	// tier — the registry does not enforce monotonicity (different
	// signers may publish different versions). Future "update" UX
	// will compare semver here.
	Version string `json:"version"`

	// BinaryPath is the absolute path to the plugin binary aish
	// spawns. MUST be absolute; relative paths are rejected at
	// validation time so the registry cannot be reinterpreted by a
	// cwd change. The selector / installer canonicalises symlinks
	// at use time.
	BinaryPath string `json:"binary_path"`

	// Kinds lists the capabilities this plugin implements. At least
	// one MUST appear; today only KindInference is recognised by
	// the shell but unknown kinds are NOT rejected so a forward-
	// compatible plugin can advertise both "inference" and (say)
	// "intent-search" without the older shell choking on the second.
	Kinds []Kind `json:"kinds"`

	// SHA256 is hex(sha256(<binary at BinaryPath>)). Carried in the
	// manifest as a belt-and-braces check against in-flight
	// corruption and as the message the signature covers.
	SHA256 string `json:"sha256"`

	// SignerID is the human-readable identifier of the keypair that
	// signed this manifest. Looked up against the compiled-in trust
	// anchor list at verify time.
	SignerID string `json:"signer_id"`

	// Signature is base64(ed25519.Sign(priv, sha256_bytes)). 64 raw
	// bytes → 88 base64 chars.
	Signature string `json:"signature"`

	// CreatedAt is the manifest's signing time in RFC3339 UTC.
	// Informational; no expiry semantics at this tier.
	CreatedAt string `json:"created_at"`
}

// ErrManifestMalformed is returned by Validate when the manifest fails
// structural checks. Wrapped errors carry the specific field-level
// detail; callers branch on errors.Is(err, ErrManifestMalformed).
var ErrManifestMalformed = errors.New("registry: manifest malformed")

// nameRE matches the allowed Name shape. We avoid importing
// regexp/syntax in this small package and check by hand. Allowed
// characters: lowercase ASCII letters, digits, hyphen. MUST start with
// a letter or digit. Length 1..63.
func validName(name string) bool {
	if len(name) == 0 || len(name) > 63 {
		return false
	}
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-' && i > 0:
		default:
			return false
		}
	}
	return true
}

// Validate runs the static-shape check on a Manifest. Used by both
// the installer (before writing the manifest to disk) and the loader
// (before trusting an on-disk manifest). Does NOT verify the
// signature — that's verify.go's job. Splitting the two lets the
// CLI's `verify` subcommand emit "manifest malformed" before paying
// the crypto cost.
func (m Manifest) Validate() error {
	if m.FormatVersion <= 0 {
		return fmt.Errorf("%w: format_version must be > 0", ErrManifestMalformed)
	}
	if !validName(m.Name) {
		return fmt.Errorf("%w: name %q does not match [a-z0-9][a-z0-9-]{0,62}", ErrManifestMalformed, m.Name)
	}
	if strings.TrimSpace(m.Version) == "" {
		return fmt.Errorf("%w: version is empty", ErrManifestMalformed)
	}
	if strings.TrimSpace(m.BinaryPath) == "" {
		return fmt.Errorf("%w: binary_path is empty", ErrManifestMalformed)
	}
	// Absolute path required. We accept both POSIX and Windows
	// absolute paths (drive-letter prefix) to keep the format
	// cross-platform; the runtime walks symlinks at use time.
	if !isAbsolutePath(m.BinaryPath) {
		return fmt.Errorf("%w: binary_path %q is not absolute", ErrManifestMalformed, m.BinaryPath)
	}
	if len(m.Kinds) == 0 {
		return fmt.Errorf("%w: kinds is empty", ErrManifestMalformed)
	}
	for _, k := range m.Kinds {
		if strings.TrimSpace(string(k)) == "" {
			return fmt.Errorf("%w: kinds contains an empty entry", ErrManifestMalformed)
		}
	}
	if strings.TrimSpace(m.SHA256) == "" {
		return fmt.Errorf("%w: sha256 is empty", ErrManifestMalformed)
	}
	if _, err := hex.DecodeString(m.SHA256); err != nil {
		return fmt.Errorf("%w: sha256 is not valid hex: %v", ErrManifestMalformed, err)
	}
	if len(m.SHA256) != 64 {
		return fmt.Errorf("%w: sha256 must be 64 hex chars (got %d)", ErrManifestMalformed, len(m.SHA256))
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
	if strings.TrimSpace(m.CreatedAt) == "" {
		return fmt.Errorf("%w: created_at is empty", ErrManifestMalformed)
	}
	return nil
}

// HasKind reports whether the manifest advertises the requested
// capability. Used by the shell's plugin selector to filter the
// registry by kind.
func (m Manifest) HasKind(k Kind) bool {
	for _, mk := range m.Kinds {
		if mk == k {
			return true
		}
	}
	return false
}

// isAbsolutePath returns true for POSIX absolute paths (leading /)
// and Windows absolute paths (drive-letter + ':\' or '/' OR a UNC
// path '\\server\share' / '//server/share'). We deliberately avoid
// importing filepath here — the package compiles to identical bytes
// on every platform so cross-validation of a Linux-built manifest on
// macOS gives the same answer.
func isAbsolutePath(p string) bool {
	if p == "" {
		return false
	}
	if p[0] == '/' || p[0] == '\\' {
		return true
	}
	// Windows drive-letter prefix: C:\ or C:/ — letter, colon, sep.
	if len(p) >= 3 && p[1] == ':' &&
		((p[0] >= 'a' && p[0] <= 'z') || (p[0] >= 'A' && p[0] <= 'Z')) &&
		(p[2] == '\\' || p[2] == '/') {
		return true
	}
	return false
}
