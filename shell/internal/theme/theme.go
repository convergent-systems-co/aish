// Package theme compiles Brand-Atoms shell brands into immutable
// render-ready Theme values, manages a Registry of available themes,
// and persists the active theme to disk.
//
// v0.2-5 scope:
//   - Compile a proto.Brand into a Theme with pre-resolved ANSI escapes
//     and a flat glyph table (one-time work at theme load).
//   - Registry of bundled themes + activation by name; an atomic pointer
//     swap is the entire "render-switch" cost.
//   - Persistence: read and write the active theme name in
//     ~/.aish/config.toml.
//
// Out of scope (deferred):
//   - Brand-Atoms HTTP fetch + cache (issue #73; waits for theme-atoms.com).
//   - Filesystem-loaded user themes at ~/.aish/themes/*.toml.
//   - Performance benchmarks (#81).
//   - `extends:` base-brand resolution (Brand Atoms emits flattened
//     brands; the consumer doesn't yet need to walk inheritance).
package theme

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	proto "github.com/convergent-systems-co/aish/libs/proto/theme"
)

// AnsiReset is the universal "return to default colors" escape sequence.
const AnsiReset = "\x1b[0m"

// Theme is a compiled, immutable view of a Brand ready for render. All
// ANSI escape sequences are pre-computed at compile time so the render
// path is pure memory access (no formatting, no allocations).
type Theme struct {
	name string

	// Pre-compiled ANSI foreground sequences for the most-used roles.
	// An empty string means "no color" (renderer falls through to default).
	prompt  string
	primary string
	accent  string
	muted   string
	errorC  string
	success string

	// Flat glyph table: role-name -> Nerd-Font / Unicode glyph.
	glyphs map[string]string

	// Ordered list of prompt segment names (e.g. ["cwd", "prompt"]).
	segments []string

	// Separator style: "powerline" | "minimal" (informational; renderer
	// chooses on this value).
	separators string
}

// Name returns the theme's user-visible identifier.
func (t *Theme) Name() string {
	if t == nil {
		return ""
	}
	return t.name
}

// Segments returns the ordered prompt segments. The slice is shared;
// callers must not mutate it.
func (t *Theme) Segments() []string {
	if t == nil {
		return nil
	}
	return t.segments
}

// Separators returns the separator style ("powerline" or "minimal").
func (t *Theme) Separators() string {
	if t == nil {
		return ""
	}
	return t.separators
}

// Glyph returns the glyph for role (e.g. "prompt_char", "git_clean") or
// the supplied fallback if the theme has no glyph for that role.
func (t *Theme) Glyph(role, fallback string) string {
	if t == nil {
		return fallback
	}
	if g, ok := t.glyphs[role]; ok && g != "" {
		return g
	}
	return fallback
}

// ColorPrompt wraps s in the theme's prompt-role ANSI codes. A nil theme
// or a theme with no prompt color returns s unchanged so the caller
// always gets a valid string.
func (t *Theme) ColorPrompt(s string) string {
	if t == nil || t.prompt == "" {
		return s
	}
	return t.prompt + s + AnsiReset
}

// ColorAccent wraps s in the theme's accent-role ANSI codes.
func (t *Theme) ColorAccent(s string) string {
	if t == nil || t.accent == "" {
		return s
	}
	return t.accent + s + AnsiReset
}

// ColorMuted wraps s in the theme's muted-role ANSI codes.
func (t *Theme) ColorMuted(s string) string {
	if t == nil || t.muted == "" {
		return s
	}
	return t.muted + s + AnsiReset
}

// ColorError wraps s in the theme's error-role ANSI codes.
func (t *Theme) ColorError(s string) string {
	if t == nil || t.errorC == "" {
		return s
	}
	return t.errorC + s + AnsiReset
}

