// Package theme is the canonical Brand-Atoms shell-brand schema. It is
// the contract every aish-compatible shell consumes; the catalog
// (Brand Atoms) publishes `brands/shell/<name>.toml` documents that
// deserialise directly into the types declared here.
//
// This package is intentionally **types only** — no fetch logic, no
// compilation, no I/O. The aish-side compilation (Brand → ANSI escape
// table + glyph map) lives in shell/internal/theme.
//
// See GOALS.md §"Theming — Brand Atoms Integration" for the broader
// architecture.
package theme

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
