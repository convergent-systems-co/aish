package shell

import (
	"testing"

	"github.com/convergent-systems-co/aish/shell/internal/parser"
	"github.com/convergent-systems-co/aish/shell/internal/secrets"
)

// TestTagPipelineTaint_DirectSecretGet — a pipeline that starts with
// `secret get NAME` is tainted by the heuristic alone (no registry
// match needed).
func TestTagPipelineTaint_DirectSecretGet(t *testing.T) {
	pl := &parser.Pipeline{
		Commands: []parser.Command{
			{Name: "secret", Args: []string{"get", "API_KEY"}},
			{Name: "cat"},
		},
	}
	tagPipelineTaint(pl, nil)
	if !pl.Tainted {
		t.Fatalf("expected Pipeline.Tainted = true for direct secret-get stage")
	}
	if !pl.Commands[0].Tainted {
		t.Fatalf("expected stage 0 (secret get) to be Tainted")
	}
	if pl.Commands[1].Tainted {
		t.Fatalf("did NOT expect downstream `cat` to be marked Tainted by per-command flag (sticky-bit propagates only to Pipeline level in MVP)")
	}
}

// TestTagPipelineTaint_RegistryMatch — a token whose literal exactly
// matches a registered tainted value flips the bit on that stage AND
// at the pipeline level.
func TestTagPipelineTaint_RegistryMatch(t *testing.T) {
	reg := secrets.NewTaintedRegistry()
	const sentinel = "[REDACTED:test-value-tag]"
	reg.Add(sentinel)

	pl := &parser.Pipeline{
		Commands: []parser.Command{
			{Name: "curl", Args: []string{"-H", "X-Auth: " + sentinel, "https://example.test"}},
		},
	}
	// curl's Args[1] is `X-Auth: [REDACTED:test-value-tag]` — that's
	// a substring, NOT an exact match. The MVP contract is
	// exact-match only; this should NOT be flagged.
	tagPipelineTaint(pl, reg)
	if pl.Tainted {
		t.Fatalf("MVP contract is exact-match; substring should not trigger taint. (If you've added substring matching, update the alternatives table in v0.3-fu-secrets.md.)")
	}

	// Direct-arg form: the sentinel IS the literal arg value.
	pl2 := &parser.Pipeline{
		Commands: []parser.Command{
			{Name: "echo", Args: []string{sentinel}},
		},
	}
	tagPipelineTaint(pl2, reg)
	if !pl2.Tainted {
		t.Fatalf("expected Pipeline.Tainted = true when arg exact-matches registered literal")
	}
	if !pl2.Commands[0].Tainted {
		t.Fatalf("expected stage to be marked Tainted")
	}
}

// TestTagPipelineTaint_NilRegistryStillCoversDirectHeuristic — the
// registry can be nil and the direct-secret-get heuristic still fires.
// Belt-and-suspenders for the "shell startup before per-line setup"
// edge case.
func TestTagPipelineTaint_NilRegistryStillCoversDirectHeuristic(t *testing.T) {
	pl := &parser.Pipeline{
		Commands: []parser.Command{
			{Name: "secret", Args: []string{"get", "K"}},
		},
	}
	tagPipelineTaint(pl, nil)
	if !pl.Tainted {
		t.Fatalf("expected direct secret-get to flag taint with nil registry")
	}
}

// TestTagPipelineTaint_NoMatchLeavesUntouched — a pipeline with no
// tainted contributors is unchanged (the bit stays false and pre-walk
// equality holds).
func TestTagPipelineTaint_NoMatchLeavesUntouched(t *testing.T) {
	reg := secrets.NewTaintedRegistry()
	reg.Add("[REDACTED:not-this-value]")
	pl := &parser.Pipeline{
		Commands: []parser.Command{
			{Name: "ls", Args: []string{"-la"}},
		},
	}
	tagPipelineTaint(pl, reg)
	if pl.Tainted {
		t.Fatalf("non-matching pipeline got Tainted=true")
	}
	if pl.Commands[0].Tainted {
		t.Fatalf("non-matching command got Tainted=true")
	}
}

// TestIsSecretGetInvocation_Detection covers the RunForCapture-side
// heuristic that decides whether to add captured stdout to the
// registry.
func TestIsSecretGetInvocation_Detection(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"secret get NAME", true},
		{"  secret  get  NAME  ", true},
		{"secret\tget\tNAME", true},
		{"secret list", false},
		{"secret", false},
		{"secret-other get NAME", false},
		{"echo secret get NAME", false},
		{"", false},
	}
	for _, tc := range cases {
		got := isSecretGetInvocation(tc.in)
		if got != tc.want {
			t.Errorf("isSecretGetInvocation(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
