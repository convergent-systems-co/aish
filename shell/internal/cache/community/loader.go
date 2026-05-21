package community

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// ErrBundleNotFound is returned by discovery functions when no
// candidate directory contains a recognisable bundle. The shell
// treats this the same as "L3 unavailable" — silent degradation.
var ErrBundleNotFound = errors.New("community: bundle not found")

// ErrDowngradeRefused is returned by Install when the on-disk
// installed bundle has a strictly greater BundleVersion than the
// candidate. The caller can pass Force=true to override.
var ErrDowngradeRefused = errors.New("community: refusing to downgrade installed bundle")

// Sidecar is the JSON struct aish writes alongside the installed
// community-bundle.db to record provenance + version. Used by
// Install for downgrade protection and by `aish community info` to
// report install state.
type Sidecar struct {
	BundleVersion int    `json:"bundle_version"`
	SignerID      string `json:"signer_id"`
	SHA256        string `json:"sha256"`
	IntentCount   int    `json:"intent_count"`
	CreatedAt     string `json:"created_at"`
	InstalledAt   string `json:"installed_at"`
	SourcePath    string `json:"source_path"`
}

// InstallOpts controls Install behavior.
type InstallOpts struct {
	// Force, when true, bypasses the downgrade check. The
	// `aish community refresh` built-in passes Force=true.
	Force bool
	// Logger, when non-nil, receives stderr-style status lines as
	// the install progresses. nil → silent. The shell threads
	// os.Stderr here.
	Logger io.Writer
}

// DiscoverBundleDir walks a list of candidate directories and
// returns the first one that contains manifest.json + bundle.db.
// Candidates are tried in order:
//
//  1. $AISH_COMMUNITY_BUNDLE_DIR (if set + non-empty)
//  2. dirs supplied by caller (typically:
//     /usr/local/share/aish/community,
//     <binary>/../share/aish/community)
//
// Returns ErrBundleNotFound when none match. Does NOT verify the
// bundle — that's Install's job. Discovery is just "is something
// shaped like a bundle present here?"
func DiscoverBundleDir(envOverride string, candidates []string) (string, error) {
	tryDirs := []string{}
	if envOverride != "" {
		tryDirs = append(tryDirs, envOverride)
	}
	tryDirs = append(tryDirs, candidates...)
	for _, d := range tryDirs {
		if d == "" {
			continue
		}
		mf := filepath.Join(d, ManifestFileName)
		bf := filepath.Join(d, BundleDBFileName)
		if isRegularFile(mf) && isRegularFile(bf) {
			return d, nil
		}
	}
	return "", ErrBundleNotFound
}

// isRegularFile returns true iff path exists and is a regular file
// (not a directory, not a symlink to a directory). Symlinks to
// regular files are accepted at this stage; VerifyBundleDir's
// containment check is the strict barrier.
func isRegularFile(path string) bool {
	fi, err := os.Stat(path)
	if err != nil {
		return false
	}
	return fi.Mode().IsRegular()
}

// Install runs the full first-run pipeline:
//
//  1. Verify the bundle directory (VerifyBundleDir).
//  2. Compare BundleVersion against any existing sidecar; refuse
//     downgrade unless Force=true.
//  3. Copy bundle.db to dotAish/community-bundle.db (atomic via
//     write-to-temp + rename).
//  4. Write the sidecar JSON.
//
// Returns the verified Manifest on success.
//
// Install is idempotent under steady state: running it twice from
// the same source directory yields the same destination bytes and a
// sidecar with the same content (except installed_at). Tested by
// TestInstallIdempotent.
func Install(srcDir, dotAish string, opts InstallOpts) (Manifest, error) {
	manifest, err := VerifyBundleDir(srcDir)
	if err != nil {
		return Manifest{}, err
	}
	if err := os.MkdirAll(dotAish, 0o755); err != nil {
		return Manifest{}, fmt.Errorf("community: ensure ~/.aish: %w", err)
	}

	// Downgrade protection.
	sidecarPath := filepath.Join(dotAish, SidecarFileName)
	if prev, err := readSidecar(sidecarPath); err == nil {
		if prev.BundleVersion > manifest.BundleVersion && !opts.Force {
			return Manifest{}, fmt.Errorf("%w (installed=%d candidate=%d)",
				ErrDowngradeRefused, prev.BundleVersion, manifest.BundleVersion)
		}
	}

	// Copy bundle.db atomically.
	srcBundle := filepath.Join(srcDir, BundleDBFileName)
	dstBundle := filepath.Join(dotAish, InstalledBundleFileName)
	if err := copyFileAtomic(srcBundle, dstBundle); err != nil {
		return Manifest{}, err
	}

	// Sidecar write.
	sc := Sidecar{
		BundleVersion: manifest.BundleVersion,
		SignerID:      manifest.SignerID,
		SHA256:        manifest.SHA256,
		IntentCount:   manifest.IntentCount,
		CreatedAt:     manifest.CreatedAt,
		InstalledAt:   time.Now().UTC().Format(time.RFC3339),
		SourcePath:    srcDir,
	}
	if err := writeSidecar(sidecarPath, sc); err != nil {
		return Manifest{}, err
	}
	if opts.Logger != nil {
		fmt.Fprintf(opts.Logger, "aish community: installed %s v%d (%d intents, signer=%s)\n",
			InstalledBundleFileName, manifest.BundleVersion, manifest.IntentCount, manifest.SignerID)
	}
	return manifest, nil
}

