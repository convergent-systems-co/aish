// Package persona — bundle.go implements the v0.3-5.1 persona-bundle
// format. A persona bundle is a signed, versioned directory of
// community personas that the user can install with `aish persona
// install <dir>`. The format mirrors the v0.2-3 community-cache
// pattern: a TOML manifest with format / bundle versions, signer ID,
// and signature; a personas.jsonl payload listing one persona per
// line; and a compiled-in trust anchor list (trust.go) gating which
// signers the installer accepts.
//
// Why a separate format from the community cache: community-cache
// rows are (intent, invocation) tuples; persona rows are
// (name, system_prompt, tone, capability_gates, prompt_overrides)
// tuples. They sign with different keys (different blast radius),
// they verify against different anchors, and they install into
// different on-disk locations. Reusing the schema would have
// hidden these differences behind a leaky generic.
//
// On-disk layout of a bundle directory:
//
//	<dir>/
//	├── manifest.toml      — TOML manifest (versions, signer_id, signature, …)
//	├── personas.jsonl     — JSONL payload (one persona per line)
//	└── trust-anchors.toml — informational; compiled-in anchors are the trust boundary
//
// The signature covers SHA-256(personas.jsonl), encoded base64. The
// trust anchor's public key is compiled in; the on-disk
// trust-anchors.toml is for human audit only and is never consulted
// at verify time.

package persona

import (
	"bufio"
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
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Bundle-related on-disk layout constants. Persona bundles live as
// directories — manifest + personas.jsonl + trust-anchors copy.
const (
	// BundleManifestFileName is the canonical name of the manifest
	// TOML file inside a bundle directory.
	BundleManifestFileName = "manifest.toml"
	// BundlePersonasFileName is the canonical name of the JSONL
	// payload (one persona per line, JSON-encoded persona schema).
	BundlePersonasFileName = "personas.jsonl"
	// BundleTrustAnchorsFileName is the informational human-readable
	// copy of the trust anchors compiled into aish.
	BundleTrustAnchorsFileName = "trust-anchors.toml"

	// BundlesDirName is the per-user directory that holds every
	// installed bundle's sidecar metadata + personas.jsonl.
	BundlesDirName = "persona-bundles"
	// BundleSidecarFileName is the JSON sidecar aish writes per
	// installed bundle to record installed version + signer.
	BundleSidecarFileName = "bundle.json"

	// BundleStaleAfterDays mirrors the community cache's stale-after
	// threshold. A bundle older than this emits a stderr warning at
	// open time but is still loaded — offline users still benefit
	// from a stale persona corpus.
	BundleStaleAfterDays = 90
)

// BundleManifest is the on-disk manifest.toml shape. Embedded in
// every bundle directory. Signature is base64(ed25519.Sign(priv,
// sha256(personas.jsonl))).
type BundleManifest struct {
	// FormatVersion pins the on-disk format. v0.3-5.1 ships format
	// version 1; bump on any breaking change.
	FormatVersion int `toml:"format_version"`
	// BundleVersion is the corpus version. Monotonic per signer.
	// Used for downgrade protection in Install.
	BundleVersion int `toml:"bundle_version"`
	// BundleID is the human-friendly slug used for the on-disk
	// install directory under ~/.aish/persona-bundles/<id>/. Must
	// match [a-z0-9][a-z0-9-]{0,63}.
	BundleID string `toml:"bundle_id"`
	// CreatedAt is the bundle build time in RFC3339 UTC.
	CreatedAt string `toml:"created_at"`
	// PersonaCount is the count of personas in personas.jsonl at
	// sign time. Informational; the loader does not re-count.
	PersonaCount int `toml:"persona_count"`
	// SignerID is the human-readable identifier of the keypair
	// that signed the bundle. Looked up against the compiled-in
	// persona trust anchors at verify time.
	SignerID string `toml:"signer_id"`
	// Signature is base64(ed25519.Sign(priv, sha256(personas.jsonl))).
	Signature string `toml:"signature"`
	// SHA256 is hex(sha256(personas.jsonl)). Belt-and-braces; the
	// verifier recomputes the hash anyway.
	SHA256 string `toml:"sha256"`
}

// BundleSidecar is the installed-bundle metadata aish writes under
// ~/.aish/persona-bundles/<id>/bundle.json after a successful
// install. Used by `persona bundles` and by downgrade protection.
type BundleSidecar struct {
	BundleVersion int    `json:"bundle_version"`
	BundleID      string `json:"bundle_id"`
	SignerID      string `json:"signer_id"`
	SHA256        string `json:"sha256"`
	PersonaCount  int    `json:"persona_count"`
	CreatedAt     string `json:"created_at"`
	InstalledAt   string `json:"installed_at"`
	SourcePath    string `json:"source_path"`
}

// InstalledBundle is the summary returned by ListBundles for the
// `persona bundles` built-in. Decoupled from BundleSidecar so future
// fields can land without breaking the public surface.
type InstalledBundle struct {
	ID            string
	BundleVersion int
	SignerID      string
	PersonaCount  int
	CreatedAt     string
}

// bundleIDRe matches valid bundle IDs. Identical character class to
// persona names — keeps the on-disk slug shape consistent.
var bundleIDRe = nameRe // shared

// Manifest errors used by tests + the install path.
var (
	ErrBundleManifestMalformed = errors.New("persona bundle: manifest malformed")
	ErrBundleSignatureInvalid  = errors.New("persona bundle: signature verification failed")
	ErrBundleHashMismatch      = errors.New("persona bundle: personas.jsonl hash mismatch")
	ErrBundleDowngradeRefused  = errors.New("persona bundle: refusing to downgrade installed bundle")
	ErrBundleSafetyBypass      = errors.New("persona bundle: persona attempts safety bypass")
)

// ReadBundleManifest parses manifest.toml. Strict on undecoded
// keys — a manifest carrying an extra field (e.g. a smuggled
// trust-anchor override) is rejected.
func ReadBundleManifest(path string) (BundleManifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return BundleManifest{}, fmt.Errorf("persona bundle: read manifest: %w", err)
	}
	var m BundleManifest
	meta, err := toml.Decode(string(raw), &m)
	if err != nil {
		return BundleManifest{}, fmt.Errorf("%w: %v", ErrBundleManifestMalformed, err)
	}
	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		return BundleManifest{}, fmt.Errorf("%w: unknown keys %v", ErrBundleManifestMalformed, undecoded)
	}
	if err := m.Validate(); err != nil {
		return BundleManifest{}, err
	}
	return m, nil
}

