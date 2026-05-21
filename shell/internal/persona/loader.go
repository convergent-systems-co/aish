package persona

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// PersonaDirName is the subdirectory under $HOME/.aish where user
// persona overrides live.
const PersonaDirName = "personas"

// Loader is the in-memory registry of all personas available to the
// running shell: bundled personas plus any user-supplied files under
// ~/.aish/personas/. User files override bundled personas with the
// same name.
//
// Loader is read-only after construction; the shell builds a single
// Loader at startup and discards it on Close. Re-loading after a
// `persona create` (#121 deferred) is a future concern.
type Loader struct {
	byName map[string]Persona
}

// NewLoader constructs a Loader from the bundled set and any
// user-supplied .toml files under userPersonaDir. userPersonaDir may
// be the empty string, in which case only bundled personas are loaded
// — used by tests and by sessions where $HOME is unset.
//
// Failure modes:
//   - A bundled persona fails to parse/validate (build defect, fatal).
//   - Two user files declare the same persona name (configuration
//     error; loader rejects with a clear message).
//   - A user file fails to parse/validate (skipped, NOT fatal — the
//     shell should not be unusable because the user typo'd one TOML).
func NewLoader(userPersonaDir string) (*Loader, error) {
	all, err := LoadBundled()
	if err != nil {
		return nil, err
	}
	byName := make(map[string]Persona, len(all)+4)
	for _, p := range all {
		byName[p.Name] = p
	}

	if userPersonaDir == "" {
		return &Loader{byName: byName}, nil
	}

	entries, err := os.ReadDir(userPersonaDir)
	if err != nil {
		if os.IsNotExist(err) {
			return &Loader{byName: byName}, nil
		}
		return nil, fmt.Errorf("persona: read user dir %s: %w", userPersonaDir, err)
	}
	// Track names seen in the USER directory so we can reject internal
	// duplicates (two files both declaring `name = "twin"`). Bundled
	// names are explicitly allowed to be overridden by a user file.
	userSeen := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".toml") {
			continue
		}
		path := filepath.Join(userPersonaDir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			// Best-effort: skip unreadable files. A malformed perm or
			// race-deleted file should not break the shell.
			continue
		}
		p, err := ParseTOML(data)
		if err != nil {
			// Skip malformed user personas. Future: surface a warning
			// via stderr in the shell startup path. For now, silent
			// skip is the same posture as the theme cache.
			continue
		}
		if err := p.Validate(); err != nil {
			// Skip invalid personas. The safety-bypass denylist is
			// part of Validate; a malicious user file therefore never
			// enters the registry.
			continue
		}
		if userSeen[p.Name] {
			return nil, fmt.Errorf("persona: duplicate user persona name %q (two files declare it)", p.Name)
		}
		userSeen[p.Name] = true
		// User wins over bundled.
		byName[p.Name] = p
	}
	return &Loader{byName: byName}, nil
}

// Get returns the persona by name. ok is false if no such persona is
// known.
func (l *Loader) Get(name string) (Persona, bool) {
	if l == nil {
		return Persona{}, false
	}
	p, ok := l.byName[name]
	return p, ok
}

// List returns every persona in the registry, sorted by name. Used by
// the `persona list` built-in.
func (l *Loader) List() []Persona {
	if l == nil {
		return nil
	}
	out := make([]Persona, 0, len(l.byName))
	for _, p := range l.byName {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Names returns just the sorted names; convenience for the dispatch
// tier-resolver and for shell startup logging.
func (l *Loader) Names() []string {
	if l == nil {
		return nil
	}
	out := make([]string, 0, len(l.byName))
	for n := range l.byName {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// DefaultPersona returns the "default" bundled persona. The shell
// falls back to this when no active persona is configured. Guaranteed
// to exist (the bundled set is verified at LoadBundled time); a
// missing default indicates a build defect.
func (l *Loader) DefaultPersona() Persona {
	if l == nil {
		return Persona{}
	}
	if p, ok := l.byName["default"]; ok {
		return p
	}
	// Defensive fallback — should never happen because LoadBundled
	// validates the curated set, but a nil persona here would cause
	// the composer to emit just the safety floor, which is still
	// well-defined behaviour.
	return Persona{}
}
