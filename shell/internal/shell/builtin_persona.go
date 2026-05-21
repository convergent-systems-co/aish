package shell

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/convergent-systems-co/aish/shell/internal/env"
	"github.com/convergent-systems-co/aish/shell/internal/persona"
)

// openPersona constructs the persona.Loader from the bundled set plus
// any user TOML files under ~/.aish/personas/, then restores the
// persisted active selection from ~/.aish/config.toml.
//
// Best-effort: on any failure s.personas stays nil and the persona
// built-in will print "registry not available". Inference dispatch
// falls through to no-persona behaviour. This matches the
// graceful-degradation posture of openCache / openHistory / openTelemetry.
func (s *Shell) openPersona(e *env.Env) {
	home := homeDir(e)
	userDir := ""
	if home != "" {
		userDir = filepath.Join(home, persona.ConfigDirName, persona.PersonaDirName)
	}
	loader, err := persona.NewLoader(userDir)
	if err != nil {
		// Loader can fail when a user file declares a duplicate name.
		// Surface to stderr at shell startup time so the user knows;
		// fall back to the bundled-only set is not possible because
		// the loader's error is "two of your files conflict" — better
		// to leave the registry empty than silently pick one.
		return
	}
	s.personas = loader
	if home != "" {
		if name := persona.ReadActivePersona(home); name != "" {
			if _, ok := loader.Get(name); ok {
				s.activePersona = name
			}
			// Unknown name silently falls through to default — same
			// posture as theme.SetActive.
		}
	}
}

// personaBuiltin implements the `aish persona` built-in per v0.3-5
// tasks #115–#129. Returns the exit code the dispatch loop should
// record.
//
// Subcommands:
//
//	persona list           — every persona in the registry, name + description.
//	persona show <name>    — full schema of one persona.
//	persona set <name>     — activate <name>; persist to ~/.aish/config.toml.
//	persona use <name>     — alias for `set` (matches GOALS.md verb).
//	persona active         — print the currently-active persona name.
//
// Bare `persona` prints a usage hint and exits 2.
//
// Deferred to v0.3-5.1:
//   - `persona create <name>` — guided bootstrap (#121).
func (s *Shell) personaBuiltin(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "Usage: persona list | show <name> | set <name> | use <name> | active")
		return 2
	}
	if s.personas == nil {
		fmt.Fprintln(stderr, "persona: registry not available (check ~/.aish/personas/ permissions)")
		return 1
	}
	sub := strings.ToLower(args[0])
	rest := args[1:]
	switch sub {
	case "list":
		return s.personaList(stdout)
	case "show":
		return s.personaShow(rest, stdout, stderr)
	case "set", "use":
		return s.personaSet(rest, stdout, stderr)
	case "active":
		return s.personaActive(stdout)
	default:
		fmt.Fprintf(stderr, "persona: unknown subcommand %q (try `persona list`)\n", sub)
		return 2
	}
}

// personaList renders every persona name + one-line description in
// the registry, marking the active one with `*`.
func (s *Shell) personaList(stdout io.Writer) int {
	all := s.personas.List()
	activeName := ""
	if s.activePersona != "" {
		activeName = s.activePersona
	} else {
		activeName = "default"
	}
	for _, p := range all {
		marker := "  "
		if p.Name == activeName {
			marker = "* "
		}
		fmt.Fprintf(stdout, "%s%-16s %s\n", marker, p.Name, p.Description)
	}
	return 0
}

