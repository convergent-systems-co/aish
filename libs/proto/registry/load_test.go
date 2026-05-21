package registry

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// helperManifestFor produces a dev-signed manifest pointing at a real
// fake binary on disk. Lives in the proto package so we don't need to
// pull plugins/cloud/internal/registry into the test imports.
func helperManifestFor(t *testing.T, name, binDir string) Manifest {
	t.Helper()
	binPath := filepath.Join(binDir, name+"-bin")
	if err := os.WriteFile(binPath, []byte("fake-"+name+"\n"), 0o755); err != nil {
		t.Fatalf("write binary: %v", err)
	}
	priv := DevPrivateKey()
	sig, shaHex, err := SignBinary(priv, binPath)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return Manifest{
		FormatVersion: CurrentFormatVersion,
		Name:          name,
		Version:       "0.0.1",
		BinaryPath:    binPath,
		Kinds:         []Kind{KindInference},
		SHA256:        shaHex,
		SignerID:      "aish-dev",
		Signature:     sig,
		CreatedAt:     time.Now().UTC().Format(time.RFC3339),
	}
}

func writeManifestUnder(t *testing.T, root, name string, m Manifest) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := WriteManifest(filepath.Join(dir, ManifestFileName), m); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
}

func TestLoad_EmptyDir_ReturnsEmpty(t *testing.T) {
	root := t.TempDir()
	entries, err := Load(root, nil)
	if err != nil {
		t.Fatalf("Load empty dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries, got %d", len(entries))
	}
}

func TestLoad_MissingDir_ReturnsEmpty(t *testing.T) {
	root := filepath.Join(t.TempDir(), "does-not-exist")
	entries, err := Load(root, nil)
	if err != nil {
		t.Fatalf("Load missing dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries, got %d", len(entries))
	}
}

func TestLoad_OneValidPlugin(t *testing.T) {
	root := t.TempDir()
	binDir := t.TempDir()
	m := helperManifestFor(t, "fake", binDir)
	writeManifestUnder(t, root, "fake", m)
	entries, err := Load(root, nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(entries) != 1 || entries[0].Manifest.Name != "fake" {
		t.Fatalf("unexpected entries: %v", entries)
	}
}

func TestLoad_SortedByName(t *testing.T) {
	root := t.TempDir()
	for _, n := range []string{"zeta", "alpha", "mu"} {
		binDir := t.TempDir()
		m := helperManifestFor(t, n, binDir)
		writeManifestUnder(t, root, n, m)
	}
	entries, err := Load(root, nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
	want := []string{"alpha", "mu", "zeta"}
	for i, e := range entries {
		if e.Manifest.Name != want[i] {
			t.Fatalf("entry[%d] = %q want %q", i, e.Manifest.Name, want[i])
		}
	}
}

func TestLoad_SkipsMalformedManifest_EmitsWarning(t *testing.T) {
	root := t.TempDir()
	binDir := t.TempDir()
	writeManifestUnder(t, root, "good", helperManifestFor(t, "good", binDir))

	badDir := filepath.Join(root, "broken")
	if err := os.MkdirAll(badDir, 0o755); err != nil {
		t.Fatalf("mkdir broken: %v", err)
	}
	if err := os.WriteFile(filepath.Join(badDir, ManifestFileName), []byte("not json"), 0o644); err != nil {
		t.Fatalf("write broken manifest: %v", err)
	}

	var warn bytes.Buffer
	entries, err := Load(root, &warn)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(entries) != 1 || entries[0].Manifest.Name != "good" {
		t.Fatalf("expected only the 'good' entry, got %v", entries)
	}
	if !strings.Contains(warn.String(), "broken") {
		t.Fatalf("expected warning about broken plugin, got %q", warn.String())
	}
}

func TestLoad_SkipsUnknownSigner(t *testing.T) {
	root := t.TempDir()
	binDir := t.TempDir()
	good := helperManifestFor(t, "good", binDir)
	writeManifestUnder(t, root, "good", good)

	// Stranger: same valid manifest but with an unknown SignerID.
	// Validate() still passes (SignerID is non-empty), but signature
	// math will fail at trust-anchor lookup.
	stranger := good
	stranger.Name = "stranger"
	stranger.SignerID = "not-in-anchors"
	// Re-sign so the JSON is well-formed (still wrong signer).
	sig, _, _ := SignBinary(DevPrivateKey(), good.BinaryPath)
	stranger.Signature = sig
	writeManifestUnder(t, root, "stranger", stranger)

	var warn bytes.Buffer
	entries, err := Load(root, &warn)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(entries) != 1 || entries[0].Manifest.Name != "good" {
		t.Fatalf("expected only 'good' entry, got %v", entries)
	}
	if !strings.Contains(warn.String(), "stranger") {
		t.Fatalf("expected warning about stranger, got %q", warn.String())
	}
}

func TestLoad_SkipsHiddenAndNonDirectories(t *testing.T) {
	root := t.TempDir()
	// Place a .lock file and a stray manifest.json directly in root —
	// neither should be picked up.
	_ = os.WriteFile(filepath.Join(root, ".lock"), []byte(""), 0o644)
	_ = os.WriteFile(filepath.Join(root, ManifestFileName), []byte("ignored"), 0o644)

	binDir := t.TempDir()
	writeManifestUnder(t, root, "real", helperManifestFor(t, "real", binDir))

	entries, err := Load(root, nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(entries) != 1 || entries[0].Manifest.Name != "real" {
		t.Fatalf("expected only 'real', got %v", entries)
	}
}

func TestSelectByKind_Match(t *testing.T) {
	root := t.TempDir()
	binDir := t.TempDir()
	writeManifestUnder(t, root, "ollama", helperManifestFor(t, "ollama", binDir))
	entries, _ := Load(root, nil)
	got, ok := SelectByKind(entries, KindInference)
	if !ok {
		t.Fatalf("expected inference kind to match")
	}
	if got.Manifest.Name != "ollama" {
		t.Fatalf("name = %q want ollama", got.Manifest.Name)
	}
}

func TestSelectByKind_NoMatch(t *testing.T) {
	root := t.TempDir()
	binDir := t.TempDir()
	writeManifestUnder(t, root, "ollama", helperManifestFor(t, "ollama", binDir))
	entries, _ := Load(root, nil)
	if _, ok := SelectByKind(entries, Kind("imaginary")); ok {
		t.Fatalf("expected no match for imaginary kind")
	}
}

func TestSelectByKind_EmptyEntries(t *testing.T) {
	if _, ok := SelectByKind(nil, KindInference); ok {
		t.Fatalf("expected no match on empty slice")
	}
}
