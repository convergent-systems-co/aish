// Package persona implements the v0.3-5 persona engine: the shell's
// embedded personality.
//
// A persona is a typed character: voice description, system-prompt
// template injected into inference requests, tone parameters, and
// optional capability gates layered above the non-overridable safety
// floor. Personas ship as TOML files under data/personas/ (bundled
// into the binary) and may be overridden or extended by user files
// under ~/.aish/personas/. The currently-active persona is recorded
// in ~/.aish/config.toml's [persona] section.
//
// Distinct from v0.3-3 identity: identity answers *who you are*;
// persona answers *who the shell is being for you*. The two are
// orthogonal and may be paired in any combination.
//
// The safety floor (safety.go) is hard-coded and cannot be lowered
// by any persona configuration. See GOALS.md §"Epic v0.3-5" — the
// safety floor is a non-negotiable invariant of the engine.
package persona

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
)

// SchemaVersion is the only version this engine accepts. Bumping the
// version is a deliberate forward-only step that requires migrating
// every bundled persona at the same time.
const SchemaVersion = 1

// MaxSystemPromptBytes caps the persona-supplied system_prompt to keep
// prompt-injection attacks bounded. The safety floor adds a few hundred
// bytes on top; total composed prompt stays well under any sane LLM
// gateway limit.
const MaxSystemPromptBytes = 8 * 1024

// nameRe matches persona names: lowercase letters, digits, and dashes.
// Filenames mirror names: name "mentor" lives at "mentor.toml".
var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// Persona is the on-disk and in-memory schema for an aish persona.
//
// TOML field tags drive both decoding (BurntSushi/toml) and the
// strict-decode check (any field not in this struct's tag set causes
// MetaData.Undecoded() to return a non-empty slice, which ParseTOML
// rejects).
type Persona struct {
	// Name uniquely identifies the persona. Matches the file basename
	// (without .toml). Lowercase, digits, dashes only.
	Name string `toml:"name"`

	// Version is the schema version. Always SchemaVersion in v0.3-5.
	Version int `toml:"version"`

	// Description is the one-line human summary. Shown by `persona list`.
	Description string `toml:"description"`

	// Voice is the prose description of how the persona speaks. Shown
	// by `persona show <name>`. Not injected into the LLM prompt —
	// that's SystemPrompt's job; Voice is documentation for humans.
	Voice string `toml:"voice"`

	// SystemPrompt is the persona-shaping prompt injected into the
	// LLM gateway. The safety floor is automatically prepended at
	// compose time; SystemPrompt is the persona's contribution
	// AFTER the floor.
	SystemPrompt string `toml:"system_prompt"`

	// Tone parameters. Currently advisory — surface to humans via
	// `persona show`; the LLM observes them via SystemPrompt
	// composition.
	Tone Tone `toml:"tone"`

	// CapabilityGates are opt-in restrictions that LAYER ABOVE the
	// safety floor. They can only ADD refusals, never remove them.
	CapabilityGates CapabilityGates `toml:"capability_gates"`

	// PromptOverrides drives the v0.2-5 prompt-segment system. Wired
	// in v0.3-5.1 (#124 deferred).
	PromptOverrides PromptOverrides `toml:"prompt_overrides"`
}

// Tone is the typed tone block. Verbosity and Formality are enums;
// Emoji is a boolean toggle.
type Tone struct {
	Verbosity string `toml:"verbosity"` // "terse" | "medium" | "verbose"
	Formality string `toml:"formality"` // "casual" | "neutral" | "formal"
	Emoji     bool   `toml:"emoji"`
}

