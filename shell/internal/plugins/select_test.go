package plugins

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	proto "github.com/convergent-systems-co/aish/libs/proto/registry"
)

func writeDevSignedManifest(t *testing.T, root, name string) (proto.Manifest, string) {
	t.Helper()
	binDir := t.TempDir()
	binPath := filepath.Join(binDir, name+"-bin")
	if err := os.WriteFile(binPath, []byte("fake-"+name+"\n"), 0o755); err != nil {
		t.Fatalf("write bin: %v", err)
	}
	priv := proto.DevPrivateKey()
	sig, shaHex, err := proto.SignBinary(priv, binPath)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	m := proto.Manifest{
		FormatVersion: proto.CurrentFormatVersion,
		Name:          name,
		Version:       "0.0.1",
		BinaryPath:    binPath,
		Kinds:         []proto.Kind{proto.KindInference},
		SHA256:        shaHex,
		SignerID:      "aish-dev",
		Signature:     sig,
		CreatedAt:     time.Now().UTC().Format(time.RFC3339),
	}
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := proto.WriteManifest(filepath.Join(dir, proto.ManifestFileName), m); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return m, binPath
}

func TestSelect_EmptyDotAish_NoSelection(t *testing.T) {
	dotAish := t.TempDir()
	sel, ok := Select(proto.KindInference, dotAish, nil)
	if ok {
		t.Fatalf("expected ok=false on empty dotAish, got sel=%+v", sel)
	}
}

func TestSelect_BlankDotAish_NoSelection(t *testing.T) {
	sel, ok := Select(proto.KindInference, "", nil)
	if ok {
		t.Fatalf("expected ok=false on blank dotAish, got sel=%+v", sel)
	}
}

func TestSelect_OneInferencePlugin(t *testing.T) {
	dotAish := t.TempDir()
	pluginRoot := filepath.Join(dotAish, proto.DirName)
	m, _ := writeDevSignedManifest(t, pluginRoot, "ollama")

	sel, ok := Select(proto.KindInference, dotAish, nil)
	if !ok {
		t.Fatalf("expected ok=true, got sel=%+v", sel)
	}
	if sel.Name != "ollama" || sel.BinaryPath != m.BinaryPath {
		t.Fatalf("selection mismatch: got %+v want name=ollama path=%s", sel, m.BinaryPath)
	}
	if sel.SignerID != "aish-dev" {
		t.Fatalf("signer = %q want aish-dev", sel.SignerID)
	}
}

func TestSelect_UnknownKind_NoSelection(t *testing.T) {
	dotAish := t.TempDir()
	pluginRoot := filepath.Join(dotAish, proto.DirName)
	writeDevSignedManifest(t, pluginRoot, "ollama")

	_, ok := Select(proto.Kind("nope"), dotAish, nil)
	if ok {
		t.Fatalf("expected ok=false for unknown kind")
	}
}

func TestAll_ReturnsSortedEntries(t *testing.T) {
	dotAish := t.TempDir()
	pluginRoot := filepath.Join(dotAish, proto.DirName)
	writeDevSignedManifest(t, pluginRoot, "zeta")
	writeDevSignedManifest(t, pluginRoot, "alpha")

	entries := All(dotAish, nil)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Manifest.Name != "alpha" || entries[1].Manifest.Name != "zeta" {
		t.Fatalf("entries not sorted: %v", entries)
	}
}

func TestAll_EmptyRegistry(t *testing.T) {
	dotAish := t.TempDir()
	entries := All(dotAish, nil)
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries on empty dotAish, got %d", len(entries))
	}
}

func TestRoot_ReturnsPluginsSubdir(t *testing.T) {
	dotAish := "/home/x/.aish"
	got := Root(dotAish)
	want := filepath.Join(dotAish, proto.DirName)
	if got != want {
		t.Fatalf("Root mismatch: got %q want %q", got, want)
	}
}

func TestSelect_WarnsOnMalformed(t *testing.T) {
	dotAish := t.TempDir()
	pluginRoot := filepath.Join(dotAish, proto.DirName)
	if err := os.MkdirAll(filepath.Join(pluginRoot, "broken"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginRoot, "broken", proto.ManifestFileName), []byte("nope"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	var warn bytes.Buffer
	_, ok := Select(proto.KindInference, dotAish, &warn)
	if ok {
		t.Fatalf("expected ok=false on malformed-only registry")
	}
	if warn.Len() == 0 {
		t.Fatalf("expected a warning to be emitted")
	}
}