// personaShow prints the full schema of one persona for `persona show <name>`.
func (s *Shell) personaShow(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "persona show: missing <name>")
		return 2
	}
	name := args[0]
	p, ok := s.personas.Get(name)
	if !ok {
		fmt.Fprintf(stderr, "persona: unknown persona %q (try `persona list`)\n", name)
		return 1
	}
	fmt.Fprintf(stdout, "name:          %s\n", p.Name)
	fmt.Fprintf(stdout, "version:       %d\n", p.Version)
	fmt.Fprintf(stdout, "description:   %s\n", p.Description)
	fmt.Fprintf(stdout, "voice:         %s\n", p.Voice)
	fmt.Fprintf(stdout, "tone:          verbosity=%s formality=%s emoji=%t\n",
		p.Tone.Verbosity, p.Tone.Formality, p.Tone.Emoji)
	fmt.Fprintf(stdout, "capability gates:\n")
	fmt.Fprintf(stdout, "  refuse_when_no_files_provided:        %t\n", p.CapabilityGates.RefuseWhenNoFilesProvided)
	fmt.Fprintf(stdout, "  refuse_to_write_code:                 %t\n", p.CapabilityGates.RefuseToWriteCode)
	fmt.Fprintf(stdout, "  no_direct_answers_to_ambiguous_intents: %t\n", p.CapabilityGates.NoDirectAnswersToAmbiguousIntents)
	// v0.3-5.1 (#124): surface prompt overrides so the user sees what the
	// theme's "persona" segment would render. The shell renders
	// greeting_glyph today; voice_phrase / accent_char are display-only
	// until the proto extension lands.
	fmt.Fprintf(stdout, "prompt overrides:\n")
	fmt.Fprintf(stdout, "  greeting_glyph: %q\n", p.PromptOverrides.GreetingGlyph)
	fmt.Fprintf(stdout, "  voice_phrase:   %q\n", p.PromptOverrides.VoicePhrase)
	fmt.Fprintf(stdout, "  accent_char:    %q\n", p.PromptOverrides.AccentChar)
	fmt.Fprintln(stdout, "system_prompt:")
	for _, line := range strings.Split(strings.TrimRight(p.SystemPrompt, "\n"), "\n") {
		fmt.Fprintf(stdout, "  %s\n", line)
	}
	return 0
}

// personaSet activates <name> and persists the choice to
// ~/.aish/config.toml's [persona] section.
//
// On success, an in-memory activePersona name is updated; subsequent
// inference dispatches inject the new persona's system prompt.
//
// Per the v0.3-5 MVP cut, this writes a plain (unsigned) history
// event semantically — wire-up to the signed-history pipeline lands
// in v0.3-5.1 alongside #118's signing follow-up.
func (s *Shell) personaSet(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "persona set: missing <name>")
		return 2
	}
	name := args[0]
	if _, ok := s.personas.Get(name); !ok {
		fmt.Fprintf(stderr, "persona: unknown persona %q (try `persona list`)\n", name)
		return 1
	}
	home := homeDir(s.env)
	if home == "" {
		fmt.Fprintln(stderr, "persona: $HOME not set; cannot persist active persona")
		return 1
	}
	if err := persona.WriteActivePersona(home, name); err != nil {
		fmt.Fprintf(stderr, "persona: %v\n", err)
		return 1
	}
	s.activePersona = name
	fmt.Fprintf(stdout, "persona: active = %s\n", name)
	return 0
}

// personaActive prints the currently-active persona name. When no
// persona has been set, prints "default".
func (s *Shell) personaActive(stdout io.Writer) int {
	name := s.activePersona
	if name == "" {
		name = "default"
	}
	fmt.Fprintln(stdout, name)
	return 0
}

// Persona returns the active persona (or the default if none is set).
// Exposed so the dispatch path can compose the persona's system
// prompt into Infer requests.
func (s *Shell) Persona() persona.Persona {
	if s.personas == nil {
		return persona.Persona{}
	}
	if s.activePersona != "" {
		if p, ok := s.personas.Get(s.activePersona); ok {
			return p
		}
	}
	return s.personas.DefaultPersona()
}

// personaSystemPromptForInfer returns the wrapped system-prompt block
// the cache layer prepends to Infer calls. Always includes the safety
// floor; appends the persona's voice/tone instructions when a persona
// is active. Empty string when the persona loader is unavailable.
//
// The format is `<persona-system>\n{floor + persona-prompt}\n
// </persona-system>` — the cache appends `\n<user>...\n</user>` so
// the LLM gateway sees one composite intent. This is the v0.3-5
// workaround for the deferred proto.InferParams extension.
func (s *Shell) personaSystemPromptForInfer() string {
	if s.personas == nil {
		return ""
	}
	p := s.Persona()
	// Build only the system block; the cache wraps with <user>...</user>.
	return "<persona-system>\n" + persona.SystemPromptFor(p) + "</persona-system>"
}

// composeIntent (test helper) returns the full composed intent
// including the <user>...</user> block. Kept on the Shell so the
// persona-dispatch tests can exercise the composition outside the
// cache code path.
func (s *Shell) composeIntent(rawIntent string) string {
	if s.personas == nil {
		return persona.ComposeIntentWith(persona.Persona{}, rawIntent)
	}
	return persona.ComposeIntentWith(s.Persona(), rawIntent)
}