// Validate runs the static-shape check on a BundleManifest. Caller
// is ReadBundleManifest + the build tool's post-sign check.
func (m BundleManifest) Validate() error {
	if m.FormatVersion <= 0 {
		return fmt.Errorf("%w: format_version must be > 0", ErrBundleManifestMalformed)
	}
	if m.BundleVersion <= 0 {
		return fmt.Errorf("%w: bundle_version must be > 0", ErrBundleManifestMalformed)
	}
	if !bundleIDRe.MatchString(m.BundleID) {
		return fmt.Errorf("%w: bundle_id %q is invalid", ErrBundleManifestMalformed, m.BundleID)
	}
	if strings.TrimSpace(m.SignerID) == "" {
		return fmt.Errorf("%w: signer_id is empty", ErrBundleManifestMalformed)
	}
	if strings.TrimSpace(m.Signature) == "" {
		return fmt.Errorf("%w: signature is empty", ErrBundleManifestMalformed)
	}
	if _, err := base64.StdEncoding.DecodeString(m.Signature); err != nil {
		return fmt.Errorf("%w: signature is not valid base64", ErrBundleManifestMalformed)
	}
	if _, err := hex.DecodeString(m.SHA256); err != nil {
		return fmt.Errorf("%w: sha256 is not valid hex", ErrBundleManifestMalformed)
	}
	if strings.TrimSpace(m.CreatedAt) == "" {
		return fmt.Errorf("%w: created_at is empty", ErrBundleManifestMalformed)
	}
	return nil
}