// Compile resolves a proto.Brand into a Theme:
//
//  1. Each role reference (`$palette.primary` or bare `primary`) is
//     resolved against the brand's Palette to a hex color.
//  2. Each hex color is pre-translated into an ANSI 24-bit foreground
//     escape sequence.
//  3. The glyph table is flattened for O(1) lookup.
//
// Unknown roles, invalid hex colors, or missing palette references are
// dropped silently rather than erroring — the renderer falls back to
// no-color. A theme with zero resolvable roles is still usable; it just
// renders monochrome.
func Compile(b proto.Brand) (*Theme, error) {
	if b.Name == "" {
		return nil, fmt.Errorf("theme: brand has no name")
	}

	t := &Theme{
		name:       b.Name,
		separators: b.Prompt.Separators,
	}

	if len(b.Prompt.Segments) > 0 {
		t.segments = make([]string, len(b.Prompt.Segments))
		copy(t.segments, b.Prompt.Segments)
	}

	// Resolve each role we care about. Roles unknown to v0.2-5 are kept
	// in the proto but ignored here — future epics will extend this list.
	t.prompt = resolveAnsi(b, "prompt")
	t.primary = resolveAnsi(b, "primary")
	t.accent = resolveAnsi(b, "accent")
	t.muted = resolveAnsi(b, "muted")
	t.errorC = resolveAnsi(b, "error")
	t.success = resolveAnsi(b, "success")

	if len(b.Glyphs.Static) > 0 {
		t.glyphs = make(map[string]string, len(b.Glyphs.Static))
		for k, v := range b.Glyphs.Static {
			t.glyphs[k] = v
		}
	}

	return t, nil
}

// resolveAnsi looks up role in Brand.Roles, walks the value to a palette
// hex, and converts that hex to an ANSI foreground escape. Returns ""
// if any step fails — caller treats "" as "no color".
func resolveAnsi(b proto.Brand, role string) string {
	val, ok := b.Roles[role]
	if !ok {
		// If the role isn't in Roles, try Palette directly so themes can
		// skip the Roles indirection for simple palettes.
		hex, paletteOK := b.Palette[role]
		if !paletteOK {
			return ""
		}
		return hexToAnsi(hex)
	}
	hex := resolvePaletteRef(b.Palette, val)
	return hexToAnsi(hex)
}

// resolvePaletteRef walks a roles-value to a hex color. Handles two
// reference forms:
//
//   - "$palette.<key>"  -> b.Palette[<key>]
//   - "<key>"            -> b.Palette[<key>]
//
// Returns "" if the key doesn't resolve.
func resolvePaletteRef(p proto.Palette, ref string) string {
	ref = strings.TrimSpace(ref)
	if strings.HasPrefix(ref, "$palette.") {
		key := strings.TrimPrefix(ref, "$palette.")
		// Trim opacity suffix (e.g. " at 50% opacity") — silently dropped
		// for v0.2-5.
		if idx := strings.Index(key, " "); idx >= 0 {
			key = key[:idx]
		}
		return p[key]
	}
	// Bare key.
	if idx := strings.Index(ref, " "); idx >= 0 {
		ref = ref[:idx]
	}
	return p[ref]
}

// hexToAnsi converts "#88c0d0" to a 24-bit ANSI foreground escape:
// "\x1b[38;2;136;192;208m". Returns "" for malformed input.
func hexToAnsi(hex string) string {
	if !strings.HasPrefix(hex, "#") || len(hex) != 7 {
		return ""
	}
	r, err1 := strconv.ParseUint(hex[1:3], 16, 8)
	g, err2 := strconv.ParseUint(hex[3:5], 16, 8)
	b, err3 := strconv.ParseUint(hex[5:7], 16, 8)
	if err1 != nil || err2 != nil || err3 != nil {
		return ""
	}
	return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", r, g, b)
}

// Inspect returns a multiline human-readable summary of the theme. Used
// by `aish theme show <name>` to print the active brand details.
func (t *Theme) Inspect() string {
	if t == nil {
		return "(no theme)"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Name:        %s\n", t.name)
	fmt.Fprintf(&b, "Separators:  %s\n", t.separators)
	fmt.Fprintf(&b, "Segments:    %s\n", strings.Join(t.segments, ", "))

	// Sorted glyph dump for stable output.
	if len(t.glyphs) > 0 {
		keys := make([]string, 0, len(t.glyphs))
		for k := range t.glyphs {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		fmt.Fprintf(&b, "Glyphs:\n")
		for _, k := range keys {
			fmt.Fprintf(&b, "  %-14s %s\n", k+":", t.glyphs[k])
		}
	}

	// Role swatches: print "ROLE: ███" with each role's color.
	fmt.Fprintf(&b, "Roles:\n")
	roles := []struct {
		name string
		ansi string
	}{
		{"prompt", t.prompt},
		{"primary", t.primary},
		{"accent", t.accent},
		{"muted", t.muted},
		{"error", t.errorC},
		{"success", t.success},
	}
	for _, r := range roles {
		if r.ansi == "" {
			fmt.Fprintf(&b, "  %-10s (no color)\n", r.name+":")
			continue
		}
		fmt.Fprintf(&b, "  %-10s %s███%s\n", r.name+":", r.ansi, AnsiReset)
	}
	return b.String()
}
