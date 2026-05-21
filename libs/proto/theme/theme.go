// Package theme is the canonical Brand-Atoms shell-brand schema. It is
// the contract every aish-compatible shell consumes; the catalog
// (Brand Atoms) publishes `brands/shell/<name>.toml` documents that
// deserialise directly into the types declared here.
//
// This package is intentionally **types only** — no fetch logic, no
// compilation, no I/O. The aish-side compilation (Brand → ANSI escape
// table + glyph map) lives in shell/internal/theme.
//
// # On-the-wire schema (v1)
//
// The published catalog declares each brand as a TOML document with a
// `schema = "https://theme-atoms.com/schemas/theme-v1.json"` tag and
// the field set decoded by shell/internal/theme/fetch.go's parseV1.
// Publishers MUST call Brand.Validate before publishing; consumers
// MAY call it to fail fast on malformed catalogs. See the Validate
// method below for the exact rules enforced.
//
// See GOALS.md §"Theming — Brand Atoms Integration" for the broader
// architecture.
package theme

import (
	"fmt"
	"strconv"
	"strings"
)

// Brand is the top-level shell-brand definition published by Brand
// Atoms. Each `brands/shell/<name>.toml` deserialises into one Brand.
type Brand struct {
	// Name is the user-visible identifier (e.g. "nord-powerline"). Must
	// be unique within the catalog and stable across versions.
	Name string `toml:"name"    json:"name"`
	// Type is always "shell" for shell brands. Other Brand-Atoms brand
	// types ("general", "doc", future categories) are out of scope here.
	Type string `toml:"type"    json:"type"`
	// Extends optionally points at a base general-brand whose palette
	// and glyphs are inherited. Resolution is the consumer's job — this
	// package keeps the raw reference. Example: "brands/general/nord".
	Extends string `toml:"extends" json:"extends,omitempty"`

	// Palette is the base color palette. Keys are semantic palette names
	// (`primary`, `accent`, `muted`, `red`, `green`, …); values are
	// 6-digit hex strings (`#88c0d0`).
	Palette Palette `toml:"palette" json:"palette"`
	// Roles maps semantic UI roles to palette references. Values are
	// either a bare palette key (`muted`) or a `$palette.<key>` reference
	// (`$palette.green`). Optional `at NN% opacity` suffix is reserved
	// for v0.3+; v0.2-5 consumers ignore opacity.
	Roles Roles `toml:"roles"   json:"roles"`
	// Glyphs is the Nerd-Font glyph table (filetype map name + static
	// per-role glyphs like prompt_char, git_clean, git_dirty).
	Glyphs Glyphs `toml:"glyphs"  json:"glyphs"`
	// Prompt is the prompt-specific configuration (segments to render,
	// separator style, font name).
	Prompt PromptConfig `toml:"prompt" json:"prompt"`
}

// Palette is the brand's hex-coded color table.
type Palette map[string]string

// Roles maps semantic role names to palette references.
type Roles map[string]string

// Glyphs is the Nerd-Font glyph configuration for a brand.
type Glyphs struct {
	// FiletypeMap names a glyph set published separately by Brand Atoms
	// (e.g. "nerd-default"). Consumers without that set fall back to
	// no-glyph plain text.
	FiletypeMap string `toml:"filetype_map" json:"filetype_map,omitempty"`
	// Static is the per-role glyph table: `prompt_char`, `git_clean`,
	// `git_dirty`, `arrow`, etc. Keys are role names; values are
	// single-character or short-string glyphs.
	Static map[string]string `toml:"static" json:"static,omitempty"`
}

