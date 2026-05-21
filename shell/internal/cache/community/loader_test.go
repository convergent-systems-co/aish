package community

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// ed25519PublicKey aliases the stdlib type so the dev-key test can
// assert without re-importing at every call site.
type ed25519PublicKey = ed25519.PublicKey

func bytesToHex(b []byte) string { return hex.EncodeToString(b) }

func TestDiscoverBundleDirEnvOverride(t *testing.T) {
	src := signBundleDir(t, []seedRow{{intent: "x", os: "darwin", invocation: "echo x"}}, nil, "")
	got, err := DiscoverBundleDir(src, nil)
	if err != nil {
		t.Fatalf("DiscoverBundleDir: %v", err)
	}
	if got != src {
		t.Errorf("got = %q, want %q", got, src)
	}
}

func TestDiscoverBundleDirFallback(t *testing.T) {
	src := signBundleDir(t, []seedRow{{intent: "x", os: "darwin", invocation: "echo x"}}, nil, "")
	// env override empty → fallback to candidates list.
	got, err := DiscoverBundleDir("", []string{"/nonexistent/path", src})
	if err != nil {
		t.Fatalf("DiscoverBundleDir: %v", err)
	}
	if got != src {
		t.Errorf("got = %q, want %q", got, src)
	}
}

func TestDiscoverBundleDirNotFound(t *testing.T) {
	_, err := DiscoverBundleDir("", []string{"/nonexistent/path"})
	if !errors.Is(err, ErrBundleNotFound) {
		t.Errorf("err = %v, want ErrBundleNotFound", err)
	}
}

func TestInstallIdempotent(t *testing.T) {
	src := signBundleDir(t, []seedRow{{intent: "list", os: "darwin", invocation: "ls"}}, nil, "")
	dot := t.TempDir()
	var buf1 bytes.Buffer
	m1, err := Install(src, dot, InstallOpts{Logger: &buf1})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	// Read installed bundle bytes after first install.
	first, err := os.ReadFile(filepath.Join(dot, InstalledBundleFileName))
	if err != nil {
		t.Fatalf("read installed: %v", err)
	}
	// Re-install — same source → same destination bytes.
	var buf2 bytes.Buffer
	m2, err := Install(src, dot, InstallOpts{Logger: &buf2})
	if err != nil {
		t.Fatalf("second Install: %v", err)
	}
	if m1.SHA256 != m2.SHA256 {
		t.Errorf("manifest hash changed: %s -> %s", m1.SHA256, m2.SHA256)
	}
	second, err := os.ReadFile(filepath.Join(dot, InstalledBundleFileName))
	if err != nil {
		t.Fatalf("read installed (second): %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Errorf("installed bundle bytes changed across re-install")
	}
}

func TestInstallRefusesDowngrade(t *testing.T) {
	dot := t.TempDir()
	// Stage a v2 install.
	srcV2 := signBundleDir(t, []seedRow{{intent: "v2", os: "darwin", invocation: "echo v2"}}, nil, "")
	// Bump the manifest version on disk to 2 (signBundleDir defaults to 1).
	rewriteVersion(t, srcV2, 2)
	if _, err := Install(srcV2, dot, InstallOpts{}); err != nil {
		t.Fatalf("install v2: %v", err)
	}
	// Now attempt v1.
	srcV1 := signBundleDir(t, []seedRow{{intent: "v1", os: "darwin", invocation: "echo v1"}}, nil, "")
	if _, err := Install(srcV1, dot, InstallOpts{}); !errors.Is(err, ErrDowngradeRefused) {
		t.Errorf("err = %v, want ErrDowngradeRefused", err)
	}
	// Force=true bypasses.
	if _, err := Install(srcV1, dot, InstallOpts{Force: true}); err != nil {
		t.Errorf("Force install failed: %v", err)
	}
}

// rewriteVersion overrides BundleVersion in the manifest at dir
// and re-signs it. Used to stage downgrade-protection scenarios.
func rewriteVersion(t *testing.T, dir string, newVersion int) {
	t.Helper()
	mpath := filepath.Join(dir, ManifestFileName)
	raw, err := os.ReadFile(mpath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	m.BundleVersion = newVersion
	out, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(mpath, out, 0o644); err != nil {
		t.Fatalf("rewrite manifest: %v", err)
	}
}

func TestLoadRejectsSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks unreliable on Windows CI")
	}
	// Create a bundle, then replace bundle.db with a symlink pointing
	// at /etc/hosts — outside the bundle directory.
	dir := signBundleDir(t, []seedRow{{intent: "x", os: "darwin", invocation: "echo x"}}, nil, "")
	bp := filepath.Join(dir, BundleDBFileName)
	if err := os.Remove(bp); err != nil {
		t.Fatalf("remove bundle.db: %v", err)
	}
	// Create the target outside the dir and symlink to it.
	outside := filepath.Join(t.TempDir(), "outside.db")
	if err := os.WriteFile(outside, []byte("not a sqlite file"), 0o644); err != nil {
		t.Fatalf("create outside: %v", err)
	}
	if err := os.Symlink(outside, bp); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	_, err := VerifyBundleDir(dir)
	if err == nil {
		t.Error("VerifyBundleDir: expected error on symlink escape, got nil")
	}
}

