package persona

import (
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

// bundledFS embeds the curated personas shipped with every aish binary.
// The canonical source of these files is data/personas/ at the repo
// root; this directory mirrors them so //go:embed can reach them
// (embed cannot escape the package via `..`).
//
// Keep the two directories in sync — TestBundledMatchesDataDir is a
// CI seatbelt that catches drift.
//
//go:embed builtin/*.toml
var bundledFS embed.FS

// LoadBundled returns the curated personas shipped with the binary.
// Order is name-sorted so callers (notably `persona list`) observe a
// stable presentation.
//
// Each persona is parsed and validated; a malformed bundled file is a
// build-time defect and surfaces as an error from LoadBundled — the
// caller (typically NewLoader) treats this as fatal.
func LoadBundled() ([]Persona, error) {
	entries, err := fs.ReadDir(bundledFS, "builtin")
	if err != nil {
		return nil, fmt.Errorf("persona: read bundled dir: %w", err)
	}
	var out []Persona
	seen := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".toml") {
			continue
		}
		data, err := fs.ReadFile(bundledFS, "builtin/"+e.Name())
		if err != nil {
			return nil, fmt.Errorf("persona: read bundled %q: %w", e.Name(), err)
		}
		p, err := ParseTOML(data)
		if err != nil {
			return nil, fmt.Errorf("persona: parse bundled %q: %w", e.Name(), err)
		}
		if err := p.Validate(); err != nil {
			return nil, fmt.Errorf("persona: validate bundled %q: %w", e.Name(), err)
		}
		if seen[p.Name] {
			return nil, fmt.Errorf("persona: duplicate bundled name %q", p.Name)
		}
		seen[p.Name] = true
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
