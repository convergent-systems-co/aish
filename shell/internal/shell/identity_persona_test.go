package shell

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/convergent-systems-co/aish/shell/internal/persona"
	"github.com/convergent-systems-co/aish/shell/internal/secrets"
)

// TestIdentityUse_WithPersonaActivatesBoth — the `identity use NAME
// --persona <p>` form activates the identity AND switches the active
// persona in one call.
func TestIdentityUse_WithPersonaActivatesBoth(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	// Seed an identity profile so SetActive doesn't fail.
	if err := secrets.CreateProfile(home, secrets.Identity{Name: "work"}); err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}

	s := New()
	t.Cleanup(func() { _ = s.Close() })

	var out, errBuf bytes.Buffer
	code := s.identityBuiltin([]string{"use", "work", "--persona", "mentor"}, nil, &out, &errBuf)
	if code != 0 {
		t.Fatalf("identity use exit = %d; stderr=%q", code, errBuf.String())
	}

	// Identity should be active.
	active, err := secrets.LoadActive(home)
	if err != nil || active.Name != "work" {
		t.Errorf("active identity = (%v,%v); want work", active.Name, err)
	}

	// Persona should be active.
	if got := persona.ReadActivePersona(home); got != "mentor" {
		t.Errorf("ReadActivePersona = %q; want mentor", got)
	}
	if s.Persona().Name != "mentor" {
		t.Errorf("s.Persona().Name = %q; want mentor", s.Persona().Name)
	}

	// Binding should be persisted.
	if got := persona.ReadBinding(home, "work"); got != "mentor" {
		t.Errorf("ReadBinding(work) = %q; want mentor", got)
	}

	if !strings.Contains(out.String(), "active = work") {
		t.Errorf("missing identity-active line:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "active = mentor") {
		t.Errorf("missing persona-active line:\n%s", out.String())
	}
}

// TestIdentityUse_PersonaFlagRequiresValue — `identity use NAME
// --persona` with no value is a user error.
func TestIdentityUse_PersonaFlagRequiresValue(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	if err := secrets.CreateProfile(home, secrets.Identity{Name: "work"}); err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}

	s := New()
	t.Cleanup(func() { _ = s.Close() })

	var out, errBuf bytes.Buffer
	code := s.identityBuiltin([]string{"use", "work", "--persona"}, nil, &out, &errBuf)
	if code == 0 {
		t.Fatalf("identity use with bare --persona should fail")
	}
	if !strings.Contains(errBuf.String(), "Usage") {
		t.Errorf("expected usage message; got %q", errBuf.String())
	}
}

// TestIdentityUse_BindingPersistsAcrossSwitches — once a binding is
// recorded, a plain `identity use NAME` (without --persona) re-
// activates the bound persona.
func TestIdentityUse_BindingPersistsAcrossSwitches(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	for _, n := range []string{"work", "personal"} {
		if err := secrets.CreateProfile(home, secrets.Identity{Name: n}); err != nil {
			t.Fatalf("CreateProfile %s: %v", n, err)
		}
	}

	s := New()
	t.Cleanup(func() { _ = s.Close() })

	// Bind work -> mentor.
	if code := s.identityBuiltin([]string{"use", "work", "--persona", "mentor"}, nil, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("identity use work --persona mentor: %d", code)
	}
	// Bind personal -> playful.
	if code := s.identityBuiltin([]string{"use", "personal", "--persona", "playful"}, nil, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("identity use personal --persona playful: %d", code)
	}
	if s.Persona().Name != "playful" {
		t.Errorf("after switching to personal: persona = %q; want playful", s.Persona().Name)
	}
	// Switch back to work without --persona: mentor should re-activate.
	var out, errBuf bytes.Buffer
	if code := s.identityBuiltin([]string{"use", "work"}, nil, &out, &errBuf); code != 0 {
		t.Fatalf("identity use work: %d, stderr=%q", code, errBuf.String())
	}
	if s.Persona().Name != "mentor" {
		t.Errorf("after switching back to work: persona = %q; want mentor (binding)", s.Persona().Name)
	}
}

// TestIdentityUse_UnknownPersonaWarnsButKeepsBinding — when the
// --persona argument names a persona that is not in the registry,
// the binding is still written (so adding the persona later
// activates it on next use) but the in-process activation is
// skipped with a stderr warning.
func TestIdentityUse_UnknownPersonaWarnsButKeepsBinding(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	if err := secrets.CreateProfile(home, secrets.Identity{Name: "work"}); err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}

	s := New()
	t.Cleanup(func() { _ = s.Close() })

	var out, errBuf bytes.Buffer
	code := s.identityBuiltin([]string{"use", "work", "--persona", "no-such"}, nil, &out, &errBuf)
	if code != 0 {
		t.Fatalf("identity use with unknown persona returned %d; want 0 (warn-only)", code)
	}
	if !strings.Contains(errBuf.String(), "no-such") {
		t.Errorf("expected stderr warning about unknown persona; got %q", errBuf.String())
	}
	if got := persona.ReadBinding(home, "work"); got != "no-such" {
		t.Errorf("binding should still be written; ReadBinding = %q", got)
	}
	// Confirm no spurious activation.
	if filepath.Base(s.Persona().Name) == "no-such" {
		t.Errorf("unknown persona should not have been activated; got %q", s.Persona().Name)
	}
}
