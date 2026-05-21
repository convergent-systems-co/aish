package theme

import (
	"strings"
	"testing"
)

// TestValidate_AcceptsCanonicalBrand — a fully populated brand passes
// validation. Smoke that the happy path is permissive enough to handle
// the canonical Nord-Powerline shape.
func TestValidate_AcceptsCanonicalBrand(t *testing.T) {
	b := Brand{
		Name: "nord-powerline",
		Type: "shell",
		Palette: Palette{
			"primary": "#88c0d0",
			"accent":  "#a3be8c",
		},
		Roles: Roles{
			"prompt":  "$palette.primary",
			"primary": "$palette.primary",
			"accent":  "$palette.accent",
		},
		Glyphs: Glyphs{
			Static: map[string]string{"prompt_char": "❯"},
		},
		Prompt: PromptConfig{
			Segments:   []string{"cwd"},
			Separators: "powerline",
		},
	}
	if err := b.Validate(); err != nil {
		t.Errorf("Validate(canonical) = %v, want nil", err)
	}
}

// TestValidate_RejectsEmptyName — every brand must declare a name.
// The fetch path and Compile both already reject this; Validate gives
// publishers a fast-fail before either runs.
func TestValidate_RejectsEmptyName(t *testing.T) {
	if err := (Brand{}).Validate(); err == nil {
		t.Fatal("Validate(empty) = nil, want non-nil")
	}
}

// TestValidate_RejectsNonShellType — the shell brand type schema is
// the only one aish consumes. Other Brand-Atoms types (general, doc)
// must not slip through as shell brands.
func TestValidate_RejectsNonShellType(t *testing.T) {
	b := Brand{Name: "x", Type: "general", Palette: Palette{"primary": "#000000"}}
	err := b.Validate()
	if err == nil {
		t.Fatal("Validate(non-shell) = nil, want error")
	}
	if !strings.Contains(err.Error(), "type") {
		t.Errorf("Validate(non-shell) error %q should mention type", err)
	}
}

// TestValidate_AllowsEmptyType — type is implied "shell" when omitted;
// the catalog publishes the type via the directory layout
// (`brands/shell/<name>.toml`), and v1 wire TOMLs sometimes omit it.
// Strict-on-mismatch but permissive-on-omit.
func TestValidate_AllowsEmptyType(t *testing.T) {
	b := Brand{
		Name:    "x",
		Palette: Palette{"primary": "#000000"},
		Roles:   Roles{"prompt": "$palette.primary"},
	}
	if err := b.Validate(); err != nil {
		t.Errorf("Validate(no-type) = %v, want nil (empty type is permitted)", err)
	}
}

// TestValidate_RejectsRoleWithMissingPaletteKey — a role pointing at
// `$palette.notdefined` is a publisher bug; flag it so theme-atoms.com
// authors see it before users do.
func TestValidate_RejectsRoleWithMissingPaletteKey(t *testing.T) {
	b := Brand{
		Name:    "x",
		Type:    "shell",
		Palette: Palette{"primary": "#000000"},
		Roles:   Roles{"prompt": "$palette.notdefined"},
	}
	err := b.Validate()
	if err == nil {
		t.Fatal("Validate(missing-palette-key) = nil, want error")
	}
	if !strings.Contains(err.Error(), "notdefined") {
		t.Errorf("error should name the missing key; got %v", err)
	}
}

// TestValidate_RejectsMalformedHex — a palette value that doesn't look
// like a 6-digit hex color should be flagged. The Compile path silently
// drops these (renderer falls through to no-color); Validate catches
// them before they ship.
func TestValidate_RejectsMalformedHex(t *testing.T) {
	cases := map[string]string{
		"too short":  "#88c0d",
		"too long":   "#88c0d0aa",
		"no #":       "88c0d0",
		"bad hex":    "#XXYYZZ",
		"plain text": "blue",
	}
	for name, hex := range cases {
		t.Run(name, func(t *testing.T) {
			b := Brand{
				Name:    "x",
				Type:    "shell",
				Palette: Palette{"primary": hex},
			}
			if err := b.Validate(); err == nil {
				t.Errorf("Validate(palette=%q) = nil, want error", hex)
			}
		})
	}
}

// TestValidate_AllowsBarePaletteKeyRole — the `roles.prompt = "primary"`
// (no `$palette.` prefix) is a valid v1 form. Compile resolves it; so
// must Validate.
func TestValidate_AllowsBarePaletteKeyRole(t *testing.T) {
	b := Brand{
		Name:    "x",
		Type:    "shell",
		Palette: Palette{"primary": "#88c0d0"},
		Roles:   Roles{"prompt": "primary"}, // bare, not $palette.primary
	}
	if err := b.Validate(); err != nil {
		t.Errorf("Validate(bare role) = %v, want nil", err)
	}
}
