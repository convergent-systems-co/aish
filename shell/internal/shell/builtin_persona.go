package shell

import (
	"fmt"
	"io"
	"os"
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
//
// Side effect: when both the persona registry and the history engine
// are wired, this also opens the persona-events sidecar (#125) and
// registers a persona-aware Interceptor that records the active
// persona against each new history event. The interceptor is appended
// LAST so it sees the event ID after history.History.Before has
// already appended it.
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

	// v0.3-5.1 (#125): wire the persona-events sidecar + interceptor
	// when both registry and history are open. A re-entry of openPersona
	// (e.g. after `persona create`) is harmless: the interceptor is
	// idempotent on already-attributed event IDs, and re-opening the
	// MetaStore re-reads the same on-disk file.
	if home != "" && s.history != nil && s.personaMeta == nil {
		dotAish := filepath.Join(home, persona.ConfigDirName)
		if meta, mErr := persona.OpenMetaStore(dotAish); mErr == nil {
			s.personaMeta = meta
			s.interceptors = append(s.interceptors, &personaHistoryInterceptor{
				meta:    meta,
				store:   s.history.Store(),
				persona: func() string { return s.Persona().Name },
			})
		}
	}
}

// personaBuiltin implements the `aish persona` built-in per v0.3-5
// tasks #115–#129. Returns the exit code the dispatch loop should
// record.
//
// Subcommands:
//
//	persona list             — every persona in the registry, name + description.
//	persona show <name>      — full schema of one persona.
//	persona set <name>       — activate <name>; persist to ~/.aish/config.toml.
//	persona use <name>       — alias for `set` (matches GOALS.md verb).
//	persona active           — print the currently-active persona name.
//	persona create <name>    — interactive bootstrap (v0.3-5.1 #121).
//	persona install <dir>    — verify + install a signed bundle (v0.3-5.1 #127).
//	persona bundles          — list installed bundles (v0.3-5.1 #127).
//
// Bare `persona` prints a usage hint and exits 2.
func (s *Shell) personaBuiltin(args []string, stdout, stderr io.Writer) int {
	return s.personaBuiltinIO(args, nil, stdout, stderr)
}

// personaBuiltinIO is the stdin-aware variant. Production dispatch
// passes the REPL's stdin so `persona create` can read prompt
// responses; the existing personaBuiltin shim preserves the
// pre-v0.3-5.1 callsites that never needed stdin.
func (s *Shell) personaBuiltinIO(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "Usage: persona list | show <name> | set <name> | use <name> | active | create <name> | install <dir> | bundles")
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
	case "create":
		return s.personaCreate(rest, stdin, stdout, stderr)
	case "install":
		return s.personaInstall(rest, stdout, stderr)
	case "bundles":
		return s.personaBundles(stdout, stderr)
	default:
		fmt.Fprintf(stderr, "persona: unknown subcommand %q (try `persona list`)\n", sub)
		return 2
	}
}

// personaInstall verifies + installs a signed persona bundle
// directory into ~/.aish/persona-bundles/<bundle_id>/. The personas
// inside the bundle are also copied to ~/.aish/personas/ where the
// loader picks them up on the next session (or after the in-process
// reopen).
func (s *Shell) personaInstall(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "Usage: persona install <dir>")
		return 2
	}
	home := homeDir(s.env)
	if home == "" {
		fmt.Fprintln(stderr, "persona: $HOME not set; cannot install bundle")
		return 1
	}
	dotAish := filepath.Join(home, persona.ConfigDirName)
	manifest, err := persona.InstallBundle(args[0], dotAish, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "persona install: %v\n", err)
		return 1
	}
	// Reopen the loader so new personas appear immediately.
	s.openPersona(s.env)
	fmt.Fprintf(stdout, "persona install: installed bundle %s v%d (%d personas, signer=%s)\n",
		manifest.BundleID, manifest.BundleVersion, manifest.PersonaCount, manifest.SignerID)
	return 0
}

