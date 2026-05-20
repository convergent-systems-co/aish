package history

import (
	"path/filepath"
	"strings"
)

// DefaultIgnorePatterns is the v0.1 baseline set the Snapshotter
// applies before copying bytes to ~/.aish/snapshots/. The intent is
// to skip the well-known "generated / vendored / temp" trees so a
// `rm -rf node_modules` does not balloon the snapshot store.
//
// Patterns use a deliberately small grammar (see Matcher.Match):
//   - `name/` — a directory anywhere in the path
//   - `*.ext` — a suffix on the basename
//   - other  — exact basename match
//
// The full gitignore grammar (anchored patterns, negation, `**`) is
// out of scope for v0.1; revisit when patterns grow beyond this list
// per the Alternatives Table in .artifacts/plans/v0.1-4.md.
var DefaultIgnorePatterns = []string{
	".git/",
	"node_modules/",
	"vendor/",
	"dist/",
	"build/",
	"target/",
	"__pycache__/",
	".cache/",
	"*.tmp",
	"*.log",
}

// Matcher tests paths against a set of ignore patterns.
type Matcher struct {
	// dirNames are the leading-component values of patterns that end
	// in "/" — matched when any path segment equals one of these.
	dirNames map[string]struct{}
	// suffixes are the values from "*.ext" patterns, stored with the
	// leading "." preserved for the strings.HasSuffix check.
	suffixes []string
	// literals are exact-basename patterns.
	literals map[string]struct{}
}

// NewIgnoreMatcher compiles patterns into a Matcher. Unknown grammar
// degrades to an exact-basename literal — never a panic, never an
// error — so a future hand-edited ~/.aish/snapshot.ignore can grow
// without breaking the shell.
func NewIgnoreMatcher(patterns []string) *Matcher {
	m := &Matcher{
		dirNames: map[string]struct{}{},
		literals: map[string]struct{}{},
	}
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		if p == "" || strings.HasPrefix(p, "#") {
			continue
		}
		switch {
		case strings.HasSuffix(p, "/"):
			m.dirNames[strings.TrimSuffix(p, "/")] = struct{}{}
		case strings.HasPrefix(p, "*."):
			m.suffixes = append(m.suffixes, p[1:]) // ".ext"
		default:
			m.literals[p] = struct{}{}
		}
	}
	return m
}

// DefaultIgnoreMatcher returns a Matcher pre-loaded with
// DefaultIgnorePatterns. The shell wires this in by default;
// user-supplied overrides are out of scope for v0.1 (the file would
// live at ~/.aish/snapshot.ignore in v0.2).
func DefaultIgnoreMatcher() *Matcher {
	return NewIgnoreMatcher(DefaultIgnorePatterns)
}

// Match returns true when path should be skipped at snapshot time.
//
// Path semantics: the matcher works on the path components, so an
// absolute or relative path matches identically — `node_modules/foo`
// and `/Users/x/repo/node_modules/foo` both hit `node_modules/`. This
// is the right default for "skip everywhere this directory appears."
func (m *Matcher) Match(path string) bool {
	if m == nil {
		return false
	}
	clean := filepath.ToSlash(filepath.Clean(path))
	parts := strings.Split(clean, "/")
	// Directory-name patterns: any path segment that matches.
	if len(m.dirNames) > 0 {
		for _, seg := range parts {
			if _, ok := m.dirNames[seg]; ok {
				return true
			}
		}
	}
	// Literal basename match.
	base := parts[len(parts)-1]
	if _, ok := m.literals[base]; ok {
		return true
	}
	// Suffix match (e.g. ".log", ".tmp"). Use Match against full clean
	// for *.log etc. so the test does not surprise.
	for _, suf := range m.suffixes {
		if strings.HasSuffix(base, suf) {
			return true
		}
	}
	return false
}