// CapabilityGates is the typed capability-gate block. Every gate is an
// opt-in additional restriction.
type CapabilityGates struct {
	// RefuseWhenNoFilesProvided makes the persona ask for files before
	// answering questions that obviously need them.
	RefuseWhenNoFilesProvided bool `toml:"refuse_when_no_files_provided"`

	// RefuseToWriteCode makes the persona explain rather than draft.
	// Useful for the `socratic` and `mentor` personas.
	RefuseToWriteCode bool `toml:"refuse_to_write_code"`

	// NoDirectAnswersToAmbiguousIntents makes the persona ask one
	// clarifying question when the user's intent is under-specified.
	NoDirectAnswersToAmbiguousIntents bool `toml:"no_direct_answers_to_ambiguous_intents"`
}

// PromptOverrides drives the v0.2-5 prompt segments. Deferred to
// v0.3-5.1 (#124).
type PromptOverrides struct {
	GreetingGlyph string `toml:"greeting_glyph"`
	VoicePhrase   string `toml:"voice_phrase"`
	AccentChar    string `toml:"accent_char"`
}

// validVerbosity and validFormality define the enum domains.
var (
	validVerbosity = map[string]bool{"terse": true, "medium": true, "verbose": true}
	validFormality = map[string]bool{"casual": true, "neutral": true, "formal": true}
)

// Validate returns an error describing the first schema violation, or
// nil when the persona is well-formed. Run after ParseTOML (which
// catches structural issues) and again before any persona is added to
// the loader's registry.
func (p Persona) Validate() error {
	if p.Name == "" {
		return errors.New("persona: name is required")
	}
	if !nameRe.MatchString(p.Name) {
		return fmt.Errorf("persona: name %q must match [a-z0-9][a-z0-9-]{0,63}", p.Name)
	}
	if p.Version != SchemaVersion {
		return fmt.Errorf("persona: version = %d; want %d", p.Version, SchemaVersion)
	}
	if strings.TrimSpace(p.SystemPrompt) == "" {
		return errors.New("persona: system_prompt is required")
	}
	if len(p.SystemPrompt) > MaxSystemPromptBytes {
		return fmt.Errorf("persona: system_prompt size %d exceeds cap %d bytes", len(p.SystemPrompt), MaxSystemPromptBytes)
	}
	// Tone enums — default-zero is empty string, which we reject so a
	// silently-malformed file (`[tone]` block missing entirely) doesn't
	// pass under "zero values are fine."
	if !validVerbosity[p.Tone.Verbosity] {
		return fmt.Errorf("persona: tone.verbosity = %q; want one of terse|medium|verbose", p.Tone.Verbosity)
	}
	if !validFormality[p.Tone.Formality] {
		return fmt.Errorf("persona: tone.formality = %q; want one of casual|neutral|formal", p.Tone.Formality)
	}

	// Safety-bypass denylist. See safety.go for the full rationale.
	if err := checkSafetyBypassAttempt(p.SystemPrompt); err != nil {
		return err
	}
	return nil
}

// ParseTOML decodes a persona from raw TOML bytes. Strict mode: any
// undecoded keys (i.e. keys not present in the Persona struct) cause
// rejection. This is the front-line defence against a persona TOML
// that tries to smuggle in a `safety_overrides` block or any other
// rogue top-level configuration.
func ParseTOML(data []byte) (Persona, error) {
	var p Persona
	meta, err := toml.Decode(string(data), &p)
	if err != nil {
		return Persona{}, fmt.Errorf("persona: toml decode: %w", err)
	}
	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, 0, len(undecoded))
		for _, k := range undecoded {
			keys = append(keys, k.String())
		}
		return Persona{}, fmt.Errorf("persona: unknown keys: %s", strings.Join(keys, ", "))
	}
	return p, nil
}

// EncodeTOML returns a canonical TOML representation of the persona.
// Used for `persona show <name>` rendering and (future) export
// workflows. Not on the hot path — uses the BurntSushi encoder.
func EncodeTOML(p Persona) ([]byte, error) {
	var buf strings.Builder
	enc := toml.NewEncoder(&buf)
	if err := enc.Encode(p); err != nil {
		return nil, fmt.Errorf("persona: toml encode: %w", err)
	}
	return []byte(buf.String()), nil
}