// personaBundles lists installed bundles under
// ~/.aish/persona-bundles/ with their manifest summary.
func (s *Shell) personaBundles(stdout, stderr io.Writer) int {
	home := homeDir(s.env)
	if home == "" {
		fmt.Fprintln(stderr, "persona: $HOME not set")
		return 1
	}
	dotAish := filepath.Join(home, persona.ConfigDirName)
	bundles, err := persona.ListBundles(dotAish)
	if err != nil {
		fmt.Fprintf(stderr, "persona bundles: %v\n", err)
		return 1
	}
	if len(bundles) == 0 {
		fmt.Fprintln(stdout, "(no persona bundles installed)")
		return 0
	}
	for _, b := range bundles {
		fmt.Fprintf(stdout, "%-24s  v%-3d  %-20s  %d personas\n",
			b.ID, b.BundleVersion, b.SignerID, b.PersonaCount)
	}
	return 0
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
	p, ok := s.personas.Get(name)
	if !ok {
		fmt.Fprintf(stderr, "persona: unknown persona %q (try `persona list`)\n", name)
		return 1
	}
	// v0.3-3 (#104): personas with declared external bindings route
	// through personaSetAtomic so SSH / cloud / kube / git mutate
	// atomically. Personas with no [external] block fall through to
	// the legacy single-write path below — bit-identical to pre-#104
	// behavior (asserted by TestAtomicPersonaSwitch_NoBindingsPreservesLegacyBehaviour).
	if hasAnyExternalBinding(p) {
		return s.personaSetAtomic(name, stdout, stderr)
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
	s.recordPersonaUse(name)
	return 0
}

// hasAnyExternalBinding reports whether the persona declares one or
// more [external.*] sub-blocks. Sole bridge between the legacy
// single-write path and the atomic-switch path.
func hasAnyExternalBinding(p persona.Persona) bool {
	b := p.ExternalBindings
	return b.SSH != nil || b.Cloud != nil || b.Kube != nil || b.Git != nil
}

// personaCreate runs the guided-bootstrap flow for #121. It reads
// description, voice, tone (verbosity + formality + emoji), and
// system_prompt from stdin and writes a TOML file under
// ~/.aish/personas/<name>.toml. The persona registry is re-opened so
// the new file is visible to `persona list` in the same session.
//
// When stdin is nil (test path), the call still creates the persona
// using the supplied name + minimal defaults so unit tests can drive
// it without piping. Production dispatch always provides stdin (the
// REPL's caller stream).
//
// Validation runs BEFORE the file is written — a persona whose name
// breaks the regex, whose system_prompt is empty, or whose system_prompt
// trips the safety-bypass denylist surfaces a non-zero exit and no
// disk artefact.
func (s *Shell) personaCreate(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "Usage: persona create <name>")
		return 2
	}
	name := args[0]
	home := homeDir(s.env)
	if home == "" {
		fmt.Fprintln(stderr, "persona: $HOME not set; cannot create persona")
		return 1
	}
	if _, exists := s.personas.Get(name); exists {
		fmt.Fprintf(stderr, "persona: %q already exists (use a different name)\n", name)
		return 1
	}
	p := persona.Persona{
		Name:    name,
		Version: persona.SchemaVersion,
		Tone: persona.Tone{
			Verbosity: "medium",
			Formality: "neutral",
		},
		// Minimal default so the file is valid even when the user
		// presses Enter through every prompt. The bootstrap flow
		// overwrites this when stdin gives a non-empty response.
		SystemPrompt: "You are a helpful assistant inside the aish shell.",
	}

	if stdin != nil {
		// Step through prompts. Each is optional (blank Enter keeps
		// the default).
		fmt.Fprintln(stderr, "description (one-line summary, blank to skip):")
		if line, err := readSingleLine(stdin); err == nil {
			p.Description = strings.TrimSpace(line)
		}
		fmt.Fprintln(stderr, "voice (how this persona speaks, blank to skip):")
		if line, err := readSingleLine(stdin); err == nil {
			p.Voice = strings.TrimSpace(line)
		}
		fmt.Fprintln(stderr, "verbosity [terse|medium|verbose] (default medium):")
		if line, err := readSingleLine(stdin); err == nil {
			v := strings.ToLower(strings.TrimSpace(line))
			if v != "" {
				p.Tone.Verbosity = v
			}
		}
		fmt.Fprintln(stderr, "formality [casual|neutral|formal] (default neutral):")
		if line, err := readSingleLine(stdin); err == nil {
			f := strings.ToLower(strings.TrimSpace(line))
			if f != "" {
				p.Tone.Formality = f
			}
		}
		fmt.Fprintln(stderr, "system_prompt (one line; blank keeps the default):")
		if line, err := readSingleLine(stdin); err == nil {
			if sp := strings.TrimSpace(line); sp != "" {
				p.SystemPrompt = sp
			}
		}
	}

	// Validate before touching disk so a malformed persona never lands.
	if err := p.Validate(); err != nil {
		fmt.Fprintf(stderr, "persona: %v\n", err)
		return 1
	}

	dir := filepath.Join(home, persona.ConfigDirName, persona.PersonaDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		fmt.Fprintf(stderr, "persona: create dir: %v\n", err)
		return 1
	}
	path := filepath.Join(dir, name+".toml")
	if _, err := os.Stat(path); err == nil {
		fmt.Fprintf(stderr, "persona: file %s already exists\n", path)
		return 1
	}
	body, err := persona.EncodeTOML(p)
	if err != nil {
		fmt.Fprintf(stderr, "persona: encode: %v\n", err)
		return 1
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		fmt.Fprintf(stderr, "persona: write: %v\n", err)
		return 1
	}

	// Reopen the loader so the new file is visible without restart.
	s.openPersona(s.env)
	fmt.Fprintf(stdout, "persona: created %s at %s\n", name, path)
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