func TestOpenInstalledMissing(t *testing.T) {
	dot := t.TempDir()
	_, err := OpenInstalled(dot, nil)
	if !errors.Is(err, ErrBundleNotFound) {
		t.Errorf("err = %v, want ErrBundleNotFound", err)
	}
}

func TestOpenInstalledReturnsBundle(t *testing.T) {
	src := signBundleDir(t, []seedRow{
		{intent: "list files", os: "darwin", invocation: "ls -la"},
	}, nil, "")
	dot := t.TempDir()
	if _, err := Install(src, dot, InstallOpts{}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	b, err := OpenInstalled(dot, nil)
	if err != nil {
		t.Fatalf("OpenInstalled: %v", err)
	}
	defer b.Close()
	got, hit, err := b.Lookup("list files", "darwin")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if !hit || got != "ls -la" {
		t.Errorf("Lookup = (%q, %v), want (ls -la, true)", got, hit)
	}
}

func TestLookupReadOnly(t *testing.T) {
	src := signBundleDir(t, []seedRow{
		{intent: "list files", os: "darwin", invocation: "ls -la"},
	}, nil, "")
	dot := t.TempDir()
	if _, err := Install(src, dot, InstallOpts{}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	bp := filepath.Join(dot, InstalledBundleFileName)
	before, err := os.ReadFile(bp)
	if err != nil {
		t.Fatalf("read before: %v", err)
	}
	b, err := OpenInstalled(dot, nil)
	if err != nil {
		t.Fatalf("OpenInstalled: %v", err)
	}
	defer b.Close()
	for i := 0; i < 5; i++ {
		if _, _, err := b.Lookup("list files", "darwin"); err != nil {
			t.Fatalf("Lookup #%d: %v", i, err)
		}
	}
	after, err := os.ReadFile(bp)
	if err != nil {
		t.Fatalf("read after: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("bundle.db mutated by Lookup (read-only invariant violated)")
	}
}

func TestLookupMiss(t *testing.T) {
	src := signBundleDir(t, []seedRow{
		{intent: "list files", os: "darwin", invocation: "ls -la"},
	}, nil, "")
	dot := t.TempDir()
	if _, err := Install(src, dot, InstallOpts{}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	b, err := OpenInstalled(dot, nil)
	if err != nil {
		t.Fatalf("OpenInstalled: %v", err)
	}
	defer b.Close()
	_, hit, err := b.Lookup("a brand new intent", "linux")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if hit {
		t.Error("hit = true, want false")
	}
}

func TestTrustAnchorsContainsDev(t *testing.T) {
	got := TrustAnchorsForTest()
	found := false
	for _, a := range got {
		if a.SignerID == "aish-dev" && !a.Revoked {
			found = true
			break
		}
	}
	if !found {
		t.Error("dev anchor missing from compiled-in trust list")
	}
}

func TestDevPrivateKeyMatchesAnchor(t *testing.T) {
	// The dev keypair is deterministic — same seed → same public
	// key. The anchor pinned in trust.go MUST match what
	// DevPrivateKey().Public() produces or `make bundle` signs with
	// a key the runtime rejects.
	priv := DevPrivateKey()
	pub := priv.Public().(ed25519PublicKey)
	gotHex := bytesToHex(pub)
	if gotHex != DevPublicKeyHex {
		t.Errorf("dev pub key hex = %s; want %s (trust anchor drift)", gotHex, DevPublicKeyHex)
	}
}