// OpenInstalled returns a Bundle backed by ~/.aish/community-bundle.db,
// using the sidecar JSON for manifest data. Returns ErrBundleNotFound
// when no installed bundle is present.
//
// OpenInstalled does NOT re-verify the signature on every call —
// that would be expensive. Verification happened at Install time and
// the file's integrity inside ~/.aish is the user's responsibility
// (same trust posture as ~/.aish/cache.db).
//
// Emits a stderr warning via opts.Logger when the bundle's
// created_at is older than StaleAfterDays.
func OpenInstalled(dotAish string, logger io.Writer) (*Bundle, error) {
	dbPath := filepath.Join(dotAish, InstalledBundleFileName)
	if !isRegularFile(dbPath) {
		return nil, ErrBundleNotFound
	}
	sc, err := readSidecar(filepath.Join(dotAish, SidecarFileName))
	if err != nil {
		// Bundle present, sidecar missing — still usable; synthesize
		// a partial manifest so `aish community info` has something
		// to print. This is a soft degradation path.
		return &Bundle{
			manifest: Manifest{
				FormatVersion: 1,
				BundleVersion: 0,
				SignerID:      "unknown",
				CreatedAt:     "",
			},
			dbPath: dbPath,
		}, nil
	}
	createdAt := sc.CreatedAt
	if createdAt == "" {
		// Sidecar predates the CreatedAt field — fall back to
		// InstalledAt so `aish community info` still has something
		// useful to print.
		createdAt = sc.InstalledAt
	}
	m := Manifest{
		FormatVersion: 1,
		BundleVersion: sc.BundleVersion,
		SignerID:      sc.SignerID,
		SHA256:        sc.SHA256,
		IntentCount:   sc.IntentCount,
		CreatedAt:     createdAt,
	}
	b := &Bundle{manifest: m, dbPath: dbPath}
	if logger != nil && isStale(createdAt) {
		fmt.Fprintf(logger,
			"aish community: warning: bundle older than %d days (created=%s)\n",
			StaleAfterDays, createdAt)
	}
	return b, nil
}

// isStale returns true iff rfc3339Time is parseable and older than
// StaleAfterDays. Unparseable strings return false — we'd rather
// suppress a confusing warning than crash on a malformed sidecar.
func isStale(rfc3339Time string) bool {
	t, err := time.Parse(time.RFC3339, rfc3339Time)
	if err != nil {
		return false
	}
	return time.Since(t) > time.Duration(StaleAfterDays)*24*time.Hour
}

func readSidecar(path string) (Sidecar, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Sidecar{}, err
	}
	var sc Sidecar
	if err := json.Unmarshal(raw, &sc); err != nil {
		return Sidecar{}, err
	}
	return sc, nil
}

func writeSidecar(path string, sc Sidecar) error {
	raw, err := json.MarshalIndent(sc, "", "  ")
	if err != nil {
		return fmt.Errorf("community: marshal sidecar: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return fmt.Errorf("community: write sidecar: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("community: rename sidecar: %w", err)
	}
	return nil
}

// copyFileAtomic copies src to dst via dst.tmp + rename, so a partial
// write never replaces a previously-installed bundle. mode is fixed
// at 0o644.
func copyFileAtomic(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("community: open src %s: %w", src, err)
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("community: open dst %s: %w", tmp, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("community: copy: %w", err)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("community: close dst: %w", err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("community: rename dst: %w", err)
	}
	return nil
}

// DefaultCandidates returns the standard list of bundle directories
// to consult when DiscoverBundleDir is called without explicit
// candidates. Encapsulates the platform-specific search path so the
// shell wiring is one line.
func DefaultCandidates(binaryPath string) []string {
	out := []string{
		"/usr/local/share/aish/community",
	}
	if binaryPath != "" {
		// <binary>/../share/aish/community is the canonical
		// "installed in /usr/local/bin → bundle in /usr/local/share/aish"
		// relationship for `make install`.
		dir := filepath.Dir(binaryPath)
		out = append(out, filepath.Join(dir, "..", "share", "aish", "community"))
	}
	return out
}