// ReadBundlePersonas reads personas.jsonl into a slice of validated
// Persona structs. The safety-bypass denylist runs on every persona
// — a malformed or hostile persona in the JSONL causes the entire
// bundle to be rejected (better to refuse install than to ship a
// half-trusted corpus).
func ReadBundlePersonas(path string) ([]Persona, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("persona bundle: open personas.jsonl: %w", err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)
	var out []Persona
	seen := map[string]bool{}
	line := 0
	for scanner.Scan() {
		line++
		row := strings.TrimSpace(scanner.Text())
		if row == "" {
			continue
		}
		var p Persona
		if err := json.Unmarshal([]byte(row), &p); err != nil {
			return nil, fmt.Errorf("persona bundle: row %d: %w", line, err)
		}
		if err := p.Validate(); err != nil {
			return nil, fmt.Errorf("persona bundle: row %d: %w", line, err)
		}
		if seen[p.Name] {
			return nil, fmt.Errorf("persona bundle: row %d: duplicate name %q", line, p.Name)
		}
		seen[p.Name] = true
		out = append(out, p)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("persona bundle: scan personas.jsonl: %w", err)
	}
	return out, nil
}

// VerifyBundleDir runs the full verification pipeline against a
// directory containing manifest.toml + personas.jsonl. Mirrors
// community.VerifyBundleDir's shape; differences are the trust
// anchor list (persona-specific) and the payload filename (jsonl,
// not a SQLite blob).
//
// Returns (manifest, personas) on success. Either return is the
// zero-value on error.
func VerifyBundleDir(dir string) (BundleManifest, []Persona, error) {
	canon, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return BundleManifest{}, nil, fmt.Errorf("persona bundle: resolve dir: %w", err)
	}
	manifestPath := filepath.Join(canon, BundleManifestFileName)
	payloadPath := filepath.Join(canon, BundlePersonasFileName)

	m, err := ReadBundleManifest(manifestPath)
	if err != nil {
		return BundleManifest{}, nil, err
	}

	// Resolve trust anchor.
	anchor, ok := findPersonaAnchor(m.SignerID)
	if !ok {
		return BundleManifest{}, nil, fmt.Errorf("persona bundle: signer %q not in trust anchors", m.SignerID)
	}
	if anchor.Revoked {
		return BundleManifest{}, nil, fmt.Errorf("persona bundle: signer %q is revoked", m.SignerID)
	}

	// Recompute hash + compare.
	gotHash, err := HashFile(payloadPath)
	if err != nil {
		return BundleManifest{}, nil, err
	}
	if gotHash != m.SHA256 {
		return BundleManifest{}, nil, fmt.Errorf("%w (manifest=%s got=%s)", ErrBundleHashMismatch, m.SHA256, gotHash)
	}

	// Decode signature + verify.
	sig, err := base64.StdEncoding.DecodeString(m.Signature)
	if err != nil {
		return BundleManifest{}, nil, fmt.Errorf("%w: signature decode: %v", ErrBundleManifestMalformed, err)
	}
	pub, err := anchor.decodePublicKey()
	if err != nil {
		return BundleManifest{}, nil, err
	}
	hashBytes, _ := hex.DecodeString(gotHash)
	if !ed25519.Verify(ed25519.PublicKey(pub), hashBytes, sig) {
		return BundleManifest{}, nil, ErrBundleSignatureInvalid
	}

	// Parse + validate personas. Validate() runs the safety-bypass
	// denylist; a single bad row torches the whole bundle.
	personas, err := ReadBundlePersonas(payloadPath)
	if err != nil {
		// Re-classify a bypass detection error so callers (and tests)
		// can grep for ErrBundleSafetyBypass cleanly.
		if strings.Contains(err.Error(), "safety-bypass") {
			return BundleManifest{}, nil, fmt.Errorf("%w: %v", ErrBundleSafetyBypass, err)
		}
		return BundleManifest{}, nil, err
	}
	if len(personas) != m.PersonaCount {
		return BundleManifest{}, nil, fmt.Errorf(
			"persona bundle: manifest persona_count=%d but personas.jsonl has %d",
			m.PersonaCount, len(personas))
	}
	return m, personas, nil
}

