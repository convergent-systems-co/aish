package theme

import "testing"

// TestBrandZeroValue — a zero-value Brand is usable; nil maps don't panic
// on read. Consumers should never have to nil-check map fields.
func TestBrandZeroValue(t *testing.T) {
	var b Brand
	if b.Palette["whatever"] != "" {
		t.Errorf("zero-value Palette lookup should return empty; got %q", b.Palette["whatever"])
	}
	if b.Roles["whatever"] != "" {
		t.Errorf("zero-value Roles lookup should return empty; got %q", b.Roles["whatever"])
	}
	if b.Glyphs.Static["prompt_char"] != "" {
		t.Errorf("zero-value Glyphs.Static lookup should return empty; got %q", b.Glyphs.Static["prompt_char"])
	}
}

// TestBrandRoundTrip — populating every field and reading it back works
// without surprises. Smoke test of the public API surface.
func TestBrandRoundTrip(t *testing.T) {
	b := Brand{
		Name:    "test",
		Type:    "shell",
		Extends: "brands/general/nord",
		Palette: Palette{
			"primary": "#88c0d0",
			"accent":  "#a3be8c",
		},
		Roles: Roles{
			"prompt": "$palette.primary",
		},
		Glyphs: Glyphs{
			FiletypeMap: "nerd-default",
			Static: map[string]string{
				"prompt_char": "❯",
				"git_clean":   "",
				"git_dirty":   "±",
			},
		},
		Prompt: PromptConfig{
			Segments:   []string{"cwd", "git", "prompt"},
			Separators: "powerline",
			Font:       "jetbrainsmono-nerdfont",
		},
	}

	if b.Palette["primary"] != "#88c0d0" {
		t.Errorf("Palette.primary = %q, want %q", b.Palette["primary"], "#88c0d0")
	}
	if b.Glyphs.Static["prompt_char"] != "❯" {
		t.Errorf("Glyphs.Static.prompt_char = %q, want %q", b.Glyphs.Static["prompt_char"], "❯")
	}
	if got := len(b.Prompt.Segments); got != 3 {
		t.Errorf("Prompt.Segments has %d entries, want 3", got)
	}
}