// Validate enforces the publish-time invariants of a shell Brand:
//
//   - Name is required (non-empty).
//   - Type, when set, MUST be "shell" — other Brand-Atoms types
//     (general, doc, future categories) are not consumed by the
//     shell-brand schema. Empty Type is permitted: catalogs publish
//     the type via directory layout (`brands/shell/<name>.toml`) and
//     some v1 wire TOMLs omit the field.
//   - Every Palette value MUST be a 6-digit hex color with a leading
//     `#`. The compile step silently drops malformed colors; Validate
//     refuses to publish them.
//   - Every Roles value MUST resolve to a key that exists in Palette.
//     The `$palette.<key>` form is unwrapped; the bare-key form is
//     looked up directly. An optional " at NN% opacity" suffix is
//     stripped before lookup (the v1 schema allows it; v0.2-5
//     renderers ignore opacity).
//
// Validate returns the first error encountered with enough context for
// a publisher to fix the brand. It is a sibling of (not a replacement
// for) Compile — Compile is non-strict by design so a partial brand
// still renders; Validate is strict so the catalog stays clean.
func (b Brand) Validate() error {
	if b.Name == "" {
		return fmt.Errorf("theme: brand has no name")
	}
	if b.Type != "" && b.Type != "shell" {
		return fmt.Errorf("theme: brand %q has type %q; only \"shell\" (or empty) is valid for the shell brand type", b.Name, b.Type)
	}
	for k, v := range b.Palette {
		if !isHexColor(v) {
			return fmt.Errorf("theme: brand %q palette[%s] = %q is not a 6-digit hex color (e.g. #88c0d0)", b.Name, k, v)
		}
	}
	for role, ref := range b.Roles {
		key := paletteRefKey(ref)
		if key == "" {
			// Empty role value — skip rather than reject; the renderer
			// falls back to no-color, same as Compile's lenient path.
			continue
		}
		if _, ok := b.Palette[key]; !ok {
			return fmt.Errorf("theme: brand %q role[%s] = %q references missing palette key %q", b.Name, role, ref, key)
		}
	}
	return nil
}

// paletteRefKey extracts the palette key from a Roles value. Supports
// the canonical forms:
//
//   - "$palette.<key>"  → "<key>"
//   - "<key>"            → "<key>" (when the bare value is a known palette key)
//
// Returns "" when the input is empty or doesn't look like a palette
// reference at all. An optional " at NN% opacity" suffix is trimmed
// before returning. This mirrors shell/internal/theme.resolvePaletteRef
// but is duplicated here to keep proto/ I/O-free.
func paletteRefKey(ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ""
	}
	if strings.HasPrefix(ref, "$palette.") {
		key := strings.TrimPrefix(ref, "$palette.")
		if idx := strings.Index(key, " "); idx >= 0 {
			key = key[:idx]
		}
		return key
	}
	// Bare form: trim opacity suffix.
	if idx := strings.Index(ref, " "); idx >= 0 {
		ref = ref[:idx]
	}
	return ref
}

// isHexColor reports whether s is a `#RRGGBB` 6-digit hex color.
func isHexColor(s string) bool {
	if !strings.HasPrefix(s, "#") || len(s) != 7 {
		return false
	}
	for _, c := range s[1:] {
		if !(c >= '0' && c <= '9') && !(c >= 'a' && c <= 'f') && !(c >= 'A' && c <= 'F') {
			return false
		}
	}
	// Parse for extra defense — guards against unicode lookalikes.
	if _, err := strconv.ParseUint(s[1:], 16, 32); err != nil {
		return false
	}
	return true
}

// PromptConfig is the prompt-specific brand configuration.
type PromptConfig struct {
	// Segments is the ordered list of segment names rendered in the
	// prompt. v0.2-5 supports `cwd` and `prompt`; later epics layer
	// in `git`, `ai_tier`, `drachma`, etc.
	Segments []string `toml:"segments"   json:"segments"`
	// Separators is the separator style between segments:
	// "powerline" (filled triangles) or "minimal" (single space).
	Separators string `toml:"separators" json:"separators"`
	// Font is the suggested font name (informational; the actual font
	// is chosen by the terminal emulator).
	Font string `toml:"font" json:"font,omitempty"`
}