// InstallBundle verifies the source directory and copies its
// personas.jsonl + sidecar into ~/.aish/persona-bundles/<bundle_id>/.
// Idempotent under steady state — re-running the same source yields
// the same on-disk bytes plus a fresh InstalledAt timestamp.
//
// Downgrade protection: when an existing sidecar reports a strictly
// greater BundleVersion than the candidate, InstallBundle returns
// ErrBundleDowngradeRefused.
//
// On success, every persona in the bundle is also copied to
// ~/.aish/personas/ so the existing loader picks them up without
// any registry plumbing. Names collisions with the user's existing
// personas are reported via stderr and the bundled persona is NOT
// copied (the user wins).
func InstallBundle(srcDir, dotAish string, logger io.Writer) (BundleManifest, error) {
	manifest, personas, err := VerifyBundleDir(srcDir)
	if err != nil {
		return BundleManifest{}, err
	}
	if err := os.MkdirAll(dotAish, 0o700); err != nil {
		return BundleManifest{}, fmt.Errorf("persona bundle: ensure ~/.aish: %w", err)
	}
	installDir := filepath.Join(dotAish, BundlesDirName, manifest.BundleID)
	if err := os.MkdirAll(installDir, 0o700); err != nil {
		return BundleManifest{}, fmt.Errorf("persona bundle: ensure install dir: %w", err)
	}

	// Downgrade check.
	sidecarPath := filepath.Join(installDir, BundleSidecarFileName)
	if prev, err := readBundleSidecar(sidecarPath); err == nil {
		if prev.BundleVersion > manifest.BundleVersion {
			return BundleManifest{}, fmt.Errorf("%w (installed=%d candidate=%d)",
				ErrBundleDowngradeRefused, prev.BundleVersion, manifest.BundleVersion)
		}
	}

	// Copy personas.jsonl atomically.
	srcPayload := filepath.Join(srcDir, BundlePersonasFileName)
	dstPayload := filepath.Join(installDir, BundlePersonasFileName)
	if err := copyFileAtomic(srcPayload, dstPayload); err != nil {
		return BundleManifest{}, err
	}

	// Sidecar write.
	sc := BundleSidecar{
		BundleVersion: manifest.BundleVersion,
		BundleID:      manifest.BundleID,
		SignerID:      manifest.SignerID,
		SHA256:        manifest.SHA256,
		PersonaCount:  manifest.PersonaCount,
		CreatedAt:     manifest.CreatedAt,
		InstalledAt:   time.Now().UTC().Format(time.RFC3339),
		SourcePath:    srcDir,
	}
	if err := writeBundleSidecar(sidecarPath, sc); err != nil {
		return BundleManifest{}, err
	}

	// Copy personas into ~/.aish/personas/ so the existing loader
	// picks them up without any new wiring. A user persona of the
	// same name wins — we never overwrite a user file.
	personasDir := filepath.Join(dotAish, PersonaDirName)
	if err := os.MkdirAll(personasDir, 0o700); err != nil {
		return BundleManifest{}, fmt.Errorf("persona bundle: ensure personas dir: %w", err)
	}
	for _, p := range personas {
		dst := filepath.Join(personasDir, p.Name+".toml")
		if _, err := os.Stat(dst); err == nil {
			if logger != nil {
				fmt.Fprintf(logger, "persona bundle: %s already exists in ~/.aish/personas/; skipping (user file wins)\n", p.Name)
			}
			continue
		}
		body, err := EncodeTOML(p)
		if err != nil {
			return BundleManifest{}, fmt.Errorf("persona bundle: encode %s: %w", p.Name, err)
		}
		if err := os.WriteFile(dst, body, 0o600); err != nil {
			return BundleManifest{}, fmt.Errorf("persona bundle: write %s: %w", dst, err)
		}
	}
	if logger != nil {
		fmt.Fprintf(logger, "persona bundle: installed %s v%d (%d personas, signer=%s)\n",
			manifest.BundleID, manifest.BundleVersion, manifest.PersonaCount, manifest.SignerID)
	}
	return manifest, nil
}

