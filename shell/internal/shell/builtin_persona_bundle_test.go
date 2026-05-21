package shell

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/convergent-systems-co/aish/shell/internal/persona"
)

// buildSyntheticBundleForShell mirrors the persona package's test
// helper but writes into a caller-supplied directory. Used by the
// shell-side integration tests.
func buildSyntheticBundleForShell(t *testing.T, dir string, personas []persona.Persona, id string, version int) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	payload := filepath.Join(dir, persona.BundlePersonasFileName)
	if err := persona.WritePersonasJSONL(personas, payload); err != nil {
		t.Fatalf("WritePersonasJSONL: %v", err)
	}
	sig, sha, err := persona.SignPersonasJSONL(persona.PersonaDevPrivateKey(), payload)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	m := persona.BundleManifest{
		FormatVersion: 1,
		BundleVersion: version,
		BundleID:      id,
		CreatedAt:     time.Now().UTC().Format(time.RFC3339),
		PersonaCount:  len(personas),
		SignerID:      "aish-persona-dev",
		Signature:     sig,
		SHA256:        sha,
	}
	body, err := persona.EncodeBundleManifest(m)
	if err != nil {
		t.Fatalf("EncodeBundleManifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, persona.BundleManifestFileName), body, 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
}

func samplePersona(name string) persona.Persona {
	return persona.Persona{
		Name:         name,
		Version:      persona.SchemaVersion,
		Description:  "community " + name,
		Voice:        "test",
		SystemPrompt: "you are " + name + " — be helpful.",
		Tone:         persona.Tone{Verbosity: "medium", Formality: "neutral"},
	}
}

// TestPersonaInstall_HappyPath — `persona install <dir>` verifies +
// installs a bundle; the personas inside become visible via
// `persona list` in the same session.
func TestPersonaInstall_HappyPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	src := filepath.Join(home, "incoming-bundle")
	buildSyntheticBundleForShell(t, src,
		[]persona.Persona{samplePersona("archivist"), samplePersona("skeptic")},
		"community-pack", 1)

	s := New()
	t.Cleanup(func() { _ = s.Close() })

	var out, errBuf bytes.Buffer
	code := s.personaBuiltin([]string{"install", src}, &out, &errBuf)
	if code != 0 {
		t.Fatalf("persona install exit = %d; stderr=%q", code, errBuf.String())
	}
	if !strings.Contains(out.String(), "community-pack v1") {
		t.Errorf("missing success line:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "2 personas") {
		t.Errorf("expected '2 personas' in success line:\n%s", out.String())
	}
	// New personas must be in the loader.
	for _, want := range []string{"archivist", "skeptic"} {
		if _, ok := s.personas.Get(want); !ok {
			t.Errorf("persona %s not in loader after install", want)
		}
	}
	// Bundle sidecar must exist.
	sidecar := filepath.Join(home, ".aish", persona.BundlesDirName, "community-pack", persona.BundleSidecarFileName)
	if _, err := os.Stat(sidecar); err != nil {
		t.Errorf("sidecar missing: %v", err)
	}
}

// TestPersonaBundles_ListsInstalledBundles — `persona bundles` shows
// the installed bundle in tabular form.
func TestPersonaBundles_ListsInstalledBundles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	src := filepath.Join(home, "incoming")
	buildSyntheticBundleForShell(t, src, []persona.Persona{samplePersona("a")}, "first", 1)
	s := New()
	t.Cleanup(func() { _ = s.Close() })
	if code := s.personaBuiltin([]string{"install", src}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("install: code=%d", code)
	}
	var out, errBuf bytes.Buffer
	if code := s.personaBuiltin([]string{"bundles"}, &out, &errBuf); code != 0 {
		t.Fatalf("bundles: code=%d stderr=%q", code, errBuf.String())
	}
	if !strings.Contains(out.String(), "first") {
		t.Errorf("'first' missing from bundles output:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "aish-persona-dev") {
		t.Errorf("signer id missing:\n%s", out.String())
	}
}

// TestPersonaBundles_NoneInstalled — friendly empty message.
func TestPersonaBundles_NoneInstalled(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	s := New()
	t.Cleanup(func() { _ = s.Close() })
	var out, errBuf bytes.Buffer
	if code := s.personaBuiltin([]string{"bundles"}, &out, &errBuf); code != 0 {
		t.Fatalf("bundles: code=%d", code)
	}
	if !strings.Contains(out.String(), "(no persona bundles installed)") {
		t.Errorf("expected empty-state message; got %q", out.String())
	}
}

// TestPersonaInstall_RejectsBadBundle — install with a missing
// manifest fails cleanly.
func TestPersonaInstall_RejectsBadBundle(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	bad := filepath.Join(home, "bad")
	if err := os.MkdirAll(bad, 0o700); err != nil {
		t.Fatal(err)
	}
	s := New()
	t.Cleanup(func() { _ = s.Close() })
	var out, errBuf bytes.Buffer
	code := s.personaBuiltin([]string{"install", bad}, &out, &errBuf)
	if code == 0 {
		t.Fatalf("install of empty dir should fail; stderr=%q", errBuf.String())
	}
}
