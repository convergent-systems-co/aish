package shell

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPersonaHistory_AttributesDestructiveCommandToActivePersona —
// the full smoke: activate a persona, run a destructive command, see
// the persona in `history list` output.
func TestPersonaHistory_AttributesDestructiveCommandToActivePersona(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	s := New()
	t.Cleanup(func() { _ = s.Close() })
	if s.history == nil {
		t.Skip("history engine unavailable in this environment")
	}

	// Activate a non-default persona.
	if code := s.personaBuiltin([]string{"set", "mentor"}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("persona set mentor: code=%d", code)
	}

	// Create a file then delete it through the dispatch path.
	victim := filepath.Join(home, "victim.txt")
	if err := os.WriteFile(victim, []byte("bye"), 0o644); err != nil {
		t.Fatalf("write victim: %v", err)
	}
	var dispatchOut, dispatchErr bytes.Buffer
	if err := s.dispatch("rm "+victim, strings.NewReader(""), &dispatchOut, &dispatchErr); err != nil {
		t.Fatalf("dispatch rm: %v", err)
	}

	// `history list` should show (persona=mentor) on the rm line.
	var out, errBuf bytes.Buffer
	if code := s.historyBuiltin([]string{"list"}, &out, &errBuf); code != 0 {
		t.Fatalf("history list code=%d, stderr=%q", code, errBuf.String())
	}
	got := out.String()
	if !strings.Contains(got, "(persona=mentor)") {
		t.Errorf("history list output missing persona attribution:\n%s", got)
	}
	if !strings.Contains(got, "victim.txt") {
		t.Errorf("history list output missing the rm command:\n%s", got)
	}
}

// TestPersonaHistory_DefaultPersonaIsRecordedExplicitly — when no
// persona is set, the active persona is "default" and the sidecar
// records it as such (not "?"). Tests the coercion rule in
// MetaStore.Record.
func TestPersonaHistory_DefaultPersonaIsRecordedExplicitly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	s := New()
	t.Cleanup(func() { _ = s.Close() })
	if s.history == nil {
		t.Skip("history engine unavailable")
	}
	// No `persona set` — leave activePersona == "".

	victim := filepath.Join(home, "v2.txt")
	if err := os.WriteFile(victim, []byte("x"), 0o644); err != nil {
		t.Fatalf("write victim: %v", err)
	}
	var dout, derr bytes.Buffer
	if err := s.dispatch("rm "+victim, strings.NewReader(""), &dout, &derr); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	var out, errBuf bytes.Buffer
	if code := s.historyBuiltin([]string{"list"}, &out, &errBuf); code != 0 {
		t.Fatalf("history list: code=%d", code)
	}
	if !strings.Contains(out.String(), "(persona=default)") {
		t.Errorf("expected (persona=default) for default-persona row; got:\n%s", out.String())
	}
}

// TestPersonaHistory_ShowRendersPersonaLine — `history show <id>`
// emits a "persona:" line for sidecar-attributed events.
func TestPersonaHistory_ShowRendersPersonaLine(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	s := New()
	t.Cleanup(func() { _ = s.Close() })
	if s.history == nil {
		t.Skip("history engine unavailable")
	}
	if code := s.personaBuiltin([]string{"set", "playful"}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("persona set playful: %d", code)
	}

	victim := filepath.Join(home, "v3.txt")
	if err := os.WriteFile(victim, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	var dout, derr bytes.Buffer
	if err := s.dispatch("rm "+victim, strings.NewReader(""), &dout, &derr); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	// Find the event ID from history.
	events, err := s.history.Store().List(1)
	if err != nil || len(events) == 0 {
		t.Fatalf("List: %v / len=%d", err, len(events))
	}
	id := events[0].ID

	var out, errBuf bytes.Buffer
	if code := s.historyBuiltin([]string{"show", id}, &out, &errBuf); code != 0 {
		t.Fatalf("history show: code=%d stderr=%q", code, errBuf.String())
	}
	if !strings.Contains(out.String(), "persona:   playful") {
		t.Errorf("history show output missing persona line:\n%s", out.String())
	}
}