// ListBundles returns the installed bundles under
// ~/.aish/persona-bundles/. Bundles with a missing sidecar are
// skipped — they cannot be summarised reliably. Returns an empty
// slice when no bundles are installed.
func ListBundles(dotAish string) ([]InstalledBundle, error) {
	dir := filepath.Join(dotAish, BundlesDirName)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []InstalledBundle{}, nil
		}
		return nil, fmt.Errorf("persona bundle: read bundles dir: %w", err)
	}
	out := make([]InstalledBundle, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sidecarPath := filepath.Join(dir, e.Name(), BundleSidecarFileName)
		sc, err := readBundleSidecar(sidecarPath)
		if err != nil {
			continue
		}
		out = append(out, InstalledBundle{
			ID:            sc.BundleID,
			BundleVersion: sc.BundleVersion,
			SignerID:      sc.SignerID,
			PersonaCount:  sc.PersonaCount,
			CreatedAt:     sc.CreatedAt,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// SignPersonasJSONL signs a personas.jsonl file with the supplied
// Ed25519 private key. Returns the base64 signature + hex sha256 of
// the file. Used by cmd/aish-persona at build time; exposed here so
// tests can sign synthetic bundles.
func SignPersonasJSONL(priv ed25519.PrivateKey, payloadPath string) (sigB64, sha256Hex string, err error) {
	sha256Hex, err = HashFile(payloadPath)
	if err != nil {
		return "", "", err
	}
	hashBytes, err := hex.DecodeString(sha256Hex)
	if err != nil {
		return "", "", fmt.Errorf("persona bundle: hex decode hash: %w", err)
	}
	sig := ed25519.Sign(priv, hashBytes)
	return base64.StdEncoding.EncodeToString(sig), sha256Hex, nil
}

// HashFile returns the SHA-256 hex digest of the file at path.
func HashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("persona bundle: open %s: %w", path, err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("persona bundle: hash: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// EncodeBundleManifest renders a BundleManifest as TOML. Used by the
// build tool.
func EncodeBundleManifest(m BundleManifest) ([]byte, error) {
	var buf strings.Builder
	enc := toml.NewEncoder(&buf)
	if err := enc.Encode(m); err != nil {
		return nil, fmt.Errorf("persona bundle: encode manifest: %w", err)
	}
	return []byte(buf.String()), nil
}

// WritePersonasJSONL writes the personas to disk one JSON line each.
// Order is the input order so the build tool can produce
// reproducible bundles by sorting personas before calling.
func WritePersonasJSONL(personas []Persona, path string) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("persona bundle: open tmp: %w", err)
	}
	w := bufio.NewWriter(f)
	for _, p := range personas {
		raw, err := json.Marshal(&p)
		if err != nil {
			_ = f.Close()
			_ = os.Remove(tmp)
			return fmt.Errorf("persona bundle: marshal %s: %w", p.Name, err)
		}
		if _, err := w.Write(append(raw, '\n')); err != nil {
			_ = f.Close()
			_ = os.Remove(tmp)
			return fmt.Errorf("persona bundle: write %s: %w", p.Name, err)
		}
	}
	if err := w.Flush(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("persona bundle: flush: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("persona bundle: close tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("persona bundle: rename: %w", err)
	}
	return nil
}

// readBundleSidecar parses a JSON sidecar file. Returns an error
// when the file is missing or malformed.
func readBundleSidecar(path string) (BundleSidecar, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return BundleSidecar{}, err
	}
	var sc BundleSidecar
	if err := json.Unmarshal(raw, &sc); err != nil {
		return BundleSidecar{}, fmt.Errorf("persona bundle: parse sidecar: %w", err)
	}
	return sc, nil
}

// writeBundleSidecar writes the JSON sidecar atomically. mode 0o600.
func writeBundleSidecar(path string, sc BundleSidecar) error {
	raw, err := json.MarshalIndent(sc, "", "  ")
	if err != nil {
		return fmt.Errorf("persona bundle: marshal sidecar: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("persona bundle: write sidecar: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("persona bundle: rename sidecar: %w", err)
	}
	return nil
}

// copyFileAtomic copies src to dst via dst.tmp + rename so a partial
// write never replaces a previously-installed payload. mode 0o600.
func copyFileAtomic(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("persona bundle: open src: %w", err)
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("persona bundle: open dst: %w", err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("persona bundle: copy: %w", err)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("persona bundle: close dst: %w", err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("persona bundle: rename dst: %w", err)
	}
	return nil
}
