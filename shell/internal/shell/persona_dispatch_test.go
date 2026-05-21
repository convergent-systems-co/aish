package shell

import (
	"strings"
	"testing"
)

// TestComposeIntent_DefaultPersona — when no persona has been set, the
// composer still wraps with the safety floor. The shell never sends
// raw user intent to the LLM gateway without the floor.
func TestComposeIntent_DefaultPersona(t *testing.T) {
	s, _ := newTestShellForPersona(t)
	got := s.composeIntent("list files")
	if !strings.Contains(got, "<persona-system>") {
		t.Fatalf("composeIntent: missing <persona-system> wrapper:\n%s", got)
	}
	if !strings.Contains(got, "CSAM") {
		t.Fatalf("composeIntent: safety floor not present (CSAM marker missing)")
	}
	if !strings.Contains(got, "list files") {
		t.Fatalf("composeIntent: user intent stripped")
	}
}

// TestComposeIntent_ActivePersona — when a persona is set, its
// system_prompt appears in the wrapped block.
func TestComposeIntent_ActivePersona(t *testing.T) {
	s, _ := newTestShellForPersona(t)
	// Use socratic — it has a recognisable phrase ("clarifying
	// question") that's safe to grep for.
	if code := s.personaBuiltin([]string{"set", "socratic"}, &noopWriter{}, &noopWriter{}); code != 0 {
		t.Fatalf("seed set socratic: exit=%d", code)
	}
	got := s.composeIntent("delete log files")
	if !strings.Contains(got, "clarifying question") && !strings.Contains(got, "Socratic") {
		t.Errorf("composeIntent: socratic prompt not injected:\n%s", got)
	}
	if !strings.Contains(got, "delete log files") {
		t.Errorf("composeIntent: user intent stripped")
	}
}

// TestComposeIntent_NilLoader — defensive: if the persona loader
// failed to initialise, the composer still injects the safety floor.
func TestComposeIntent_NilLoader(t *testing.T) {
	s, _ := newTestShellForPersona(t)
	s.personas = nil
	got := s.composeIntent("rm tmp")
	if !strings.Contains(got, "CSAM") {
		t.Errorf("composeIntent with nil loader: safety floor missing:\n%s", got)
	}
}

// noopWriter discards all writes. Used in tests that don't care
// about output.
type noopWriter struct{}

func (noopWriter) Write(p []byte) (int, error) { return len(p), nil }
