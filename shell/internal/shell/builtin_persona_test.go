package shell

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/convergent-systems-co/aish/shell/internal/persona"
)

// newTestShellForPersona constructs a Shell with HOME pointed at a
// fresh temp directory. The persona registry is preloaded from the
// bundled set; tests can override active selection via WriteActivePersona.
func newTestShellForPersona(t *testing.T) (*Shell, string) {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	s := New()
	t.Cleanup(func() { _ = s.Close() })
	return s, tmp
}

func TestPersonaBuiltin_List_PrintsBundledNames(t *testing.T) {
	s, _ := newTestShellForPersona(t)
	var out bytes.Buffer
	code := s.personaBuiltin([]string{"list"}, &out, &bytes.Buffer{})
	if code != 0 {
		t.Fatalf("persona list exit = %d, want 0", code)
	}
	got := out.String()
	for _, name := range []string{"default", "mentor", "terse-veteran", "playful", "socratic"} {
		if !strings.Contains(got, name) {
			t.Errorf("persona list output missing %q:\n%s", name, got)
		}
	}
}

func TestPersonaBuiltin_Show_KnownName(t *testing.T) {
	s, _ := newTestShellForPersona(t)
	var out bytes.Buffer
	code := s.personaBuiltin([]string{"show", "mentor"}, &out, &bytes.Buffer{})
	if code != 0 {
		t.Fatalf("persona show mentor exit = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "mentor") {
		t.Errorf("persona show output does not mention mentor:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "Patient") {
		t.Errorf("persona show output missing description excerpt:\n%s", out.String())
	}
}

func TestPersonaBuiltin_Show_UnknownName(t *testing.T) {
	s, _ := newTestShellForPersona(t)
	var out, errBuf bytes.Buffer
	code := s.personaBuiltin([]string{"show", "no-such-persona"}, &out, &errBuf)
	if code == 0 {
		t.Fatalf("persona show unknown: exit = 0, want non-zero")
	}
	if !strings.Contains(errBuf.String(), "no-such-persona") {
		t.Errorf("stderr does not name the missing persona:\n%s", errBuf.String())
	}
}

func TestPersonaBuiltin_Set_PersistsActive(t *testing.T) {
	s, tmp := newTestShellForPersona(t)
	var out bytes.Buffer
	code := s.personaBuiltin([]string{"set", "playful"}, &out, &bytes.Buffer{})
	if code != 0 {
		t.Fatalf("persona set playful: exit = %d, want 0", code)
	}
	// Verify persistence on disk.
	got := persona.ReadActivePersona(tmp)
	if got != "playful" {
		t.Errorf("ReadActivePersona = %q, want playful", got)
	}
	// Verify in-memory active matches.
	active := s.Persona()
	if active.Name != "playful" {
		t.Errorf("Shell.Persona() = %q, want playful", active.Name)
	}
}

func TestPersonaBuiltin_Set_UnknownRefuses(t *testing.T) {
	s, _ := newTestShellForPersona(t)
	var out, errBuf bytes.Buffer
	code := s.personaBuiltin([]string{"set", "no-such-persona"}, &out, &errBuf)
	if code == 0 {
		t.Fatalf("persona set unknown: exit = 0, want non-zero")
	}
	if !strings.Contains(errBuf.String(), "no-such-persona") {
		t.Errorf("stderr does not name the missing persona:\n%s", errBuf.String())
	}
}

func TestPersonaBuiltin_Active_PrintsName(t *testing.T) {
	s, _ := newTestShellForPersona(t)
	// Pre-set
	if code := s.personaBuiltin([]string{"set", "mentor"}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("seed set: exit = %d", code)
	}
	var out bytes.Buffer
	code := s.personaBuiltin([]string{"active"}, &out, &bytes.Buffer{})
	if code != 0 {
		t.Fatalf("persona active: exit = %d", code)
	}
	if !strings.Contains(out.String(), "mentor") {
		t.Errorf("persona active output: %q; want contains 'mentor'", out.String())
	}
}

func TestPersonaBuiltin_Use_AliasForSet(t *testing.T) {
	s, _ := newTestShellForPersona(t)
	code := s.personaBuiltin([]string{"use", "socratic"}, &bytes.Buffer{}, &bytes.Buffer{})
	if code != 0 {
		t.Fatalf("persona use socratic: exit = %d", code)
	}
	if s.Persona().Name != "socratic" {
		t.Errorf("Shell.Persona() = %q after `use socratic`, want socratic", s.Persona().Name)
	}
}

func TestPersonaBuiltin_BareUsage(t *testing.T) {
	s, _ := newTestShellForPersona(t)
	var out, errBuf bytes.Buffer
	code := s.personaBuiltin(nil, &out, &errBuf)
	if code == 0 {
		t.Fatalf("bare persona: exit = 0, want non-zero")
	}
	if !strings.Contains(errBuf.String(), "Usage") {
		t.Errorf("stderr missing usage hint:\n%s", errBuf.String())
	}
}

// TestShellNew_LoadsPersistedPersona — the Shell constructor reads
// ~/.aish/config.toml on startup and activates the persisted persona.
func TestShellNew_LoadsPersistedPersona(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	// Pre-write the config so the next New() reads it.
	if err := persona.WriteActivePersona(tmp, "terse-veteran"); err != nil {
		t.Fatal(err)
	}

	s := New()
	t.Cleanup(func() { _ = s.Close() })

	if got := s.Persona().Name; got != "terse-veteran" {
		t.Errorf("Shell.Persona() = %q after New(), want terse-veteran", got)
	}
}

// TestShellNew_ReadsUserDirOverride — a user persona on disk overrides
// the bundled persona with the same name.
func TestShellNew_ReadsUserDirOverride(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	pdir := filepath.Join(tmp, ".aish", "personas")
	if err := os.MkdirAll(pdir, 0o755); err != nil {
		t.Fatal(err)
	}
	override := `
name = "mentor"
version = 1
system_prompt = "I am the override mentor."

[tone]
verbosity = "medium"
formality = "neutral"
`
	if err := os.WriteFile(filepath.Join(pdir, "mentor.toml"), []byte(override), 0o644); err != nil {
		t.Fatal(err)
	}

	s := New()
	t.Cleanup(func() { _ = s.Close() })

	if code := s.personaBuiltin([]string{"set", "mentor"}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("set mentor: exit = %d", code)
	}
	if !strings.Contains(s.Persona().SystemPrompt, "override mentor") {
		t.Errorf("user override did not win; SystemPrompt = %q", s.Persona().SystemPrompt)
	}
}
