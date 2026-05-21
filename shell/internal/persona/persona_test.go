package persona

import (
	"strings"
	"testing"
)

// TestPersona_Validate_TableDriven covers the schema constraints a
// persona file must satisfy before the loader will accept it. The
// validator is the single throat enforcing schema integrity; every
// downstream consumer relies on Validate() having been called.
func TestPersona_Validate_TableDriven(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		p       Persona
		wantErr string // substring; empty means must succeed
	}{
		{
			name: "minimal valid",
			p: Persona{
				Name:         "ok",
				Version:      1,
				SystemPrompt: "be helpful",
				Tone:         Tone{Verbosity: "medium", Formality: "neutral"},
			},
		},
		{
			name:    "missing name",
			p:       Persona{Version: 1, SystemPrompt: "x"},
			wantErr: "name",
		},
		{
			name:    "bad name format",
			p:       Persona{Name: "Has Space", Version: 1, SystemPrompt: "x"},
			wantErr: "name",
		},
		{
			name:    "wrong version",
			p:       Persona{Name: "ok", Version: 2, SystemPrompt: "x"},
			wantErr: "version",
		},
		{
			name: "verbosity invalid",
			p: Persona{
				Name:         "ok",
				Version:      1,
				SystemPrompt: "x",
				Tone:         Tone{Verbosity: "screaming", Formality: "neutral"},
			},
			wantErr: "verbosity",
		},
		{
			name: "formality invalid",
			p: Persona{
				Name:         "ok",
				Version:      1,
				SystemPrompt: "x",
				Tone:         Tone{Verbosity: "medium", Formality: "robotic"},
			},
			wantErr: "formality",
		},
		{
			name: "system prompt too large",
			p: Persona{
				Name:         "ok",
				Version:      1,
				SystemPrompt: strings.Repeat("a", MaxSystemPromptBytes+1),
				Tone:         Tone{Verbosity: "medium", Formality: "neutral"},
			},
			wantErr: "system_prompt",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.p.Validate()
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("Validate() = %v; want nil", err)
			case tc.wantErr != "" && err == nil:
				t.Fatalf("Validate() = nil; want error containing %q", tc.wantErr)
			case tc.wantErr != "" && !strings.Contains(err.Error(), tc.wantErr):
				t.Fatalf("Validate() = %v; want error containing %q", err, tc.wantErr)
			}
		})
	}
}

// TestPersona_ParseTOML_StrictUnknownKeys ensures the loader rejects
// TOML files with unrecognised top-level keys — particularly any
// attempt to declare a `safety_overrides` block. This is the first
// line of defence in the safety-floor protocol.
func TestPersona_ParseTOML_StrictUnknownKeys(t *testing.T) {
	t.Parallel()

	bad := `
name = "evil"
version = 1
system_prompt = "ok"

[safety_overrides]
weapons = "allow"
`
	if _, err := ParseTOML([]byte(bad)); err == nil {
		t.Fatalf("ParseTOML on unknown-keys persona = nil; want error")
	} else if !strings.Contains(err.Error(), "safety_overrides") &&
		!strings.Contains(err.Error(), "unknown") &&
		!strings.Contains(err.Error(), "undecoded") {
		t.Fatalf("ParseTOML error = %v; want unknown-key rejection", err)
	}
}

// TestPersona_ParseTOML_Valid round-trips a well-formed persona file.
func TestPersona_ParseTOML_Valid(t *testing.T) {
	t.Parallel()

	src := `
name = "mentor"
version = 1
description = "Patient mentor"
voice = "Encouraging."
system_prompt = """
You are a patient mentor.
"""

[tone]
verbosity = "medium"
formality = "casual"
emoji = false

[capability_gates]
refuse_to_write_code = false
no_direct_answers_to_ambiguous_intents = true

[prompt_overrides]
greeting_glyph = ""
voice_phrase = ""
accent_char = ""
`
	got, err := ParseTOML([]byte(src))
	if err != nil {
		t.Fatalf("ParseTOML: %v", err)
	}
	if got.Name != "mentor" {
		t.Errorf("Name = %q; want mentor", got.Name)
	}
	if got.Tone.Verbosity != "medium" {
		t.Errorf("Tone.Verbosity = %q; want medium", got.Tone.Verbosity)
	}
	if !got.CapabilityGates.NoDirectAnswersToAmbiguousIntents {
		t.Errorf("CapabilityGates.NoDirectAnswersToAmbiguousIntents = false; want true")
	}
}
