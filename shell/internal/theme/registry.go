package theme

import (
	"fmt"
	"sort"
	"sync"
)

// Registry is the theme lookup table. Operations are concurrency-safe so
// `aish theme set` can swap the active theme while another goroutine
// reads. Switch cost is a single atomic-style pointer write under lock.
type Registry struct {
	mu     sync.RWMutex
	themes map[string]*Theme
	active *Theme
}

// NewRegistry returns a Registry pre-populated with the bundled themes.
// The default theme is set as the initial active.
func NewRegistry() *Registry {
	r := &Registry{themes: make(map[string]*Theme)}
	for _, b := range BundledBrands() {
		t, err := Compile(b)
		if err != nil {
			continue
		}
		r.themes[t.Name()] = t
	}
	r.active = r.themes[DefaultThemeName]
	return r
}

// DefaultThemeName names the theme the registry falls back to when no
// other selection is requested.
const DefaultThemeName = "default"

// Lookup returns the theme with the given name, or (nil, false).
func (r *Registry) Lookup(name string) (*Theme, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.themes[name]
	return t, ok
}

// List returns the names of registered themes, sorted for stable output.
// The active theme's name is included in the list (no special marker —
// callers cross-reference Active() to mark it).
func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.themes))
	for n := range r.themes {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Active returns the currently-active theme. Never nil for a registry
// constructed by NewRegistry — at minimum the bundled "default" is
// active.
func (r *Registry) Active() *Theme {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.active
}

// SetActive activates the named theme atomically. Returns an error if no
// theme with that name is registered; the previously-active theme is
// unchanged on error.
func (r *Registry) SetActive(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.themes[name]
	if !ok {
		return fmt.Errorf("theme: unknown theme %q (use `aish theme list` for available themes)", name)
	}
	r.active = t
	return nil
}

// Add registers a theme. If a theme with the same name already exists
// it is replaced. Returns the theme for chaining.
//
// Intended for future filesystem-loaded themes (~/.aish/themes/*.toml)
// and HTTP-fetched themes from Brand Atoms; bundled themes are added at
// registry construction.
func (r *Registry) Add(t *Theme) *Theme {
	if t == nil || t.Name() == "" {
		return t
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.themes[t.Name()] = t
	return t
}
