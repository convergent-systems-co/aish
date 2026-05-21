package registry

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	proto "github.com/convergent-systems-co/aish/libs/proto/registry"
)

// InstallOpts controls Install behavior.
type InstallOpts struct {
	// Force, when true, replaces an existing manifest for the same
	// plugin name. Default behavior refuses to overwrite.
	Force bool
	// Logger receives stderr-style status lines. nil → silent.
	Logger io.Writer
}

// InstallSource describes one plugin to install. The caller fills
// Name + BinaryPath + Version + Kinds; Install computes the SHA-256,
// fills the timestamp, signs with PrivateKey, and writes the
// resulting manifest under <root>/<name>/manifest.json.
type InstallSource struct {
	Name       string
	Version    string
	BinaryPath string
	Kinds      []proto.Kind
	SignerID   string
	PrivateKey ed25519.PrivateKey
}

// Install signs the binary at src.BinaryPath, writes the manifest to
// <root>/<name>/manifest.json, and returns the resulting Manifest.
//
// Steps:
//  1. Acquire the registry lock at <root>/.lock (waits up to
//     LockTimeout for a concurrent install to release it).
//  2. Verify <root>/<name>/ does not already contain a manifest
//     (unless opts.Force).
//  3. Hash src.BinaryPath and sign the hash with src.PrivateKey.
//  4. Run the full verify pipeline against the produced manifest to
//     catch any local-config drift (e.g. signer not in trust anchors).
//  5. Write the manifest via the standard temp+rename atomic write.
func Install(root string, src InstallSource, opts InstallOpts) (proto.Manifest, error) {
	if root == "" {
		return proto.Manifest{}, errors.New("registry: Install: empty root")
	}
	if src.Name == "" {
		return proto.Manifest{}, errors.New("registry: Install: empty Name")
	}
	if src.BinaryPath == "" {
		return proto.Manifest{}, errors.New("registry: Install: empty BinaryPath")
	}
	if src.SignerID == "" {
		return proto.Manifest{}, errors.New("registry: Install: empty SignerID")
	}
	if len(src.PrivateKey) == 0 {
		return proto.Manifest{}, errors.New("registry: Install: nil PrivateKey")
	}
	if len(src.Kinds) == 0 {
		return proto.Manifest{}, errors.New("registry: Install: empty Kinds")
	}
	absBin, err := filepath.Abs(src.BinaryPath)
	if err != nil {
		return proto.Manifest{}, fmt.Errorf("registry: Install: abs binary path: %w", err)
	}
	if _, err := os.Stat(absBin); err != nil {
		return proto.Manifest{}, fmt.Errorf("registry: Install: binary not accessible: %w", err)
	}

	if err := os.MkdirAll(root, 0o755); err != nil {
		return proto.Manifest{}, fmt.Errorf("registry: Install: ensure root: %w", err)
	}

	release, err := acquireLock(root)
	if err != nil {
		return proto.Manifest{}, err
	}
	defer release()

	pluginDir := filepath.Join(root, src.Name)
	manifestPath := filepath.Join(pluginDir, proto.ManifestFileName)
	if _, err := os.Stat(manifestPath); err == nil && !opts.Force {
		return proto.Manifest{}, fmt.Errorf("%w: %s (use --force to overwrite)", ErrAlreadyInstalled, src.Name)
	}
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		return proto.Manifest{}, fmt.Errorf("registry: Install: ensure plugin dir: %w", err)
	}

	sigB64, shaHex, err := proto.SignBinary(src.PrivateKey, absBin)
	if err != nil {
		return proto.Manifest{}, err
	}

	m := proto.Manifest{
		FormatVersion: proto.CurrentFormatVersion,
		Name:          src.Name,
		Version:       src.Version,
		BinaryPath:    absBin,
		Kinds:         src.Kinds,
		SHA256:        shaHex,
		SignerID:      src.SignerID,
		Signature:     sigB64,
		CreatedAt:     time.Now().UTC().Format(time.RFC3339),
	}
	if err := proto.VerifyManifestSignature(m); err != nil {
		return proto.Manifest{}, fmt.Errorf("registry: Install: post-sign verify: %w", err)
	}
	if err := proto.WriteManifest(manifestPath, m); err != nil {
		return proto.Manifest{}, err
	}
	if opts.Logger != nil {
		fmt.Fprintf(opts.Logger,
			"aish: registry: installed %s v%s (kinds=%v signer=%s)\n",
			m.Name, m.Version, m.Kinds, m.SignerID)
	}
	return m, nil
}

// Remove deletes the manifest directory for name. Returns ErrNotFound
// when nothing is installed under that name.
func Remove(root, name string, logger io.Writer) error {
	if root == "" {
		return errors.New("registry: Remove: empty root")
	}
	if name == "" {
		return errors.New("registry: Remove: empty name")
	}
	release, err := acquireLock(root)
	if err != nil {
		return err
	}
	defer release()

	pluginDir := filepath.Join(root, name)
	manifestPath := filepath.Join(pluginDir, proto.ManifestFileName)
	if _, err := os.Stat(manifestPath); errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w: %s", ErrNotFound, name)
	} else if err != nil {
		return fmt.Errorf("registry: Remove: stat: %w", err)
	}
	if err := os.RemoveAll(pluginDir); err != nil {
		return fmt.Errorf("registry: Remove: remove dir: %w", err)
	}
	if logger != nil {
		fmt.Fprintf(logger, "aish: registry: removed %s\n", name)
	}
	return nil
}

// VerifyInstalled runs the full verify pipeline (signature + binary
// hash) on the manifest under <root>/<name>/manifest.json. Used by
// the `aish plugin verify` CLI subcommand.
func VerifyInstalled(root, name string) (proto.Manifest, error) {
	if root == "" {
		return proto.Manifest{}, errors.New("registry: VerifyInstalled: empty root")
	}
	if name == "" {
		return proto.Manifest{}, errors.New("registry: VerifyInstalled: empty name")
	}
	manifestPath := filepath.Join(root, name, proto.ManifestFileName)
	m, err := proto.ReadManifest(manifestPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return proto.Manifest{}, fmt.Errorf("%w: %s", ErrNotFound, name)
		}
		return proto.Manifest{}, err
	}
	if err := proto.VerifyManifestAgainstBinary(m); err != nil {
		return m, err
	}
	return m, nil
}

// acquireLock opens (creating if needed) <root>/.lock and tries to
// claim it. Returns a release closure to be deferred.
//
// Implementation note: O_CREATE|O_EXCL — portable and cross-platform
// without pulling in golang.org/x/sys/unix's flock. A process that
// crashes mid-install leaves a stale lock file, recovered when the
// next install runs after LockTimeout.
func acquireLock(root string) (release func(), err error) {
	lockPath := filepath.Join(root, LockFileName)
	deadline := time.Now().Add(LockTimeout)
	for {
		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			_ = f.Close()
			return func() { _ = os.Remove(lockPath) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("registry: acquire lock: %w", err)
		}
		st, statErr := os.Stat(lockPath)
		if statErr == nil && time.Since(st.ModTime()) > LockTimeout {
			_ = os.Remove(lockPath)
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("%w (held at %s)", ErrLockBusy, lockPath)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
