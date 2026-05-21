package shell

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	proto "github.com/convergent-systems-co/aish/libs/proto/registry"
	"github.com/convergent-systems-co/aish/shell/internal/env"
	"github.com/convergent-systems-co/aish/shell/internal/term"
)

// withFakeHome returns a Shell whose env binds HOME to a temp dir.
// The Shell is constructed without invoking New() (which would try to
// open ~/.aish/cache.db etc); we only need s.env + s.cwd for the
// plugin built-in's dotAishDir() helper.
func withFakeHome(t *testing.T, home string) *Shell {
	t.Helper()
	e := env.FromSlice([]string{"HOME=" + home, "PATH=" + os.Getenv("PATH")})
	cwd, _ := os.Getwd()
	return &Shell{cwd: cwd, env: e}
}

func writeFakeManifest(t *testing.T, dotAish, name string) {
	t.Helper()
	binDir := t.TempDir()
	binPath := filepath.Join(binDir, name+"-bin")
	if err := os.WriteFile(binPath, []byte("fake-"+name+"\n"), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
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
	dir := filepath.Join(dotAish, proto.DirName, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := proto.WriteManifest(filepath.Join(dir, proto.ManifestFileName), m); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
}

func TestPluginBuiltin_BareUsage(t *testing.T) {
	home := t.TempDir()
	s := withFakeHome(t, home)
	var stdout, stderr bytes.Buffer
	code := s.pluginBuiltin(nil, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("expected exit 2 on bare invocation, got %d", code)
	}
	if !strings.Contains(stderr.String(), "Usage:") {
		t.Fatalf("expected usage line, got stderr=%q", stderr.String())
	}
}

func TestPluginBuiltin_UnknownSub(t *testing.T) {
	home := t.TempDir()
	s := withFakeHome(t, home)
	var stdout, stderr bytes.Buffer
	code := s.pluginBuiltin([]string{"danceparty"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("expected exit 2, got %d", code)
	}
	if !strings.Contains(stderr.String(), "unknown subcommand") {
		t.Fatalf("expected unknown-subcommand message, got %q", stderr.String())
	}
}

func TestPluginBuiltin_ListEmpty(t *testing.T) {
	home := t.TempDir()
	s := withFakeHome(t, home)
	var stdout, stderr bytes.Buffer
	code := s.pluginBuiltin([]string{"list"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("list empty exit=%d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "No plugins registered") {
		t.Fatalf("expected 'No plugins registered', got %q", stdout.String())
	}
}

func TestPluginBuiltin_ListShowsRegisteredPlugin(t *testing.T) {
	home := t.TempDir()
	dotAish := filepath.Join(home, ".aish")
	if err := os.MkdirAll(dotAish, 0o755); err != nil {
		t.Fatalf("mkdir dotAish: %v", err)
	}
	writeFakeManifest(t, dotAish, "fake")

	s := withFakeHome(t, home)
	var stdout, stderr bytes.Buffer
	code := s.pluginBuiltin([]string{"list"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("list exit=%d stderr=%q", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "fake") || !strings.Contains(out, "v0.0.1") || !strings.Contains(out, "signer=aish-dev") {
		t.Fatalf("expected plugin row, got %q", out)
	}
}

func TestPluginBuiltin_StatusEmpty(t *testing.T) {
	home := t.TempDir()
	s := withFakeHome(t, home)
	var stdout, stderr bytes.Buffer
	code := s.pluginBuiltin([]string{"status"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("status exit=%d stderr=%q", code, stderr.String())
	}
	out := stdout.String()
	// Either "fallback (PATH=...)" or "none registered" is acceptable;
	// depends on whether aish-inference-cloud is on PATH in CI.
	if !strings.Contains(out, "plugin:") {
		t.Fatalf("expected plugin: prefix in status, got %q", out)
	}
}

func TestPluginBuiltin_StatusRegistered(t *testing.T) {
	home := t.TempDir()
	dotAish := filepath.Join(home, ".aish")
	if err := os.MkdirAll(dotAish, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFakeManifest(t, dotAish, "ollama")

	s := withFakeHome(t, home)
	var stdout, stderr bytes.Buffer
	code := s.pluginBuiltin([]string{"status"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("status exit=%d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "1 registered") || !strings.Contains(stdout.String(), "ollama") {
		t.Fatalf("expected '1 registered (ollama)', got %q", stdout.String())
	}
}

func TestPluginBuiltin_ListIsReadOnly(t *testing.T) {
	// Calling list twice MUST NOT mutate state. We construct a
	// registry with one plugin, snapshot the manifest file's mtime,
	// call list twice, and assert mtime is unchanged.
	home := t.TempDir()
	dotAish := filepath.Join(home, ".aish")
	if err := os.MkdirAll(dotAish, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFakeManifest(t, dotAish, "fake")
	manifestPath := filepath.Join(dotAish, proto.DirName, "fake", proto.ManifestFileName)
	before, err := os.Stat(manifestPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	s := withFakeHome(t, home)
	for i := 0; i < 2; i++ {
		var stdout, stderr bytes.Buffer
		if code := s.pluginBuiltin([]string{"list"}, &stdout, &stderr); code != 0 {
			t.Fatalf("list#%d exit=%d", i, code)
		}
	}
	after, err := os.Stat(manifestPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if !before.ModTime().Equal(after.ModTime()) {
		t.Fatalf("list mutated manifest mtime: before=%v after=%v",
			before.ModTime(), after.ModTime())
	}
}

func TestSelectRegistryInferencePlugin_EmptyDotAish(t *testing.T) {
	// Calling the boot helper with a brand-new HOME should yield "".
	home := t.TempDir()
	dotAish := filepath.Join(home, ".aish")
	got := selectRegistryInferencePlugin(dotAish, nil)
	if got != "" {
		t.Fatalf("expected empty selection on empty registry, got %q", got)
	}
}

func TestSelectRegistryInferencePlugin_BlankDotAish(t *testing.T) {
	got := selectRegistryInferencePlugin("", nil)
	if got != "" {
		t.Fatalf("expected empty selection on blank dotAish, got %q", got)
	}
}

func TestSelectRegistryInferencePlugin_RegisteredPlugin(t *testing.T) {
	home := t.TempDir()
	dotAish := filepath.Join(home, ".aish")
	if err := os.MkdirAll(dotAish, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFakeManifest(t, dotAish, "ollama")
	got := selectRegistryInferencePlugin(dotAish, nil)
	if got == "" {
		t.Fatalf("expected selection to find registered ollama plugin")
	}
	if !strings.HasSuffix(got, "ollama-bin") {
		t.Fatalf("got %q, expected suffix ollama-bin", got)
	}
}

func TestResolveTier_PluginIsBuiltin(t *testing.T) {
	s := withFakeHome(t, t.TempDir())
	// ResolveTier's switch lookup is what we're asserting here —
	// "plugin" must be recognised as a built-in so the highlighter
	// colors it as such and the dispatch loop catches it before the
	// known-binary tier.
	tier := s.ResolveTier("plugin")
	if tier != term.TierBuiltin {
		t.Fatalf("expected TierBuiltin for 'plugin', got %v", tier)
	}
}
